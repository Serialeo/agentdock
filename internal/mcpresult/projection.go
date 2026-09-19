// Package mcpresult owns the model-facing result boundary, not durable/runtime state.
package mcpresult

import (
	"encoding/json"
	"reflect"
	"strings"
)

// 只精简已知工具的已知元数据，不递归清除业务对象中的 false、0、null 或 ID。
// 两个独立发行的服务使用相同策略及回归样例；不引入未发布的协议依赖。
var removed = map[string][]string{
	"read_file":                {"encoding", "size_bytes"},
	"search_text":              {"query", "engine", "total_matches", "files_scanned", "bytes_scanned"},
	"task_manage":              {"action", "count", "state_dir", "checkpoint_policy"},
	"skill_package":            {"action", "count"},
	"mcp_manage":               {"action", "count"},
	"mcp_tool_search":          {"query", "server", "count"},
	"mcp_tool_inspect":         {"name"},
	"acp_session":              {"action", "count"},
	"acp_prompt":               {"action", "started_at", "ended_at"},
	"acp_interaction":          {"action", "count"},
	"workflow_template_manage": {"ok", "action", "count", "root", "source", "index_path"},
	"recall_search":            {"ok", "query", "count", "recall_endpoint", "recall_kind", "recall_store"},
	"recall_read":              {"ok", "recall_endpoint", "recall_kind", "recall_store"},
	"recall_write":             {"ok", "recall_endpoint", "recall_kind", "recall_store", "confirmed"},
	"recall_maintain":          {"ok", "count", "recall_endpoint", "recall_kind", "recall_store", "recall_action", "files_scanned", "finding_count"},
	"private_note_manage":      {"ok", "count", "root", "private_note_store", "recall_endpoint", "action", "query", "metadata_only"},
	"project_list":             {"count"},
}
var commandRemoved = []string{"command_ok", "elapsed_ms", "event_id", "outcome_state", "sandbox", "workdir", "work_session_id", "target_id", "project_id", "deployment_id", "node_id", "request_id", "session_reason", "observe_after_ms", "kill_operation_ms", "terminal", "count", "stdout_output_bytes", "stderr_output_bytes", "stdout_dropped_bytes", "stderr_dropped_bytes", "stdout_omitted_bytes", "stderr_omitted_bytes", "stdout_output_lines", "stderr_output_lines"}

