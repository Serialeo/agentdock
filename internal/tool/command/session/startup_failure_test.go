package session

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"

	processcontrol "github.com/uvwt/agentdock/internal/process"
)

func TestControllerFailureAfterProcessCreationIsExplicitlyUncertain(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	injected := errors.New("injected process controller failure")
	_, err := startStandardRunnerWithController(cmd, io.Discard, io.Discard, func(cmd *exec.Cmd) (*processcontrol.Controller, error) {
		if cmd.Process == nil {
			t.Fatal("controller called before process creation")
		}
		return nil, injected
	})
	var startError *StartError
	if !errors.As(err, &startError) || !startError.ProcessStarted || !errors.Is(err, injected) {
		t.Fatalf("post-spawn error=%#v", err)
	}
}

func TestMissingExecutableIsNotMisclassifiedAsPostSpawn(t *testing.T) {
	cmd := exec.Command("agentdock-test-missing-executable-2c4e67")
	_, err := startStandardRunner(cmd, io.Discard, io.Discard)
	var startError *StartError
	if err == nil || errors.As(err, &startError) {
		t.Fatalf("pre-spawn error=%#v", err)
	}
}
