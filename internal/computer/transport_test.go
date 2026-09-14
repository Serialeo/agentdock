package computer

import (
	"context"
	"encoding/json"
	protocol "github.com/Serialeo/agentdock-protocol"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTDOCK_COMPUTER_TEST_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "serve" {
		e, err := NewEngine(os.Getenv("AGENTDOCK_COMPUTER_TEST_HOME"), &fakeBackend{}, true)
		if err != nil {
			os.Exit(2)
		}
		if err = Run(context.Background(), os.Stdin, os.Stdout, e); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestPrivatePipeRoundTripAndExplicitRecovery(t *testing.T) {
	t.Setenv("AGENTDOCK_COMPUTER_TEST_CHILD", "1")
	t.Setenv("AGENTDOCK_COMPUTER_TEST_HOME", t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Start(ctx, exe)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invoke := func(name string, args any) Reply {
		t.Helper()
		r, err := client.Call(ctx, Request{Owner: testOwner, Tool: name, Args: raw(args)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	session := invoke(protocol.ToolComputerSession, map[string]any{"action": "acquire", "desktop_id": "desktop"})
	var lease struct {
		Session protocol.ComputerSession `json:"session"`
	}
	if err = json.Unmarshal(session.Result, &lease); err != nil {
		t.Fatal(err)
	}
	observed := invoke(protocol.ToolComputerObserve, map[string]any{"desktop_id": "desktop", "display_id": "display"})
	var observation protocol.ComputerObservation
	if err = json.Unmarshal(observed.Result, &observation); err != nil {
		t.Fatal(err)
	}
	if string(observed.PNG) != "private screenshot" {
		t.Fatal("image bytes lost in IPC")
	}
	invoke(protocol.ToolComputerAct, action(lease.Session.SessionID, observation.ObservationID, "pipe-operation"))
	// Simulate a killed helper after the response was delivered; never resend input automatically.
	client.mu.Lock()
	p := client.process
	client.mu.Unlock()
	_ = p.cmd.Process.Kill()
	<-p.done
	saved := invoke(protocol.ToolComputerStatus, map[string]any{"operation_id": "pipe-operation"})
	var result protocol.ComputerActionResult
	if err = json.Unmarshal(saved.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.ReplayedResult || result.Effects != "applied" {
		t.Fatalf("history lost after helper restart: %s", saved.Result)
	}
}
func TestRenewReleaseAndStatusOutputs(t *testing.T) {
	e, s, _ := setup(t, &fakeBackend{})
	for _, args := range []map[string]any{{"action": "renew", "session_id": s}, {"action": "release", "session_id": s}} {
		if _, _, err := call(t, e, protocol.ToolComputerSession, args); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := call(t, e, protocol.ToolComputerStatus, map[string]any{}); err != nil {
		t.Fatal(err)
	}
}
