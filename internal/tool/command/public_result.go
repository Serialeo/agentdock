package command

import "fmt"

// compactExecResult is the model-facing projection for exec_command.
// Command journaling and session internals deliberately keep richer state; this
// projection only exposes information the caller needs to interpret the command
// or continue an asynchronous session.
func compactExecResult(result Result) Result {
	if result == nil {
		return nil
	}
	out := compactCommandOutput(result)

	status, _ := result["status"].(string)
	if status == "running" || status == "starting" || status == "outcome_unknown" || status == "interrupted" {
		copyResultField(out, result, "session_id")
		out["status"] = status
	} else if status != "" && status != "exited" && status != "timeout" {
		// Preserve exceptional durable states such as outcome_unknown. Timeout is
		// already represented by timed_out=true and does not need a second label.
		out["status"] = status
	}

	if replayed, _ := result["replayed"].(bool); replayed {
		out["replayed"] = true
	}
	return out
}

// compactSessionResult is the model-facing projection for a status/write/kill
// response. The caller already supplied the session id, so echoing it adds no
// information; status and newly observed output are the useful result.
func compactSessionResult(result Result) Result {
	if result == nil {
		return nil
	}
	out := compactCommandOutput(result)
	if status, _ := result["status"].(string); status != "" && status != "exited" && status != "timeout" {
		out["status"] = status
	}
	return out
}

// compactSessionCollection strips per-session execution identity and telemetry.
// session_id plus status are sufficient to choose a session for a later action.
func compactSessionCollection(result Result) Result {
	if result == nil {
		return nil
	}
	raw, _ := result["sessions"].([]map[string]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		compact := map[string]any(compactCommandOutput(Result(item)))
		copyMapField(compact, item, "session_id")
		copyMapField(compact, item, "status")
		items = append(items, compact)
	}
	return Result{"sessions": items}
}

func compactCommandOutput(result Result) Result {
	out := Result{}
	if stdout, _ := result["stdout"].(string); stdout != "" {
		out["stdout"] = stdout
	}
	if stderr, _ := result["stderr"].(string); stderr != "" {
		out["stderr"] = stderr
	}
	timedOut, _ := result["timed_out"].(bool)
	if timedOut {
		out["timed_out"] = true
	} else {
		copyResultField(out, result, "exit_code")
	}
	copyTruncation(out, result, "stdout")
	copyTruncation(out, result, "stderr")

	if persistenceError, _ := result["persistence_error"].(string); persistenceError != "" {
		out["persistence_error"] = persistenceError
	}
	if commandError, _ := result["command_error"].(string); commandError != "" {
		code, hasCode := result["exit_code"]
		redundant := hasCode && commandError == fmt.Sprintf("exit status %v", code)
		if timedOut && commandError == "signal: killed" {
			redundant = true
		}
		if !redundant {
			out["command_error"] = commandError
		}
	}
	return out
}

func copyTruncation(out, result Result, prefix string) {
	key := prefix + "_truncated"
	truncated, _ := result[key].(bool)
	if !truncated {
		return
	}
	out[key] = true
	copyResultField(out, result, prefix+"_total_bytes")
}

func copyResultField(out, result Result, key string) {
	if value, ok := result[key]; ok {
		out[key] = value
	}
}

func copyMapField(out, result map[string]any, key string) {
	if value, ok := result[key]; ok {
		out[key] = value
	}
}
