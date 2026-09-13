package runtimeapi

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type taskQueryRuntimeStub struct {
	runtimeStub
	options taskstate.ListOptions
	calls   int
}

func (r *taskQueryRuntimeStub) RuntimeTasks(options taskstate.ListOptions) (app.Result, error) {
	r.options = options
	r.calls++
	return app.Result{"ok": true}, nil
}

func TestDispatchTaskQueryForwardsAllFilters(t *testing.T) {
	runtime := &taskQueryRuntimeStub{}
	_, err := Dispatch(context.Background(), runtime, Request{
		Method: "GET", Path: "/internal/runtime/tasks",
		Query: url.Values{
			"status": {"completed"}, "q": {"older deployment"}, "time_field": {"created_at"},
			"from": {"2026-08-01T00:00:00-07:00"}, "to": {"2026-09-01T00:00:00-07:00"},
			"offset": {"201"}, "limit": {"200"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	options := runtime.options
	if runtime.calls != 1 || options.Status != taskstate.StatusCompleted || options.Query != "older deployment" || options.TimeField != "created_at" || options.Offset != 201 || options.Limit != 200 {
		t.Fatalf("runtime calls = %d, options = %#v", runtime.calls, options)
	}
	wantFrom := time.Date(2026, 8, 1, 7, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	if options.From == nil || !options.From.Equal(wantFrom) || options.To == nil || !options.To.Equal(wantTo) {
		t.Fatalf("time bounds = %v, %v", options.From, options.To)
	}
}

func TestDispatchTaskQueryDefaultsAndAllStatus(t *testing.T) {
	for _, query := range []url.Values{nil, {"status": {""}}, {"status": {"all"}}} {
		runtime := &taskQueryRuntimeStub{}
		_, err := Dispatch(context.Background(), runtime, Request{Method: "GET", Path: "/internal/runtime/tasks", Query: query})
		if err != nil {
			t.Fatal(err)
		}
		options := runtime.options
		if runtime.calls != 1 || options.Status != "" || options.TimeField != "updated_at" || options.Offset != 0 || options.Limit != 50 || options.From != nil || options.To != nil || options.Query != "" {
			t.Fatalf("query %#v: calls = %d, defaults = %#v", query, runtime.calls, options)
		}
	}
}

func TestDispatchRejectsInvalidTaskQueryBeforeRuntime(t *testing.T) {
	for _, test := range []struct {
		name  string
		query url.Values
		code  string
	}{
		{"status", url.Values{"status": {"pending"}}, "INVALID_STATUS"},
		{"limit empty", url.Values{"limit": {""}}, "INVALID_LIMIT"},
		{"limit zero", url.Values{"limit": {"0"}}, "INVALID_LIMIT"},
		{"limit negative", url.Values{"limit": {"-1"}}, "INVALID_LIMIT"},
		{"limit too large", url.Values{"limit": {"201"}}, "INVALID_LIMIT"},
		{"limit decimal", url.Values{"limit": {"1.5"}}, "INVALID_LIMIT"},
		{"limit overflow", url.Values{"limit": {"999999999999999999999999999999999999"}}, "INVALID_LIMIT"},
		{"offset negative", url.Values{"offset": {"-1"}}, "INVALID_TASK_QUERY"},
		{"offset empty", url.Values{"offset": {""}}, "INVALID_TASK_QUERY"},
		{"offset decimal", url.Values{"offset": {"1.5"}}, "INVALID_TASK_QUERY"},
		{"offset overflow", url.Values{"offset": {"999999999999999999999999999999999999"}}, "INVALID_TASK_QUERY"},
		{"time field", url.Values{"time_field": {"completed_at"}}, "INVALID_TASK_QUERY"},
		{"from missing time", url.Values{"from": {"2026-09-13"}}, "INVALID_TASK_QUERY"},
		{"from empty", url.Values{"from": {""}}, "INVALID_TASK_QUERY"},
		{"to empty", url.Values{"to": {""}}, "INVALID_TASK_QUERY"},
		{"from missing zone", url.Values{"from": {"2026-09-13T12:00:00"}}, "INVALID_TASK_QUERY"},
		{"to invalid", url.Values{"to": {"yesterday"}}, "INVALID_TASK_QUERY"},
		{"equal bounds", url.Values{"from": {"2026-09-13T00:00:00-07:00"}, "to": {"2026-09-13T07:00:00Z"}}, "INVALID_TASK_QUERY"},
		{"reversed bounds", url.Values{"from": {"2026-09-14T00:00:00Z"}, "to": {"2026-09-13T00:00:00Z"}}, "INVALID_TASK_QUERY"},
		{"unknown field", url.Values{"updated_from": {"2026-09-13T00:00:00Z"}}, "INVALID_TASK_QUERY"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertTaskQueryRejected(t, test.query, test.code)
		})
	}
	for key, value := range map[string]string{
		"status": "active", "q": "search", "time_field": "updated_at", "from": "2026-09-13T00:00:00Z",
		"to": "2026-09-14T00:00:00Z", "offset": "0", "limit": "50",
	} {
		t.Run("repeated "+key, func(t *testing.T) {
			assertTaskQueryRejected(t, url.Values{key: {value, value}}, "INVALID_TASK_QUERY")
		})
		t.Run("zero values "+key, func(t *testing.T) {
			assertTaskQueryRejected(t, url.Values{key: nil}, "INVALID_TASK_QUERY")
		})
	}
}

func assertTaskQueryRejected(t *testing.T, query url.Values, code string) {
	t.Helper()
	runtime := &taskQueryRuntimeStub{}
	_, err := Dispatch(context.Background(), runtime, Request{Method: "GET", Path: "/internal/runtime/tasks", Query: query})
	var toolErr *app.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != code {
		t.Fatalf("query %#v error = %#v, want %s", query, err, code)
	}
	if runtime.calls != 0 {
		t.Fatalf("invalid query reached runtime %d times", runtime.calls)
	}
}
