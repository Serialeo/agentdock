package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
)

type Server struct {
	runtime     *app.Runtime
	cfg         config.Config
	sdkMu       sync.Mutex
	sdk         *mcpsdk.Server
	httpHandler http.Handler
}

func NewServer(runtime *app.Runtime, cfg config.Config) *Server {
	server := &Server{runtime: runtime, cfg: cfg}
	_ = server.currentSDK()
	server.httpHandler = mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server.currentSDK() },
		&mcpsdk.StreamableHTTPOptions{
			// 仅在显式配置公网 URL 且启用认证时放宽 SDK 的 localhost Host 校验。
			// 反代或 Tunnel 会保留公网 Host，入口仍由静态 Token 或 OAuth resource 绑定保护。
			DisableLocalhostProtection:   cfg.OAuthServerURL != "" && cfg.AuthRequired(),
			Stateless:                    true,
			JSONResponse:                 true,
			MaxRequestBodyBytes:          1 << 20,
			PropagateRequestCancellation: true,
		},
	)
	return server
}

func (s *Server) currentSDK() *mcpsdk.Server {
	if s == nil {
		return nil
	}
	s.sdkMu.Lock()
	defer s.sdkMu.Unlock()
	if s.sdk != nil {
		return s.sdk
	}
	s.sdk = mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: config.ServerName, Version: buildinfo.Version},
		&mcpsdk.ServerOptions{
			Capabilities: &mcpsdk.ServerCapabilities{},
		},
	)
	if s.runtime != nil {
		s.registerAppResources()
		for _, definition := range s.runtime.ToolDefinitions() {
			s.registerTool(definition)
		}
	}
	return s.sdk
}

func (s *Server) AgentDockContext(ctx context.Context) (app.Result, error) {
	return s.runtime.AgentDockContext(ctx)
}

// AgentDockLocalContext 为 Nexus Bridge 私有操作提供节点本地 Context，
// 返回结构沿用 agentdock_context 的标准工具结果 envelope。
func (s *Server) AgentDockLocalContext(ctx context.Context) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	result, err := s.runtime.AgentDockLocalContext(ctx)
	return toolEnvelope("agentdock_context", result, err), nil
}

func (s *Server) PrepareProjectExecution(ctx context.Context, executionContext *protocol.ExecutionContext) (context.Context, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	return s.runtime.PrepareProjectExecution(ctx, executionContext)
}

func (s *Server) PrepareProjectSessionControlExecution(ctx context.Context, executionContext *protocol.ExecutionContext) (context.Context, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	return s.runtime.PrepareProjectSessionControlExecution(ctx, executionContext)
}

func (s *Server) ApplyProjectDeployment(deployment protocol.Deployment) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	applied, err := s.runtime.ApplyProjectDeployment(deployment)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deployment": applied}, nil
}

func (s *Server) RemoveProjectDeployment(deploymentID string) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	if err := s.runtime.RemoveProjectDeployment(deploymentID); err != nil {
		return nil, err
	}
	return map[string]any{"deployment_id": deploymentID, "removed": true}, nil
}

func (s *Server) LoadProjectPrompt(request protocol.ProjectPromptLoadRequest) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	result, err := s.runtime.LoadProjectPrompt(request)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode Project Prompt result: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		return nil, fmt.Errorf("normalize Project Prompt result: %w", err)
	}
	return output, nil
}

func (s *Server) WriteProjectPrompt(request protocol.ProjectPromptWriteRequest) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	result, err := s.runtime.WriteProjectPrompt(request)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode Project Prompt write result: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(encoded, &output); err != nil {
		return nil, fmt.Errorf("normalize Project Prompt write result: %w", err)
	}
	return output, nil
}

func (s *Server) BindProjectTarget(request protocol.ProjectTargetBindRequest) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	binding, err := s.runtime.BindProjectTarget(request)
	if err != nil {
		return nil, err
	}
	return map[string]any{"target": binding}, nil
}

func (s *Server) RebindProjectTarget(request protocol.ProjectTargetRebindRequest) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	binding, err := s.runtime.RebindProjectTarget(request)
	if err != nil {
		return nil, err
	}
	return map[string]any{"target": binding}, nil
}

func (s *Server) RevokeProjectTarget(targetID string) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	s.runtime.RevokeProjectTarget(targetID)
	return map[string]any{"target_id": targetID, "revoked": true}, nil
}

func (s *Server) RevokeProjectSession(workSessionID string) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	s.runtime.RevokeProjectSession(workSessionID)
	return map[string]any{"work_session_id": workSessionID, "revoked": true}, nil
}

