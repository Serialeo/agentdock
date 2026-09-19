package command

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolExecCommand    = "exec_command"
	ToolSessionObserve = "session_observe"
	ToolSessionAct     = "session_act"
)

func InputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	boolProp := toolcontract.Boolean
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string

	switch name {
	case ToolExecCommand:
		props["request_id"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable idempotency key for this command within the Project Target. Reuse with the same execution arguments to recover the original result after a lost response; conflicting reuse is rejected. Does not imply exactly-once external side effects."}
		props["cmd"] = stringProp("Command to run.")
		props["workdir"] = stringProp("Host working directory. Relative paths resolve from ~/AgentDock.")
		props["skill"] = stringProp("Optional active Skill context. When workdir is omitted, the command runs from the active installed Skill root and loads that Skill isolated environment.")
		props["skill_env"] = stringProp("Optional Skill name whose isolated environment is loaded without changing workdir. Kept for environment-only compatibility.")
		props["env"] = map[string]any{"type": "object", "description": "Explicit command environment values. These override the selected Skill environment.", "additionalProperties": map[string]any{"type": "string"}}
		props["timeout_ms"] = boundedIntProp("Timeout in milliseconds. Must be positive and is capped at 86400000.", 1, 86400000)
		props["execution_mode"] = map[string]any{"type": "string", "description": "Execution mode. Defaults to auto: wait up to yield_time_ms, then return a running session. sync waits for exit; async returns a session immediately.", "enum": []string{"auto", "sync", "async"}}
		props["yield_time_ms"] = boundedIntProp("Foreground wait threshold for execution_mode=auto. Defaults to 5000 and is capped at 30000 milliseconds.", 0, 30000)
		props["max_output_bytes"] = boundedIntProp("Maximum output bytes. Defaults to 65536 and is capped at 4194304.", 1, MaxOutputBytes)
		props["stdin"] = stringProp("Initial stdin.")
		props["tty"] = boolProp("Keep stdin open.")
		required = []string{"cmd"}
	case ToolSessionObserve:
		props["action"] = map[string]any{"type": "string", "description": "Read-only session action.", "enum": []string{"list", "status"}}
		props["session_id"] = stringProp("Session id returned by exec_command, required for status.")
		props["max_output_bytes"] = boundedIntProp("Maximum output bytes. Defaults to 65536 and is capped at 4194304.", 1, MaxOutputBytes)
	case ToolSessionAct:
		props["action"] = map[string]any{"type": "string", "description": "Mutating session action.", "enum": []string{"write", "kill", "kill_all"}}
		props["session_id"] = stringProp("Session id returned by exec_command, required for write/kill.")
		props["chars"] = stringProp("Characters to write when action=write.")
		props["max_output_bytes"] = boundedIntProp("Maximum output bytes. Defaults to 65536 and is capped at 4194304.", 1, MaxOutputBytes)
	default:
		return nil, false
	}
	return toolcontract.InputObject(props, required...), true
}

func OutputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	props := map[string]any{
		"stdout":             stringProp("Captured stdout. Omitted when empty."),
		"stderr":             stringProp("Captured stderr. Omitted when empty."),
		"command_error":      stringProp("Command process error when it adds information beyond stdout/stderr."),
		"exit_code":          intProp("Process exit code, when available."),
		"timed_out":          boolProp("Present only when the command timed out."),
		"stdout_truncated":   boolProp("Present only when stdout was truncated."),
		"stderr_truncated":   boolProp("Present only when stderr was truncated."),
		"stdout_total_bytes": intProp("Total stdout bytes, present only when stdout was truncated."),
		"stderr_total_bytes": intProp("Total stderr bytes, present only when stderr was truncated."),
		"persistence_error":  stringProp("Durable output persistence warning, when one occurred."),
	}
	switch name {
	case ToolExecCommand:
		props["session_id"] = stringProp("Session id, present only when the command is still running.")
		props["status"] = stringProp("Exceptional or running status. Normal completed commands omit it.")
		props["replayed"] = boolProp("Present only when request_id recovered an earlier command instead of starting a new one.")
	case ToolSessionObserve, ToolSessionAct:
		props["status"] = stringProp("Session status for status/write/kill responses.")
		// Keep the historical open item schema so old/new AgentDock providers can
		// coexist during a rolling upgrade. Runtime projection still emits only
		// session_id and status.
		props["sessions"] = arrayProp("Compact session summaries returned by list or kill_all.")
	default:
		return nil, false
	}
	return toolcontract.OutputObject(props), true
}

func Description() string {
	return "Run a bounded command on the Host OS. Bind an active Skill with skill to use its installed root and isolated environment for this command; explicit workdir and env values override those defaults."
}
