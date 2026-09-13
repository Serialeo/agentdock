package nexusbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/gorilla/websocket"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/runtimeapi"
)

const (
	maxMessageBytes    = 8 << 20
	invokeDrainTimeout = 5 * time.Second
)

// NodeAPI 由 Nexus Bridge 消费方定义，只包含握手和远程 operation 真正需要的节点能力。
type NodeAPI interface {
	ToolNames() []string
	ToolDescriptors() []map[string]any
	UIResources() []protocol.UIResourceCapability
	ToolContractHash() string
	AgentDockLocalContext(context.Context) (map[string]any, error)
	PrepareProjectExecution(context.Context, *protocol.ExecutionContext) (context.Context, error)
	PrepareProjectSessionControlExecution(context.Context, *protocol.ExecutionContext) (context.Context, error)
	ApplyProjectDeployment(protocol.Deployment) (map[string]any, error)
	RemoveProjectDeployment(string) (map[string]any, error)
	LoadProjectPrompt(protocol.ProjectPromptLoadRequest) (map[string]any, error)
	WriteProjectPrompt(protocol.ProjectPromptWriteRequest) (map[string]any, error)
	BindProjectTarget(protocol.ProjectTargetBindRequest) (map[string]any, error)
	RebindProjectTarget(protocol.ProjectTargetRebindRequest) (map[string]any, error)
	RevokeProjectTarget(string) (map[string]any, error)
	RevokeProjectSession(string) (map[string]any, error)
	Invoke(context.Context, string, map[string]any) (map[string]any, error)
	ReadAppResource(string) (map[string]any, error)
}

// Optional extension: older/fake nodes need not implement durable outcomes.
type commandOutcomeAPI interface {
	ReadCommandOutcomes(context.Context, protocol.CommandOutcomesReadRequest) (protocol.CommandOutcomesReadResult, error)
	AckCommandOutcomes(context.Context, protocol.CommandOutcomesAckRequest) (protocol.CommandOutcomesAckResult, error)
}

type Client struct {
	identity  Identity
	node      NodeAPI
	runtime   runtimeapi.Runtime
	artifacts publicartifacts.Store
	state     *ConnectionState
	invokeWG  sync.WaitGroup
	writeMu   sync.Mutex
	cancelMu  sync.Mutex
	cancels   map[string]context.CancelFunc
}

func NewClient(identity Identity, node NodeAPI, runtime runtimeapi.Runtime, artifacts publicartifacts.Store, state *ConnectionState) *Client {
	return &Client{identity: identity, node: node, runtime: runtime, artifacts: artifacts, state: state, cancels: make(map[string]context.CancelFunc)}
}