func (s *Server) ToolNames() []string {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.ToolNames()
}

func (s *Server) ToolContractHash() string {
	encoded, err := json.Marshal(s.ToolDescriptors())
	if err != nil {
		return ""
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
}

func (s *Server) ToolDescriptors() []map[string]any {
	if s == nil || s.runtime == nil {
		return nil
	}
	return toolDescriptors(s.runtime.ToolDefinitions(), s.cfg.MCPAppsEnabled)
}

func (s *Server) Invoke(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	if s == nil || s.runtime == nil {
		return nil, errors.New("AgentDock runtime is not initialized")
	}
	result, err := s.runtime.Call(ctx, name, arguments)
	return toolEnvelope(name, result, err), nil
}

func (s *Server) HTTPHandler() http.Handler {
	return s.httpHandler
}

func (s *Server) ServeStdio(in io.Reader, out io.Writer) error {
	server := s.currentSDK()
	if server == nil {
		return errors.New("MCP server is not initialized")
	}
	return server.Run(context.Background(), &mcpsdk.IOTransport{
		Reader: readCloser{Reader: in},
		Writer: writeCloser{Writer: out},
	})
}

func (s *Server) registerTool(def ToolDefinition) {
	meta := toolMetadata(def, s.cfg.MCPAppsEnabled)
	tool := &mcpsdk.Tool{
		Name:         def.Name,
		Title:        def.Title,
		Description:  def.Description,
		InputSchema:  def.InputSchema,
		OutputSchema: def.OutputSchema,
	}
	if def.Annotations != nil {
		tool.Annotations = &mcpsdk.ToolAnnotations{
			Title:           def.Annotations.Title,
			ReadOnlyHint:    def.Annotations.ReadOnlyHint,
			DestructiveHint: cloneBoolPointer(def.Annotations.DestructiveHint),
			IdempotentHint:  def.Annotations.IdempotentHint,
			OpenWorldHint:   cloneBoolPointer(def.Annotations.OpenWorldHint),
		}
	}
	if len(meta) > 0 {
		tool.Meta = mcpsdk.Meta(meta)
	}
	// 使用低层 AddTool：AgentDock 的参数校验、权限错误和结构化输出都由
	// Runtime 统一处理，SDK 只负责协议、会话与传输语义。
	s.sdk.AddTool(tool, func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return s.callTool(ctx, def.Name, request)
	})
}

func (s *Server) callTool(ctx context.Context, name string, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	started := time.Now()
	arguments := map[string]any{}
	if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 && string(request.Params.Arguments) != "null" {
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			slog.Warn("tool params invalid", "tool", name, "duration_ms", time.Since(started).Milliseconds())
			return nil, &sdkjsonrpc.Error{Code: sdkjsonrpc.CodeInvalidParams, Message: "tool arguments must be a JSON object"}
		}
	}
	slog.Info("tool started", "tool", name)
	result, err := s.runtime.Call(ctx, name, arguments)
	finishedAttrs := []any{"tool", name, "duration_ms", time.Since(started).Milliseconds(), "ok", err == nil}
	if err != nil {
		finishedAttrs = append(finishedAttrs, "error", err)
	}
	slog.Info("tool finished", finishedAttrs...)

	encoded, encodeErr := json.Marshal(toolEnvelope(name, result, err))
	if encodeErr != nil {
		return nil, fmt.Errorf("encode MCP tool result: %w", encodeErr)
	}
	var response mcpsdk.CallToolResult
	if decodeErr := json.Unmarshal(encoded, &response); decodeErr != nil {
		return nil, fmt.Errorf("decode MCP tool result: %w", decodeErr)
	}
	if def, ok := s.runtime.ToolDefinition(name); ok {
		if meta := toolResultMetadata(def, arguments, s.cfg.MCPAppsEnabled); len(meta) > 0 {
			response.Meta = meta
		}
	}
	return &response, nil
}

func toolMetadata(def ToolDefinition, mcpAppsEnabled bool) map[string]any {
	meta := map[string]any{}
	if mcpAppsEnabled && def.UIBinding != nil && def.UIBinding.Action == "" {
		meta["ui"] = map[string]any{"resourceUri": def.UIBinding.ResourceURI}
	}
	if len(def.FileArgRewritePaths) > 0 {
		paths := append([]string(nil), def.FileArgRewritePaths...)
		meta["file_arg_rewrite_paths"] = paths
		meta["openai/fileParams"] = paths
	}
	if len(def.FileResultRewritePaths) > 0 {
		paths := append([]string(nil), def.FileResultRewritePaths...)
		meta["file_result_rewrite_paths"] = paths
		meta["openai/fileResultPaths"] = paths
		meta["openai/fileOutputs"] = paths
	}
	return meta
}

