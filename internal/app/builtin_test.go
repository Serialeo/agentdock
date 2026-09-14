package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
	toolacp "github.com/uvwt/agentdock/internal/tool/acp"
)

func builtinTestRuntime(t testing.TB) (*Runtime, config.Config) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), BrowserExecutablePath: executable, Builtins: builtin.Choices{Browser: true}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, cfg
}
func setBuiltinTest(t *testing.T, r *Runtime, id string, enabled bool) {
	t.Helper()
	if _, err := r.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: id, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
}
func stateBuiltinTest(r *Runtime, id string) protocol.BuiltinCapability {
	for _, state := range r.BuiltinCapabilities() {
		if state.ID == id {
			return state
		}
	}
	panic(id)
}

func TestBuiltinDisableCancelsAdmittedCallsAndPersists(t *testing.T) {
	r, cfg := builtinTestRuntime(t)
	callCtx, release, err := r.enterBuiltin(t.Context(), "browser")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	done := make(chan error, 1)
	go func() {
		enabled := false
		_, err := r.SetBuiltin(context.Background(), protocol.BuiltinUpdate{ID: "browser", Enabled: &enabled})
		done <- err
	}()
	select {
	case <-callCtx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight call was not cancelled")
	}
	if _, ok := r.ToolDefinition("browser_session"); ok {
		t.Fatal("disabled group remains discoverable")
	}
	if _, err := r.Call(t.Context(), "browser_session", map[string]any{"action": "start"}); err == nil {
		t.Fatal("cached direct call bypassed disable")
	}
	if _, ok := r.ToolDefinition("read_file"); !ok {
		t.Fatal("unrelated group disabled")
	}
	select {
	case <-done:
		t.Fatal("disable returned before admitted call drained")
	default:
	}
	release()
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disable did not finish")
	}
	if stateBuiltinTest(r, "browser").Available {
		t.Fatal("disabled state is available")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if stateBuiltinTest(restarted, "browser").Enabled {
		t.Fatal("constructor defaults overrode persisted choice")
	}
	oldBrowser := restarted.browser
	setBuiltinTest(t, restarted, "browser", true)
	if !stateBuiltinTest(restarted, "browser").Available || restarted.browser == oldBrowser {
		t.Fatal("enable did not create new backend")
	}
	before := restarted.browser
	setBuiltinTest(t, restarted, "browser", true)
	if restarted.browser != before {
		t.Fatal("idempotent enable restarted active backend")
	}
}

func TestBuiltinMissingBackendDoesNotDisableCoreTools(t *testing.T) {
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), BrowserExecutablePath: filepath.Join(t.TempDir(), "missing"), ACPCommand: "/missing/adapter", Builtins: builtin.Choices{Browser: true, ACP: true}}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range []string{"browser", "acp"} {
		s := stateBuiltinTest(r, id)
		if !s.Enabled || s.Ready != (id == "browser") || s.Available != (id == "browser") || s.Reason == "" {
			t.Fatalf("%s state=%+v", id, s)
		}
	}
	if _, ok := r.ToolDefinition("exec_command"); !ok {
		t.Fatal("missing optional backend broke core tools")
	}
	yes := true
	if _, err := r.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: "external-service", Enabled: &yes}); err == nil {
		t.Fatal("external service accepted as a builtin group")
	}
}

func TestBuiltinConcurrentSwitchAndDiscovery(t *testing.T) {
	r, _ := builtinTestRuntime(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 12; j++ {
				enabled := j%2 == 0
				if _, err := r.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: "browser", Enabled: &enabled}); err != nil {
					t.Error(err)
					return
				}
				r.ToolDefinitions()
				r.BuiltinCapabilities()
				ctx, release, err := r.enterBuiltin(t.Context(), "browser")
				if err == nil {
					_ = ctx.Err()
					release()
				}
			}
		})
	}
	wg.Wait()
	setBuiltinTest(t, r, "browser", false)
	for _, name := range r.ToolNames() {
		if strings.HasPrefix(name, "browser_") {
			t.Fatal(name)
		}
	}
}

func TestBuiltinPersistenceFailureDoesNotChangeLiveState(t *testing.T) {
	r, cfg := builtinTestRuntime(t)
	if err := os.Mkdir(builtin.Path(cfg.AgentDockHome), 0700); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := r.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: "browser", Enabled: &disabled}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !stateBuiltinTest(r, "browser").Available {
		t.Fatal("failed write changed running capability")
	}
}

