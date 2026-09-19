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
	var result Result
	var err error
	switch action {
	case "list":
		result, err = s.listSessions(ctx)
	case "status":
		result, err = s.sessionStatus(ctx, request)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported session_observe action", "validation", map[string]any{"action": request.Action, "allowed": []string{"list", "status"}})
	}
	if err != nil {
		return nil, err
	}
	if action == "list" {
		return compactSessionCollection(result), nil
	}
	return compactSessionResult(result), nil
}

func (s *Service) Act(ctx context.Context, request SessionActRequest) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(request.Action))
	var result Result
	var err error
	switch action {
	case "write":
		result, err = s.writeStdin(ctx, request)
	case "kill":
		result, err = s.killSession(ctx, request)
	case "kill_all":
		result, err = s.killAll(ctx)
	default:
		return nil, toolErrorDetails("INVALID_ACTION", "unsupported session_act action", "validation", map[string]any{"action": request.Action, "allowed": []string{"write", "kill", "kill_all"}})
	}
	if err != nil {
		return nil, err
	}
	if action == "kill_all" {
		return compactSessionCollection(result), nil
	}
	return compactSessionResult(result), nil
}
