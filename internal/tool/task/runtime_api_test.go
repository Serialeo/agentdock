package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestRuntimeTasksReturnsFilteredPaginationAndCounts(t *testing.T) {
	store, err := taskstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for i, status := range []taskstate.Status{taskstate.StatusActive, taskstate.StatusCompleted, taskstate.StatusCompleted, taskstate.StatusBlocked} {
		task := taskstate.Task{
			SchemaVersion: taskstate.SchemaVersion, ID: fmt.Sprintf("tsk_%016x", i+1),
			Title: "Release deployment", Goal: "Ship a release", Status: status, Phase: taskstate.PhaseExecute,
			CreatedAt: at.Add(-24 * time.Hour), UpdatedAt: at,
			Events: []taskstate.Event{{Type: "created", CreatedAt: at.Add(-24 * time.Hour)}},
		}
		data, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.Root(), task.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := New(nil, store)
	result, err := service.RuntimeTasks(taskstate.ListOptions{
		Status: taskstate.StatusCompleted, Query: "release", From: &at, Offset: 1, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		OK      bool                 `json:"ok"`
		Source  string               `json:"source"`
		Action  string               `json:"action"`
		Count   int                  `json:"count"`
		Total   int                  `json:"total"`
		Offset  int                  `json:"offset"`
		Limit   int                  `json:"limit"`
		HasMore bool                 `json:"has_more"`
		Counts  taskstate.TaskCounts `json:"counts"`
		Tasks   []struct {
			ID         string    `json:"id"`
			CreatedAt  time.Time `json:"created_at"`
			EventCount int       `json:"event_count"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if !wire.OK || wire.Source != "agentdock-api" || wire.Action != "list" || wire.Count != 1 || wire.Total != 2 || wire.Offset != 1 || wire.Limit != 1 || wire.HasMore {
		t.Fatalf("response metadata = %s", data)
	}
	if wire.Counts != (taskstate.TaskCounts{All: 4, Active: 1, Blocked: 1, Completed: 2}) {
		t.Fatalf("counts = %#v", wire.Counts)
	}
	if len(wire.Tasks) != 1 || wire.Tasks[0].ID != "tsk_0000000000000002" || wire.Tasks[0].EventCount != 1 || !wire.Tasks[0].CreatedAt.Equal(at.Add(-24*time.Hour)) {
		t.Fatalf("tasks = %s", data)
	}
	counts, ok := result["counts"]
	if !ok {
		t.Fatal("response is missing counts")
	}
	countsJSON, err := json.Marshal(counts)
	if err != nil {
		t.Fatal(err)
	}
	var countKeys map[string]int
	if err := json.Unmarshal(countsJSON, &countKeys); err != nil {
		t.Fatal(err)
	}
	if len(countKeys) != 4 || countKeys["all"] != 4 || countKeys["active"] != 1 || countKeys["blocked"] != 1 || countKeys["completed"] != 2 {
		t.Fatalf("count JSON fields = %s", countsJSON)
	}
}

func TestRuntimeTasksEmptyPageSerializesAsArray(t *testing.T) {
	store, err := taskstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(nil, store).RuntimeTasks(taskstate.ListOptions{Offset: 100, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["tasks"]) != "[]" || string(wire["count"]) != "0" || string(wire["total"]) != "0" || string(wire["has_more"]) != "false" || string(wire["offset"]) != "100" || string(wire["limit"]) != "20" {
		t.Fatalf("empty response = %s", data)
	}
}