func Project(name string, input map[string]any) map[string]any {
	out := cloneMap(input)
	if out == nil {
		out = map[string]any{}
	}
	action, _ := out["action"].(string)
	for _, key := range removed[name] {
		switch key {
		case "count":
			omitCollectionCount(out)
		case "ok":
			if truth(out[key]) {
				delete(out, key)
			}
		default:
			delete(out, key)
		}
	}
	switch name {
	case "exec_command", "session_observe", "session_act":
		remove(out, commandRemoved...)
		status, _ := out["status"].(string)
		if name != "exec_command" || status != "running" && status != "starting" && status != "outcome_unknown" && status != "interrupted" {
			delete(out, "session_id")
		}
		if status == "exited" || status == "timeout" {
			delete(out, "status")
		}
		if truth(out["timed_out"]) {
			delete(out, "exit_code")
		}
		// 非零 exit_code 不能解释信号、持久化故障等错误；只删除纯粹重复的退出码文本。
		if message, _ := out["command_error"].(string); message != "" {
			if code, ok := out["exit_code"]; ok && message == "exit status "+numberText(code) {
				delete(out, "command_error")
			}
			if truth(out["timed_out"]) && message == "signal: killed" {
				delete(out, "command_error")
			}
		}
		omitEmpty(out, "stdout", "stderr", "command_error", "persistence_error")
		omitFalse(out, "timed_out", "replayed", "stdout_truncated", "stderr_truncated")
		for _, prefix := range []string{"stdout", "stderr"} {
			if !truth(out[prefix+"_truncated"]) {
				delete(out, prefix+"_total_bytes")
			}
		}
		eachObject(out, "sessions", func(item map[string]any) {
			remove(item, commandRemoved...)
			if truth(item["timed_out"]) {
				delete(item, "exit_code")
			}
			if message, _ := item["command_error"].(string); message != "" {
				if code, ok := item["exit_code"]; ok && message == "exit status "+numberText(code) {
					delete(item, "command_error")
				}
				if truth(item["timed_out"]) && message == "signal: killed" {
					delete(item, "command_error")
				}
			}
			omitEmpty(item, "stdout", "stderr", "command_error", "persistence_error")
			omitFalse(item, "timed_out", "replayed", "stdout_truncated", "stderr_truncated")
			for _, prefix := range []string{"stdout", "stderr"} {
				if !truth(item[prefix+"_truncated"]) {
					delete(item, prefix+"_total_bytes")
				}
			}
		})
	case "read_file":
		omitFalse(out, "truncated")
		if !truth(out["truncated"]) && integer(out["start_line"]) == 1 && integer(out["end_line"]) == integer(out["total_lines"]) {
			remove(out, "start_line", "end_line", "total_lines")
		}
		omitEmpty(out, "truncated_reason")
	case "list_dir":
		eachObject(out, "entries", func(item map[string]any) { remove(item, "name", "modified", "is_hidden", "size_bytes") })
		omitFalse(out, "partial", "truncated")
		omitEmpty(out, "skipped_paths")
	case "search_text":
		omitFalse(out, "partial", "truncated")
		omitZero(out, "skipped_large_files")
		eachObject(out, "matches", func(item map[string]any) {
			if preview, ok := item["preview"].(string); ok {
				if text, ok := item["match_text"].(string); ok && strings.Contains(preview, text) {
					delete(item, "match_text")
				}
			}
			// 只有确实可从行号/上下文推导时才删除；旧节点可能提供非连续上下文。
			if integer(item["context_start_line"]) == integer(item["line"])-int64(length(item["before"])) {
				delete(item, "context_start_line")
			}
			if integer(item["context_end_line"]) == integer(item["line"])+int64(length(item["after"])) {
				delete(item, "context_end_line")
			}
			omitEmpty(item, "before", "after")
		})
	case "file_edit":
		omitFalse(out, "dry_run", "recursive", "truncated")
	case "task_manage":
		if summary, ok := out["task_summary"].(map[string]any); ok {
			if reflect.DeepEqual(summary["id"], out["task_id"]) {
				delete(summary, "id")
			}
			if _, ok := summary["steps"]; ok {
				remove(summary, "step_count", "completed_step_count")
			}
			if _, ok := summary["condition_refs"]; ok {
				delete(summary, "condition_count")
			}
			remove(summary, "updated_at")
		}
	case "skill_package":
		omitTrue(out, "configured", "removed")
	case "mcp_manage":
		omitTrue(out, "configured", "removed")
		if _, ok := out["tools"]; ok {
			delete(out, "tool_count")
		}
	case "acp_session":
		omitTrue(out, "changed", "authenticated", "deleted")
		stripSession := func(item map[string]any) {
			remove(item, "schema_version", "remote_session_id", "work_session_id", "target_id", "project_id", "deployment_id", "node_id")
		}
		if item, ok := out["session"].(map[string]any); ok {
			stripSession(item)
		}
		eachObject(out, "sessions", stripSession)
		omitEmpty(out, "auth_methods")
	case "acp_interaction":
		omitTrue(out, "responded", "cancelled")
	case "acp_prompt":
		omitEmpty(out, "stop_reason", "error_code", "message", "session_id", "run_id")
		omitFalse(out, "has_more", "truncated")
		if !truth(out["truncated"]) {
			remove(out, "first_seq", "latest_seq", "dropped_count")
		} else {
			omitZero(out, "dropped_count")
		}
		if action == "events" || out["events"] != nil {
			eachObject(out, "events", func(item map[string]any) {
				remove(item, "session_id", "run_id", "created_at")
				omitEmpty(item, "stop_reason", "error_code", "message")
				omitFalse(item, "update_truncated")
			})
		}
	case "browser_session", "browser_act", "browser_snapshot":
		delete(out, "browser_ok")
		if err, ok := out["error"].(map[string]any); ok && reflect.DeepEqual(err["code"], out["code"]) {
			delete(out, "code")
		}
		omitEmpty(out, "console_errors", "network_errors", "page_errors", "removed_sessions")
		if _, ok := out["removed_sessions"]; ok {
			delete(out, "removed_count")
		}
		if name != "browser_session" {
			delete(out, "session_id")
		}
		if pages, ok := out["pages"].([]any); ok && len(pages) == 1 {
			page, ok := pages[0].(map[string]any)
			if ok && reflect.DeepEqual(page["page_id"], out["page_id"]) && reflect.DeepEqual(page["url"], out["url"]) && reflect.DeepEqual(page["title"], out["title"]) {
				delete(out, "pages")
			}
		}
	case "project_list":
		eachObject(out, "projects", func(project map[string]any) { remove(project, "revision", "enabled") })
	case "project_open", "project_context":
		remove(out, "context_revision", "delivery")
		project, _ := out["project"].(map[string]any)
		remove(project, "revision", "enabled")
		eachObject(out, "deployments", func(item map[string]any) {
			omitEmpty(item, "last_error")
			remove(item, "project_id", "node_id", "desired_revision", "applied_revision", "enabled", "apply_status", "online")
		})
		if deployment, ok := out["deployment"].(map[string]any); ok {
			omitEmpty(deployment, "last_error")
			remove(deployment, "project_id", "node_id", "desired_revision", "applied_revision", "enabled", "apply_status", "online")
		}
		trimTarget := func(item map[string]any) {
			remove(item, "work_session_id", "project_id", "node_id", "permissions", "context_revision", "deployment_revision", "source_provenance")
			if prompt, ok := item["prompt"].(map[string]any); ok {
				remove(prompt, "bytes", "prompt_revision")
				eachObject(prompt, "sources", func(source map[string]any) { remove(source, "bytes", "sha256") })
			}
		}
		eachObject(out, "targets", trimTarget)
		if target, ok := out["target"].(map[string]any); ok {
			trimTarget(target)
		}
	case "agentdock_context":
		trimContext := func(context map[string]any) {
			if common, ok := context["common_skills"].(map[string]any); ok {
				omitFalse(common, "truncated")
				omitZero(common, "shadowed")
				if integer(common["effective"]) == int64(length(common["items"])) {
					delete(common, "effective")
				}
			}
		}
		trimContext(out)
		eachObject(out, "nodes", func(node map[string]any) {
			if context, ok := node["context"].(map[string]any); ok {
				trimContext(context)
			}
		})
	}
	return out
}

