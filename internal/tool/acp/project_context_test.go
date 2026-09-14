package acp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"github.com/uvwt/agentdock/internal/workspace"
)

func acpProjectExecutionForTest(targetID string, allowed bool) projectstate.Execution {
	permissions := protocol.DeploymentPermissions{Files: protocol.FileCapabilityReadOnly, ACP: allowed}
	return projectstate.Execution{
		Deployment: protocol.Deployment{
			ID: "deployment-1", ProjectID: "project-1", NodeID: "node-1",
			AppliedRevision: "rev-1", Permissions: permissions, Enabled: true, ApplyStatus: protocol.DeploymentApplyApplied,
		},
		Target: projectstate.TargetBinding{
			WorkSessionID: "ws-1", TargetID: targetID, ProjectID: "project-1", DeploymentID: "deployment-1",
			DeploymentRevision: "rev-1", ContextRevision: "ctx-1",
		},
		Permissions: permissions,
	}
}

func requireACPToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error %T %v is not ToolError", err, err)
	}
	if toolErr.Code != code {
		t.Fatalf("error code = %q, want %q (%v)", toolErr.Code, code, err)
	}
}

func TestACPCurrentPermissionAndOwnerAreIndependent(t *testing.T) {
	service := &Service{}
	execution := acpProjectExecutionForTest("target-a", true)
	ctx := projectstate.WithExecution(context.Background(), execution)
	if _, err := service.requireCurrentACP(ctx); err != nil {
		t.Fatalf("current ACP permission rejected: %v", err)
	}

	record := acpruntime.SessionRecord{
		WorkSessionID: "ws-1", TargetID: "target-a", ProjectID: "project-1", DeploymentID: "deployment-1", NodeID: "node-1",
	}
	if !sessionOwnedByACPExecution(execution, record) {
		t.Fatal("matching ACP owner was not recognized")
	}
	other := acpProjectExecutionForTest("target-b", true)
	if sessionOwnedByACPExecution(other, record) {
		t.Fatal("different Target reused ACP session")
	}

	execution.Permissions.ACP = false
	execution.Deployment.Permissions.ACP = false
	ctx = projectstate.WithExecution(context.Background(), execution)
	if _, err := service.requireCurrentACP(ctx); err == nil {
		t.Fatal("ACP execution remained available after permission was disabled")
	} else {
		requireACPToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}

	execution = acpProjectExecutionForTest("target-a", true)
	execution.Deployment.AppliedRevision = "rev-2"
	ctx = projectstate.WithExecution(context.Background(), execution)
	if _, err := service.requireCurrentACP(ctx); err == nil {
		t.Fatal("ACP execution remained available for stale Deployment revision")
	} else {
		requireACPToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}

	execution = acpProjectExecutionForTest("target-a", true)
	execution.Target.Revoked = true
	ctx = projectstate.WithExecution(context.Background(), execution)
	if _, err := service.requireCurrentACP(ctx); err == nil {
		t.Fatal("ACP execution remained available for revoked Target")
	} else {
		requireACPToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}
}

func TestACPRelativeDirectoryUsesTargetScopedCWD(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "project", "backend")
	if err := os.MkdirAll(filepath.Join(target, "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{ws: ws}
	execution := acpProjectExecutionForTest("target-a", true)
	ctx := projectstate.WithExecution(context.Background(), execution)
	ctx, err = workspace.WithCWD(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.resolveACPDirectory(ctx, "auth")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(target, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("ACP relative cwd = %q, want %q", resolved, want)
	}
	if ws.DefaultCWD() == target {
		t.Fatal("ACP scoped cwd mutated Workspace default")
	}
}
