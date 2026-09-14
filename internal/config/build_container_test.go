//go:build agentdock_docker

package config

import "testing"

func TestDockerIgnoresUnavailableToolEnvironment(t *testing.T) {
	for _, enabled := range []string{"true", "not-a-boolean"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("AGENTDOCK_ACP_ENABLED", enabled)
			t.Setenv("AGENTDOCK_ACP_COMMAND", "missing-adapter")
			t.Setenv("AGENTDOCK_ACP_ARGS_JSON", "invalid-json")
			t.Setenv("AGENTDOCK_ACP_ENV_FROM_ENV_JSON", "invalid-json")
			t.Setenv("AGENTDOCK_ACP_MAX_CONCURRENT_PROMPTS", "invalid-number")
			t.Setenv("AGENTDOCK_ACP_INTERACTION_TIMEOUT_MS", "invalid-number")
			t.Setenv("AGENTDOCK_COMPUTER_HELPER_PATH", "/missing/helper")
			t.Setenv("AGENTDOCK_BROWSER_ENABLED", "true")
			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("unused desktop settings blocked Docker startup: %v", err)
			}
			assertDisabledACPDefaults(t, cfg)
			if cfg.ComputerAvailable || cfg.ComputerHelperPath != "" || !cfg.BrowserEnabled {
				t.Fatalf("unexpected Docker capabilities: %#v", cfg)
			}
		})
	}
}

func TestDockerNormalizeDisablesProgrammaticDesktopConfiguration(t *testing.T) {
	cfg := Config{
		AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(),
		ACPEnabled: true, ACPCommand: "missing-adapter", ACPAgentName: "invalid\nname",
		ComputerAvailable: true, ComputerHelperPath: "/missing/helper", BrowserEnabled: true,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	assertDisabledACPDefaults(t, cfg)
	if cfg.ComputerAvailable || cfg.ComputerHelperPath != "" || !cfg.BrowserEnabled {
		t.Fatalf("unexpected Docker capabilities: %#v", cfg)
	}
}
