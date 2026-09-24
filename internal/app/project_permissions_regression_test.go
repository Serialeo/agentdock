package app

import (
	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func TestProjectFullAccessACPThroughRuntime(t *testing.T) {
	t.Setenv("GO_WANT_OUTPUT_CONTRACT_ACP_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock"),
		Builtins: builtin.Choices{ACP: true}, ACPAgentName: "helper", ACPCommand: executable,
		ACPArgs:       []string{"-test.run=^TestOutputContractACPHelper$"},
		ACPEnvFromEnv: map[string]string{"GO_WANT_OUTPUT_CONTRACT_ACP_HELPER": "GO_WANT_OUTPUT_CONTRACT_ACP_HELPER"},
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	p := protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone}
	ctx := projectContextForTest(t, runtime, root, p)
	if _, ok := runtime.ToolDefinition("acp_session"); !ok {
		t.Fatal("test ACP adapter did not become available")
	}
	if _, err := runtime.Call(ctx, "acp_session", map[string]any{"action": "info"}); err != nil {
		t.Fatalf("available ACP backend is rejected despite Node Full Access: %v", err)
	}
}

func TestProjectPermissionDenialsRemainFailClosed(t *testing.T) {
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone})
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": "missing.txt"}},
		{"list_dir", map[string]any{"path": "."}},
		{"search_text", map[string]any{"path": ".", "query": "example"}},
		{"file_edit", map[string]any{"action": "add", "path": "must-not-exist.txt", "content": "review"}},
		{"exec_command", map[string]any{"cmd": "echo denied"}},
		{"view_image", map[string]any{"path": "missing.png"}},
		{"mcp_manage", map[string]any{"action": "list"}},
		{"mcp_tool_search", map[string]any{"query": "example"}},
		{"mcp_tool_inspect", map[string]any{"name": "review:example"}},
		{"mcp_tool_call", map[string]any{"name": "review:example", "arguments": map[string]any{}}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := runtime.Call(ctx, call.name, call.args)
			assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
		})
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied edit reached disk: %v", err)
	}
}

func TestProjectFullAccessDoesNotEnableDisabledBuiltins(t *testing.T) {
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone})
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"browser_session", map[string]any{"action": "start"}},
		{"acp_session", map[string]any{"action": "info"}},
	} {
		_, err := runtime.Call(ctx, call.name, call.args)
		assertProjectErrorCode(t, err, "CAPABILITY_UNAVAILABLE")
	}
}

func TestProjectFullAccessDoesNotBypassCommandOwnership(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX cat; action permissions are covered on all platforms")
	}
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone})
	execution, _ := projectstate.ExecutionFromContext(ctx)
	result, err := runtime.Call(ctx, "exec_command", map[string]any{"cmd": "cat", "tty": true, "execution_mode": "async", "timeout_ms": 5000})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result["session_id"].(string)
	if id == "" {
		t.Fatalf("missing command session: %#v", result)
	}
	defer runtime.Call(ctx, "session_act", map[string]any{"action": "kill", "session_id": id})
	binding := protocol.ProjectTargetBindRequest{
		WorkSessionID: "other-ws", TargetID: "other-target", ProjectID: execution.Target.ProjectID, DeploymentID: execution.Target.DeploymentID,
		CWDRel: ".", DeploymentRevision: execution.Target.DeploymentRevision, ContextRevision: "other-context",
		PromptScopes: execution.Target.PromptScopes, SourceProvenance: execution.Target.SourceProvenance,
	}
	if _, err := runtime.BindProjectTarget(binding); err != nil {
		t.Fatal(err)
	}
	other, err := runtime.PrepareProjectExecution(t.Context(), executionContextForBinding(binding))
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"session_observe", map[string]any{"action": "status", "session_id": id}},
		{"session_act", map[string]any{"action": "write", "session_id": id, "chars": "not-authorized\n"}},
		{"session_act", map[string]any{"action": "kill", "session_id": id}},
	} {
		_, err := runtime.Call(other, call.name, call.args)
		assertProjectErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	}
	state, err := runtime.Call(ctx, "session_observe", map[string]any{"action": "status", "session_id": id})
	if err != nil || state["status"] != "running" {
		t.Fatalf("owner command state changed: %#v %v", state, err)
	}
}

func TestProjectBrowserDeniedWithBackendAvailable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock"), Builtins: builtin.Choices{Browser: true}, BrowserExecutablePath: executable}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, ok := runtime.ToolDefinition("browser_session"); !ok {
		t.Fatal("browser tool must be available before checking permission")
	}
	ctx := projectContextForTest(t, runtime, root, protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone})
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"browser_session", map[string]any{"action": "start", "url": "about:blank"}},
		{"browser_snapshot", map[string]any{"session_id": "not-owned"}},
		{"browser_act", map[string]any{"session_id": "not-owned", "actions": []any{map[string]any{"action": "goto", "url": "about:blank"}}}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := runtime.Call(ctx, call.name, call.args)
			assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
		})
	}
}

func TestProjectFullAccessCommandStdin(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX cat; action permissions are covered on all platforms")
	}
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone})
	result, err := runtime.Call(ctx, "exec_command", map[string]any{"cmd": "cat", "tty": true, "execution_mode": "async", "timeout_ms": 5000})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result["session_id"].(string)
	if id == "" {
		t.Fatalf("missing command session: %#v", result)
	}
	defer runtime.Call(ctx, "session_act", map[string]any{"action": "kill", "session_id": id})
	if _, err := runtime.Call(ctx, "session_act", map[string]any{"action": "write", "session_id": id, "chars": "authorized-stdin\n"}); err != nil {
		t.Fatalf("Full Access started a command but could not write stdin: %v", err)
	}
}

func TestProjectRevokedBrowserAllowsCloseButNotNewWork(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock"), Builtins: builtin.Choices{Browser: true}, BrowserExecutablePath: executable}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, fullProjectPermissionsForTest())
	execution, _ := projectstate.ExecutionFromContext(ctx)
	// 注册所属身份以验证 close 到达真实服务；不启动浏览器进程。
	runtime.rememberBrowserSession(ctx, "no-such-session")
	runtime.RevokeProjectTarget(execution.Target.TargetID)
	historical, err := runtime.PrepareProjectSessionControlExecution(t.Context(), &execution.Context)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Call(historical, "browser_session", map[string]any{"action": "close", "session_id": "no-such-session"})
	if err != nil || result["code"] != "SESSION_NOT_FOUND" {
		t.Fatalf("close did not reach session ownership lookup: %#v %v", result, err)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"browser_session", map[string]any{"action": "start", "url": "about:blank"}},
		{"browser_snapshot", map[string]any{"session_id": "no-such-session"}},
		{"browser_act", map[string]any{"session_id": "no-such-session", "actions": []any{map[string]any{"action": "goto", "url": "about:blank"}}, "close_after": true}},
	} {
		_, err := runtime.Call(historical, call.name, call.args)
		assertProjectErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	}
}
