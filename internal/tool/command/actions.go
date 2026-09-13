package command

import (
	"context"
	"strings"
)

func (s *Service) Observe(ctx context.Context, request SessionObserveRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		return s.listSessions(ctx)
	case "status":
		return s.sessionStatus(ctx, request)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported session_observe action", "validation", map[string]any{"action": request.Action, "allowed": []string{"list", "status"}})
	}
}

func (s *Service) Act(ctx context.Context, request SessionActRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	switch action {
	case "write":
		return s.writeStdin(ctx, request)
	case "kill":
		return s.killSession(ctx, request)
	case "kill_all":
		return s.killAll(ctx)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported session_act action", "validation", map[string]any{"action": request.Action, "allowed": []string{"write", "kill", "kill_all"}})
	}
}
