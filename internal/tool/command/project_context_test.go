package command

import (
	"context"
	"errors"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func commandProjectExecutionForTest(targetID string, shell bool) projectstate.Execution {
	permissions := protocol.DeploymentPermissions{Files: protocol.FileCapabilityReadOnly, Shell: shell}
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

func requireCommandToolErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error %T %v is not ToolError", err, err)
	}
	if toolErr.Code != code {
		t.Fatalf("error code = %q, want %q (%v)", toolErr.Code, code, err)
	}
}

func TestCommandSessionOwnerIsTargetScoped(t *testing.T) {
	commandSession := &session.Session{}
	commandSession.SetExecutionContext(session.ExecutionContext{
		WorkSessionID: "ws-1", TargetID: "target-a", ProjectID: "project-1", DeploymentID: "deployment-1", NodeID: "node-1",
	})
	ownerCtx := projectstate.WithExecution(context.Background(), commandProjectExecutionForTest("target-a", true))
	if _, err := requireSessionOwner(ownerCtx, commandSession); err != nil {
		t.Fatalf("owner Target rejected: %v", err)
	}
	otherCtx := projectstate.WithExecution(context.Background(), commandProjectExecutionForTest("target-b", true))
	if _, err := requireSessionOwner(otherCtx, commandSession); err == nil {
		t.Fatal("different Target reused command session")
	} else {
		requireCommandToolErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	}
}

func TestCommandStdinRequiresCurrentShellButCleanupCanKeepOwnership(t *testing.T) {
	execution := commandProjectExecutionForTest("target-a", true)
	if err := requireSessionStdinPermission(execution); err != nil {
		t.Fatalf("current shell permission rejected: %v", err)
	}

	execution.Permissions.Shell = false
	execution.Deployment.Permissions.Shell = false
	if err := requireSessionStdinPermission(execution); err == nil {
		t.Fatal("stdin remained available after shell permission was disabled")
	} else {
		requireCommandToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}

	execution = commandProjectExecutionForTest("target-a", true)
	execution.Deployment.AppliedRevision = "rev-2"
	if err := requireSessionStdinPermission(execution); err == nil {
		t.Fatal("stdin remained available with stale Deployment revision")
	} else {
		requireCommandToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}

	execution = commandProjectExecutionForTest("target-a", true)
	execution.Target.Revoked = true
	if err := requireSessionStdinPermission(execution); err == nil {
		t.Fatal("stdin remained available for revoked Target")
	} else {
		requireCommandToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
	}
}

func TestCommandFullAccessStdinKeepsRevocationChecks(t *testing.T) {
	for _, state := range []string{"current", "disabled", "stale", "revoked"} {
		t.Run(state, func(t *testing.T) {
			execution := commandProjectExecutionForTest("target-a", false)
			execution.Permissions.FullAccess = true
			switch state {
			case "disabled":
				execution.Permissions.FullAccess = false
			case "stale":
				execution.Deployment.AppliedRevision = "rev-2"
			case "revoked":
				execution.Target.Revoked = true
			}
			err := requireSessionStdinPermission(execution)
			if state == "current" {
				if err != nil {
					t.Fatalf("Full Access stdin rejected: %v", err)
				}
			} else {
				requireCommandToolErrorCode(t, err, protocol.ErrorCapabilityDenied)
			}
		})
	}
}
