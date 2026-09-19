package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskCheckpointLifecycleThroughRuntimeContract(t *testing.T) {
	runtime := newRuntimeValidationTestRuntime(t)
	call := func(args map[string]any) Result {
		t.Helper()
		result, err := runtime.Call(t.Context(), "task_manage", args)
		if err != nil {
			t.Fatal(err)
		}
		assertToolResultMatchestestOutputSchema(t, "task_manage", result)
		return result
	}
	created := call(map[string]any{"action": "create", "title": "Recover", "goal": "save progress", "completion_conditions": []string{"done"}})
	policy := created["checkpoint_policy"]
	if policy == nil {
		t.Fatal("create did not deliver checkpoint policy")
	}
	id := created["task_id"]
	call(map[string]any{"action": "checkpoint", "task_id": id, "summary": "inspected; next: fix"})
	loaded := call(map[string]any{"action": "get", "task_id": id})
	if !reflect.DeepEqual(loaded["checkpoint_policy"], policy) || loaded["task"].(taskstate.Task).Summary != "inspected; next: fix" {
		t.Fatalf("get lost progress or policy: %#v", loaded)
	}
	call(map[string]any{"action": "block", "task_id": id, "summary": "waiting for dependency"})
	resumed := call(map[string]any{"action": "resume", "task_id": id, "summary": "dependency ready"})
	if !reflect.DeepEqual(resumed["checkpoint_policy"], policy) {
		t.Fatalf("resume lost policy: %#v", resumed)
	}
	call(map[string]any{"action": "checkpoint", "task_id": id, "summary": "fixed and tested"})
	call(map[string]any{"action": "final_review", "task_id": id, "status": "pass", "summary": "verified", "verified": []string{"done"}})
	call(map[string]any{"action": "complete", "task_id": id})

	description := taskManageToolSpecs()[0].Description
	for _, trigger := range []string{"checkpoint timing and content", "task_id and summary", "summary-only", "checkpoint_policy"} {
		if !strings.Contains(description, trigger) {
			t.Fatalf("tool discovery lost checkpoint trigger %q: %s", trigger, description)
		}
	}
}
