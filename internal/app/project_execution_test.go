package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func TestStandaloneExecutionDoesNotRequireProjectContext(t *testing.T) {
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	if err := os.WriteFile(filepath.Join(root, "project.txt"), []byte("project\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Call(context.Background(), "read_file", map[string]any{"path": "project.txt"}); err != nil {
		t.Fatalf("standalone read_file: %v", err)
	}
	if _, err := runtime.Call(context.Background(), "file_edit", map[string]any{
		"action": "replace", "path": "project.txt", "old": "project", "new": "changed", "dry_run": true,
	}); err != nil {
		t.Fatalf("standalone file_edit: %v", err)
	}
	if _, err := runtime.Call(context.Background(), "exec_command", map[string]any{"cmd": commandNoopForTest(), "execution_mode": "sync"}); err != nil {
		t.Fatalf("standalone exec_command: %v", err)
	}
	if _, err := runtime.Call(context.Background(), "mcp_manage", map[string]any{"action": "list"}); err != nil {
		t.Fatalf("standalone mcp_manage: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "project.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "project\n" {
		t.Fatalf("standalone dry-run file_edit changed file: %q", contents)
	}
}

func TestProjectFilePermissionMatrix(t *testing.T) {
	for _, test := range []struct {
		name       string
		files      protocol.FileCapability
		readOK     bool
		fileEditOK bool
	}{
		{name: "none", files: protocol.FileCapabilityNone},
		{name: "read_only", files: protocol.FileCapabilityReadOnly, readOK: true},
		{name: "read_write", files: protocol.FileCapabilityReadWrite, readOK: true, fileEditOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, root := newCodeToolsRuntime(t)
			defer runtime.Close()
			if err := os.WriteFile(filepath.Join(root, "permission.txt"), []byte("before\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			permissions := fullProjectPermissionsForTest()
			permissions.Files = test.files
			ctx := projectContextForTest(t, runtime, root, permissions)

			_, readErr := runtime.Call(ctx, "read_file", map[string]any{"path": "permission.txt"})
			if test.readOK {
				if readErr != nil {
					t.Fatalf("read_file: %v", readErr)
				}
			} else {
				assertProjectErrorCode(t, readErr, protocol.ErrorCapabilityDenied)
			}

			_, editErr := runtime.Call(ctx, "file_edit", map[string]any{
				"action": "replace", "path": "permission.txt", "old": "before", "new": "after", "dry_run": true,
			})
			if test.fileEditOK {
				if editErr != nil {
					t.Fatalf("file_edit: %v", editErr)
				}
			} else {
				assertProjectErrorCode(t, editErr, protocol.ErrorCapabilityDenied)
			}
		})
	}
}

func TestProjectNodeFullAccessIsIndependentFromConfiguredFolder(t *testing.T) {
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	projectFolder := filepath.Join(root, "project")
	if err := os.MkdirAll(projectFolder, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	permissions := protocol.DeploymentPermissions{FullAccess: true, Files: protocol.FileCapabilityNone}
	ctx := projectContextForTest(t, runtime, projectFolder, permissions)

	if _, err := runtime.Call(ctx, "read_file", map[string]any{"path": outsideFile}); err != nil {
		t.Fatalf("Full Access did not allow absolute file read outside Project Folder: %v", err)
	}
	if _, err := runtime.Call(ctx, "file_edit", map[string]any{
		"action": "replace", "path": outsideFile, "old": "outside", "new": "changed", "dry_run": true,
	}); err != nil {
		t.Fatalf("Full Access did not allow absolute file edit outside Project Folder: %v", err)
	}
	result, err := runtime.Call(ctx, "exec_command", map[string]any{
		"cmd": commandNoopForTest(), "workdir": outside, "execution_mode": "sync",
	})
	if err != nil {
		t.Fatalf("Full Access did not allow command workdir outside Project Folder: %v", err)
	}
	if got, _ := result["workdir"].(string); got == "" {
		t.Fatalf("Full Access command did not report workdir: %#v", result)
	}
}

func TestProjectBooleanCapabilitiesRejectAtRuntimeBoundary(t *testing.T) {
	t.Run("shell", func(t *testing.T) {
		runtime, root := newCodeToolsRuntime(t)
		defer runtime.Close()
		permissions := fullProjectPermissionsForTest()
		permissions.Shell = false
		ctx := projectContextForTest(t, runtime, root, permissions)
		_, err := runtime.Call(ctx, "exec_command", map[string]any{"cmd": commandNoopForTest()})
		assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
	})

	t.Run("dynamic_mcp", func(t *testing.T) {
		runtime, root := newCodeToolsRuntime(t)
		defer runtime.Close()
		permissions := fullProjectPermissionsForTest()
		permissions.DynamicMCP = false
		ctx := projectContextForTest(t, runtime, root, permissions)
		_, err := runtime.Call(ctx, "mcp_manage", map[string]any{"action": "list"})
		assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
	})

	t.Run("browser", func(t *testing.T) {
		executable, _ := os.Executable()
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
		permissions := fullProjectPermissionsForTest()
		permissions.Browser = false
		ctx := projectContextForTest(t, runtime, root, permissions)
		_, err = runtime.Call(ctx, "browser_session", map[string]any{"action": "cleanup_stale"})
		assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
	})

	t.Run("acp", func(t *testing.T) {
		t.Setenv("GO_WANT_OUTPUT_CONTRACT_ACP_HELPER", "1")
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		cfg := config.Config{
			AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock"),
			Builtins: builtin.Choices{ACP: true}, ACPAgentName: "helper", ACPCommand: executable, ACPArgs: []string{"-test.run=^TestOutputContractACPHelper$"}, ACPEnvFromEnv: map[string]string{"GO_WANT_OUTPUT_CONTRACT_ACP_HELPER": "GO_WANT_OUTPUT_CONTRACT_ACP_HELPER"},
		}
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
		runtime, err := NewRuntime(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		permissions := fullProjectPermissionsForTest()
		permissions.ACP = false
		ctx := projectContextForTest(t, runtime, root, permissions)
		_, err = runtime.Call(ctx, "acp_session", map[string]any{"action": "info"})
		assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
	})
}

func TestProjectCommandSessionOwnershipAndPermissionTightening(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("test command uses POSIX sleep")
	}
	runtime, root := newCodeToolsRuntime(t)
	defer runtime.Close()
	ctx := projectContextForTest(t, runtime, root, fullProjectPermissionsForTest())
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok {
		t.Fatal("prepared Project execution missing from context")
	}

	started, err := runtime.Call(ctx, "exec_command", map[string]any{
		"cmd": "sleep 30", "execution_mode": "async", "timeout_ms": 60000,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := started["session_id"].(string)
	if sessionID == "" || started["target_id"] != execution.Target.TargetID || started["node_id"] != execution.Deployment.NodeID {
		t.Fatalf("command session identity = %#v", started)
	}

	secondBinding := protocol.ProjectTargetBindRequest{
		WorkSessionID:      "test-work-session-2",
		TargetID:           "test-target-2",
		ProjectID:          execution.Target.ProjectID,
		DeploymentID:       execution.Target.DeploymentID,
		CWDRel:             ".",
		DeploymentRevision: execution.Target.DeploymentRevision,
		ContextRevision:    "test-context-rev-2",
		PromptScopes:       append([]protocol.PromptScopeRevision(nil), execution.Target.PromptScopes...),
		SourceProvenance:   execution.Target.SourceProvenance,
	}
	if _, err := runtime.BindProjectTarget(secondBinding); err != nil {
		t.Fatal(err)
	}
	secondCtx, err := runtime.PrepareProjectExecution(context.Background(), &protocol.ExecutionContext{
		WorkSessionID:      secondBinding.WorkSessionID,
		TargetID:           secondBinding.TargetID,
		ProjectID:          secondBinding.ProjectID,
		DeploymentID:       secondBinding.DeploymentID,
		DeploymentRevision: secondBinding.DeploymentRevision,
		ContextRevision:    secondBinding.ContextRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Call(secondCtx, "session_observe", map[string]any{"action": "status", "session_id": sessionID})
	assertProjectErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	_, err = runtime.Call(secondCtx, "session_act", map[string]any{"action": "kill", "session_id": sessionID})
	assertProjectErrorCode(t, err, protocol.ErrorSessionTargetDenied)

	updated := execution.Deployment
	updated.DesiredRevision = "test-rev-2"
	updated.AppliedRevision = "test-rev-2"
	updated.Permissions.Shell = false
	if _, err := runtime.ApplyProjectDeployment(updated); err != nil {
		t.Fatal(err)
	}

	_, err = runtime.Call(ctx, "exec_command", map[string]any{"cmd": "true"})
	assertProjectErrorCode(t, err, protocol.ErrorRevisionConflict)
	_, err = runtime.Call(ctx, "session_act", map[string]any{"action": "write", "session_id": sessionID, "chars": "ignored\n"})
	assertProjectErrorCode(t, err, protocol.ErrorCapabilityDenied)
	killed, err := runtime.Call(ctx, "session_act", map[string]any{"action": "kill", "session_id": sessionID})
	if err != nil {
		t.Fatalf("owner could not terminate pre-existing command after permission tightening: %v", err)
	}
	if killed["status"] != "killed" && killed["status"] != "exited" {
		t.Fatalf("kill result = %#v", killed)
	}
}

func assertProjectErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s", code)
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		if toolErr.Code != code {
			t.Fatalf("ToolError code = %q, want %q: %v", toolErr.Code, code, err)
		}
		return
	}
	var remoteErr *protocol.RemoteError
	if errors.As(err, &remoteErr) {
		if remoteErr.Code != code {
			t.Fatalf("RemoteError code = %q, want %q: %v", remoteErr.Code, code, err)
		}
		return
	}
	t.Fatalf("error %T does not carry code %s: %v", err, code, err)
}
