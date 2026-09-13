package app

import (
	"context"
	"strings"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

type browserSessionOwner struct {
	WorkSessionID string
	TargetID      string
	ProjectID     string
	DeploymentID  string
	NodeID        string
}

func browserOwnerFromExecution(execution projectstate.Execution) browserSessionOwner {
	return browserSessionOwner{
		WorkSessionID: execution.Target.WorkSessionID,
		TargetID:      execution.Target.TargetID,
		ProjectID:     execution.Target.ProjectID,
		DeploymentID:  execution.Target.DeploymentID,
		NodeID:        execution.Deployment.NodeID,
	}
}

func sameBrowserOwner(owner browserSessionOwner, execution projectstate.Execution) bool {
	return owner.WorkSessionID != "" &&
		owner.WorkSessionID == execution.Target.WorkSessionID &&
		owner.TargetID == execution.Target.TargetID &&
		owner.ProjectID == execution.Target.ProjectID &&
		owner.DeploymentID == execution.Target.DeploymentID
}

func (r *Runtime) rememberBrowserSession(ctx context.Context, sessionID string) {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return
	}
	r.browserOwnerMu.Lock()
	r.browserOwners[sessionID] = browserOwnerFromExecution(execution)
	r.browserOwnerMu.Unlock()
}

func (r *Runtime) forgetBrowserSession(sessionID string) {
	r.browserOwnerMu.Lock()
	delete(r.browserOwners, strings.TrimSpace(sessionID))
	r.browserOwnerMu.Unlock()
}

func (r *Runtime) requireBrowserSessionOwner(ctx context.Context, sessionID string) error {
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok {
		// Standalone AgentDock browser sessions are intentionally unowned by a
		// Project Target. Ownership isolation applies only to Project-scoped calls.
		return nil
	}
	r.browserOwnerMu.RLock()
	owner, exists := r.browserOwners[strings.TrimSpace(sessionID)]
	r.browserOwnerMu.RUnlock()
	if !exists || !sameBrowserOwner(owner, execution) {
		return toolErrorDetails(
			protocol.ErrorSessionTargetDenied,
			"browser session does not belong to the current Project Target",
			"authorization",
			map[string]any{"session_id": sessionID, "target_id": execution.Target.TargetID},
		)
	}
	return nil
}

func (r *Runtime) handleProjectBrowserSession(ctx context.Context, args map[string]any) (Result, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "close" {
		sessionID, _ := args["session_id"].(string)
		if err := r.requireBrowserSessionOwner(ctx, sessionID); err != nil {
			return nil, err
		}
		result, err := r.browser.HandleSession(ctx, args)
		if err == nil && result["browser_ok"] == true && result["closed"] == true {
			r.forgetBrowserSession(sessionID)
		}
		return result, err
	}
	result, err := r.browser.HandleSession(ctx, args)
	if err == nil && action == "start" && result["browser_ok"] == true {
		if sessionID, _ := result["session_id"].(string); sessionID != "" {
			r.rememberBrowserSession(ctx, sessionID)
		}
	}
	return result, err
}

func (r *Runtime) handleProjectBrowserAct(ctx context.Context, args map[string]any) (Result, error) {
	sessionID, _ := args["session_id"].(string)
	if err := r.requireBrowserSessionOwner(ctx, sessionID); err != nil {
		return nil, err
	}
	result, err := r.browser.HandleAct(ctx, args)
	if err == nil && result["closed"] == true {
		r.forgetBrowserSession(sessionID)
	}
	return result, err
}

func (r *Runtime) handleProjectBrowserSnapshot(ctx context.Context, args map[string]any) (Result, error) {
	sessionID, _ := args["session_id"].(string)
	if err := r.requireBrowserSessionOwner(ctx, sessionID); err != nil {
		return nil, err
	}
	result, err := r.browser.HandleSnapshot(ctx, args)
	if err == nil && result["closed"] == true {
		r.forgetBrowserSession(sessionID)
	}
	return result, err
}
