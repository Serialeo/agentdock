package config

import "testing"

func TestMissingOptionalACPDoesNotBlockCoreConfiguration(t *testing.T) {
	t.Setenv("AGENTDOCK_ACP_ARGS_JSON", "not-json")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AgentDockHome = t.TempDir()
	cfg.AgentDockDefaultDir = t.TempDir()
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.ACPBackendError == "" || cfg.ValidateACPBackend() == nil {
		t.Fatal("missing ACP error")
	}
}

func TestRetiredEnvironmentSwitchesCannotEnableBuiltins(t *testing.T) {
	t.Setenv("AGENTDOCK_ACP_ENABLED", "true")
	t.Setenv("AGENTDOCK_BROWSER_ENABLED", "true")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Builtins.ACP || cfg.Builtins.Browser {
		t.Fatal("retired environment switches enabled tools")
	}
}
