package acp

import (
	"context"
	"testing"
	"time"
)

func TestTerminalTransitionBlocksLateRunPersistence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	manager, err := newTestManager(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := newRun("acpr_late_finalize", created.Session.ID)
	manager.mu.Lock()
	manager.runs[run.ID] = run
	manager.activeRunBySession[created.Session.ID] = run.ID
	manager.mu.Unlock()

	if _, _, err := manager.beginTerminalTransition(context.Background(), created.Session.ID, sessionDeleted); err != nil {
		t.Fatal(err)
	}
	if err := manager.store.Delete(created.Session.ID); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	delete(manager.sessions, created.Session.ID)
	delete(manager.remoteToLocal, created.Session.RemoteSessionID)
	delete(manager.loaded, created.Session.ID)
	manager.mu.Unlock()

	manager.finishRun(run, RunCompleted, "end_turn", nil)
	if _, err := manager.store.Get(created.Session.ID); err == nil {
		t.Fatal("late run finalization recreated a deleted session")
	}
	if _, err := manager.sessionForActivation(created.Session.ID); err == nil {
		t.Fatal("deleted terminal session remained activatable")
	}
}

func TestTerminalTransitionWaitsForSessionOperations(t *testing.T) {
	manager, err := newTestManager(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	created, err := manager.NewSession(context.Background(), manager.opts.DefaultCWD, nil)
	if err != nil {
		t.Fatal(err)
	}
	endOperation, err := manager.beginSessionOperation(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}

	transitionDone := make(chan error, 1)
	go func() {
		_, _, transitionErr := manager.beginTerminalTransition(context.Background(), created.Session.ID, SessionClosed)
		transitionDone <- transitionErr
	}()
	select {
	case err := <-transitionDone:
		t.Fatalf("terminal transition did not wait for the active operation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	endOperation()
	select {
	case err := <-transitionDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal transition did not resume after the operation ended")
	}
	if _, err := manager.beginSessionOperation(created.Session.ID); err == nil {
		t.Fatal("new session operation entered after terminal transition")
	}
}

func TestTerminalTransitionCancellationRollsBackMarker(t *testing.T) {
	manager, err := newTestManager(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()

	created, err := manager.NewSession(context.Background(), manager.opts.DefaultCWD, nil)
	if err != nil {
		t.Fatal(err)
	}
	endOperation, err := manager.beginSessionOperation(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, _, err := manager.beginTerminalTransition(ctx, created.Session.ID, SessionClosed); err == nil || errorCode(err) != "ACP_SESSION_TRANSITION_CANCELLED" {
		t.Fatalf("terminal transition error = %#v", err)
	}
	endOperation()
	nextOperation, err := manager.beginSessionOperation(created.Session.ID)
	if err != nil {
		t.Fatalf("cancelled transition left a terminal marker: %v", err)
	}
	nextOperation()
}

func TestManagerCloseSealsLateSessionWrites(t *testing.T) {
	workspace := t.TempDir()
	manager, err := newTestManager(t.TempDir(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.NewSession(context.Background(), workspace, nil)
	if err != nil {
		_ = manager.Close()
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.beginSessionOperation(created.Session.ID); err == nil {
		t.Fatal("session operation entered after manager close")
	}
	if _, err := manager.persistNewSession(sessionLifecycleResponse{SessionID: "remote-after-close"}, workspace, nil, SessionOwnership{}); err == nil {
		t.Fatal("new session metadata was persisted after manager close")
	}
	if _, err := manager.markSessionReady(created.Session, sessionLifecycleResponse{}); err == nil {
		t.Fatal("session metadata was rewritten after manager close")
	}
	records, err := manager.store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RemoteSessionID == "remote-after-close" {
			t.Fatal("late session record exists after manager close")
		}
	}
}
