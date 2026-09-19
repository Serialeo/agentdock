package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/envstore"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

func TestRuntimeAvailableToolInputsAreStrictAtRoot(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	for _, definition := range runtime.ToolDefinitions() {
		if got := definition.InputSchema["additionalProperties"]; got != false {
			t.Fatalf("%s additionalProperties = %#v, want false", definition.Name, got)
		}
	}
}

func TestRuntimeCallRejectsUnknownArgumentsForFormerlyPermissiveTools(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	for _, test := range []struct {
		tool string
		args map[string]any
	}{
		{tool: "search_text", args: map[string]any{"query": "never", "future_field": true}},
		{tool: "read_file", args: map[string]any{"path": "missing.txt", "future_field": true}},
		{tool: "file_edit", args: map[string]any{"action": "replace", "future_field": true}},
		{tool: "task_manage", args: map[string]any{"action": "list", "future_field": true}},
		{tool: "skill_package", args: map[string]any{"action": "env_list", "future_field": true}},
		{tool: "view_image", args: map[string]any{"path": "missing.png", "future_field": true}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			assertInvalidToolArguments(t, runtime, test.tool, test.args)
		})
	}
}

func TestRuntimeCallEnforcesRequiredEnumBoundsAndOneOf(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	for _, test := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "required", tool: "exec_command", args: map[string]any{}},
		{name: "enum", tool: "exec_command", args: map[string]any{"cmd": "true", "execution_mode": "eventually"}},
		{name: "minimum", tool: "exec_command", args: map[string]any{"cmd": "true", "timeout_ms": 0}},
		{name: "maximum", tool: "mcp_tool_search", args: map[string]any{"query": "x", "limit": 101}},
		{name: "task_limit", tool: "task_manage", args: map[string]any{"action": "list", "limit": 201}},
		{name: "image_quality", tool: "view_image", args: map[string]any{"path": "a.png", "quality": 96}},
		{name: "image_format", tool: "view_image", args: map[string]any{"path": "a.png", "format": "webp"}},
		{name: "one_of", tool: "view_image", args: map[string]any{"path": "a.png", "url": "https://example.invalid/a.png"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidToolArguments(t, runtime, test.tool, test.args)
		})
	}
}

func TestRuntimeCallEnforcesTaskCreateRequiredFields(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	valid := map[string]any{
		"action":                "create",
		"title":                 "schema contract",
		"goal":                  "match runtime requirements",
		"completion_conditions": []any{"task is created"},
	}
	for _, field := range []string{"title", "goal", "completion_conditions"} {
		args := make(map[string]any, len(valid)-1)
		for key, value := range valid {
			if key != field {
				args[key] = value
			}
		}
		t.Run("missing_"+field, func(t *testing.T) {
			assertInvalidToolArguments(t, runtime, "task_manage", args)
		})
	}

	if _, err := runtime.Call(context.Background(), "task_manage", valid); err != nil {
		t.Fatalf("schema-complete task create failed: %v", err)
	}
}

func TestRuntimeCallEnforcesTaskCheckpointShape(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	invalid := []struct {
		name string
		args map[string]any
	}{
		{name: "checkpoint missing task id", args: map[string]any{"action": "checkpoint", "summary": "progress"}},
		{name: "checkpoint missing summary", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing"}},
		{name: "checkpoint blank summary", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "summary": " \n "}},
		{name: "checkpoint step missing status", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "step_id": "step-1", "summary": "progress"}},
		{name: "checkpoint status missing step", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "status": "pending", "summary": "progress"}},
		{name: "checkpoint wrong step status", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "step_id": "step-1", "status": "active", "summary": "progress"}},
		{name: "checkpoint batch with single field", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "completed_step_ids": []any{"step-1"}, "step_id": "step-1", "summary": "progress"}},
		{name: "checkpoint batch with status", args: map[string]any{"action": "checkpoint", "task_id": "tsk_missing", "current_step_id": "step-1", "status": "in_progress", "summary": "progress"}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidToolArguments(t, runtime, "task_manage", test.args)
		})
	}
}

