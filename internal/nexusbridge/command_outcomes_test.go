package nexusbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/gorilla/websocket"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

// Use the real private Bridge and Runtime, including on-disk command state.
func TestBridgeCommandOutcomesSurviveLostAckAndRuntimeRestart(t *testing.T) {
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
	deployment := protocol.Deployment{
		ID: "outcome-deployment", ProjectID: "outcome-project", NodeID: "outcome-node", WorkingFolder: cfg.AgentDockDefaultDir,
		Role: "test", Purpose: "durable outcome test", Permissions: protocol.DeploymentPermissions{Computer: protocol.ComputerPermissionNone, Files: protocol.FileCapabilityReadWrite, Shell: true},
		DesiredRevision: "rev-1", AppliedRevision: "rev-1", Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
	}
	if _, err := runtime.ApplyProjectDeployment(deployment); err != nil {
		t.Fatal(err)
	}
	prompt, err := runtime.LoadProjectPrompt(protocol.ProjectPromptLoadRequest{DeploymentID: deployment.ID, DeploymentRevision: "rev-1", CWDRel: "."})
	if err != nil {
		t.Fatal(err)
	}
	binding := protocol.ProjectTargetBindRequest{
		WorkSessionID: "outcome-work-session", TargetID: "outcome-target", ProjectID: deployment.ProjectID, DeploymentID: deployment.ID,
		DeploymentRevision: "rev-1", ContextRevision: "context-1", CWDRel: ".",
		PromptScopes: []protocol.PromptScopeRevision{{Scope: prompt.CWDRel, PromptRevision: prompt.Prompt.PromptRevision}}, SourceProvenance: prompt.SourceProvenance,
	}
	if _, err := runtime.BindProjectTarget(binding); err != nil {
		t.Fatal(err)
	}
	ctx, err := runtime.PrepareProjectExecution(context.Background(), &protocol.ExecutionContext{
		WorkSessionID: binding.WorkSessionID, TargetID: binding.TargetID, ProjectID: binding.ProjectID, DeploymentID: binding.DeploymentID,
		DeploymentRevision: binding.DeploymentRevision, ContextRevision: binding.ContextRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(ctx, "exec_command", map[string]any{"cmd": "echo bridge-result", "execution_mode": "sync", "request_id": "bridge-once"})
	if err != nil || result["command_ok"] != true {
		t.Fatalf("exec=%+v %v", result, err)
	}
	sessionID := result["session_id"].(string)
	if _, err := runtime.Call(context.Background(), "exec_command", map[string]any{"cmd": "echo standalone-result", "execution_mode": "sync"}); err != nil {
		t.Fatal(err)
	}
	var fact protocol.CommandOutcome
	// Read twice before ACK, then ACK twice: a disconnected collector may repeat
	// either operation, without losing facts or changing the event identity.
	t.Run("private bridge", func(t *testing.T) {
		invoke := commandOutcomeBridgeForTest(t, mcp.NewServer(runtime, cfg), true)
		for range 2 {
			response := invoke(protocol.OperationCommandOutcomesRead, protocol.CommandOutcomesReadRequest{PendingOnly: true})
			var read protocol.CommandOutcomesReadResult
			decodeOutcomeResponse(t, response, &read)
			if len(read.Outcomes) != 1 || read.Outcomes[0].CommandSessionID != sessionID || !strings.Contains(read.Outcomes[0].Stdout, "bridge-result") || !read.Outcomes[0].PendingReport {
				t.Fatalf("pending read leaked local command or lost Project result: %+v", read)
			}
			if fact.EventID != "" && !reflect.DeepEqual(fact, read.Outcomes[0]) {
				t.Fatal("unacknowledged delivery changed the outcome")
			}
			fact = read.Outcomes[0]
		}
		for range 2 {
			var ack protocol.CommandOutcomesAckResult
			decodeOutcomeResponse(t, invoke(protocol.OperationCommandOutcomesAck, protocol.CommandOutcomesAckRequest{EventIDs: []string{fact.EventID}}), &ack)
			if !reflect.DeepEqual(ack.AcknowledgedEventIDs, []string{fact.EventID}) {
				t.Fatalf("ack=%+v", ack)
			}
		}
		var pending protocol.CommandOutcomesReadResult
		decodeOutcomeResponse(t, invoke(protocol.OperationCommandOutcomesRead, protocol.CommandOutcomesReadRequest{PendingOnly: true}), &pending)
		if len(pending.Outcomes) != 0 {
			t.Fatalf("ACK did not drain pending results: %+v", pending)
		}
		var explicit protocol.CommandOutcomesReadResult
		decodeOutcomeResponse(t, invoke(protocol.OperationCommandOutcomesRead, protocol.CommandOutcomesReadRequest{PendingOnly: true, CommandSessionIDs: []string{sessionID}}), &explicit)
		fact.PendingReport = false
		if len(explicit.Outcomes) != 1 || !reflect.DeepEqual(explicit.Outcomes[0], fact) {
			t.Fatalf("ACK mutated terminal facts: %+v", explicit)
		}
		invalid := invoke(protocol.OperationCommandOutcomesRead, map[string]any{"node_id": "untrusted-node"})
		if invalid.Type != protocol.MessageToolError {
			t.Fatalf("unexpected node selector accepted: %+v", invalid)
		}
	})
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	t.Run("restart", func(t *testing.T) {
		invoke := commandOutcomeBridgeForTest(t, mcp.NewServer(reopened, cfg), true)
		var read protocol.CommandOutcomesReadResult
		decodeOutcomeResponse(t, invoke(protocol.OperationCommandOutcomesRead, protocol.CommandOutcomesReadRequest{CommandSessionIDs: []string{sessionID}}), &read)
		if len(read.Outcomes) != 1 || !reflect.DeepEqual(read.Outcomes[0], fact) {
			t.Fatalf("restarted runtime lost acknowledged result: %+v", read)
		}
	})
	t.Run("optional extension", func(t *testing.T) {
		// The embedded mandatory interface intentionally hides the optional methods.
		node := struct{ NodeAPI }{mcp.NewServer(reopened, cfg)}
		invoke := commandOutcomeBridgeForTest(t, node, false)
		response := invoke(protocol.OperationCommandOutcomesRead, protocol.CommandOutcomesReadRequest{})
		if response.Type != protocol.MessageToolError || response.Error == nil || response.Error.Code != "BRIDGE_CAPABILITY_UNAVAILABLE" {
			t.Fatalf("optional API=%+v", response)
		}
	})
}

func decodeOutcomeResponse(t *testing.T, response protocol.Message, dst any) {
	t.Helper()
	if response.Type != protocol.MessageToolResult {
		t.Fatalf("Bridge operation failed: %+v", response)
	}
	if err := json.Unmarshal(response.Result, dst); err != nil {
		t.Fatal(err)
	}
}

func commandOutcomeBridgeForTest(t *testing.T, node NodeAPI, supportsOutcomes bool) func(string, any) protocol.Message {
	t.Helper()
	sockets := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer outcome-device-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer socket.Close()
		sockets <- socket
		<-release
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(Identity{Endpoint: server.URL, NodeID: "outcome-node", DeviceID: "outcome-device", DeviceToken: "outcome-device-token"}, node, nil, publicartifacts.Store{}, &ConnectionState{})
	done := make(chan struct{})
	go func() { _ = client.connect(ctx); close(done) }()
	var socket *websocket.Conn
	t.Cleanup(func() {
		cancel()
		close(release)
		if socket != nil {
			_ = socket.Close()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Bridge failed to stop")
		}
	})
	select {
	case socket = <-sockets:
	case <-time.After(5 * time.Second):
		t.Fatal("Bridge failed to connect")
	}
	_ = socket.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello protocol.Message
	if err := socket.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != protocol.MessageNodeHello || hello.Hello == nil {
		t.Fatalf("hello=%+v", hello)
	}
	if slices.Contains(hello.Hello.BridgeCapabilities, protocol.CommandOutcomesCapability) != supportsOutcomes {
		t.Fatalf("optional capability=%+v", hello.Hello)
	}
	for _, capability := range hello.Hello.Capabilities {
		if capability == protocol.CommandOutcomesCapability || strings.HasPrefix(capability, "command.outcomes.") {
			t.Fatal("private operation leaked into model tools")
		}
	}
	if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeReady, ProtocolVersion: protocol.ConnectionProtocolVersion, HeartbeatMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	return func(operation string, request any) protocol.Message {
		t.Helper()
		arguments, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = socket.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := socket.WriteJSON(protocol.Message{Type: protocol.MessageToolInvoke, RequestID: "outcome-request", Operation: operation, Arguments: arguments}); err != nil {
			t.Fatal(err)
		}
		var response protocol.Message
		if err := socket.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if response.RequestID != "outcome-request" {
			t.Fatalf("wrong response identity: %+v", response)
		}
		return response
	}
}
