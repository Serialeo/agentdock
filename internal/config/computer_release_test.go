package config

import "testing"

func TestComputerReleaseCannotBeEnabledByEnvironment(t *testing.T) {
	t.Setenv("AGENTDOCK_COMPUTER_HELPER_PATH", "/missing/unfinished-helper")
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
	if BuiltinProvided("computer") {
		t.Fatal("current release must exclude computer use")
	}
}
