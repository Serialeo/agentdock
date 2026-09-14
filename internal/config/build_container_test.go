//go:build agentdock_docker

package config

import "testing"

func TestDockerIgnoresUnavailableBackendConfiguration(t *testing.T) {
	t.Setenv("AGENTDOCK_ACP_ARGS_JSON", "invalid-json")
	t.Setenv("AGENTDOCK_ACP_ENV_FROM_ENV_JSON", "invalid-json")
	t.Setenv("AGENTDOCK_ACP_MAX_CONCURRENT_PROMPTS", "invalid-number")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ACPBackendError != "" {
		t.Fatal("Docker parsed excluded ACP backend")
	}
	if BuiltinProvided("acp") || !BuiltinProvided("browser") {
		t.Fatal("invalid Docker policy")
	}
}
