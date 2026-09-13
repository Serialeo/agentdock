package browser

import (
	"context"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

type projectOwner struct {
	WorkSessionID string
	TargetID      string
	ProjectID     string
	DeploymentID  string
}

func projectOwnerFromContext(ctx context.Context) (projectOwner, bool) {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok {
		return projectOwner{}, false
	}
	return projectOwner{
		WorkSessionID: execution.Target.WorkSessionID,
		TargetID:      execution.Target.TargetID,
		ProjectID:     execution.Target.ProjectID,
		DeploymentID:  execution.Target.DeploymentID,
	}, true
}

func (owner projectOwner) matches(other projectOwner) bool {
	return owner.WorkSessionID != "" &&
		owner.WorkSessionID == other.WorkSessionID &&
		owner.TargetID == other.TargetID &&
		owner.ProjectID == other.ProjectID &&
		owner.DeploymentID == other.DeploymentID
}

func (s *Service) requireProjectSessionOwner(ctx context.Context, sessionID string) error {
	owner, scoped := projectOwnerFromContext(ctx)
	if !scoped {
		return nil
	}
	sess, err := s.getSession(sessionID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	sessionOwner := sess.projectOwner
	sess.mu.Unlock()
	if !owner.matches(sessionOwner) {
		return browserError(protocol.ErrorSessionTargetDenied, "browser session does not belong to the current Project Target", "authorization", &ErrorDetails{SessionID: sessionID}, nil)
	}
	return nil
}
