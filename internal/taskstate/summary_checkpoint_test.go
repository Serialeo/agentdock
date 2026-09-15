package taskstate

import (
	"reflect"
	"strings"
	"testing"
)

func TestSummaryCheckpointPersistsWithoutChangingSteps(t *testing.T) {
	for _, withSteps := range []bool{false, true} {
		name := "without_steps"
		var steps []TaskStepInput
		if withSteps {
			name = "long_running_step"
			steps = []TaskStepInput{{ID: "work", Title: "Work", Phase: PhaseExecute}}
		}
		t.Run(name, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.Create("Recover work", "save progress", []string{"done"}, steps, nil)
			if err != nil {
				t.Fatal(err)
			}
			if withSteps {
				before, err = store.Checkpoint(before.ID, "work", StepInProgress, "started")
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err = store.Get(before.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, summary := range []string{"found cause; next: fix", strings.Repeat("x", maxTaskSummaryBytes)} {
				updated, err := store.SummaryCheckpoint(before.ID, " "+summary+" ")
				if err != nil {
					t.Fatal(err)
				}
				if updated.Summary != summary || updated.Phase != before.Phase || updated.Status != before.Status || !reflect.DeepEqual(updated.Steps, before.Steps) || len(updated.Events) != len(before.Events)+1 {
					t.Fatalf("unexpected summary checkpoint: %#v", updated)
				}
				if updated.Events[len(updated.Events)-1].Type != "checkpoint" || !taskExecutionStarted(updated) {
					t.Fatal("summary checkpoint must record execution progress")
				}
				retry, err := store.SummaryCheckpoint(before.ID, summary)
				if err != nil || len(retry.Events) != len(updated.Events) {
					t.Fatalf("duplicate checkpoint appended an event: %v", err)
				}
				store, err = New(store.Root())
				if err != nil {
					t.Fatal(err)
				}
				saved, err := store.Get(before.ID)
				if err != nil || !reflect.DeepEqual(saved, retry) {
					t.Fatalf("checkpoint did not survive restart: %v", err)
				}
				before = saved
			}
		})
	}
}

func TestSummaryCheckpointRejectsInvalidInputAndClosedStateAtomically(t *testing.T) {
	for _, state := range []string{"active", "blocked", "review_passed", "completed"} {
		t.Run(state, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.Create("Recover", "save progress", []string{"done"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "blocked":
				_, err = store.Block(task.ID, "waiting")
			case "review_passed", "completed":
				_, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "done", VerifiedFacts: []string{"done"}})
				if err == nil && state == "completed" {
					_, err = store.Complete(task.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.Get(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			summaries := []string{"should reject closed task"}
			if state == "active" {
				summaries = []string{"", " \n ", strings.Repeat("x", maxTaskSummaryBytes+1)}
			}
			for _, summary := range summaries {
				if _, err := store.SummaryCheckpoint(task.ID, summary); err == nil {
					t.Fatal("invalid checkpoint accepted")
				}
				after, err := store.Get(task.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected checkpoint changed state: %v", err)
				}
			}
		})
	}
}

func TestSummaryCheckpointPreservesFailedReviewHistory(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("Recover", "save progress", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewFailed, Summary: "needs fix", OpenRisks: []string{"regression"}})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	review := *task.FinalReview
	task, err = store.SummaryCheckpoint(task.ID, "needs fix")
	if err != nil {
		t.Fatal(err)
	}
	if task.FinalReview != nil || len(task.ReviewHistory) != 1 || !reflect.DeepEqual(task.ReviewHistory[0].Review, review) || task.Events[len(task.Events)-1].Type != "checkpoint" {
		t.Fatalf("failed review was lost or reused: %#v", task)
	}
	if _, err := store.Complete(task.ID); err == nil {
		t.Fatal("summary checkpoint bypassed final review")
	}
}
