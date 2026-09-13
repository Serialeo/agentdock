package app

import (
	"context"
	"errors"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func projectAuthorizationContextForTest(targetID string, permissions protocol.DeploymentPermissions) context.Context {
	revision := "rev-1"
	execution := projectstate.Execution{
		Deployment: protocol.Deployment{
			ID: "deployment-1", ProjectID: "project-1", NodeID: "node-1",
			AppliedRevision: revision, Permissions: permissions, Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
		},
		Target: projectstate.TargetBinding{
			WorkSessionID: "ws-1", TargetID: targetID, ProjectID: "project-1", DeploymentID: "deployment-1",
			DeploymentRevision: revision, ContextRevision: "ctx-1",
		},
		Permissions: permissions,
	}
	return projectstate.WithExecution(context.Background(), execution)
}

func requireAppToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error %T %v is not ToolError", err, err)
	}
	if toolErr.Code != code {
		t.Fatalf("error code = %q, want %q (%v)", toolErr.Code, code, err)
	}
}

func TestProjectCapabilityMatrixUsesTrustedExecutionContext(t *testing.T) {
	runtime := &Runtime{}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "read_file", args: map[string]any{"path": "README.md"}},
		{name: "file_edit", args: map[string]any{"action": "add", "path": "standalone.txt"}},
		{name: "exec_command", args: map[string]any{"cmd": "true"}},
		{name: "browser_session", args: map[string]any{"action": "start"}},
		{name: "browser_snapshot", args: map[string]any{"session_id": "standalone"}},
		{name: "mcp_tool_call", args: map[string]any{"name": "demo:tool"}},
		{name: "acp_session", args: map[string]any{"action": "list"}},
	} {
		if err := runtime.authorizeProjectTool(context.Background(), call.name, call.args); err != nil {
			t.Fatalf("standalone %s unexpectedly required Project execution context: %v", call.name, err)
		}
	}

	readOnly := protocol.DeploymentPermissions{Files: protocol.FileCapabilityReadOnly}
	readOnlyCtx := projectAuthorizationContextForTest("target-read-only", readOnly)
	if err := runtime.authorizeProjectTool(readOnlyCtx, "read_file", map[string]any{"path": "README.md"}); err != nil {
		t.Fatalf("read_file denied for read_only: %v", err)
	}
	if err := runtime.authorizeProjectTool(readOnlyCtx, "file_edit", map[string]any{"action": "add"}); err == nil {
		t.Fatal("file_edit unexpectedly allowed for read_only")
	} else {
		requireAppToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}

	full := fullProjectPermissionsForTest()
	fullCtx := projectAuthorizationContextForTest("target-full", full)
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "file_edit", args: map[string]any{"action": "add"}},
		{name: "exec_command", args: map[string]any{"cmd": "true"}},
		{name: "browser_session", args: map[string]any{"action": "start"}},
		{name: "mcp_tool_call", args: map[string]any{"name": "demo:tool"}},
	} {
		if err := runtime.authorizeProjectTool(fullCtx, call.name, call.args); err != nil {
			t.Fatalf("%s denied with full permissions: %v", call.name, err)
		}
	}

	nodeFullAccess := protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone}
	nodeFullAccessCtx := projectAuthorizationContextForTest("target-node-full-access", nodeFullAccess)
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "read_file", args: map[string]any{"path": "/tmp/example"}},
		{name: "file_edit", args: map[string]any{"action": "add", "path": "/tmp/example"}},
		{name: "exec_command", args: map[string]any{"cmd": "true", "workdir": "/tmp"}},
		{name: "browser_session", args: map[string]any{"action": "start"}},
		{name: "mcp_tool_call", args: map[string]any{"name": "demo:tool"}},
	} {
		if err := runtime.authorizeProjectTool(nodeFullAccessCtx, call.name, call.args); err != nil {
			t.Fatalf("%s denied by fine-grained permissions while Node Full Access is enabled: %v", call.name, err)
		}
	}
	if err := runtime.authorizeProjectTool(nodeFullAccessCtx, "browser_session", map[string]any{"action": "cleanup_stale"}); err == nil {
		t.Fatal("retired Project action unexpectedly restored by Node Full Access")
	}

	denied := protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone}
	deniedCtx := projectAuthorizationContextForTest("target-denied", denied)
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "read_file", args: map[string]any{"path": "README.md"}},
		{name: "exec_command", args: map[string]any{"cmd": "true"}},
		{name: "browser_session", args: map[string]any{"action": "start"}},
		{name: "mcp_tool_call", args: map[string]any{"name": "demo:tool"}},
	} {
		if err := runtime.authorizeProjectTool(deniedCtx, call.name, call.args); err == nil {
			t.Fatalf("%s unexpectedly allowed without capability", call.name)
		} else {
			requireAppToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
		}
	}
}

func TestProjectSpecialSessionControlsDoNotRestoreGeneralExecution(t *testing.T) {
	runtime := &Runtime{}
	permissions := protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone}
	ctx := projectAuthorizationContextForTest("target-1", permissions)

	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{name: "session_observe", args: map[string]any{"action": "list"}},
		{name: "session_act", args: map[string]any{"action": "kill", "session_id": "cmd"}},
		{name: "acp_session", args: map[string]any{"action": "close", "session_id": "acp"}},
		{name: "browser_session", args: map[string]any{"action": "close", "session_id": "browser"}},
	} {
		if err := runtime.authorizeProjectTool(ctx, call.name, call.args); err != nil {
			t.Fatalf("cleanup control %s denied before owner check: %v", call.name, err)
		}
	}
	if err := runtime.authorizeProjectTool(ctx, "browser_session", map[string]any{"action": "cleanup_stale"}); err == nil {
		t.Fatal("Project browser cleanup_stale unexpectedly allowed")
	} else {
		requireAppToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}
}

func TestBrowserSessionOwnerIsTargetScoped(t *testing.T) {
	permissions := fullProjectPermissionsForTest()
	ctxA := projectAuthorizationContextForTest("target-a", permissions)
	ctxB := projectAuthorizationContextForTest("target-b", permissions)
	executionA, _ := projectstate.ExecutionFromContext(ctxA)
	runtime := &Runtime{
		browserOwners: map[string]browserSessionOwner{
			"browser-1": browserOwnerFromExecution(executionA),
		},
	}
	if err := runtime.requireBrowserSessionOwner(context.Background(), "standalone-browser"); err != nil {
		t.Fatalf("standalone browser session unexpectedly required Project ownership: %v", err)
	}
	if err := runtime.requireBrowserSessionOwner(ctxA, "browser-1"); err != nil {
		t.Fatalf("owner Target rejected: %v", err)
	}
	if err := runtime.requireBrowserSessionOwner(ctxB, "browser-1"); err == nil {
		t.Fatal("different Target reused browser session")
	} else {
		requireAppToolErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	}
}
