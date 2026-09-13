package session

import (
	"testing"
	"time"
)

func TestExecutionContextIsIncludedInSnapshotAndSummary(t *testing.T) {
	s := &Session{
		ID:        "session-test",
		StartedAt: time.Now(),
		Done:      make(chan struct{}),
		Terminal:  "conpty",
	}
	s.SetExecutionContext(ExecutionContext{Workdir: "/srv/project"})

	snapshot := s.Snapshot("running", 1024)
	if snapshot.Workdir != "/srv/project" {
		t.Fatalf("snapshot execution metadata = %#v", snapshot)
	}

	summary := s.Summary()
	if summary.Workdir != "/srv/project" {
		t.Fatalf("summary execution metadata = %#v", summary)
	}
}
