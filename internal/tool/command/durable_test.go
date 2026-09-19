package command

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func newDurableService(t *testing.T) *Service {
	t.Helper()
	service, cfg := newCommandTestService(t)
	journal, err := NewJournal(cfg.AgentDockHome)
	if err != nil {
		t.Fatal(err)
	}
	service.journal = journal
	return service
}
func durableOwnerContext() context.Context {
	return projectstate.WithExecution(context.Background(), commandProjectExecutionForTest("target-a", true))
}

func durableRecordForRequestID(t *testing.T, service *Service, requestID string) commandRecord {
	t.Helper()
	records, err := service.journal.list()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RequestID == requestID {
			return record
		}
	}
	t.Fatalf("durable record for request_id %q not found", requestID)
	return commandRecord{}
}

func TestDurableCommandFastExitConcurrentRetryAndCursorIndependentReplay(t *testing.T) {
	service := newDurableService(t)
	ctx := durableOwnerContext()
	request := ExecRequest{RequestID: "once", Cmd: "echo once >> counter.txt; echo durable-result", ExecutionMode: "async"}
	originalWrite := service.journal.write
	service.journal.write = func(path string, data []byte, mode os.FileMode) error {
		var record commandRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		if record.State == protocol.CommandOutcomeStarting {
			if record.Execution.WorkSessionID != "ws-1" || record.Execution.ContextRevision != "ctx-1" || record.ID == "" {
				t.Error("identity missing before process spawn")
			}
			if _, err := os.Stat(filepath.Join(service.ws.DefaultCWD(), "counter.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Error("process spawned before starting record")
			}
		}
		return originalWrite(path, data, mode)
	}
	var wg sync.WaitGroup
	results := make(chan Result, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Exec(ctx, request)
			if err != nil {
				errs <- err
			} else {
				results <- result
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	id := ""
	for result := range results {
		got := result["session_id"].(string)
		if id != "" && id != got {
			t.Fatal("retry spawned a second command")
		}
		id = got
	}
	if id == "" {
		t.Fatal("no session returned")
	}
	live, exists := service.sessions.Get(id)
	if !exists {
		t.Fatal("async session not registered")
	}
	select {
	case <-live.Done:
	case <-time.After(15 * time.Second):
		t.Fatal("fast command timed out")
	}
	// Consume the live output. Durable results must not share this cursor.
	_ = live.Snapshot("exited", MaxOutputBytes)
	first, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: id})
	if err != nil || !strings.Contains(first["stdout"].(string), "durable-result") {
		t.Fatalf("durable result=%+v %v", first, err)
	}
	counter := filepath.Join(service.ws.DefaultCWD(), "counter.txt")
	before, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	service.sessions.Delete(id)
	second, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: id})
	if err != nil || second["stdout"] != first["stdout"] {
		t.Fatal("in-memory cleanup consumed durable output")
	}
	request.ExecutionMode = "sync"
	small := 1024
	request.MaxOutputBytes = &small
	replay, err := service.Exec(ctx, request)
	if err != nil || replay["replayed"] != true {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	after, _ := os.ReadFile(counter)
	if string(before) != string(after) {
		t.Fatal("replayed request executed a second external side effect")
	}
	request.Cmd = "echo changed"
	_, err = service.Exec(ctx, request)
	requireCommandToolErrorCode(t, err, "COMMAND_REQUEST_CONFLICT")
	otherCtx := projectstate.WithExecution(context.Background(), commandProjectExecutionForTest("target-b", true))
	_, err = service.Observe(otherCtx, SessionObserveRequest{Action: "status", SessionID: id})
	requireCommandToolErrorCode(t, err, protocol.ErrorSessionTargetDenied)
	eventID := durableRecordForRequestID(t, service, "once").EventID
	if _, err := service.AckCommandOutcomes(context.Background(), protocol.CommandOutcomesAckRequest{EventIDs: []string{eventID}}); err != nil {
		t.Fatal(err)
	}
	home := service.config().AgentDockHome
	if err := service.journal.Close(); err != nil {
		t.Fatal(err)
	}
	service.journal, err = NewJournal(home)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: id})
	if err != nil || recovered["stdout"] != first["stdout"] {
		t.Fatalf("restart/ACK result=%+v %v", recovered, err)
	}
	listed, err := service.Observe(ctx, SessionObserveRequest{Action: "list"})
	sessions, _ := listed["sessions"].([]map[string]any)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("recovered session absent from listing: %+v %v", listed, err)
	}
}

func TestDurableCommandDoesNotSpawnWhenStartingCommitFails(t *testing.T) {
	service := newDurableService(t)
	originalWrite := service.journal.write
	service.journal.write = func(string, []byte, os.FileMode) error { return errors.New("injected disk full") }
	request := ExecRequest{RequestID: "no-spawn", Cmd: "echo forbidden > must-not-exist.txt", ExecutionMode: "sync"}
	_, err := service.Exec(context.Background(), request)
	requireCommandToolErrorCode(t, err, "COMMAND_JOURNAL_UNAVAILABLE")
	if _, err := os.Stat(filepath.Join(service.ws.DefaultCWD(), "must-not-exist.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command started before durable starting record")
	}
	if service.sessions.ReservationCount() != 0 || service.sessions.StartingCount() != 0 {
		t.Fatal("failed start leaked command reservation")
	}
	service.journal.write = originalWrite
	// The failed/ambiguous starting write retains its identity. Retrying must not
	// automatically spawn, even if storage becomes available again.
	result, err := service.Exec(context.Background(), request)
	if err != nil || result["replayed"] != true || result["command_error"] == "" {
		t.Fatalf("starting retry=%+v %v", result, err)
	}
	if record := durableRecordForRequestID(t, service, "no-spawn"); record.State != protocol.CommandOutcomeFailed {
		t.Fatalf("starting retry durable state = %s", record.State)
	}
	if _, err := os.Stat(filepath.Join(service.ws.DefaultCWD(), "must-not-exist.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retry of ambiguous starting write spawned a process")
	}
}

