package taskstate

import (
	"reflect"
	"strings"
	"testing"
)

func TestCreatePreservesDistinctAcceptanceRequirements(t *testing.T) {
	pairs := [][2]string{
		{"普通用户可以读取项目配置文件", "普通用户不可以读取项目配置文件"},
		{"请求超时不得超过 10 秒", "请求超时不得超过 30 秒"},
		{"macOS 原生点击后必须获取新截图", "Windows 原生点击后必须获取新截图"},
		{"部署完成后服务必须正常运行", "部署完成后服务必须正常运行，并通过健康检查"},
		{"environment key is PATH", "environment key is Path"},
	}
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		task, err := store.Create("review", "preserve requirements", []string{pair[0], pair[1], " " + pair[0] + " "}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := store.Get(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Conditions) != 2 || saved.Conditions[0].Text != pair[0] || saved.Conditions[1].Text != pair[1] {
			t.Fatalf("requirements lost: %#v", saved.Conditions)
		}
	}
	if _, err := store.Create("review", "limits", make([]string, maxTaskConditions+1), nil, nil); err == nil {
		t.Fatal("accepted oversized raw condition array")
	}
	if _, err := store.Create("review", "limits", []string{strings.Repeat(" ", maxTaskConditionBytes+1)}, nil, nil); err == nil {
		t.Fatal("accepted oversized raw text")
	}
}

func TestReopenInvalidatesReviewAndKeepsEvidenceHistory(t *testing.T) {
	for _, status := range []string{FinalReviewPass, FinalReviewFailed} {
		t.Run(status, func(t *testing.T) {
			store, _ := New(t.TempDir())
			task, err := store.Create("review", "fix", []string{"tests pass"}, []TaskStepInput{{ID: "test", Title: "Test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			task, err = store.Checkpoint(task.ID, "test", StepCompleted, "tested")
			if err != nil {
				t.Fatal(err)
			}
			task, err = store.FinalReview(task.ID, FinalReviewInput{Status: status, Summary: "review", OpenRisks: []string{"recheck"}, Evidence: []ConditionEvidence{{ConditionID: task.Conditions[0].ID, Summary: "test result", EvidenceRef: "command-result:123"}}})
			if err != nil {
				t.Fatal(err)
			}
			task, err = store.Get(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			original := *task.FinalReview
			task, err = store.Reopen(task.ID, "test", "regression found")
			if err != nil {
				t.Fatal(err)
			}
			if task.Steps[0].Status != StepInProgress || task.Steps[0].Revision != 1 || task.FinalReview != nil || len(task.ReviewHistory) != 1 || !reflect.DeepEqual(task.ReviewHistory[0].Review, original) {
				t.Fatalf("bad reopen: %#v", task)
			}
			if _, err := store.Complete(task.ID); err == nil {
				t.Fatal("completed using invalid review")
			}
			store, _ = New(store.Root())
			task, err = store.Get(task.ID)
			if err != nil || task.ReviewHistory[0].Reason != "regression found" {
				t.Fatalf("history not persisted: %v", err)
			}
			if _, err := store.Checkpoint(task.ID, "test", StepCompleted, "fixed"); err != nil {
				t.Fatal(err)
			}
			task, err = store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "fixed", VerifiedFacts: []string{"passed"}})
			if err != nil {
				t.Fatal(err)
			}
			if task.FinalReview.ReviewRevision == original.ReviewRevision {
				t.Fatal("reused review revision")
			}
			if _, err := store.Complete(task.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Reopen(task.ID, "test", "late"); err == nil {
				t.Fatal("reopened archived task")
			}
		})
	}
}

func TestConditionEvidenceRejectsUnknownIDsAndMachineClaimsAtomically(t *testing.T) {
	store, _ := New(t.TempDir())
	task, _ := store.Create("review", "evidence", []string{"done"}, nil, nil)
	for _, item := range []ConditionEvidence{{ConditionID: "missing", Summary: "done"}, {ConditionID: "cond_01", Summary: "done", Source: "machine_verified"}} {
		if _, err := store.FinalReview(task.ID, FinalReviewInput{Status: FinalReviewPass, Summary: "done", Evidence: []ConditionEvidence{item}}); err == nil {
			t.Fatal("invalid evidence accepted")
		}
		saved, _ := store.Get(task.ID)
		if saved.FinalReview != nil {
			t.Fatal("failed review mutated task")
		}
	}
}
