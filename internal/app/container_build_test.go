//go:build agentdock_docker

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestDockerDoesNotPublishOrExecuteDesktopTools(t *testing.T) {
	// 故意不调用 Normalize：程序直接传入配置也不能启用容器没有的能力。
	cfg := config.Config{
		AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(),
		ACPEnabled: true, ACPCommand: "missing-adapter",
		ACPEnvFromEnv:     map[string]string{"TOKEN": "AGENTDOCK_TEST_MISSING_ACP_SECRET"},
		ComputerAvailable: true, ComputerHelperPath: "/missing/helper", BrowserEnabled: true,
	}
	names, _, err := compileAvailableToolContracts(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "acp_") || strings.HasPrefix(name, "computer_") {
			t.Fatalf("Docker advertised unavailable tool %s", name)
		}
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
	if r.acp != nil || r.computer != nil || r.Config().ACPEnabled || r.Config().ComputerAvailable {
		t.Fatal("Docker initialized a desktop backend")
	}
	for _, name := range []string{"acp_session", "acp_prompt", "acp_interaction", "computer_status", "computer_session", "computer_observe", "computer_act", "computer_stop"} {
		if _, ok := r.ToolDefinition(name); ok {
			t.Fatalf("Docker exposed %s", name)
		}
		_, err := r.Call(context.Background(), name, nil)
		requireAppToolErrorCode(t, err, "UNKNOWN_TOOL")
	}
	for _, name := range []string{"exec_command", "read_file", "browser_session", "browser_act", "mcp_tool_search"} {
		if _, ok := r.ToolDefinition(name); !ok {
			t.Fatalf("Docker lost supported tool %s", name)
		}
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