// Action-scoped Apps UI lives on the call result rather than the tool descriptor,
// so unrelated actions on the same action-based tool do not render a widget.
func toolResultMetadata(def ToolDefinition, arguments map[string]any, mcpAppsEnabled bool) mcpsdk.Meta {
	if !mcpAppsEnabled || def.UIBinding == nil || def.UIBinding.Action == "" {
		return nil
	}
	action, _ := arguments["action"].(string)
	if action != def.UIBinding.Action {
		return nil
	}
	return mcpsdk.Meta{"ui": map[string]any{"resourceUri": def.UIBinding.ResourceURI}}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

func toolDescriptors(definitions []ToolDefinition, mcpAppsEnabled bool) []map[string]any {
	descriptors := make([]map[string]any, 0, len(definitions))
	for _, def := range definitions {
		descriptor := map[string]any{
			"name":         def.Name,
			"title":        def.Title,
			"description":  def.Description,
			"inputSchema":  def.InputSchema,
			"outputSchema": def.OutputSchema,
		}
		if def.Annotations != nil {
			descriptor["annotations"] = map[string]any{
				"title": def.Annotations.Title, "readOnlyHint": def.Annotations.ReadOnlyHint,
				"destructiveHint": def.Annotations.DestructiveHint, "idempotentHint": def.Annotations.IdempotentHint,
				"openWorldHint": def.Annotations.OpenWorldHint,
			}
		}
		meta := toolMetadata(def, mcpAppsEnabled)
		if paths, ok := meta["file_arg_rewrite_paths"].([]string); ok {
			descriptor["file_arg_rewrite_paths"] = paths
		}
		if paths, ok := meta["file_result_rewrite_paths"].([]string); ok {
			descriptor["file_result_rewrite_paths"] = paths
		}
		if len(meta) > 0 {
			descriptor["_meta"] = meta
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

func toolEnvelope(name string, structured any, err error) map[string]any {
	if err != nil {
		payload := map[string]any{"tool": name, "error": err.Error()}
		var toolErr *app.ToolError
		if errors.As(err, &toolErr) {
			payload["code"] = toolErr.Code
			payload["category"] = toolErr.Category
			payload["retryable"] = toolErr.Retryable
			payload["details"] = toolErr.Details
			if toolErr.Code == "PERMISSION_REQUIRED" {
				payload["permission_request"] = map[string]any{
					"tool_name":  name,
					"permission": toolErr.Details["permission"],
					"status":     "required",
				}
			}
		}
		return map[string]any{"isError": true, "structuredContent": payload, "content": []map[string]any{{"type": "text", "text": pretty(payload)}}}
	}
	if name == "view_image" || name == "computer_observe" || name == "computer_act" {
		payload := asMap(structured)
		if data, _ := payload["_mcp_image_base64"].(string); data != "" {
			mimeType, _ := payload["_mcp_image_mime_type"].(string)
			clean := cloneWithoutInternalImage(payload)
			return map[string]any{"isError": false, "structuredContent": clean, "content": []map[string]any{{"type": "image", "data": data, "mimeType": mimeType}}}
		}
	}
	if name == "mcp_tool_call" {
		return dynamicMCPToolEnvelope(structured)
	}
	return map[string]any{"isError": false, "structuredContent": structured, "content": []map[string]any{{"type": "text", "text": pretty(structured)}}}
}

func dynamicMCPToolEnvelope(structured any) map[string]any {
	payload := asMap(structured)
	remote, _ := payload["result"].(map[string]any)
	isError, _ := remote["isError"].(bool)
	content, ok := remote["content"]
	if !ok {
		content = []map[string]any{{"type": "text", "text": pretty(payload)}}
	}
	return map[string]any{
		"isError":           isError,
		"structuredContent": payload,
		"content":           content,
	}
}

func cloneWithoutInternalImage(value map[string]any) map[string]any {
	clean := make(map[string]any, len(value))
	for key, item := range value {
		if key == "_mcp_image_base64" || key == "_mcp_image_mime_type" {
			continue
		}
		clean[key] = item
	}
	return clean
}

func asMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case app.Result:
		return map[string]any(typed)
	default:
		return map[string]any{}
	}
}

func pretty(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
