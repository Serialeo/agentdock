package taskstate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func queryTaskFixture(id int, at time.Time) Task {
	return Task{
		SchemaVersion: SchemaVersion,
		ID:            fmt.Sprintf("tsk_%016x", id),
		Title:         "ordinary task",
		Goal:          "ordinary goal",
		Status:        StatusActive,
		Phase:         PhaseExecute,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
}

func saveQueryTasks(t *testing.T, tasks ...Task) *Store {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err := store.saveLocked(task); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func queryPageIDs(page ListPage) []string {
	ids := make([]string, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestListPageFiltersAllTasksBeforePagination(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	old := at.Add(-30 * 24 * time.Hour)
	tasks := make([]Task, 0, 244)
	for i := 1; i <= 240; i++ {
		tasks = append(tasks, queryTaskFixture(i, at.Add(time.Duration(i)*time.Minute)))
	}
	for i := 241; i <= 244; i++ {
		task := queryTaskFixture(i, old.Add(time.Duration(i-241)*time.Hour))
		task.Title = "Archived deployment"
		task.Status = StatusCompleted
		tasks = append(tasks, task)
	}
	store := saveQueryTasks(t, tasks...)
	before := old.Add(24 * time.Hour)
	page, err := store.ListPage(ListOptions{
		Status: StatusCompleted, Query: "ARCHIVED", From: &old, To: &before,
		Offset: 1, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{tasks[242].ID, tasks[241].ID}
	if got := queryPageIDs(page); !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered task IDs = %v, want %v", got, want)
	}
	if page.Total != 4 || page.Offset != 1 || page.Limit != 2 || !page.HasMore {
		t.Fatalf("filtered page metadata = %#v", page)
	}
	if page.Counts != (TaskCounts{All: 4, Completed: 4}) {
		t.Fatalf("counts = %#v", page.Counts)
	}
	last, err := store.ListPage(ListOptions{Query: "archived", Offset: 3, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if last.Total != 4 || last.HasMore || len(last.Tasks) != 1 || last.Tasks[0].ID != tasks[240].ID {
		t.Fatalf("last page = %#v", last)
	}
}

func TestListPageSortsBySelectedTimeThenID(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	a := queryTaskFixture(1, at)
	b := queryTaskFixture(2, at)
	c := queryTaskFixture(3, at.Add(time.Hour))
	a.UpdatedAt = at.Add(2 * time.Hour)
	b.UpdatedAt = a.UpdatedAt
	c.UpdatedAt = at
	store := saveQueryTasks(t, c, a, b)
	for _, test := range []struct {
		field string
		want  []string
	}{
		{"", []string{b.ID, a.ID, c.ID}},
		{"updated_at", []string{b.ID, a.ID, c.ID}},
		{"created_at", []string{c.ID, b.ID, a.ID}},
	} {
		t.Run(test.field, func(t *testing.T) {
			var ids []string
			for offset := 0; offset < 3; offset++ {
				page, err := store.ListPage(ListOptions{TimeField: test.field, Offset: offset, Limit: 1})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, queryPageIDs(page)...)
				if page.Total != 3 || page.HasMore != (offset < 2) {
					t.Fatalf("page metadata at offset %d = %#v", offset, page)
				}
			}
			if !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("task IDs across pages = %v, want %v", ids, test.want)
			}
		})
	}
}

func TestListPageTimeBoundsUseInstantsAndSelectedField(t *testing.T) {
	from, err := time.Parse(time.RFC3339Nano, "2026-09-13T00:00:00-07:00")
	if err != nil {
		t.Fatal(err)
	}
	to := from.Add(24 * time.Hour)
	for _, field := range []string{"created_at", "updated_at"} {
		t.Run(field, func(t *testing.T) {
			tasks := make([]Task, 0, 5)
			for i, instant := range []time.Time{from.Add(-time.Nanosecond), from, to.Add(-time.Nanosecond), to, to.Add(time.Nanosecond)} {
				task := queryTaskFixture(i+1, from.Add(-48*time.Hour))
				if field == "created_at" {
					task.CreatedAt = instant.UTC()
				} else {
					task.UpdatedAt = instant.UTC()
				}
				tasks = append(tasks, task)
			}
			store := saveQueryTasks(t, tasks...)
			for _, test := range []struct {
				name string
				from *time.Time
				to   *time.Time
				want []string
			}{
				{"both", &from, &to, []string{tasks[2].ID, tasks[1].ID}},
				{"from only", &from, nil, []string{tasks[4].ID, tasks[3].ID, tasks[2].ID, tasks[1].ID}},
				{"to only", nil, &to, []string{tasks[2].ID, tasks[1].ID, tasks[0].ID}},
			} {
				t.Run(test.name, func(t *testing.T) {
					page, err := store.ListPage(ListOptions{TimeField: field, From: test.from, To: test.to})
					if err != nil {
						t.Fatal(err)
					}
					if got := queryPageIDs(page); !reflect.DeepEqual(got, test.want) || page.Total != len(test.want) {
						t.Fatalf("task IDs = %v, total = %d, want %v", got, page.Total, test.want)
					}
				})
			}
		})
	}
}

func TestListPageSearchesUntruncatedListFields(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		query  string
		change func(*Task)
	}{
		{"id", "TSK_0000000000000001", func(*Task) {}},
		{"title", "needle", func(task *Task) { task.Title = "Release NEEDLE" }},
		{"goal", "needle", func(task *Task) { task.Goal = "Find NEEDLE" }},
		{"status", "ACTIVE", func(*Task) {}},
		{"summary", "needle", func(task *Task) { task.Summary = strings.Repeat("x", 300) + "NEEDLE" }},
		{"blocker", "needle", func(task *Task) { task.Blocker = strings.Repeat("x", 300) + "NEEDLE" }},
		{"current in progress", "needle", func(task *Task) {
			task.Steps = []TaskStep{{ID: "pending", Title: "Earlier pending step", Status: StepPending}, {ID: "current", Title: strings.Repeat("x", 150) + "NEEDLE", Status: StepInProgress}}
		}},
		{"current pending", "needle", func(task *Task) {
			task.Steps = []TaskStep{{ID: "done", Title: "Completed step", Status: StepCompleted}, {ID: "next", Title: "NEEDLE", Status: StepPending}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := queryTaskFixture(1, at)
			test.change(&task)
			page, err := saveQueryTasks(t, task).ListPage(ListOptions{Query: test.query})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || len(page.Tasks) != 1 || page.Tasks[0].ID != task.ID {
				t.Fatalf("search %q failed: %#v", test.query, page)
			}
		})
	}
	for _, steps := range [][]TaskStep{
		{{ID: "done", Title: "NEEDLE", Status: StepCompleted}, {ID: "next", Title: "ordinary", Status: StepPending}},
		{{ID: "current", Title: "ordinary", Status: StepInProgress}, {ID: "next", Title: "NEEDLE", Status: StepPending}},
		{{ID: "first", Title: "ordinary", Status: StepPending}, {ID: "later", Title: "NEEDLE", Status: StepPending}},
	} {
		task := queryTaskFixture(1, at)
		task.Steps = steps
		page, err := saveQueryTasks(t, task).ListPage(ListOptions{Query: "needle"})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 0 || len(page.Tasks) != 0 {
			t.Fatalf("non-current step matched: %#v", steps)
		}
	}
}

func TestListPageCountsIgnoreStatusAndPaginationButKeepOtherFilters(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	var tasks []Task
	for i, status := range []Status{StatusActive, StatusActive, StatusBlocked, StatusCompleted, StatusCompleted, StatusCompleted} {
		task := queryTaskFixture(i+1, at)
		task.Title = "matching release"
		task.Status = status
		tasks = append(tasks, task)
	}
	noQuery := queryTaskFixture(7, at)
	noTime := queryTaskFixture(8, at.Add(-24*time.Hour))
	noTime.Title = "matching release"
	store := saveQueryTasks(t, append(tasks, noQuery, noTime)...)
	for _, status := range []Status{"", "all", StatusActive, StatusBlocked, StatusCompleted} {
		page, err := store.ListPage(ListOptions{Status: status, Query: "matching", From: &at, Offset: 1, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if page.Counts != (TaskCounts{All: 6, Active: 2, Blocked: 1, Completed: 3}) {
			t.Fatalf("status %q counts = %#v", status, page.Counts)
		}
		wantTotal := map[Status]int{"": 6, "all": 6, StatusActive: 2, StatusBlocked: 1, StatusCompleted: 3}[status]
		if page.Total != wantTotal || page.HasMore != (wantTotal > 2) {
			t.Fatalf("status %q metadata = %#v", status, page)
		}
		for _, task := range page.Tasks {
			if status != "" && status != "all" && task.Status != status {
				t.Fatalf("status %q returned task %#v", status, task)
			}
		}
	}
}

func TestListPageDefaultsAndOffsetsBeyondTotal(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	var tasks []Task
	for i := 1; i <= 51; i++ {
		tasks = append(tasks, queryTaskFixture(i, at))
	}
	store := saveQueryTasks(t, tasks...)
	page, err := store.ListPage(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 51 || page.Limit != 50 || page.Offset != 0 || len(page.Tasks) != 50 || !page.HasMore {
		t.Fatalf("default page = %#v", page)
	}
	for _, offset := range []int{51, 100, int(^uint(0) >> 1)} {
		page, err := store.ListPage(ListOptions{Offset: offset, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 51 || page.Offset != offset || page.Tasks == nil || len(page.Tasks) != 0 || page.HasMore {
			t.Fatalf("page past end at offset %d = %#v", offset, page)
		}
	}
}

func TestListPageRejectsInvalidOptions(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	before := at.Add(-time.Hour)
	store := saveQueryTasks(t)
	for _, options := range []ListOptions{
		{Status: "unknown"}, {TimeField: "completed_at"}, {Offset: -1}, {Limit: -1}, {Limit: 201},
		{From: &at, To: &at}, {From: &at, To: &before},
	} {
		if _, err := NormalizeListOptions(options); err == nil {
			t.Fatalf("NormalizeListOptions accepted %#v", options)
		}
		if _, err := store.ListPage(options); err == nil {
			t.Fatalf("ListPage accepted %#v", options)
		}
	}
}
