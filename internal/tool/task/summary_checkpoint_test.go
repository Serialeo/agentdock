package task

import (
	"context"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskManageSummaryCheckpointRecoveryAndPolicy(t *testing.T) {
	svc, root := newTaskTestService(t)
	ctx := context.Background()
	created, err := svc.Manage(ctx, ManageRequest{Action: "create", Title: "Recover", Goal: "save unplanned progress", CompletionConditions: []string{"done"}})
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := created["checkpoint_policy"].(checkpointPolicy)
	if !ok || policy.Source == "" || policy.Version == "" || policy.Enforcement != "caller_driven" || len(policy.Rules) == 0 {
		t.Fatalf("missing checkpoint policy: %#v", created)
	}
	id := created["task_id"].(string)
	progress, err := svc.Manage(ctx, ManageRequest{Action: "checkpoint", TaskID: id, Summary: "found cause; next: fix"})
	if err != nil {
		t.Fatal(err)
	}
	summary := progress["task_summary"].(map[string]any)
	if summary["step_count"] != 0 || summary["summary"] != "found cause; next: fix" {
		t.Fatalf("summary checkpoint missing: %#v", progress)
	}
	svc, _ = newTaskTestServiceAt(t, root)
	loaded, err := svc.Manage(ctx, ManageRequest{Action: "get", TaskID: id})
	if err != nil {
		t.Fatal(err)
	}
	saved := loaded["task"].(taskstate.Task)
	if !reflect.DeepEqual(loaded["checkpoint_policy"], policy) || saved.Summary != summary["summary"] || saved.Events[len(saved.Events)-1].Type != "checkpoint" {
		t.Fatalf("recovery state or policy lost: %#v", loaded)
	}
	if _, err := svc.Manage(ctx, ManageRequest{Action: "block", TaskID: id, Summary: "awaiting access"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.Manage(ctx, ManageRequest{Action: "resume", TaskID: id, Summary: "access restored"})
	if err != nil || !reflect.DeepEqual(resumed["checkpoint_policy"], policy) {
		t.Fatalf("resume policy missing: %#v, %v", resumed, err)
	}
	if _, err := svc.Manage(ctx, ManageRequest{Action: "checkpoint", TaskID: id, Summary: "fixed; tests pass"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Manage(ctx, ManageRequest{Action: "final_review", TaskID: id, Status: "pass", Summary: "verified", Verified: []string{"done"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Manage(ctx, ManageRequest{Action: "complete", TaskID: id}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskManageIncompleteStepFieldsDoNotBecomeSummaryCheckpoints(t *testing.T) {
	svc, _ := newTaskTestService(t)
	ctx := context.Background()
	created, err := svc.Manage(ctx, ManageRequest{Action: "create", Title: "Recover", Goal: "validate modes", CompletionConditions: []string{"done"}, Steps: []StepRequest{{ID: "work", Title: "Work"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := created["task_id"].(string)
	before, err := svc.tasks.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	emptyBatch := []string{}
	for _, input := range []ManageRequest{
		{StepID: "work"},
		{Status: "completed"},
		{StepID: "unknown", Status: "completed"},
		{CurrentStepID: "unknown"},
		{CompletedStepIDs: &emptyBatch},
		{StepID: "work", Status: "completed", CurrentStepID: "work"},
	} {
		input.Action, input.TaskID, input.Summary = "checkpoint", id, "must reject"
		if _, err := svc.Manage(ctx, input); err == nil {
			t.Fatalf("invalid fields accepted: %#v", input)
		}
		after, err := svc.tasks.Get(id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("rejected checkpoint changed task: %v", err)
		}
	}
}