func (c *Client) Run(ctx context.Context) {
	defer c.drainInvocations()
	backoff := time.Second
	for ctx.Err() == nil {
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("NexusDock connection lost", "error", err, "retry_in", backoff)
		timer := time.NewTimer(backoff + time.Duration(rand.IntN(500))*time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) drainInvocations() {
	done := make(chan struct{})
	go func() {
		c.invokeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(invokeDrainTimeout):
		slog.Warn("NexusDock bridge in-flight invocations drain timeout")
	}
}

func (c *Client) connect(ctx context.Context) error {
	c.state.SetConnected(false)
	endpoint, err := url.Parse(c.identity.Endpoint)
	if err != nil {
		return err
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/nodes/connect"
	header := http.Header{"Authorization": []string{"Bearer " + c.identity.DeviceToken}}
	socket, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), header)
	if err != nil {
		if response != nil {
			return fmt.Errorf("连接 NexusDock（HTTP %d）: %w", response.StatusCode, err)
		}
		return fmt.Errorf("连接 NexusDock: %w", err)
	}
	defer socket.Close()
	// gorilla/websocket 的阻塞读取不会自动观察 context；父 Context 取消时主动关闭连接，
	// 让 ReadJSON 立即返回，确保 Bridge 能在 Runtime 关闭前退出。
	stopContextClose := context.AfterFunc(ctx, func() { _ = socket.Close() })
	defer stopContextClose()
	socket.SetReadLimit(maxMessageBytes)

	tools := c.node.ToolNames()
	descriptors, err := bridgeToolDescriptors(c.node.ToolDescriptors())
	if err != nil {
		return err
	}
	hello := bridgeHello(c.identity, tools, descriptors, c.node.UIResources(), c.node.ToolContractHash())
	if _, supported := c.node.(commandOutcomeAPI); supported {
		hello.BridgeCapabilities = append(hello.BridgeCapabilities, protocol.CommandOutcomesCapability)
	}
	if err := c.write(socket, protocol.Message{
		Type: protocol.MessageNodeHello, ProtocolVersion: protocol.ConnectionProtocolVersion,
		Hello: hello,
	}); err != nil {
		return err
	}
	var ready protocol.Message
	if err := socket.ReadJSON(&ready); err != nil {
		return fmt.Errorf("读取 NexusDock 握手响应: %w", err)
	}
	if ready.Type != protocol.MessageNodeReady || ready.ProtocolVersion != protocol.ConnectionProtocolVersion {
		return errors.New("NexusDock 返回了不兼容的节点协议")
	}
	c.state.SetConnected(true)
	defer c.state.SetConnected(false)
	slog.Info("NexusDock node connected", "node_id", c.identity.NodeID, "endpoint", c.identity.Endpoint)
	heartbeat := time.Duration(ready.HeartbeatMS) * time.Millisecond
	if heartbeat <= 0 {
		heartbeat = 30 * time.Second
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.heartbeat(connectionCtx, socket, heartbeat)
	for {
		var incoming protocol.Message
		if err := socket.ReadJSON(&incoming); err != nil {
			return err
		}
		switch incoming.Type {
		case protocol.MessageToolInvoke:
			c.invokeWG.Add(1)
			go func() {
				defer c.invokeWG.Done()
				c.invoke(connectionCtx, socket, incoming)
			}()
		case protocol.MessageToolCancel:
			c.cancel(incoming.RequestID)
		case protocol.MessageNodeHeartbeat:
		}
	}
}

func bridgeHello(identity Identity, tools []string, descriptors []protocol.ToolDescriptor, uiResources []protocol.UIResourceCapability, toolContractHash string) *protocol.Hello {
	return &protocol.Hello{
		DeviceID:           identity.DeviceID,
		Version:            buildinfo.Version,
		ProtocolVersion:    protocol.ConnectionProtocolVersion,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		Capabilities:       append([]string(nil), tools...),
		BridgeCapabilities: []string{protocol.ArtifactReadCapability},
		ToolContractHash:   toolContractHash,
		Tools:              descriptors,
		UIResources:        uiResources,
	}
}

func (c *Client) invoke(parent context.Context, socket *websocket.Conn, incoming protocol.Message) {
	ctx, cancel := context.WithCancel(parent)
	c.cancelMu.Lock()
	c.cancels[incoming.RequestID] = cancel
	c.cancelMu.Unlock()
	defer func() {
		recovered := recover()
		cancel()
		c.cancelMu.Lock()
		delete(c.cancels, incoming.RequestID)
		c.cancelMu.Unlock()
		if recovered != nil {
			// Nexus Bridge 是远程 RPC 边界。单次工具 panic 只结束当前请求，
			// 避免把整个 AgentDock 守护进程和其他本地会话一并带崩。
			slog.Error("NexusDock node operation panicked", "request_id", incoming.RequestID, "operation", incoming.Operation, "panic", recovered, "stack", string(debug.Stack()))
			_ = c.write(socket, protocol.Message{
				Type:      protocol.MessageToolError,
				RequestID: incoming.RequestID,
				Error:     &protocol.RemoteError{Code: "NODE_OPERATION_FAILED", Message: "AgentDock node operation failed", Category: "internal"},
			})
		}
	}()

	var result map[string]any
	var err error
	switch incoming.Operation {
	case protocol.OperationCommandOutcomesRead, protocol.OperationCommandOutcomesAck:
		result, err = c.commandOutcomes(ctx, incoming.Operation, incoming.Arguments)
	case protocol.OperationRuntimeRequest:
		var request runtimeapi.Request
		if decodeErr := json.Unmarshal(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Runtime 请求: %w", decodeErr)
		} else {
			result, err = c.dispatchRuntimeRequest(ctx, request)
		}
	case protocol.OperationContextLocal:
		result, err = c.node.AgentDockLocalContext(ctx)
	case protocol.OperationToolCall:
		var request protocol.ToolCallRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析工具请求: %w", decodeErr)
		} else {
			prepare := c.node.PrepareProjectExecution
			switch request.Tool {
			case "session_observe", "session_act", "acp_session", "acp_prompt", "acp_interaction":
				prepare = c.node.PrepareProjectSessionControlExecution
			case "browser_session":
				action, _ := request.Arguments["action"].(string)
				if strings.EqualFold(strings.TrimSpace(action), "close") {
					prepare = c.node.PrepareProjectSessionControlExecution
				}
			}
			preparedCtx, prepareErr := prepare(ctx, incoming.ExecutionContext)
			if prepareErr != nil {
				err = prepareErr
			} else {
				result, err = c.node.Invoke(preparedCtx, request.Tool, request.Arguments)
			}
		}
	case protocol.OperationProjectDeploymentApply:
		var deployment protocol.Deployment
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &deployment); decodeErr != nil {
			err = fmt.Errorf("解析 Project Deployment 应用请求: %w", decodeErr)
		} else {
			result, err = c.node.ApplyProjectDeployment(deployment)
		}
	case protocol.OperationProjectDeploymentRemove:
		var request protocol.ProjectDeploymentRemoveRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Deployment 移除请求: %w", decodeErr)
		} else {
			result, err = c.node.RemoveProjectDeployment(request.DeploymentID)
		}
	case protocol.OperationProjectPromptLoad:
		var request protocol.ProjectPromptLoadRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Prompt 加载请求: %w", decodeErr)
		} else {
			result, err = c.node.LoadProjectPrompt(request)
		}
	case protocol.OperationProjectPromptWrite:
		var request protocol.ProjectPromptWriteRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Prompt 写入请求: %w", decodeErr)
		} else {
			result, err = c.node.WriteProjectPrompt(request)
		}
	case protocol.OperationProjectTargetBind:
		var request protocol.ProjectTargetBindRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Target 绑定请求: %w", decodeErr)
		} else {
			result, err = c.node.BindProjectTarget(request)
		}
	case protocol.OperationProjectTargetRebind:
		var request protocol.ProjectTargetRebindRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Target 重绑请求: %w", decodeErr)
		} else {
			result, err = c.node.RebindProjectTarget(request)
		}
	case protocol.OperationProjectTargetRevoke:
		var request protocol.ProjectTargetRevokeRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Target 撤销请求: %w", decodeErr)
		} else {
			result, err = c.node.RevokeProjectTarget(request.TargetID)
		}
	case protocol.OperationProjectSessionRevoke:
		var request protocol.ProjectSessionRevokeRequest
		if decodeErr := decodeStrictBridgeArguments(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Project Session 撤销请求: %w", decodeErr)
		} else {
			result, err = c.node.RevokeProjectSession(request.WorkSessionID)
		}
	case protocol.OperationResourceRead:
		var request struct {
			URI string `json:"uri"`
		}
		if decodeErr := json.Unmarshal(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 MCP App resource 请求: %w", decodeErr)
		} else {
			result, err = c.node.ReadAppResource(request.URI)
		}
	case protocol.OperationArtifactRead:
		var request struct {
			ArtifactID string `json:"artifact_id"`
			Offset     int64  `json:"offset"`
			MaxBytes   int    `json:"max_bytes"`
		}
		if decodeErr := json.Unmarshal(incoming.Arguments, &request); decodeErr != nil {
			err = fmt.Errorf("解析 Artifact 读取请求: %w", decodeErr)
		} else {
			result, err = c.readArtifactChunk(request.ArtifactID, request.Offset, request.MaxBytes)
		}
	default:
		err = fmt.Errorf("不支持的 NexusDock 节点操作: %s", incoming.Operation)
	}
	if err != nil {
		_ = c.write(socket, protocol.Message{Type: protocol.MessageToolError, RequestID: incoming.RequestID, Error: bridgeError(err)})
		return
	}
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		_ = c.write(socket, protocol.Message{Type: protocol.MessageToolError, RequestID: incoming.RequestID, Error: bridgeError(fmt.Errorf("编码节点结果: %w", encodeErr))})
		return
	}
	_ = c.write(socket, protocol.Message{Type: protocol.MessageToolResult, RequestID: incoming.RequestID, Result: encoded})
}

