package runtimeapi

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/taskstate"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

type runtimeStub struct {
	taskOptions   taskstate.ListOptions
	mcpArgs       map[string]any
	skillRequest  toolskill.PackageRequest
	browseRequest toolfile.BrowseRequest
}

func (r *runtimeStub) RuntimeStatus() app.Result                           { return app.Result{"status": "ok"} }
func (r *runtimeStub) RuntimeSkills() (app.Result, error)                  { return app.Result{}, nil }
func (r *runtimeStub) RuntimeSkill(string) (app.Result, error)             { return app.Result{}, nil }
func (r *runtimeStub) RuntimeSkillFiles(string) (app.Result, error)        { return app.Result{}, nil }
func (r *runtimeStub) RuntimeSkillFile(string, string) (app.Result, error) { return app.Result{}, nil }
func (r *runtimeStub) RuntimeSkillManage(_ context.Context, request toolskill.PackageRequest) (app.Result, error) {
	r.skillRequest = request
	return app.Result{"action": request.Action, "skill": request.Skill}, nil
}
func (r *runtimeStub) RuntimeBrowseFiles(_ context.Context, request toolfile.BrowseRequest) (app.Result, error) {
	r.browseRequest = request
	return app.Result{"path": request.Path}, nil
}
func (r *runtimeStub) RuntimeTasks(options taskstate.ListOptions) (app.Result, error) {
	r.taskOptions = options
	return app.Result{"status": string(options.Status), "limit": options.Limit}, nil
}
func (r *runtimeStub) RuntimeTask(string) (app.Result, error)       { return app.Result{}, nil }
func (r *runtimeStub) RuntimeTaskDelete(string) (app.Result, error) { return app.Result{}, nil }
func (r *runtimeStub) RuntimeCapabilities(context.Context, bool) (app.Result, error) {
	return app.Result{}, nil
}
func (r *runtimeStub) RuntimeMCPServers(context.Context) (app.Result, error) {
	return app.Result{}, nil
}
func (r *runtimeStub) RuntimeMCPServer(context.Context, string) (app.Result, error) {
	return app.Result{}, nil
}
func (r *runtimeStub) RuntimeMCPManage(_ context.Context, args map[string]any) (app.Result, error) {
	r.mcpArgs = args
	return app.Result{"changed": true}, nil
}
func (r *runtimeStub) RuntimeEvolve(context.Context, map[string]any) (app.Result, error) {
	return app.Result{}, nil
}

func TestMethodContract(t *testing.T) {
	tests := []struct {
		method string
		path   string
		allow  string
		ok     bool
	}{
		{"GET", "/internal/runtime/status", "GET", true},
		{"POST", "/internal/runtime/capabilities", "GET, POST", true},
		{"POST", "/internal/runtime/direct-instructions", "GET", false},
		{"DELETE", "/internal/runtime/tasks/task-1", "GET, DELETE", true},
		{"POST", "/internal/runtime/tasks/task-1", "GET, DELETE", false},
		{"GET", "/internal/runtime/evolve", "POST", true},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			if got := MethodAllowed(test.method, test.path); got != test.ok {
				t.Fatalf("MethodAllowed() = %v, want %v", got, test.ok)
			}
			if got := AllowHeader(test.path); got != test.allow {
				t.Fatalf("AllowHeader() = %q, want %q", got, test.allow)
			}
		})
	}
}

