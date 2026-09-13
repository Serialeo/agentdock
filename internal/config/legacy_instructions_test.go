package config

import (
	"path/filepath"
	"testing"
)

func TestLegacyInstructionsEnvironmentIsIgnored(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("AGENTDOCK_HOME", filepath.Join(root, ".agentdock"))
	t.Setenv("AGENTDOCK_DEFAULT_DIR", filepath.Join(root, "work"))
	t.Setenv("AGENTDOCK_INSTRUCTIONS_FILE", filepath.Join(root, "missing-legacy-instructions.md"))

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("legacy AGENTDOCK_INSTRUCTIONS_FILE still influences config normalization: %v", err)
	}
}
