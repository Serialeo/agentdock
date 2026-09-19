package mcpresult

// Schema 为 MCP 公布裁剪后的契约；运行时、持久化及 REST 仍使用原始类型。
func Schema(name string, input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := cloneMap(input)
	for _, key := range removed[name] {
		if key == "count" || key == "ok" {
			makeOptional(out, key)
		} else {
			dropProperty(out, key)
		}
	}
	optional := []string{}
	switch name {
	case "exec_command", "session_observe", "session_act":
		for _, key := range commandRemoved {
			dropProperty(out, key)
		}
		optional = []string{"status", "session_id", "stdout", "stderr", "timed_out", "replayed", "stdout_truncated", "stderr_truncated", "stdout_total_bytes", "stderr_total_bytes", "exit_code", "command_error", "persistence_error"}
		for _, key := range []string{"stdout_truncated", "stderr_truncated", "replayed"} {
			property(out, key, map[string]any{"type": "boolean"})
		}
		for _, key := range []string{"stdout_total_bytes", "stderr_total_bytes"} {
			property(out, key, map[string]any{"type": "integer"})
		}
		property(out, "persistence_error", map[string]any{"type": "string"})
	case "agentdock_context":
		if common := objectSchema(out, "common_skills"); common != nil {
			makeOptional(common, "effective", "shadowed", "truncated")
		}
		if node := itemSchema(out, "nodes"); node != nil {
			if context := objectSchema(node, "context"); context != nil {
				if common := objectSchema(context, "common_skills"); common != nil {
					makeOptional(common, "effective", "shadowed", "truncated")
				}
			}
		}
	case "read_file":
		optional = []string{"path", "start_line", "end_line", "total_lines", "truncated"}
	case "list_dir":
		optional = []string{"partial", "truncated", "skipped_paths"}
		if entry := itemSchema(out, "entries"); entry != nil {
			for _, key := range []string{"name", "size_bytes", "modified", "is_hidden"} {
				dropProperty(entry, key)
			}
		}
	case "search_text":
		optional = []string{"truncated", "partial", "skipped_large_files"}
	case "mcp_tool_call":
		if result := objectSchema(out, "result"); result != nil {
			result["description"] = "Upstream MCP result metadata and structuredContent. Upstream content is returned once through MCP content."
		}
	case "file_edit":
		optional = []string{"dry_run", "recursive", "truncated"}
	case "skill_package", "mcp_manage":
		optional = []string{"configured", "removed"}
	case "acp_interaction":
		optional = []string{"responded", "cancelled"}
	case "acp_session":
		optional = []string{"auth_methods", "changed", "authenticated", "deleted"}
	case "acp_prompt":
		optional = []string{"has_more", "truncated", "first_seq", "latest_seq", "dropped_count", "stop_reason", "error_code", "message", "session_id", "run_id"}
	case "browser_session", "browser_act", "browser_snapshot":
		dropProperty(out, "browser_ok")
		optional = []string{"pages", "console_errors", "network_errors", "page_errors", "removed_sessions", "removed_count", "code"}
		if name != "browser_session" {
			dropProperty(out, "session_id")
		}
	case "project_list":
		if project := itemSchema(out, "projects"); project != nil {
			dropProperty(project, "revision")
			dropProperty(project, "enabled")
		}
	case "project_open", "project_context":
		dropProperty(out, "context_revision")
		dropProperty(out, "delivery")
		if project := objectSchema(out, "project"); project != nil {
			dropProperty(project, "revision")
			dropProperty(project, "enabled")
		}
		if deployment := objectSchema(out, "deployment"); deployment != nil {
			for _, field := range []string{"project_id", "node_id", "desired_revision", "applied_revision", "enabled", "apply_status", "online"} {
				dropProperty(deployment, field)
			}
			makeOptional(deployment, "last_error")
		}
		for _, field := range []string{"targets", "target"} {
			target := itemSchema(out, field)
			if field == "target" {
				target = objectSchema(out, field)
			}
			if target != nil {
				for _, field := range []string{"work_session_id", "project_id", "node_id", "permissions", "context_revision", "deployment_revision", "source_provenance"} {
					dropProperty(target, field)
				}
				if prompt := objectSchema(target, "prompt"); prompt != nil {
					dropProperty(prompt, "bytes")
					dropProperty(prompt, "prompt_revision")
					if source := itemSchema(prompt, "sources"); source != nil {
						dropProperty(source, "bytes")
						dropProperty(source, "sha256")
					}
				}
			}
		}
		if deployment := itemSchema(out, "deployments"); deployment != nil {
			for _, field := range []string{"project_id", "node_id", "desired_revision", "applied_revision", "enabled", "apply_status", "online"} {
				dropProperty(deployment, field)
			}
			makeOptional(deployment, "last_error")
		}
	}
	makeOptional(out, optional...)
	return out
}
func property(schema map[string]any, key string, value map[string]any) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		props = map[string]any{}
		schema["properties"] = props
	}
	if _, exists := props[key]; !exists {
		props[key] = value
	}
}
func dropProperty(schema map[string]any, key string) {
	if props, ok := schema["properties"].(map[string]any); ok {
		delete(props, key)
	}
	makeOptional(schema, key)
}
func makeOptional(schema map[string]any, keys ...string) {
	if schema == nil {
		return
	}
	skip := map[string]bool{}
	for _, key := range keys {
		skip[key] = true
	}
	switch old := schema["required"].(type) {
	case []string:
		out := []string{}
		for _, key := range old {
			if !skip[key] {
				out = append(out, key)
			}
		}
		if len(out) > 0 {
			schema["required"] = out
		} else {
			delete(schema, "required")
		}
	case []any:
		out := []any{}
		for _, key := range old {
			s, _ := key.(string)
			if !skip[s] {
				out = append(out, key)
			}
		}
		if len(out) > 0 {
			schema["required"] = out
		} else {
			delete(schema, "required")
		}
	}
}
func objectSchema(schema map[string]any, key string) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	obj, _ := props[key].(map[string]any)
	return obj
}
func itemSchema(schema map[string]any, key string) map[string]any {
	obj := objectSchema(schema, key)
	items, _ := obj["items"].(map[string]any)
	return items
}