func TestBuiltinDisableInterruptsDetachedACPAndRetainsOutcome(t *testing.T) {
	t.Setenv("GO_WANT_OUTPUT_CONTRACT_ACP_HELPER", "1")
	executable, _ := os.Executable()
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), Builtins: builtin.Choices{ACP: true}, ACPAgentName: "output-contract-helper", ACPCommand: executable, ACPArgs: []string{"-test.run=^TestOutputContractACPHelper$"}, ACPEnvFromEnv: map[string]string{"GO_WANT_OUTPUT_CONTRACT_ACP_HELPER": "GO_WANT_OUTPUT_CONTRACT_ACP_HELPER"}}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := projectContextForTest(t, r, cfg.AgentDockDefaultDir, fullProjectPermissionsForTest())
	created, err := r.Call(ctx, "acp_session", map[string]any{"action": "new"})
	if err != nil {
		t.Fatal(err)
	}
	session := created["session"].(acpruntime.SessionRecord)
	started, err := r.Call(ctx, "acp_prompt", map[string]any{"action": "start", "session_id": session.ID, "text": "hold"})
	if err != nil {
		t.Fatal(err)
	}
	old := r.acp
	setBuiltinTest(t, r, "acp", false)
	events, err := old.Prompt(ctx, toolacp.PromptRequest{Action: "events", RunID: started["run_id"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if events["status"] != acpruntime.RunInterrupted && events["status"] != "interrupted" {
		t.Fatalf("run survived disable: %#v", events)
	}
	setBuiltinTest(t, r, "acp", true)
	inspected, err := r.Call(ctx, "acp_session", map[string]any{"action": "inspect", "session_id": session.ID})
	if err != nil {
		t.Fatal(err)
	}
	record := inspected["session"].(acpruntime.SessionRecord)
	if record.LastStopReason != "capability_disabled" || record.Status == acpruntime.SessionRunning {
		t.Fatalf("outcome not preserved: %#v", record)
	}
}

func TestBuiltinDeadlineLeavesCleanupTransitioningAndShutdownFenced(t *testing.T) {
	r, _ := builtinTestRuntime(t)
	_, release, err := r.enterBuiltin(t.Context(), "browser")
	if err != nil {
		t.Fatal(err)
	}
	// 断言失败也必须释放准入，否则测试清理会把真正失败掩盖成十分钟超时。
	release = sync.OnceFunc(release)
	defer release()
	// Windows 的持久化可能受文件扫描影响，不能把磁盘写入限定在 40ms 内。
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	no := false
	if _, err = r.SetBuiltin(ctx, protocol.BuiltinUpdate{ID: "browser", Enabled: &no}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline=%v", err)
	}
	if state := stateBuiltinTest(r, "browser"); !state.Transitioning || state.Available {
		t.Fatalf("state=%+v", state)
	}
	other, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if _, err = r.SetBuiltin(other, protocol.BuiltinUpdate{ID: "acp", Enabled: &no}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued request=%v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	select {
	case <-closed:
		t.Fatal("closed before admitted handler drained")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to drain")
	}
}

func TestToolDefinitionSnapshotsDoNotShareMutableContracts(t *testing.T) {
	r, _ := builtinTestRuntime(t)
	first, _ := r.ToolDefinition("browser_session")
	first.InputSchema["properties"].(map[string]any)["action"] = "mutated"
	first.Annotations.Title = "mutated"
	second, _ := r.ToolDefinition("browser_session")
	if second.InputSchema["properties"].(map[string]any)["action"] == "mutated" || second.Annotations.Title == "mutated" {
		t.Fatal("caller mutated cached contract")
	}
}

func BenchmarkBuiltinToolNames(b *testing.B) {
	r, _ := builtinTestRuntime(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = r.ToolNames()
	}
}
func BenchmarkBuiltinCatalogSnapshot(b *testing.B) {
	r, _ := builtinTestRuntime(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = r.CatalogSnapshot()
	}
}
func BenchmarkBuiltinConcurrentAdmission(b *testing.B) {
	r, _ := builtinTestRuntime(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, release, err := r.enterBuiltin(context.Background(), "browser")
			if err != nil {
				b.Fatal(err)
			}
			release()
		}
	})
}