func Error(input map[string]any) map[string]any {
	out := cloneMap(input)
	omitEmpty(out, "details")
	omitFalse(out, "retryable")
	return out
}
func remove(m map[string]any, keys ...string) {
	for _, k := range keys {
		delete(m, k)
	}
}
func truth(v any) bool { b, _ := v.(bool); return b }
func omitFalse(m map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok && !v {
			delete(m, k)
		}
	}
}
func omitEmpty(m map[string]any, keys ...string) {
	for _, k := range keys {
		v, exists := m[k]
		if !exists {
			continue
		}
		if v == nil {
			delete(m, k)
			continue
		}
		r := reflect.ValueOf(v)
		switch r.Kind() {
		case reflect.String, reflect.Slice, reflect.Map:
			if r.Len() == 0 {
				delete(m, k)
			}
		}
	}
}
func omitZero(m map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := m[k]; ok && integer(v) == 0 {
			delete(m, k)
		}
	}
}
func integer(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case uint64:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
func numberText(v any) string {
	switch n := v.(type) {
	case json.Number:
		return string(n)
	default:
		b, _ := json.Marshal(n)
		return string(b)
	}
}
func length(v any) int {
	if v == nil {
		return 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return r.Len()
	}
	return 0
}
func eachObject(m map[string]any, key string, f func(map[string]any)) {
	switch items := m[key].(type) {
	case []any:
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				f(obj)
			}
		}
	case []map[string]any:
		for _, item := range items {
			f(item)
		}
	}
}
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = clone(v)
	}
	return out
}
func clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = clone(v)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(t))
		for i, v := range t {
			out[i] = cloneMap(v)
		}
		return out
	case []string:
		return append([]string{}, t...)
	default:
		return v
	}
}

func omitTrue(m map[string]any, keys ...string) {
	for _, key := range keys {
		if truth(m[key]) {
			delete(m, key)
		}
	}
}

// count 只有等于返回集合长度时才是重复值；索引总数、分页总数和不相等的计数保留。
func omitCollectionCount(m map[string]any) {
	count, exists := m["count"]
	if !exists || count == nil {
		return
	}
	for _, key := range []string{"tasks", "items", "skills", "tools", "sessions", "interactions", "candidates", "templates", "results", "entries", "projects", "cards"} {
		if items, ok := m[key].([]any); ok && integer(count) == int64(len(items)) {
			delete(m, "count")
			return
		}
		if items, ok := m[key].([]map[string]any); ok && integer(count) == int64(len(items)) {
			delete(m, "count")
			return
		}
	}
}