func TestDispatchParsesFileBrowseQuery(t *testing.T) {
	runtime := &runtimeStub{}
	result, err := Dispatch(context.Background(), runtime, Request{
		Method: "GET",
		Path:   "/internal/runtime/files",
		Query: url.Values{
			"path":           {"/srv/demo/project"},
			"offset":         {"7"},
			"limit":          {"300"},
			"include_hidden": {"true"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["path"] != "/srv/demo/project" || runtime.browseRequest.Path != "/srv/demo/project" {
		t.Fatalf("browse result/request = %#v / %#v", result, runtime.browseRequest)
	}
	if runtime.browseRequest.Offset == nil || *runtime.browseRequest.Offset != 7 || runtime.browseRequest.Limit == nil || *runtime.browseRequest.Limit != 300 || !runtime.browseRequest.IncludeHidden {
		t.Fatalf("browse request = %#v", runtime.browseRequest)
	}
}

func TestDispatchRejectsInvalidFileBrowseQuery(t *testing.T) {
	for _, query := range []url.Values{
		{"unknown": {"x"}},
		{"limit": {"501"}},
		{"offset": {"-1"}},
		{"include_hidden": {"maybe"}},
		{"path": {"a", "b"}},
	} {
		_, err := Dispatch(context.Background(), &runtimeStub{}, Request{Method: "GET", Path: "/internal/runtime/files", Query: query})
		if err == nil {
			t.Fatalf("invalid query unexpectedly succeeded: %#v", query)
		}
	}
}

func TestDispatchParsesTaskQuery(t *testing.T) {
	runtime := &runtimeStub{}
	result, err := Dispatch(context.Background(), runtime, Request{
		Method: "GET",
		Path:   "/internal/runtime/tasks",
		Query:  url.Values{"status": {"active"}, "limit": {"25"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.taskOptions.Status != "active" || runtime.taskOptions.Limit != 25 {
		t.Fatalf("captured tasks query = %#v", runtime.taskOptions)
	}
	if result["status"] != "active" || result["limit"] != 25 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDispatchMCPManagePreservesOnlyProvidedFields(t *testing.T) {
	runtime := &runtimeStub{}
	_, err := Dispatch(context.Background(), runtime, Request{
		Method: "POST",
		Path:   "/internal/runtime/mcp",
		Body:   []byte(`{"action":"add","name":"demo","transport":"stdio","args":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"action": "add", "name": "demo", "transport": "stdio", "args": []string{}}
	if !reflect.DeepEqual(runtime.mcpArgs, want) {
		t.Fatalf("MCP args = %#v, want %#v", runtime.mcpArgs, want)
	}
}

func TestDispatchKeepsRouteSpecificValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		code string
	}{
		{
			name: "task limit",
			req:  Request{Method: "GET", Path: "/internal/runtime/tasks", Query: url.Values{"limit": {"201"}}},
			code: "INVALID_LIMIT",
		},
		{
			name: "MCP body limit",
			req:  Request{Method: "POST", Path: "/internal/runtime/mcp", Body: []byte(strings.Repeat("x", 64*1024+1))},
			code: "INVALID_MCP_REQUEST",
		},
		{
			name: "evolve unknown field",
			req:  Request{Method: "POST", Path: "/internal/runtime/evolve", Body: []byte(`{"intent":"propose","unknown":true}`)},
			code: "INVALID_EVOLVE_REQUEST",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Dispatch(context.Background(), &runtimeStub{}, test.req)
			var toolErr *app.ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != test.code {
				t.Fatalf("error = %#v, want ToolError code %s", err, test.code)
			}
		})
	}
}

func TestDispatchRetiredDirectInstructionsRouteIsNotFound(t *testing.T) {
	for _, request := range []Request{
		{Method: "GET", Path: "/internal/runtime/direct-instructions"},
		{Method: "POST", Path: "/internal/runtime/direct-instructions", Body: []byte(`{"action":"replace","value":"legacy"}`)},
	} {
		_, err := Dispatch(context.Background(), &runtimeStub{}, request)
		var toolErr *app.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "NOT_FOUND" {
			t.Fatalf("retired Direct Instructions route %s error = %#v, want NOT_FOUND", request.Method, err)
		}
	}
}

func TestDispatchRuntimeSkillManagePreservesExplicitEmptyEnvironmentValue(t *testing.T) {
	runtime := &runtimeStub{}
	body := []byte(`{"action":"env_set","skill":"demo","key":"EMPTY","value":""}`)
	result, err := Dispatch(context.Background(), runtime, Request{Method: "POST", Path: "/internal/runtime/skills/manage", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.skillRequest.Action != "env_set" || runtime.skillRequest.Skill != "demo" || runtime.skillRequest.Key != "EMPTY" || runtime.skillRequest.Value == nil || *runtime.skillRequest.Value != "" {
		t.Fatalf("skill request = %#v", runtime.skillRequest)
	}
	if result["action"] != "env_set" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodeRuntimeSkillManageRejectsUnsupportedOrAmbiguousActions(t *testing.T) {
	for name, body := range map[string]string{
		"install":         `{"action":"install","skill":"demo"}`,
		"env set missing": `{"action":"env_set","skill":"demo","key":"A"}`,
		"env unset value": `{"action":"env_unset","skill":"demo","key":"A","value":""}`,
		"activate no ver": `{"action":"activate","skill":"demo"}`,
		"unknown field":   `{"action":"rollback","skill":"demo","source":"x"}`,
		"extra json":      `{"action":"rollback","skill":"demo"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRuntimeSkillManageRequest([]byte(body)); err == nil {
				t.Fatalf("invalid request accepted: %s", body)
			}
		})
	}
}

func (r *runtimeStub) RuntimeBuiltins() app.Result { return app.Result{} }
func (r *runtimeStub) SetBuiltin(context.Context, protocol.BuiltinUpdate) (app.Result, error) {
	return app.Result{}, nil
}
