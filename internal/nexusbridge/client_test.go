package nexusbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/gorilla/websocket"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/runtimeapi"
)

func TestBridgeToolDescriptorsPreservePresentationBinding(t *testing.T) {
	descriptors, err := bridgeToolDescriptors([]map[string]any{
		{
			"name":        "file_edit",
			"inputSchema": map[string]any{"type": "object"},
			"_meta":       map[string]any{"ui": map[string]any{"resourceUri": protocol.FileChangeUIResourceURI}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors[0].Name != "file_edit" {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	ui, ok := descriptors[0].Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != protocol.FileChangeUIResourceURI {
		t.Fatalf("presentation meta = %#v", descriptors[0].Meta)
	}
}

func TestBridgeHelloSeparatesToolsFromBridgeCapabilities(t *testing.T) {
	tools := []string{"read_file", "exec_command"}
	hello := bridgeHello(
		Identity{DeviceID: "device_abcdefgh"},
		tools,
		[]protocol.ToolDescriptor{{Name: "read_file"}, {Name: "exec_command"}},
		[]protocol.UIResourceCapability{},
		"sha256:test",
	)
	if !reflect.DeepEqual(hello.Capabilities, tools) {
		t.Fatalf("capabilities = %#v, want tools %#v", hello.Capabilities, tools)
	}
	if len(hello.BridgeCapabilities) != 1 || hello.BridgeCapabilities[0] != protocol.ArtifactReadCapability {
		t.Fatalf("bridge_capabilities = %#v", hello.BridgeCapabilities)
	}
	for _, capability := range hello.Capabilities {
		if capability == protocol.ArtifactReadCapability {
			t.Fatal("Bridge capability leaked into model-facing tool capabilities")
		}
	}
}

func TestBridgeWebSocketInvokeRecoveryAndShutdown(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	helloCh := make(chan protocol.Message, 1)
	responses := make(chan protocol.Message, 2)
	serverErr := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/nodes/connect" || r.Header.Get("Authorization") != "Bearer test-device-token" {
			http.Error(w, "invalid bridge request", http.StatusUnauthorized)
			return
		}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer socket.Close()

		var hello protocol.Message
		if err := socket.ReadJSON(&hello); err != nil {
			serverErr <- err
			return
		}
		helloCh <- hello
		if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeReady, ProtocolVersion: protocol.ConnectionProtocolVersion, HeartbeatMS: 60_000}); err != nil {
			serverErr <- err
			return
		}

		if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolInvoke, RequestID: "unsupported", Operation: "unsupported.operation"}); err != nil {
			serverErr <- err
			return
		}
		var unsupported protocol.Message
		if err := socket.ReadJSON(&unsupported); err != nil {
			serverErr <- err
			return
		}
		responses <- unsupported

		panicArgs := []byte(`{"method":"GET","path":"/internal/runtime/status"}`)
		if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolInvoke, RequestID: "panic", Operation: protocol.OperationRuntimeRequest, Arguments: panicArgs}); err != nil {
			serverErr <- err
			return
		}
		var recovered protocol.Message
		if err := socket.ReadJSON(&recovered); err != nil {
			serverErr <- err
			return
		}
		responses <- recovered

		// 保持服务端连接打开，确保测试取消的是 Client Context，而不是依赖服务端主动断链。
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	state := &ConnectionState{}
	client := NewClient(
		Identity{Endpoint: server.URL, NodeID: "node-test", DeviceID: "device-test", DeviceToken: "test-device-token"},
		mcp.NewServer(nil, config.Config{}),
		nil,
		publicartifacts.Store{},
		state,
	)
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()

	select {
	case hello := <-helloCh:
		if hello.Type != protocol.MessageNodeHello || hello.Hello == nil || hello.Hello.DeviceID != "device-test" {
			t.Fatalf("hello = %#v", hello)
		}
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bridge hello")
	}

	for _, requestID := range []string{"unsupported", "panic"} {
		select {
		case response := <-responses:
			if response.Type != protocol.MessageToolError || response.RequestID != requestID || response.Error == nil {
				t.Fatalf("response = %#v", response)
			}
			if requestID == "panic" && (response.Error.Code != "NODE_OPERATION_FAILED" || response.Error.Category != "internal") {
				t.Fatalf("panic error = %#v", response.Error)
			}
		case err := <-serverErr:
			t.Fatal(err)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s response", requestID)
		}
	}
	if !state.Connected() {
		t.Fatal("bridge should remain connected after recovered invoke panic")
	}

	cancel()
	select {
	case <-done:
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not exit after context cancellation")
	}
	if state.Connected() {
		t.Fatal("bridge connection state remained connected after shutdown")
	}
}

