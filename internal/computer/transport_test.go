package computer

import (
	"context"
	"encoding/json"
	protocol "github.com/Serialeo/agentdock-protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENTDOCK_COMPUTER_TEST_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "serve" {
		var backend Backend = &fakeBackend{}
		if marker := os.Getenv("AGENTDOCK_COMPUTER_TEST_HOLD_PATH"); marker != "" {
			backend = &cleanupBackend{marker: marker}
		}
		e, err := NewEngine(os.Getenv("AGENTDOCK_COMPUTER_TEST_HOME"), backend, true)
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

type cleanupBackend struct {
	fakeBackend
	marker string
}

func (b *cleanupBackend) Act(ctx context.Context, s Snapshot, a protocol.ComputerAction) (protocol.ComputerInputResult, error) {
	batches, err := planAction(s, a, macKey)
	if err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	d := &recordingDriver{send: func(events []inputEvent) (submission, error) {
		for _, event := range events {
			if event.Kind == "button_down" {
				if err := os.WriteFile(b.marker, []byte("down"), 0600); err != nil {
					return submission{}, err
				}
			}
			if event.Kind == "button_up" {
				time.Sleep(20 * time.Millisecond)
				if err := os.WriteFile(b.marker, []byte("up"), 0600); err != nil {
					return submission{}, err
				}
			}
		}
		return submission{Sent: len(events), Requested: len(events), Accepted: len(events), Known: true}, nil
	}}
	return runInput(ctx, d, batches)
}
func TestPipeCancellationAndEOFCleanUpHeldInput(t *testing.T) {
	for _, closePipe := range []bool{false, true} {
		name := "cancel-request"
		if closePipe {
			name = "close-pipe"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			marker := filepath.Join(home, "held-input")
			t.Setenv("AGENTDOCK_COMPUTER_TEST_CHILD", "1")
			t.Setenv("AGENTDOCK_COMPUTER_TEST_HOME", home)
			t.Setenv("AGENTDOCK_COMPUTER_TEST_HOLD_PATH", marker)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			client, err := Start(ctx, exe)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			pid := client.process.cmd.Process.Pid
			invoke := func(tool string, args any) Reply {
				t.Helper()
				reply, err := client.Call(ctx, Request{Owner: testOwner, Tool: tool, Args: raw(args)})
				if err != nil {
					t.Fatal(err)
				}
				return reply
			}
			reply := invoke(protocol.ToolComputerSession, map[string]any{"action": "acquire", "desktop_id": "desktop"})
			var lease struct {
				Session protocol.ComputerSession `json:"session"`
			}
			if err = json.Unmarshal(reply.Result, &lease); err != nil {
				t.Fatal(err)
			}
			reply = invoke(protocol.ToolComputerObserve, map[string]any{"desktop_id": "desktop", "display_id": "display"})
			var observation protocol.ComputerObservation
			if err = json.Unmarshal(reply.Result, &observation); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"session_id": lease.Session.SessionID, "observation_id": observation.ObservationID, "operation_id": "drag", "action": map[string]any{"kind": "drag", "point": map[string]any{"x": 10, "y": 10}, "to": map[string]any{"x": 70, "y": 50}, "duration_ms": 2000}}
			actionCtx, cancelAction := context.WithCancel(ctx)
			defer cancelAction()
			done := make(chan error, 1)
			go func() {
				_, err := client.Call(actionCtx, Request{Owner: testOwner, Tool: protocol.ToolComputerAct, Args: raw(args)})
				done <- err
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				b, _ := os.ReadFile(marker)
				if string(b) == "down" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child never held input")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if closePipe {
				if err = client.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancelAction()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancel/EOF did not drain active input")
			}
			b, err := os.ReadFile(marker)
			if err != nil || string(b) != "up" {
				t.Fatalf("helper exited/returned before release: %q %v", b, err)
			}
			if !closePipe {
				invoke(protocol.ToolComputerStatus, map[string]any{})
				if client.process.cmd.Process.Pid != pid {
					t.Fatal("ordinary cancellation killed the helper")
				}
			}
		})
	}
}
