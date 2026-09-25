//go:build windows

package project

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSourceProvenanceGitCommandHidesConsoleWindow(t *testing.T) {
	cmd := sourceProvenanceGitCommand(context.Background(), "git.exe", "status")

	if cmd.SysProcAttr == nil {
		t.Fatal("source provenance Git command did not initialize Windows process attributes")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("source provenance Git command did not hide the child process window")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