func TestBridgeV4ToolCallRejectsMissingProjectExecutionContextBeforeHostAccess(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(root, ".agentdock"), AgentDockDefaultDir: filepath.Join(root, "work")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	nodeServer := mcp.NewServer(runtime, cfg)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	responseCh := make(chan protocol.Message, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			serverErr <- upgradeErr
			return
		}
		defer socket.Close()
		var hello protocol.Message
		if readErr := socket.ReadJSON(&hello); readErr != nil {
			serverErr <- readErr
			return
		}
		if writeErr := socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeReady, ProtocolVersion: protocol.ConnectionProtocolVersion, HeartbeatMS: 60_000}); writeErr != nil {
			serverErr <- writeErr
			return
		}
		arguments, marshalErr := json.Marshal(protocol.ToolCallRequest{Tool: "read_file", Arguments: map[string]any{"path": "missing.txt"}})
		if marshalErr != nil {
			serverErr <- marshalErr
			return
		}
		if writeErr := socket.WriteJSON(protocol.Message{
			Type: protocol.MessageToolInvoke, RequestID: "missing-context", Operation: protocol.OperationToolCall, Arguments: arguments,
		}); writeErr != nil {
			serverErr <- writeErr
			return
		}
		var response protocol.Message
		if readErr := socket.ReadJSON(&response); readErr != nil {
			serverErr <- readErr
			return
		}
		responseCh <- response
		for {
			if _, _, readErr := socket.ReadMessage(); readErr != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(
		Identity{Endpoint: server.URL, NodeID: "node-test", DeviceID: "device-test", DeviceToken: "test-device-token"},
		nodeServer,
		runtime,
		publicartifacts.Store{},
		&ConnectionState{},
	)
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()

	select {
	case response := <-responseCh:
		if response.Type != protocol.MessageToolError || response.Error == nil || response.Error.Code != protocol.ErrorExecutionContextRequired {
			t.Fatalf("missing execution_context response = %#v", response)
		}
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for missing execution_context rejection")
	}
	if _, statErr := os.Stat(filepath.Join(cfg.AgentDockDefaultDir, "missing.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing-context read unexpectedly touched/created host path: %v", statErr)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop after missing-context test")
	}
}

