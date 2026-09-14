package config

import "testing"

func TestComputerReleaseIgnoresHelperConfiguration(t *testing.T) {
	t.Setenv("AGENTDOCK_COMPUTER_HELPER_PATH", "/missing/unfinished-helper")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComputerHelperPath != "" || cfg.ComputerAvailable {
		t.Fatal("environment enabled unfinished computer use")
	}
	cfg.ComputerHelperPath = "/missing/unfinished-helper"
	cfg.ComputerAvailable = true
	cfg.AgentDockHome = t.TempDir()
	cfg.AgentDockDefaultDir = t.TempDir()
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.ComputerHelperPath != "" || cfg.ComputerAvailable {
		t.Fatal("programmatic configuration enabled unfinished computer use")
	}
}