func (c *Client) dispatchRuntimeRequest(ctx context.Context, request runtimeapi.Request) (map[string]any, error) {
	const limit = 64 * 1024
	if len(request.Body) > limit {
		return nil, &app.ToolError{Code: "INVALID_ARGUMENT", Message: "runtime request body is too large", Category: "validation"}
	}
	return runtimeapi.Dispatch(ctx, c.runtime, request)
}

func decodeStrictBridgeArguments(raw json.RawMessage, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("bridge arguments must contain exactly one JSON value")
	}
	return nil
}

func bridgeToolDescriptors(descriptors []map[string]any) ([]protocol.ToolDescriptor, error) {
	encoded, err := json.Marshal(descriptors)
	if err != nil {
		return nil, fmt.Errorf("编码 Nexus Bridge 工具契约: %w", err)
	}
	var tools []protocol.ToolDescriptor
	if err := json.Unmarshal(encoded, &tools); err != nil {
		return nil, fmt.Errorf("解析 Nexus Bridge 工具契约: %w", err)
	}
	return tools, nil
}

func (c *Client) heartbeat(ctx context.Context, socket *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.write(socket, protocol.Message{Type: protocol.MessageNodeHeartbeat}); err != nil {
				_ = socket.Close()
				return
			}
		}
	}
}

func (c *Client) write(socket *websocket.Conn, outgoing protocol.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = socket.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return socket.WriteJSON(outgoing)
}

func (c *Client) cancel(requestID string) {
	c.cancelMu.Lock()
	cancel := c.cancels[requestID]
	c.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func bridgeError(err error) *protocol.RemoteError {
	var remoteErr *protocol.RemoteError
	if errors.As(err, &remoteErr) {
		converted := *remoteErr
		if remoteErr.Details != nil {
			converted.Details = make(map[string]any, len(remoteErr.Details))
			for key, value := range remoteErr.Details {
				converted.Details[key] = value
			}
		}
		return &converted
	}
	converted := &protocol.RemoteError{Code: "NODE_OPERATION_FAILED", Message: err.Error()}
	var toolErr *app.ToolError
	if errors.As(err, &toolErr) {
		converted.Code = toolErr.Code
		converted.Message = toolErr.Message
		converted.Category = toolErr.Category
		converted.Retryable = toolErr.Retryable
		converted.Details = toolErr.Details
	}
	return converted
}
