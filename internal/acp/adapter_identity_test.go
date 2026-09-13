package acp

import "testing"

func TestManagerScopesPersistentSessionsToConfiguredAgent(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	first := newStoreOnlyManager(t, home, "adapter-a")
	createdByFirst, err := first.persistNewSession(sessionLifecycleResponse{SessionID: "shared-remote-id"}, workspace, nil, SessionOwnership{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := newStoreOnlyManager(t, home, "adapter-b")
	defer func() { _ = second.Close() }()

	sessions, err := second.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("adapter-b loaded adapter-a sessions: %#v", sessions)
	}
	if _, err := second.InspectSession(createdByFirst.ID); err == nil || errorCode(err) != "ACP_SESSION_NOT_FOUND" {
		t.Fatalf("adapter-b inspected adapter-a session: %#v", err)
	}

	createdBySecond, err := second.persistNewSession(sessionLifecycleResponse{SessionID: "shared-remote-id"}, workspace, nil, SessionOwnership{})
	if err != nil {
		t.Fatalf("different adapters must be able to reuse remote session ids: %v", err)
	}
	if createdBySecond.Agent != "adapter-b" {
		t.Fatalf("adapter-b session agent = %q", createdBySecond.Agent)
	}

	reloadedFirst := newStoreOnlyManager(t, home, "adapter-a")
	defer func() { _ = reloadedFirst.Close() }()
	firstSessions, err := reloadedFirst.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(firstSessions) != 1 || firstSessions[0].ID != createdByFirst.ID {
		t.Fatalf("adapter-a sessions = %#v", firstSessions)
	}
}

func newStoreOnlyManager(t *testing.T, home, agent string) *Manager {
	t.Helper()
	manager, err := NewManager(Options{
		Home:       home,
		DefaultCWD: home,
		Agent: AgentSpec{
			Name:    agent,
			Command: "unused-in-store-only-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
