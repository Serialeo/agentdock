package acp

import (
	"context"
	"fmt"

	protocol "github.com/Serialeo/agentdock-protocol"
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func acpProjectExecution(ctx context.Context) (projectstate.Execution, bool) {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	return execution, ok
}

func (s *Service) requireCurrentACP(ctx context.Context) (projectstate.Execution, error) {
	execution, scoped := acpProjectExecution(ctx)
	if !scoped {
		return projectstate.Execution{}, nil
	}
	// Full Access 覆盖细粒度回退值，但不覆盖 Target 撤销或版本失效。
	currentRevision := execution.Deployment.AppliedRevision
	if execution.Target.Revoked || currentRevision == "" || currentRevision != execution.Target.DeploymentRevision || !(execution.Permissions.FullAccess || execution.Permissions.ACP) {
		return projectstate.Execution{}, &ToolError{
			Code: protocol.ErrorCapabilityDenied, Message: "current Project Deployment does not allow ACP execution", Category: "authorization",
			Details: map[string]any{"target_id": execution.Target.TargetID, "deployment_id": execution.Target.DeploymentID},
		}
	}
	return execution, nil
}

func sessionOwnedByACPExecution(execution projectstate.Execution, record acpruntime.SessionRecord) bool {
	return record.WorkSessionID != "" &&
		record.WorkSessionID == execution.Target.WorkSessionID &&
		record.TargetID == execution.Target.TargetID &&
		record.ProjectID == execution.Target.ProjectID &&
		record.DeploymentID == execution.Target.DeploymentID
}

func (s *Service) requireACPSessionOwner(ctx context.Context, sessionID string) (acpruntime.SessionRecord, error) {
	record, err := s.manager.InspectSession(sessionID)
	if err != nil {
		return acpruntime.SessionRecord{}, acpToolError(err)
	}
	execution, scoped := acpProjectExecution(ctx)
	if !scoped {
		return record, nil
	}
	if !sessionOwnedByACPExecution(execution, record) {
		return acpruntime.SessionRecord{}, &ToolError{
			Code: protocol.ErrorSessionTargetDenied, Message: "ACP session does not belong to the current Project Target", Category: "authorization",
			Details: map[string]any{"session_id": sessionID, "target_id": execution.Target.TargetID},
		}
	}
	return record, nil
}

func (s *Service) requireACPRunOwner(ctx context.Context, runID string) (string, error) {
	sessionID, err := s.manager.RunSessionID(runID)
	if err != nil {
		return "", acpToolError(err)
	}
	if _, err := s.requireACPSessionOwner(ctx, sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Service) requireACPInteractionOwner(ctx context.Context, interactionID string) (acpruntime.Interaction, error) {
	interaction, err := s.manager.InspectInteraction(interactionID)
	if err != nil {
		return acpruntime.Interaction{}, acpToolError(err)
	}
	if _, err := s.requireACPSessionOwner(ctx, interaction.SessionID); err != nil {
		return acpruntime.Interaction{}, err
	}
	return interaction, nil
}

func (s *Service) withNewACPSessionOwnership(ctx context.Context) context.Context {
	execution, scoped := acpProjectExecution(ctx)
	if !scoped {
		return ctx
	}
	return acpruntime.WithSessionOwnership(ctx, acpruntime.SessionOwnership{
		WorkSessionID: execution.Target.WorkSessionID,
		TargetID:      execution.Target.TargetID,
		ProjectID:     execution.Target.ProjectID,
		DeploymentID:  execution.Target.DeploymentID,
		NodeID:        execution.Deployment.NodeID,
	})
}

func (s *Service) resolveACPDirectory(ctx context.Context, raw string) (string, error) {
	if _, scoped := acpProjectExecution(ctx); !scoped {
		return raw, nil
	}
	if s.ws == nil {
		return "", fmt.Errorf("ACP Project workspace resolver is unavailable")
	}
	if raw == "" {
		raw = "."
	}
	resolved, err := s.ws.ResolveExistingContext(ctx, raw)
	if err != nil {
		return "", err
	}
	return resolved.Abs, nil
}

func (s *Service) resolveACPDirectories(ctx context.Context, values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	resolved := make([]string, 0, len(values))
	for _, value := range values {
		path, err := s.resolveACPDirectory(ctx, value)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, path)
	}
	return resolved, nil
}
