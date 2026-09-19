package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type checkpointPolicyTestServer struct {
	mu       sync.Mutex
	prompt   string
	revision string
	status   int
	body     string
	requests int
}

func (s *checkpointPolicyTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	prompt, revision, status, body := s.prompt, s.revision, s.status, s.body
	s.mu.Unlock()
	if r.Method != http.MethodGet || r.URL.Path != "/v1/settings/checkpoint" || r.Header.Get("Authorization") != "Bearer device-token" {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == "" {
		bodyBytes, _ := json.Marshal(map[string]any{"ok": true, "settings": map[string]string{"prompt": prompt, "revision": revision}})
		body = string(bodyBytes)
	}
	_, _ = w.Write([]byte(body))
}

func (s *checkpointPolicyTestServer) set(prompt, revision string, status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompt, s.revision, s.status, s.body = prompt, revision, status, body
}

func (s *checkpointPolicyTestServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func newCheckpointPolicyTestService(t *testing.T, cfg *config.Config) (*Service, *taskstate.Store) {
	t.Helper()
	tasks, err := taskstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(func() config.Config { return *cfg }, tasks), tasks
}

func createCheckpointPolicyTestTask(t *testing.T, service *Service, ctx context.Context) Result {
	t.Helper()
	result, err := service.Manage(ctx, ManageRequest{Action: "create", Title: "policy", Goal: "test policy", CompletionConditions: []string{"done"}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCheckpointPolicyUsesNexusForLifecycleAndNotCheckpoint(t *testing.T) {
	serverState := &checkpointPolicyTestServer{prompt: "prompt-v1", revision: "v1", status: http.StatusOK}
	server := httptest.NewServer(serverState)
	defer server.Close()
	cfg := &config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "device-token"}
	service, tasks := newCheckpointPolicyTestService(t, cfg)

	created := createCheckpointPolicyTestTask(t, service, context.Background())
	assertCheckpointPolicy(t, created["checkpoint_policy"], "prompt-v1", "v1")
	id := created["task_id"].(string)
	serverState.set("prompt-v2", "v2", http.StatusOK, "")
	got, err := service.Manage(context.Background(), ManageRequest{Action: "get", TaskID: id})
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointPolicy(t, got["checkpoint_policy"], "prompt-v2", "v2")
	serverState.set("prompt-v3", "v3", http.StatusOK, "")
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "block", TaskID: id, Summary: "waiting"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.Manage(context.Background(), ManageRequest{Action: "resume", TaskID: id, Summary: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointPolicy(t, resumed["checkpoint_policy"], "prompt-v3", "v3")
	requests := serverState.count()
	serverState.set("unused", "unused", http.StatusUnauthorized, "")
	if _, err := service.Manage(context.Background(), ManageRequest{Action: "checkpoint", TaskID: id, Summary: "saved"}); err != nil {
		t.Fatal(err)
	}
	if serverState.count() != requests {
		t.Fatalf("checkpoint fetched Nexus policy: requests changed from %d to %d", requests, serverState.count())
	}
	listed, err := tasks.List(taskstate.Status(""), 50)
	if err != nil || len(listed) != 1 {
		t.Fatalf("tasks after lifecycle = %d, err=%v", len(listed), err)
	}
}

func TestCheckpointPolicyStandaloneUsesBuiltInRules(t *testing.T) {
	service, _ := newCheckpointPolicyTestService(t, &config.Config{})
	result := createCheckpointPolicyTestTask(t, service, context.Background())
	policy, ok := result["checkpoint_policy"].(checkpointPolicy)
	if !ok || policy.Source != "agentdock/task_manage" || policy.Version != "1" || policy.Prompt == "" || policy.Warning != "" {
		t.Fatalf("standalone policy = %#v", result["checkpoint_policy"])
	}
}

func TestCheckpointPolicyNexusFailuresFallbackWithWarning(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "malformed", status: http.StatusOK, body: "not-json"},
		{name: "blank prompt", status: http.StatusOK, body: `{"ok":true,"settings":{"prompt":"  ","revision":"v1"}}`},
		{name: "oversized prompt", status: http.StatusOK, body: `{"ok":true,"settings":{"prompt":"` + strings.Repeat("x", 8*1024+1) + `","revision":"v1"}}`},
		{name: "missing revision", status: http.StatusOK, body: `{"ok":true,"settings":{"prompt":"prompt"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &checkpointPolicyTestServer{prompt: "unused", revision: "unused", status: tc.status, body: tc.body}
			server := httptest.NewServer(state)
			defer server.Close()
			cfg := &config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "device-token"}
			service, tasks := newCheckpointPolicyTestService(t, cfg)
			result := createCheckpointPolicyTestTask(t, service, context.Background())
			policy, ok := result["checkpoint_policy"].(checkpointPolicy)
			if !ok || policy.Source != "agentdock/task_manage" || policy.Version != "1" || policy.Warning == "" {
				t.Fatalf("fallback policy = %#v", result["checkpoint_policy"])
			}
			listed, err := tasks.List(taskstate.Status(""), 50)
			if err != nil || len(listed) != 1 {
				t.Fatalf("fallback create tasks = %d, err=%v", len(listed), err)
			}
		})
	}
}

func TestCheckpointPolicyCanceledCreateDoesNotPersist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg := &config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "device-token"}
	service, tasks := newCheckpointPolicyTestService(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Manage(ctx, ManageRequest{Action: "create", Title: "canceled", Goal: "should not persist", CompletionConditions: []string{"done"}}); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("canceled create error = %v, want cancellation error", err)
	}
	listed, err := tasks.List(taskstate.Status(""), 50)
	if err != nil || len(listed) != 0 {
		t.Fatalf("canceled create tasks = %d, err=%v", len(listed), err)
	}
}

func assertCheckpointPolicy(t *testing.T, value any, prompt, revision string) {
	t.Helper()
	policy, ok := value.(checkpointPolicy)
	if !ok || policy.Source != "nexusdock/checkpoint" || policy.Version != revision || policy.Prompt != prompt || policy.Enforcement != "caller_driven" || len(policy.Rules) != 3 || policy.Warning != "" {
		t.Fatalf("Nexus policy = %#v", value)
	}
}
