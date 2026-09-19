package mcpresult

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func jsonText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func assertJSON(t *testing.T, got, want any) {
	t.Helper()
	if jsonText(t, got) != jsonText(t, want) {
		t.Fatalf("got %s; want %s", jsonText(t, got), jsonText(t, want))
	}
}
func TestProjectionKnownFieldsOnly(t *testing.T) {
	tests := []struct {
		name        string
		input, want map[string]any
	}{
		{"exec_command", map[string]any{"exit_code": 0, "stdout": "ok", "stderr": "", "command_ok": true, "sandbox": map[string]any{"enabled": false}, "event_id": "trace", "session_id": "done", "status": "exited", "stdout_total_bytes": 2, "stdout_truncated": false}, map[string]any{"exit_code": 0, "stdout": "ok"}},
		{"exec_command", map[string]any{"status": "running", "session_id": "live", "session_reason": "auto", "observe_after_ms": 1000}, map[string]any{"status": "running", "session_id": "live"}},
		{"read_file", map[string]any{"path": "a", "content": "", "encoding": "utf-8", "size_bytes": 0, "truncated": false, "start_line": 1, "end_line": 0, "total_lines": 0}, map[string]any{"path": "a", "content": ""}},
		{"read_file", map[string]any{"path": "a", "content": "slice", "start_line": 20, "end_line": 30, "total_lines": 50, "truncated": true, "next_start_line": 31, "truncated_reason": "max_bytes"}, map[string]any{"path": "a", "content": "slice", "start_line": 20, "end_line": 30, "total_lines": 50, "truncated": true, "next_start_line": 31, "truncated_reason": "max_bytes"}},
		{"list_dir", map[string]any{"path": "root", "entries": []any{map[string]any{"path": "sub/a", "name": "a", "type": "file", "size_bytes": 99, "modified": "date", "is_hidden": false}}, "truncated": false, "partial": false, "skipped_paths": []any{}}, map[string]any{"path": "root", "entries": []any{map[string]any{"path": "sub/a", "type": "file"}}}},
		{"list_dir", map[string]any{"path": "root", "entries": []any{}, "partial": true, "truncated": true, "skipped_paths": []any{"denied"}}, map[string]any{"path": "root", "entries": []any{}, "partial": true, "truncated": true, "skipped_paths": []any{"denied"}}},
		{"search_text", map[string]any{"query": "hi", "engine": "rg", "total_matches": 1, "truncated": false, "matches": []any{map[string]any{"path": "a", "line": 10, "column": 2, "preview": " hi", "match_text": "hi", "before": []any{}, "after": nil, "context_start_line": 10, "context_end_line": 10}}}, map[string]any{"matches": []any{map[string]any{"path": "a", "line": 10, "column": 2, "preview": " hi"}}}},
		{"search_text", map[string]any{"matches": []any{}, "engine": "go_fallback", "partial": true, "skipped_large_files": 2, "files_scanned": 3, "bytes_scanned": 128}, map[string]any{"matches": []any{}, "partial": true, "skipped_large_files": 2}},
		{"task_manage", map[string]any{"action": "list", "tasks": []any{}, "count": 0, "state_dir": "private", "checkpoint_policy": map[string]any{"rules": []string{"static"}}}, map[string]any{"tasks": []any{}, "checkpoint_policy": map[string]any{"rules": []string{"static"}}}},
		{"mcp_manage", map[string]any{"action": "inspect", "count": 1, "config": map[string]any{"enabled": false, "count": 0, "nullable": nil}}, map[string]any{"count": 1, "config": map[string]any{"enabled": false, "count": 0, "nullable": nil}}},
		{"skill_package", map[string]any{"action": "env_set", "name": "skill", "key": "TOKEN", "configured": false}, map[string]any{"name": "skill", "key": "TOKEN", "configured": false}},
		{"acp_prompt", map[string]any{"action": "events", "status": "running", "events": []any{}, "next_seq": 0, "first_seq": 1, "latest_seq": 0, "dropped_count": 0, "has_more": false, "truncated": false, "stop_reason": "", "error_code": "", "message": "", "started_at": "date"}, map[string]any{"status": "running", "events": []any{}, "next_seq": 0}},
		{"acp_prompt", map[string]any{"action": "events", "status": "failed", "events": []any{}, "next_seq": 55, "first_seq": 51, "latest_seq": 90, "dropped_count": 50, "has_more": true, "truncated": true, "error_code": "FAILED", "message": "why"}, map[string]any{"status": "failed", "events": []any{}, "next_seq": 55, "first_seq": 51, "latest_seq": 90, "dropped_count": 50, "has_more": true, "truncated": true, "error_code": "FAILED", "message": "why"}},
		{"browser_snapshot", map[string]any{"browser_ok": true, "session_id": "echo", "page_id": "page", "text": "body", "console_errors": []any{}, "network_errors": []any{}, "page_errors": []any{}, "viewport": map[string]any{"width": 800, "height": 600}}, map[string]any{"page_id": "page", "text": "body", "viewport": map[string]any{"width": 800, "height": 600}}},
		{"unknown", map[string]any{"count": 0, "flag": false, "result": nil, "action": "data"}, map[string]any{"count": 0, "flag": false, "result": nil, "action": "data"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := jsonText(t, tt.input)
			got := Project(tt.name, tt.input)
			assertJSON(t, got, tt.want)
			if jsonText(t, tt.input) != before {
				t.Fatal("projection mutated runtime state")
			}
			assertJSON(t, Project(tt.name, got), got)
		})
	}
}
func TestBuildBridgeEnvelopeRetainsCommandSuccessAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		payload := map[string]any{"exit_code": 0, "stdout": "must-survive", "command_ok": true}
		if failed {
			payload = map[string]any{"code": "PERMISSION_DENIED", "error": "denied", "details": map[string]any{"target_id": "needed-for-recovery"}}
		}
		input := map[string]any{"isError": failed, "structuredContent": payload, "content": []map[string]any{{"type": "text", "text": jsonText(t, payload)}}, "_meta": map[string]any{"extension": "preserved"}}
		response, err := Build("exec_command", input, false)
		if err != nil {
			t.Fatal(err)
		}
		if response.IsError != failed {
			t.Fatalf("error flag changed: %+v", response)
		}
		normalized, err := Normalize(response.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if failed {
			if normalized["code"] != "PERMISSION_DENIED" {
				t.Fatal("Bridge error lost")
			}
		} else {
			if normalized["stdout"] != "must-survive" {
				t.Fatal("Bridge stdout lost")
			}
		}
		if response.Meta["extension"] != "preserved" {
			t.Fatal("Bridge metadata lost")
		}
	}
}
func TestBuildLargeContentAppearsOnce(t *testing.T) {
	for _, tool := range []string{"read_file", "search_text", "list_dir", "task_manage", "browser_snapshot", "acp_prompt", "workflow_template_manage", "recall_read", "agentdock_context", "project_open"} {
		t.Run(tool, func(t *testing.T) {
			text := strings.Repeat("unique-marker\n\\\"中文", 4096)
			payload := map[string]any{"content": text}
			response, err := Build(tool, payload, false)
			if err != nil {
				t.Fatal(err)
			}
			encoded := jsonText(t, response)
			if strings.Count(encoded, "unique-marker") != 4096 {
				t.Fatal("body duplicated or lost")
			}
			for _, content := range response.Content {
				if text, ok := content.(*mcpsdk.TextContent); ok && len([]rune(text.Text)) > MaxSummaryRunes+1 {
					t.Fatal("unbounded text summary")
				}
			}
		})
	}
	text := strings.Repeat("x", 4<<20)
	response, err := Build("read_file", map[string]any{"content": text}, false)
	if err != nil {
		t.Fatal(err)
	}
	size := len(jsonText(t, response))
	if size > (4<<20)+1024 {
		t.Fatalf("4 MiB read result inflated to %d", size)
	}
	t.Logf("4 MiB content MCP result: %d bytes", size)
}
func TestDynamicContentAndPrivateMetadataAreNotDuplicated(t *testing.T) {
	remote := map[string]any{
		"content":           []map[string]any{{"type": "text", "text": "upstream-unique"}, {"type": "image", "mimeType": "image/png", "data": "aGk="}, {"type": "audio", "mimeType": "audio/wav", "data": "aGk="}, {"type": "resource_link", "name": "evidence", "uri": "https://example.test/item"}},
		"structuredContent": map[string]any{"zero": 0, "false": false, "null": nil}, "isError": true, "_meta": map[string]any{"private": "hidden-from-model"},
	}
	input := map[string]any{"name": "server:tool", "result": remote}
	before := jsonText(t, input)
	response, err := Build("mcp_tool_call", input, false)
	if err != nil {
		t.Fatal(err)
	}
	if !response.IsError || len(response.Content) != 4 {
		t.Fatal("upstream content/error changed")
	}
	if response.Meta["private"] != "hidden-from-model" {
		t.Fatal("private metadata lost")
	}
	encoded := jsonText(t, response)
	if strings.Count(encoded, "upstream-unique") != 1 {
		t.Fatal("upstream text duplicated")
	}
	payload, _ := Normalize(response.StructuredContent)
	raw := payload["result"].(map[string]any)
	if raw["content"] != nil || raw["_meta"] != nil {
		t.Fatal("upstream content/metadata still nested in model result")
	}
	assertJSON(t, raw["structuredContent"], remote["structuredContent"])
	if before != jsonText(t, input) {
		t.Fatal("upstream result mutated")
	}
	wrapped, _ := Normalize(response)
	wrapped["isError"] = true
	again, err := Build("mcp_tool_call", wrapped, false)
	if err != nil {
		t.Fatal(err)
	}
	assertJSON(t, again, response)
}
func TestBrowserFailureNeverBecomesSuccess(t *testing.T) {
	response, err := Build("browser_act", map[string]any{"browser_ok": false, "code": "LAUNCH_FAILED", "error": map[string]any{"code": "LAUNCH_FAILED", "message": "could not launch"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !response.IsError {
		t.Fatal("browser failure became a successful MCP call")
	}
	payload, _ := Normalize(response.StructuredContent)
	if payload["browser_ok"] != nil || payload["code"] != nil {
		t.Fatal("browser failure duplicated status/code")
	}
}
func TestProjectionPreservesMeaningfulCommandSignals(t *testing.T) {
	for _, status := range []string{"outcome_unknown", "interrupted", "starting"} {
		payload := map[string]any{"status": status, "session_id": "recoverable"}
		assertJSON(t, Project("exec_command", payload), payload)
		if strings.Contains(Summary("exec_command", payload, false), "completed") {
			t.Fatal("uncertain outcome summarized as completed")
		}
	}
	payload := map[string]any{"exit_code": -1, "command_error": "signal: segmentation fault"}
	assertJSON(t, Project("exec_command", payload), payload)
}
func TestProjectionPreservesSessionOutputAndInspectContract(t *testing.T) {
	projected := Project("session_observe", map[string]any{"sessions": []any{map[string]any{
		"session_id": "session-1", "status": "running", "stdout": "tail",
		"stdout_truncated": true, "stdout_total_bytes": 4096, "target_id": "private",
	}}})
	item := projected["sessions"].([]any)[0].(map[string]any)
	if item["stdout"] != "tail" || item["stdout_truncated"] != true || item["stdout_total_bytes"] != 4096 {
		t.Fatalf("session output was lost: %#v", item)
	}
	if _, exists := item["target_id"]; exists {
		t.Fatalf("session execution identity leaked: %#v", item)
	}
	inspect := Project("mcp_tool_inspect", map[string]any{"name": "server:tool", "description": "useful"})
	if inspect["name"] != nil || inspect["description"] != "useful" {
		t.Fatalf("inspect projection = %#v", inspect)
	}
	schema := Schema("mcp_tool_inspect", map[string]any{"type": "object", "required": []string{"name", "description"}, "properties": map[string]any{"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}}})
	if objectSchema(schema, "name") != nil {
		t.Fatalf("inspect schema still exposes name: %#v", schema)
	}
}
func TestDynamicToolSchemaDocumentsTopLevelContent(t *testing.T) {
	schema := Schema("mcp_tool_call", map[string]any{"type": "object", "properties": map[string]any{"result": map[string]any{"type": "object"}}})
	result := objectSchema(schema, "result")
	if description, _ := result["description"].(string); !strings.Contains(description, "returned once") {
		t.Fatalf("dynamic result schema description = %#v", result)
	}
}
func TestProjectDeliveryIdentifiersStayPrivate(t *testing.T) {
	payload := map[string]any{"work_session_id": "ws", "context_revision": "sha256:exact", "delivery": map[string]any{"context_revision": "sha256:exact", "status": "returned"}, "project": map[string]any{"id": "project", "revision": "internal"}, "targets": []any{map[string]any{"target_id": "target", "project_id": "project", "work_session_id": "ws", "context_revision": "private-target", "source_provenance": map[string]any{"head": "hash"}, "prompt": map[string]any{"prompt_revision": "private-prompt", "bytes": 5, "sources": []any{map[string]any{"path": "AGENTS.md", "scope": ".", "bytes": 5, "sha256": "private-sha", "content": "rules"}}}}}}
	before := jsonText(t, payload)
	response, err := Build("project_open", payload, false)
	if err != nil {
		t.Fatal(err)
	}
	body := jsonText(t, response.StructuredContent)
	for _, key := range []string{"context_revision", "prompt_revision", "sha256", "source_provenance", "bytes"} {
		if strings.Contains(body, key) {
			t.Fatalf("model result leaked %s", key)
		}
	}
	if !strings.Contains(body, "rules") || !strings.Contains(body, "target") {
		t.Fatal("Project rules or routing handle removed")
	}
	identity := response.Meta[ProjectDeliveryMetaKey].(map[string]any)
	if identity["context_revision"] != "sha256:exact" || identity["work_session_id"] != "ws" {
		t.Fatal("Host ACK identity changed")
	}
	if before != jsonText(t, payload) {
		t.Fatal("Project delivery mutated internal state")
	}
}
func TestSchemaProjectionDoesNotMutateContract(t *testing.T) {
	raw := map[string]any{"type": "object", "required": []string{"path", "entries", "partial", "truncated", "skipped_paths"}, "properties": map[string]any{"entries": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"path", "type", "name", "size_bytes", "modified", "is_hidden"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "size_bytes": map[string]any{"type": "integer"}}}}, "partial": map[string]any{"type": "boolean"}}}
	before := jsonText(t, raw)
	out := Schema("list_dir", raw)
	assertJSON(t, out["required"], []string{"path", "entries"})
	assertJSON(t, itemSchema(out, "entries")["required"], []string{"path", "type"})
	if before != jsonText(t, raw) {
		t.Fatal("schema mutated canonical runtime contract")
	}
	if !reflect.DeepEqual(Schema("other", raw), raw) {
		t.Fatal("unrelated schema changed")
	}
}
func TestBuildRejectsInvalidEncodingAndBoundsErrorSummary(t *testing.T) {
	if _, err := Build("file_edit", map[string]any{"bad": func() {}}, false); err == nil {
		t.Fatal("unsupported JSON result accepted")
	}
	if _, err := Build("view_image", map[string]any{"_mcp_image_base64": "invalid!"}, false); err == nil {
		t.Fatal("malformed image accepted")
	}
	message := strings.Repeat("错误", 2000)
	response, err := Build("read_file", map[string]any{"error": message}, true)
	if err != nil {
		t.Fatal(err)
	}
	text := response.Content[0].(*mcpsdk.TextContent).Text
	if len([]rune(text)) > MaxSummaryRunes+1 || !response.IsError {
		t.Fatal("error summary not bounded")
	}
	if response.StructuredContent.(map[string]any)["error"] != message {
		t.Fatal("diagnostic detail removed")
	}
}

func TestOptionalEnvelopeFlagAndLargeIntegers(t *testing.T) {
	response, err := Build("read_file", map[string]any{"content": []any{map[string]any{"type": "text", "text": "old full copy"}}, "structuredContent": map[string]any{"path": "x", "content": "actual text", "sequence": json.Number("9007199254740993")}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if response.IsError || response.StructuredContent.(map[string]any)["content"] != "actual text" {
		t.Fatal("omitted isError broke envelope decoding")
	}
	if !strings.Contains(jsonText(t, response.StructuredContent), "9007199254740993") {
		t.Fatal("Bridge rounded a large integer")
	}
	bare, err := Build("read_file", map[string]any{"path": "x", "content": "plain file content"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if bare.StructuredContent.(map[string]any)["content"] != "plain file content" {
		t.Fatal("file string treated as envelope")
	}
}
func TestUnknownAndContentOnlyEnvelopeKeepsIndependentText(t *testing.T) {
	for _, name := range []string{"vendor_tool", "read_file"} {
		payload := map[string]any{"isError": false, "content": []any{map[string]any{"type": "text", "text": "only available answer"}}}
		if name == "vendor_tool" {
			payload["structuredContent"] = map[string]any{"count": 0, "flag": false}
		}
		response, err := Build(name, payload, false)
		if err != nil {
			t.Fatal(err)
		}
		if response.Content[0].(*mcpsdk.TextContent).Text != "only available answer" {
			t.Fatal("independent upstream text discarded")
		}
	}
}
func TestDynamicSerializedCopyIsRemovedButIndependentTextSurvives(t *testing.T) {
	response, err := Build("mcp_tool_call", map[string]any{"name": "upstream:read", "result": map[string]any{"structuredContent": map[string]any{"body": "unique-payload", "n": 0}, "content": []any{
		map[string]any{"type": "text", "text": "{\n \"n\": 0, \"body\": \"unique-payload\"\n}"},
		map[string]any{"type": "text", "text": "independent explanation"},
	}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(jsonText(t, response), "unique-payload") != 1 {
		t.Fatal("upstream serialized structured result still duplicated")
	}
	if len(response.Content) != 1 || response.Content[0].(*mcpsdk.TextContent).Text != "independent explanation" {
		t.Fatal("independent upstream content removed")
	}
}

func TestCountAndNegativeStateAreNotBlindlyDeleted(t *testing.T) {
	for _, test := range []struct {
		tool  string
		input map[string]any
	}{
		{"workflow_template_manage", map[string]any{"count": 900, "vector_index_status": "ready"}},
		{"recall_search", map[string]any{"count": 150, "results": []any{map[string]any{"title": "one"}}}},
		{"skill_package", map[string]any{"configured": false, "removed": false}},
		{"acp_session", map[string]any{"authenticated": false, "changed": false, "deleted": false}},
	} {
		assertJSON(t, Project(test.tool, test.input), test.input)
	}
	assertJSON(t, Project("skill_package", map[string]any{"action": "env_set", "name": "s", "key": "TOKEN", "configured": true}), map[string]any{"name": "s", "key": "TOKEN"})
	response, err := Build("recall_write", map[string]any{"ok": false, "error": "could not write"}, false)
	if err != nil || !response.IsError {
		t.Fatalf("negative operation outcome hidden: %#v %v", response, err)
	}
}
