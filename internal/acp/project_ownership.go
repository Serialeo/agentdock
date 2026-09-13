package acp

import "context"

type SessionOwnership struct {
	WorkSessionID string
	TargetID      string
	ProjectID     string
	DeploymentID  string
	NodeID        string
}

type sessionOwnershipContextKey struct{}

func WithSessionOwnership(ctx context.Context, ownership SessionOwnership) context.Context {
	return context.WithValue(ctx, sessionOwnershipContextKey{}, ownership)
}

func sessionOwnershipFromContext(ctx context.Context) SessionOwnership {
	if ctx == nil {
		return SessionOwnership{}
	}
	ownership, _ := ctx.Value(sessionOwnershipContextKey{}).(SessionOwnership)
	return ownership
}

func applySessionOwnership(record *SessionRecord, ownership SessionOwnership) {
	if record == nil {
		return
	}
	record.WorkSessionID = ownership.WorkSessionID
	record.TargetID = ownership.TargetID
	record.ProjectID = ownership.ProjectID
	record.DeploymentID = ownership.DeploymentID
	record.NodeID = ownership.NodeID
}
