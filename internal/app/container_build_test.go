//go:build agentdock_docker

package app

import (
	"context"
	"os"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
)

func TestDockerDoesNotPublishOrExecuteACP(t *testing.T) {
	// 故意不调用 Normalize：程序直接传入配置也不能启用容器没有的能力。
	executable, _ := os.Executable()
	cfg := config.Config{
		AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(),
		Builtins: builtin.Choices{ACP: true, Browser: true}, ACPCommand: "missing-adapter",
		BrowserExecutablePath: executable,
		ACPEnvFromEnv:         map[string]string{"TOKEN": "AGENTDOCK_TEST_MISSING_ACP_SECRET"},
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatalf("unused desktop configuration blocked runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	if r.acp != nil || stateBuiltinTest(r, "acp").Provided {
		t.Fatal("Docker initialized a desktop backend")
	}
	for _, name := range []string{"acp_session", "acp_prompt", "acp_interaction"} {
		if _, ok := r.ToolDefinition(name); ok {
			t.Fatalf("Docker exposed %s", name)
		}
		_, err := r.Call(context.Background(), name, nil)
		requireAppToolErrorCode(t, err, "CAPABILITY_UNAVAILABLE")
	}
	for _, name := range []string{"exec_command", "read_file", "browser_session", "browser_act", "mcp_tool_search"} {
		if _, ok := r.ToolDefinition(name); !ok {
			t.Fatalf("Docker lost supported tool %s", name)
		}
	}
	yes := true
	if _, err := r.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: "acp", Enabled: &yes}); err == nil {
		t.Fatal("Docker ACP enabled through GUI operation")
	}
	result, err := r.AgentDockLocalContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ctx capabilityContext
	if err := remarshal(result, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.ACP != nil {
		t.Fatal("Nexus context advertised Docker ACP")
	}
}