func TestDurableCommandOutlivesRequestButNodeShutdownRecordsInterruption(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep fixture")
	}
	service := newDurableService(t)
	ctx, cancel := context.WithCancel(durableOwnerContext())
	result, err := service.Exec(ctx, ExecRequest{RequestID: "background", Cmd: "sleep 0.08; printf completed", ExecutionMode: "async"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	id := result["session_id"].(string)
	live, _ := service.sessions.Get(id)
	select {
	case <-live.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not finish")
	}
	outcome, err := service.Observe(durableOwnerContext(), SessionObserveRequest{Action: "status", SessionID: id})
	if err != nil || outcome["stdout"] != "completed" || outcome["exit_code"] != 0 {
		t.Fatalf("MCP cancel killed command: %+v %v", outcome, err)
	}
	if _, exists := outcome["status"]; exists {
		t.Fatalf("completed session observation exposed redundant status: %+v", outcome)
	}
	next, err := service.Exec(durableOwnerContext(), ExecRequest{RequestID: "shutdown", Cmd: "sleep 30", ExecutionMode: "async"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	j := mustJournal(t, service.config().AgentDockHome)
	read, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{CommandSessionIDs: []string{next["session_id"].(string)}})
	if err != nil || len(read.Outcomes) != 1 || read.Outcomes[0].State != protocol.CommandOutcomeInterrupted || !read.Outcomes[0].PendingReport {
		t.Fatalf("shutdown outcome=%+v %v", read, err)
	}
}

func TestDurableCommandTimeoutRemainsFailureAfterRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep fixture")
	}
	service := newDurableService(t)
	timeout := 80
	result, err := service.Exec(durableOwnerContext(), ExecRequest{RequestID: "timeout", Cmd: "sleep 30", TimeoutMS: &timeout, ExecutionMode: "sync"})
	if err != nil || result["timed_out"] != true {
		t.Fatalf("timeout=%+v %v", result, err)
	}
	if _, exists := result["status"]; exists {
		t.Fatalf("timeout result duplicated status: %+v", result)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	journal := mustJournal(t, service.config().AgentDockHome)
	read, err := journal.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{PendingOnly: true})
	if err != nil || len(read.Outcomes) != 1 || read.Outcomes[0].State != protocol.CommandOutcomeFailed || !read.Outcomes[0].TimedOut || !read.Outcomes[0].PendingReport {
		t.Fatalf("recovered timeout=%+v %v", read, err)
	}
}

func TestDurableCommandUsesLiveOutputBeforeBoundedJournalReplay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX head/tr fixture")
	}
	service := newDurableService(t)
	ctx := durableOwnerContext()
	limit := 256 << 10
	result, err := service.Exec(ctx, ExecRequest{
		RequestID:      "wide-live-output",
		Cmd:            "head -c 131072 /dev/zero | tr '\\000' x",
		ExecutionMode:  "sync",
		MaxOutputBytes: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result["stdout"].(string)); got != 131072 {
		t.Fatalf("live result lost output: bytes=%d", got)
	}
	if _, exists := result["stdout_truncated"]; exists {
		t.Fatalf("untruncated live result exposed stdout_truncated=false: %+v", result)
	}

	// Sync sessions are not retained in the live store. A later read therefore
	// exercises the crash/restart-compatible bounded journal replay path.
	record := durableRecordForRequestID(t, service, "wide-live-output")
	replayed, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: record.ID, MaxOutputBytes: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(replayed["stdout"].(string)); got != journalOutputLimit || replayed["stdout_truncated"] != true {
		t.Fatalf("durable replay must remain bounded: bytes=%d truncated=%v", got, replayed["stdout_truncated"])
	}
}

func TestDurableAsyncStatusKeepsLargeLiveOutputRepeatable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleep/head/tr fixture")
	}
	service := newDurableService(t)
	ctx := durableOwnerContext()
	limit := 256 << 10
	started, err := service.Exec(ctx, ExecRequest{
		RequestID:      "wide-async-output",
		Cmd:            "sleep 0.05; head -c 131072 /dev/zero | tr '\\000' y",
		ExecutionMode:  "async",
		MaxOutputBytes: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := started["session_id"].(string)
	live, ok := service.sessions.Get(id)
	if !ok {
		t.Fatal("async session was not retained")
	}
	select {
	case <-live.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("async command did not finish")
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: id, MaxOutputBytes: &limit})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(result["stdout"].(string)); got != 131072 {
			t.Fatalf("status read %d lost live output: bytes=%d", attempt+1, got)
		}
		if _, exists := result["stdout_truncated"]; exists {
			t.Fatalf("status read %d exposed stdout_truncated=false: %+v", attempt+1, result)
		}
	}
	service.sessions.Delete(id)
	replayed, err := service.Observe(ctx, SessionObserveRequest{Action: "status", SessionID: id, MaxOutputBytes: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(replayed["stdout"].(string)); got != journalOutputLimit || replayed["stdout_truncated"] != true {
		t.Fatalf("post-live replay must use bounded journal: bytes=%d truncated=%v", got, replayed["stdout_truncated"])
	}
}
