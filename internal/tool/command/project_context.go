package command

import (
	"context"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func projectExecution(ctx context.Context) (projectstate.Execution, bool) {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	return execution, ok
}

func sessionOwnedByExecution(execution projectstate.Execution, commandExecution session.ExecutionContext) bool {
	return commandExecution.WorkSessionID != "" &&
		commandExecution.WorkSessionID == execution.Target.WorkSessionID &&
		commandExecution.TargetID == execution.Target.TargetID &&
		commandExecution.ProjectID == execution.Target.ProjectID &&
		commandExecution.DeploymentID == execution.Target.DeploymentID
}

func requireSessionOwner(ctx context.Context, commandSession *session.Session) (projectstate.Execution, error) {
	execution, scoped := projectExecution(ctx)
	if !scoped {
		// Internal lifecycle/tests do not use a model WorkSession. All model-facing
		// Runtime paths attach a verified Project execution before entering here.
		return projectstate.Execution{}, nil
	}
	if commandSession == nil || !sessionOwnedByExecution(execution, commandSession.ExecutionContext()) {
		return projectstate.Execution{}, toolErrorDetails(
			protocol.ErrorSessionTargetDenied,
			"command session does not belong to the current Project Target",
			"authorization",
			map[string]any{"target_id": execution.Target.TargetID},
		)
	}
	return execution, nil
}

func requireSessionStdinPermission(execution projectstate.Execution) error {
	if execution.Target.TargetID == "" {
		return nil
	}
	// Full Access 覆盖细粒度回退值，但不覆盖 Target 撤销或版本失效。
	currentRevision := execution.Deployment.AppliedRevision
	if execution.Target.Revoked || currentRevision == "" || currentRevision != execution.Target.DeploymentRevision || !(execution.Permissions.FullAccess || execution.Permissions.Shell) {
		return toolErrorDetails(
			protocol.ErrorCapabilityDenied,
			"current Project Deployment no longer allows command stdin",
			"authorization",
			map[string]any{"target_id": execution.Target.TargetID, "deployment_id": execution.Target.DeploymentID},
		)
	}
	return nil
}