func TestBridgeV4ProjectPromptLoadReturnsCompleteApplicableAgentsChain(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(work, "backend", "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "AGENTS.md"), []byte("root rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "backend", "AGENTS.md"), []byte("backend rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AgentDockHome: filepath.Join(root, ".agentdock"), AgentDockDefaultDir: work}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	nodeServer := mcp.NewServer(runtime, cfg)

	deployment := protocol.Deployment{
		ID: "deployment-prompt", ProjectID: "project-prompt", NodeID: "node-test", WorkingFolder: work,
		Role: "test", Purpose: "Bridge Project Prompt test",
		Permissions:     protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadOnly},
		DesiredRevision: "rev-1", AppliedRevision: "rev-1", Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	resultCh := make(chan protocol.ProjectPromptLoadResult, 1)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			serverErr <- upgradeErr
			return
		}
		defer socket.Close()
		var hello protocol.Message
		if readErr := socket.ReadJSON(&hello); readErr != nil {
			serverErr <- readErr
			return
		}
		if writeErr := socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeReady, ProtocolVersion: protocol.ConnectionProtocolVersion, HeartbeatMS: 60_000}); writeErr != nil {
			serverErr <- writeErr
			return
		}

		applyArgs, marshalErr := json.Marshal(deployment)
		if marshalErr != nil {
			serverErr <- marshalErr
			return
		}
		if writeErr := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolInvoke, RequestID: "apply", Operation: protocol.OperationProjectDeploymentApply, Arguments: applyArgs}); writeErr != nil {
			serverErr <- writeErr
			return
		}
		var applyResponse protocol.Message
		if readErr := socket.ReadJSON(&applyResponse); readErr != nil {
			serverErr <- readErr
			return
		}
		if applyResponse.Type != protocol.MessageToolResult {
			serverErr <- errors.New("Project Deployment apply did not return tool.result")
			return
		}

		promptArgs, marshalErr := json.Marshal(protocol.ProjectPromptLoadRequest{DeploymentID: deployment.ID, DeploymentRevision: deployment.AppliedRevision, CWDRel: "backend/auth"})
		if marshalErr != nil {
			serverErr <- marshalErr
			return
		}
		if writeErr := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolInvoke, RequestID: "prompt", Operation: protocol.OperationProjectPromptLoad, Arguments: promptArgs}); writeErr != nil {
			serverErr <- writeErr
			return
		}
		var promptResponse protocol.Message
		if readErr := socket.ReadJSON(&promptResponse); readErr != nil {
			serverErr <- readErr
			return
		}
		if promptResponse.Type != protocol.MessageToolResult {
			serverErr <- errors.New("Project Prompt load did not return tool.result")
			return
		}
		var promptResult protocol.ProjectPromptLoadResult
		if decodeErr := json.Unmarshal(promptResponse.Result, &promptResult); decodeErr != nil {
			serverErr <- decodeErr
			return
		}
		resultCh <- promptResult
		for {
			if _, _, readErr := socket.ReadMessage(); readErr != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(
		Identity{Endpoint: server.URL, NodeID: "node-test", DeviceID: "device-test", DeviceToken: "test-device-token"},
		nodeServer,
		runtime,
		publicartifacts.Store{},
		&ConnectionState{},
	)
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()

	select {
	case result := <-resultCh:
		if result.DeploymentID != deployment.ID || result.CWDRel != "backend/auth" || !result.Prompt.Complete {
			t.Fatalf("Project Prompt result = %#v", result)
		}
		if len(result.Prompt.Sources) != 2 {
			t.Fatalf("Project Prompt sources = %#v, want root and backend", result.Prompt.Sources)
		}
		if result.Prompt.Sources[0].Path != "AGENTS.md" || result.Prompt.Sources[0].Content != "root rules\n" || result.Prompt.Sources[1].Path != "backend/AGENTS.md" || result.Prompt.Sources[1].Content != "backend rules\n" {
			t.Fatalf("Project Prompt source chain = %#v", result.Prompt.Sources)
		}
		for _, source := range result.Prompt.Sources {
			if source.Scope == "" || source.Bytes != len(source.Content) || !strings.HasPrefix(source.SHA256, "sha256:") {
				t.Fatalf("Project Prompt source metadata = %#v", source)
			}
		}
	case err := <-serverErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Project Prompt Bridge result")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop after Project Prompt test")
	}
}

func TestBridgeRunReturnsAfterCanceledDialContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient(
		Identity{Endpoint: "http://127.0.0.1:1", DeviceToken: "test-device-token"},
		mcp.NewServer(nil, config.Config{}),
		nil,
		publicartifacts.Store{},
		&ConnectionState{},
	)
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge Run did not return for canceled context")
	}
}

func TestBridgeRetiredDirectInstructionsRouteHasNoSpecialBudgetOrFallback(t *testing.T) {
	home := t.TempDir()
	cfg := config.Config{AgentDockHome: filepath.Join(home, ".agentdock"), AgentDockDefaultDir: filepath.Join(home, "work")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	client := NewClient(Identity{}, nil, runtime, publicartifacts.Store{}, &ConnectionState{})

	body := []byte(strings.Repeat("x", 64*1024+1))
	if _, err := client.dispatchRuntimeRequest(t.Context(), runtimeapi.Request{
		Method: http.MethodPost, Path: "/internal/runtime/direct-instructions", Body: body,
	}); err == nil {
		t.Fatal("retired Direct Instructions route retained a special large-body budget")
	}
	if _, err := client.dispatchRuntimeRequest(t.Context(), runtimeapi.Request{
		Method: http.MethodGet, Path: "/internal/runtime/direct-instructions",
	}); err == nil {
		t.Fatal("retired Direct Instructions route unexpectedly remained available through Bridge runtime request")
	}
}
