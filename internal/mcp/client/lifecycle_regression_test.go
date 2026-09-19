package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type regressionClient struct{ calls atomic.Int32 }

func (*regressionClient) initialize(context.Context) error          { return nil }
func (*regressionClient) listTools(context.Context) ([]Tool, error) { return nil, nil }
func (c *regressionClient) callTool(context.Context, string, map[string]any) (map[string]any, error) {
	c.calls.Add(1)
	return map[string]any{}, nil
}
func (*regressionClient) close() error { return nil }

func regressionManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	for _, name := range []string{"a", "b"} {
		_, err := m.Add(ServerConfig{Name: name, Description: name, Transport: TransportStreamableHTTP, URL: "https://example.invalid/mcp", Enabled: true, TimeoutMS: 1000})
		if err != nil {
			t.Fatal(err)
		}
		m.states[name].client = &regressionClient{}
		m.states[name].toolsLoaded = true
		validator, err := compileToolInputSchema(map[string]any{"type": "object"})
		if err != nil {
			t.Fatal(err)
		}
		m.states[name].tools = map[string]Tool{"echo": {Name: "echo", inputValidator: validator}}
	}
	return m
}

func TestQueuedServerDoesNotBlockOtherServerAndHonorsCancellation(t *testing.T) {
	m := regressionManager(t)
	_, _, release, err := m.lockServer("a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	queued := make(chan error, 1)
	go func() { _, err := m.Call(ctx, "a:echo", nil); queued <- err }()
	// The same-service request remains queued while B must progress.
	bctx, bcancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer bcancel()
	if _, err := m.Call(bctx, "b:echo", nil); err != nil {
		t.Fatalf("unrelated B blocked: %v", err)
	}
	select {
	case err := <-queued:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queue error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queue ignored deadline")
	}
}

func TestExternalConfigRetirementDoesNotBlockBAndCloseWaits(t *testing.T) {
	m := regressionManager(t)
	_, _, release, err := m.lockServer("a")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	if _, err := m.store.update(func(configs map[string]ServerConfig) error { delete(configs, "a"); return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.Call(ctx, "b:echo", nil); err != nil {
		t.Fatalf("retirement blocked B: %v", err)
	}
	done := make(chan error, 2)
	go func() { done <- m.Close() }()
	go func() { done <- m.Close() }()
	select {
	case err := <-done:
		t.Fatalf("Close omitted retired connection: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	released = true
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close remained blocked")
		}
	}
}

func TestEmptyDiscoveryRemainsLoaded(t *testing.T) {
	m := regressionManager(t)
	state := m.states["a"]
	state.tools = map[string]Tool{}
	client := state.client
	for i := 0; i < 3; i++ {
		tools, err := m.ensureTools(t.Context(), "a")
		if err != nil || len(tools) != 0 {
			t.Fatalf("empty discovery: %v %v", tools, err)
		}
		if _, err := m.Call(t.Context(), "a:absent", nil); err == nil {
			t.Fatal("unknown tool accepted")
		}
	}
	if state.client != client || !state.toolsLoaded {
		t.Fatal("empty discovery reconnected")
	}
}

func TestHTTPDisconnectAfterExecutionIsUnknownWithoutReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "server/discover":
			writeRPCResult(t, w, request.ID, map[string]any{})
		case "initialize":
			writeRPCResult(t, w, request.ID, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "crash", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRPCResult(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "write", "inputSchema": map[string]any{"type": "object"}}}})
		case "tools/call":
			calls.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
		}
	}))
	defer server.Close()
	m, _ := NewManager(t.TempDir())
	defer m.Close()
	if _, err := m.Add(ServerConfig{Name: "crash", Description: "crash", Transport: TransportStreamableHTTP, URL: server.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Call(t.Context(), "crash:write", map[string]any{})
	var result *Error
	if !errors.As(err, &result) || result.Code != "MCP_EXECUTION_UNKNOWN" || result.Retryable || calls.Load() != 1 {
		t.Fatalf("unsafe disconnect result: %#v, calls %d", err, calls.Load())
	}
}

func TestStreamableHTTPTimeoutsBoundDetachedSessionDelete(t *testing.T) {
	configured := ServerConfig{
		Name: "timeout", Description: "timeout", Transport: TransportStreamableHTTP,
		URL: "https://example.invalid/mcp", Enabled: true, TimeoutMS: maxTimeoutMS,
	}
	client := newStreamableHTTPClient(configured)
	transportValue, err := client.transport()
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := transportValue.(*mcpsdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport = %T", transportValue)
	}
	httpClient := transport.HTTPClient
	if got, want := httpClient.Timeout, time.Duration(maxTimeoutMS)*time.Millisecond; got != want {
		t.Fatalf("HTTP client timeout = %s, want %s", got, want)
	}
	roundTripper, ok := httpClient.Transport.(headerRoundTripper)
	if !ok {
		t.Fatalf("HTTP transport = %T", httpClient.Transport)
	}
	if roundTripper.deleteTimeout != streamableHTTPDeleteCloseTimeout {
		t.Fatalf("DELETE close timeout = %s, want %s", roundTripper.deleteTimeout, streamableHTTPDeleteCloseTimeout)
	}
	originalDelete, err := http.NewRequest(http.MethodDelete, configured.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	redirectedGet, err := http.NewRequest(http.MethodGet, configured.URL+"/redirected", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpClient.CheckRedirect(redirectedGet, []*http.Request{originalDelete}); !errors.Is(err, errStreamableHTTPSessionDeleteRedirect) {
		t.Fatalf("DELETE redirect policy = %v, want rejection", err)
	}
	originalPost, err := http.NewRequest(http.MethodPost, configured.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpClient.CheckRedirect(redirectedGet, []*http.Request{originalPost}); err != nil {
		t.Fatalf("ordinary redirect unexpectedly rejected: %v", err)
	}
	redirectChain := make([]*http.Request, 10)
	for i := range redirectChain {
		redirectChain[i] = originalPost
	}
	if err := httpClient.CheckRedirect(redirectedGet, redirectChain); !errors.Is(err, errStreamableHTTPTooManyRedirects) {
		t.Fatalf("redirect limit policy = %v, want rejection", err)
	}
}

func TestManagerRemoveReturnsWhenStreamableHTTPSessionDeleteHangs(t *testing.T) {
	deleteStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteStarted <- struct{}{}
			<-r.Context().Done()
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": request.ID,
				"error": map[string]any{"code": -32601, "message": "Method not found"},
			})
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "hung-delete-session")
			writeRPCResult(t, w, request.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "hung-delete", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRPCResult(t, w, request.ID, map[string]any{"tools": []map[string]any{}})
		default:
			t.Errorf("unexpected method %q", request.Method)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(ServerConfig{
		Name: "hung-delete", Description: "hung delete", Transport: TransportStreamableHTTP,
		URL: server.URL, Enabled: true, TimeoutMS: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Refresh(t.Context(), "hung-delete"); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err = manager.Remove("hung-delete")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("remove error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("remove waited %s for hung DELETE", elapsed)
	}
	select {
	case <-deleteStarted:
	default:
		t.Fatal("session close did not issue DELETE")
	}
	if listed := manager.List(); len(listed) != 0 {
		t.Fatalf("removed server remained in registry: %#v", listed)
	}
}

func TestStdioExitInvalidatesAndNextExplicitCallRecovers(t *testing.T) {
	t.Setenv("TEST_MCP_HELPER_MODE", "1")
	m, _ := NewManager(t.TempDir())
	defer m.Close()
	if _, err := m.Add(ServerConfig{Name: "local", Description: "local", Transport: TransportStdio, Command: os.Args[0], Args: []string{"-test.run=TestMCPStdioHelperProcess"}, EnvFromEnv: map[string]string{"GO_WANT_MCP_HELPER": "TEST_MCP_HELPER_MODE"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Call(t.Context(), "local:echo", map[string]any{"text": "__crash"})
	var result *Error
	if !errors.As(err, &result) || result.Code != "MCP_EXECUTION_UNKNOWN" || result.Retryable {
		t.Fatalf("exit result: %#v", err)
	}
	_, summary, err := m.Inspect("local")
	if err != nil || summary.Status != "error" {
		t.Fatalf("stale ready: %#v %v", summary, err)
	}
	if _, err := m.Call(t.Context(), "local:echo", map[string]any{"text": "recovered"}); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredTimeoutIncludesQueueAndRegistryWait(t *testing.T) {
	m := regressionManager(t)
	cfg := m.servers["a"]
	cfg.TimeoutMS = 60
	m.servers["a"] = cfg
	if _, err := m.store.update(func(configs map[string]ServerConfig) error { configs["a"] = cfg; return nil }); err != nil {
		t.Fatal(err)
	}
	_, _, release, err := m.lockServer("a")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := m.Call(context.Background(), "a:echo", nil); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("configured queue timeout: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("configured timeout omitted queue")
	}
	release()
	releaseRegistry, err := m.store.acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRegistry()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = m.Call(ctx, "b:echo", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("registry wait ignored cancellation: %v", err)
	}
}