func TestRuntimeCallAllowsTaskCheckpointModes(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	call := func(args map[string]any) {
		t.Helper()
		if _, err := runtime.Call(context.Background(), "task_manage", args); err != nil {
			t.Fatalf("task_manage call failed: %v", err)
		}
	}
	created, err := runtime.Call(context.Background(), "task_manage", map[string]any{
		"action": "create", "title": "schema modes", "goal": "exercise task schema", "completion_conditions": []any{"done"},
		"steps": []any{map[string]any{"id": "step-1", "title": "first"}, map[string]any{"id": "step-2", "title": "second"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created["task_id"]
	call(map[string]any{"action": "checkpoint", "task_id": id, "summary": "summary-only progress"})
	call(map[string]any{"action": "checkpoint", "task_id": id, "step_id": "step-1", "status": "in_progress", "summary": "single-step progress"})
	call(map[string]any{"action": "checkpoint", "task_id": id, "completed_step_ids": []any{"step-1"}, "current_step_id": "step-2", "summary": "batch progress"})
}

func TestRuntimeCallRejectsNestedUnknownFields(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	assertInvalidToolArguments(t, runtime, "task_manage", map[string]any{
		"action": "create",
		"title":  "schema test",
		"goal":   "schema test",
		"steps": []any{
			map[string]any{"id": "step-1", "title": "test", "future_field": true},
		},
	})
}

func TestRuntimeCallRejectsWrongDeclaredArgumentTypes(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{name: "command integer as string", tool: "exec_command", args: map[string]any{"cmd": "true", "timeout_ms": "12"}},
		{name: "command env non string value", tool: "exec_command", args: map[string]any{"cmd": "true", "env": map[string]any{"COUNT": 1}}},
		{name: "file integer as string", tool: "read_file", args: map[string]any{"path": "missing.txt", "max_bytes": "128"}},
		{name: "file bool as string", tool: "file_edit", args: map[string]any{"action": "replace", "path": "missing.txt", "replace_all": "true"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidToolArguments(t, runtime, test.tool, test.args)
		})
	}
}

func TestRuntimeCallRejectsDeclaredNullArguments(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	assertInvalidToolArguments(t, runtime, "exec_command", map[string]any{"cmd": "true", "timeout_ms": nil})
}

func TestRuntimeValidationAllowsNullInsideDynamicMCPArguments(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	args := map[string]any{
		"name":      "demo:tool",
		"arguments": map[string]any{"nullable_downstream_value": nil},
	}
	if err := runtime.validateToolArguments("mcp_tool_call", args); err != nil {
		t.Fatalf("dynamic leaf null rejected by schema: %v", err)
	}
	var request struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := decodeToolInput("mcp_tool_call", args, &request); err != nil {
		t.Fatalf("dynamic leaf null rejected by decoder: %v", err)
	}
	if value, exists := request.Arguments["nullable_downstream_value"]; !exists || value != nil {
		t.Fatalf("arguments = %#v, want preserved nested null", request.Arguments)
	}
}

func TestRuntimeCallRejectsRemovedListDirArguments(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	for _, field := range []string{"recursive", "glob", "max_results"} {
		assertInvalidToolArguments(t, runtime, "list_dir", map[string]any{"path": ".", field: true})
	}
}

func TestRuntimeCallRejectsUnknownArgumentsForCanonicalTools(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	assertInvalidToolArguments(t, runtime, "agentdock_context", map[string]any{"future_field": true})
}

func assertInvalidToolArguments(t *testing.T, runtime *Runtime, tool string, args map[string]any) {
	t.Helper()
	_, err := runtime.Call(context.Background(), tool, args)
	toolErr, ok := err.(*ToolError)
	if !ok || toolErr.Code != "INVALID_ARGUMENT" || toolErr.Category != "validation" {
		t.Fatalf("%s error = %T %#v, want INVALID_ARGUMENT validation error", tool, err, err)
	}
	reason, _ := toolErr.Details["reason"].(string)
	if strings.TrimSpace(reason) == "" {
		t.Fatalf("%s validation error has no reason: %#v", tool, toolErr)
	}
}

func newRuntimeValidationTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		AgentDockDefaultDir: root,
		AgentDockHome:       filepath.Join(root, ".agentdock"),
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})
	return runtime
}

func TestRuntimeSkillManageEnvironmentDoesNotReturnSecretValue(t *testing.T) {
	cfg := config.Config{AgentDockHome: filepath.Join(t.TempDir(), ".agentdock"), AgentDockDefaultDir: t.TempDir()}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	empty := ""
	if _, err := runtime.RuntimeSkillManage(t.Context(), toolskill.PackageRequest{Action: "env_set", Skill: "agentdock-user-guide", Key: "EMPTY_VALUE", Value: &empty}); err != nil {
		t.Fatal(err)
	}
	listed, err := runtime.RuntimeSkillManage(t.Context(), toolskill.PackageRequest{Action: "env_list", Skill: "agentdock-user-guide"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"value"`) || strings.Contains(string(encoded), "secret") {
		t.Fatalf("Skill environment listing leaked value material: %s", encoded)
	}
	items, ok := listed["items"].([]envstore.Entry)
	if !ok || len(items) != 1 || items[0].Key != "EMPTY_VALUE" || items[0].Configured {
		t.Fatalf("Skill environment metadata = %#v", listed)
	}
	if _, err := runtime.RuntimeSkillManage(t.Context(), toolskill.PackageRequest{Action: "env_unset", Skill: "agentdock-user-guide", Key: "EMPTY_VALUE"}); err != nil {
		t.Fatal(err)
	}
}
