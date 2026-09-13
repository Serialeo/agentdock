package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func journalIdentity() session.ExecutionContext {
	return session.ExecutionContext{Workdir: "/project", WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1", NodeID: "node-1", DeploymentRevision: "rev-1", ContextRevision: "ctx-1"}
}
func mustJournal(t *testing.T, home string) *Journal {
	t.Helper()
	j, err := NewJournal(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}
func beginJournal(t *testing.T, j *Journal, id string) commandRecord {
	t.Helper()
	record, replayed, err := j.begin(journalIdentity(), id, "sha256:test")
	if err != nil || replayed {
		t.Fatalf("begin=%+v %v %v", record, replayed, err)
	}
	return record
}
func finishJournal(t *testing.T, j *Journal, r commandRecord, stdout string) {
	t.Helper()
	if err := j.finish(r.ID, session.Completion{ExitCode: 0, Stdout: stdout, StdoutTotalBytes: len(stdout), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestCommandJournalReplayAckAndRestart(t *testing.T) {
	home := t.TempDir()
	j := mustJournal(t, home)
	record := beginJournal(t, j, "stable-request")
	before, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{CommandSessionIDs: []string{record.ID}, PendingOnly: true})
	if err != nil || len(before.Outcomes) != 1 || before.Outcomes[0].State != protocol.CommandOutcomeStarting {
		t.Fatalf("starting outcome=%+v %v", before, err)
	}
	if before.Outcomes[0].ExecutionContext.ContextRevision != "ctx-1" {
		t.Fatal("execution identity was not persisted before start")
	}
	if err := j.running(record.ID); err != nil {
		t.Fatal(err)
	}
	finishJournal(t, j, record, "durable-result")
	first, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{PendingOnly: true})
	if err != nil || len(first.Outcomes) != 1 || !first.Outcomes[0].PendingReport || first.Outcomes[0].ExitCode == nil || *first.Outcomes[0].ExitCode != 0 {
		t.Fatalf("pending outcome=%+v %v", first, err)
	}
	raw, err := os.ReadFile(filepath.Join(j.root, record.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted commandRecord
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.State != protocol.CommandOutcomeCompleted || !persisted.PendingReport || persisted.Stdout != "durable-result" {
		t.Fatalf("terminal and report flag not committed together: %+v", persisted)
	}
	for range 2 {
		ack, err := j.AckOutcomes(context.Background(), protocol.CommandOutcomesAckRequest{EventIDs: []string{record.EventID}})
		if err != nil || !reflect.DeepEqual(ack.AcknowledgedEventIDs, []string{record.EventID}) {
			t.Fatalf("ack=%+v %v", ack, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = mustJournal(t, home)
	pending, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{PendingOnly: true})
	if err != nil || len(pending.Outcomes) != 0 {
		t.Fatalf("ACK was not retained: %+v %v", pending, err)
	}
	replay, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{CommandSessionIDs: []string{record.ID}, PendingOnly: true})
	if err != nil || len(replay.Outcomes) != 1 {
		t.Fatalf("explicit ACKed read=%+v %v", replay, err)
	}
	want := first.Outcomes[0]
	want.PendingReport = false
	if !reflect.DeepEqual(replay.Outcomes[0], want) {
		t.Fatalf("ACK/restart changed execution facts: got %+v want %+v", replay.Outcomes[0], want)
	}
	duplicate, exists, err := j.lookup(record.Execution, record.RequestID, record.Digest)
	if err != nil || !exists || duplicate.ID != record.ID {
		t.Fatalf("lost durable idempotency: %+v %v %v", duplicate, exists, err)
	}
}

func TestCommandJournalRecoversUnfinishedAsUnknownWithoutExitCode(t *testing.T) {
	home := t.TempDir()
	j := mustJournal(t, home)
	starting := beginJournal(t, j, "starting")
	running := beginJournal(t, j, "running")
	if err := j.running(running.ID); err != nil {
		t.Fatal(err)
	}
	if err := j.output(running.ID, false, []byte("before-crash")); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = mustJournal(t, home)
	for _, record := range []commandRecord{starting, running} {
		got, exists, err := j.get(record.ID)
		if err != nil || !exists || got.State != protocol.CommandOutcomeUnknown || got.ExitCode != nil || !got.PendingReport || got.EventID != record.EventID {
			t.Fatalf("recovered=%+v %v %v", got, exists, err)
		}
		if got.result(1024)["command_ok"] != false {
			t.Fatal("unknown outcome reported success")
		}
	}
	got, _, _ := j.get(running.ID)
	if got.Stdout != "before-crash" {
		t.Fatalf("durable pre-crash output lost: %+v", got)
	}
	// Reopening an already recovered record must not create another event.
	first := got.outcome()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = mustJournal(t, home)
	got, _, _ = j.get(running.ID)
	if !reflect.DeepEqual(first, got.outcome()) {
		t.Fatal("recovery changed an already terminal event")
	}
}

func TestCommandJournalFailedTerminalCommitCannotBeReadUntilDurable(t *testing.T) {
	j := mustJournal(t, t.TempDir())
	record := beginJournal(t, j, "atomic")
	originalWrite := j.write
	j.write = func(path string, data []byte, mode os.FileMode) error {
		var candidate commandRecord
		if err := json.Unmarshal(data, &candidate); err != nil {
			return err
		}
		if candidate.State.Terminal() {
			if !candidate.PendingReport {
				t.Error("terminal write omitted pending report")
			}
			return errors.New("injected fsync failure")
		}
		return originalWrite(path, data, mode)
	}
	if err := j.finish(record.ID, session.Completion{ExitCode: 0, Stdout: "finished", StdoutTotalBytes: 8, FinishedAt: time.Now()}); err == nil {
		t.Fatal("terminal persistence failure was hidden")
	}
	raw, err := os.ReadFile(filepath.Join(j.root, record.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var durable commandRecord
	_ = json.Unmarshal(raw, &durable)
	if durable.State.Terminal() || durable.PendingReport {
		t.Fatal("failed terminal write partly committed")
	}
	if _, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{PendingOnly: true}); err == nil {
		t.Fatal("uncommitted terminal state was exposed")
	}
	j.write = originalWrite
	read, err := j.ReadOutcomes(context.Background(), protocol.CommandOutcomesReadRequest{PendingOnly: true})
	if err != nil || len(read.Outcomes) != 1 || read.Outcomes[0].Stdout != "finished" {
		t.Fatalf("failed write did not recover: %+v %v", read, err)
	}
}

func TestCommandJournalConcurrentIdempotencyAndScope(t *testing.T) {
	j := mustJournal(t, t.TempDir())
	var fresh atomic.Int32
	ids := make(chan string, 20)
	errs := make(chan error, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record, replayed, err := j.begin(journalIdentity(), "same-request", "digest-1")
			if err != nil {
				errs <- err
				return
			}
			if !replayed {
				fresh.Add(1)
			}
			ids <- record.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var id string
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("concurrent request started multiple identities")
		}
		id = got
	}
	if fresh.Load() != 1 {
		t.Fatalf("new identities=%d", fresh.Load())
	}
	_, _, err := j.begin(journalIdentity(), "same-request", "digest-2")
	requireCommandToolErrorCode(t, err, "COMMAND_REQUEST_CONFLICT")
	other := journalIdentity()
	other.TargetID = "target-2"
	record, replayed, err := j.begin(other, "same-request", "digest-2")
	if err != nil || replayed || record.ID == id {
		t.Fatalf("different target incorrectly reused id: %+v %v %v", record, replayed, err)
	}
}

func TestCommandJournalReadsAreBoundedAndLocalCommandsStayLocal(t *testing.T) {
	j := mustJournal(t, t.TempDir())
	local, _, err := j.begin(session.ExecutionContext{}, "local", "digest")
	if err != nil {
		t.Fatal(err)
	}
	finishJournal(t, j, local, "local-output")
	payload := strings.Repeat("界", 50000)
	for index := range 40 {
		record := beginJournal(t, j, fmt.Sprintf("large-%d", index))
		if err := j.finish(record.ID, session.Completion{ExitCode: 0, Stdout: payload, Stderr: payload, StdoutTotalBytes: len(payload), StderrTotalBytes: len(payload), FinishedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	request := protocol.CommandOutcomesReadRequest{PendingOnly: true, Limit: protocol.MaxCommandOutcomesPerRead}
	first, err := j.ReadOutcomes(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(first)
	if !first.HasMore || len(first.Outcomes) == 0 || len(encoded) > protocol.MaxCommandOutcomeBatchBytes {
		t.Fatalf("unbounded batch: %d bytes/%d outcomes, has_more=%v", len(encoded), len(first.Outcomes), first.HasMore)
	}
	ids := []string{}
	for _, outcome := range first.Outcomes {
		if outcome.CommandSessionID == local.ID || !outcome.OutputTruncated || !utf8.ValidString(outcome.Stdout) || len(outcome.Stdout)+len(outcome.Stderr) > protocol.MaxCommandOutcomeOutputBytes {
			t.Fatalf("invalid bounded outcome: %+v", outcome)
		}
		ids = append(ids, outcome.EventID)
	}
	if _, err := j.AckOutcomes(context.Background(), protocol.CommandOutcomesAckRequest{EventIDs: ids}); err != nil {
		t.Fatal(err)
	}
	next, err := j.ReadOutcomes(context.Background(), request)
	if err != nil || len(next.Outcomes) == 0 {
		t.Fatalf("pending ACK drain=%+v %v", next, err)
	}
	for _, outcome := range next.Outcomes {
		for _, id := range ids {
			if outcome.EventID == id {
				t.Fatal("ACKed event redelivered as pending")
			}
		}
	}
	for _, request := range []protocol.CommandOutcomesReadRequest{{Limit: -1}, {Limit: protocol.MaxCommandOutcomesPerRead + 1}, {CommandSessionIDs: []string{"../../outside"}}} {
		if _, err := j.ReadOutcomes(context.Background(), request); err == nil {
			t.Fatal("unbounded or invalid request accepted")
		}
	}
}

func TestCommandJournalExclusiveOwnershipAndCapacity(t *testing.T) {
	home := t.TempDir()
	j := mustJournal(t, home)
	if other, err := NewJournal(home); err == nil {
		_ = other.Close()
		t.Fatal("second runtime acquired a live journal")
	}
	record := beginJournal(t, j, "existing")
	for index := len(j.records); index < maxJournalRecords; index++ {
		j.records[fmt.Sprintf("fixture-%d", index)] = record
	}
	_, _, err := j.begin(journalIdentity(), "new", "digest")
	requireCommandToolErrorCode(t, err, "COMMAND_JOURNAL_FULL")
	replay, exists, err := j.begin(journalIdentity(), record.RequestID, record.Digest)
	if err != nil || !exists || replay.ID != record.ID {
		t.Fatal("capacity blocked safe idempotent replay")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	_ = mustJournal(t, home)
}

func TestCommandJournalRejectsCorruptState(t *testing.T) {
	home := t.TempDir()
	j := mustJournal(t, home)
	record := beginJournal(t, j, "corrupt")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(filepath.Join(j.root, record.ID+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := NewJournal(home); err == nil {
		_ = reopened.Close()
		t.Fatal("corrupt durable history silently discarded")
	}
}

func TestCommandJournalPostSpawnFailureRemainsUnknown(t *testing.T) {
	j := mustJournal(t, t.TempDir())
	record := beginJournal(t, j, "post-spawn")
	if err := j.output(record.ID, false, []byte("possible-side-effect")); err != nil {
		t.Fatal(err)
	}
	if err := j.startFailed(record.ID, &session.StartError{Cause: errors.New("controller attach failed"), ProcessStarted: true}); err != nil {
		t.Fatal(err)
	}
	got, _, err := j.get(record.ID)
	if err != nil || got.State != protocol.CommandOutcomeUnknown || got.ExitCode != nil || !got.PendingReport || got.Stdout != "possible-side-effect" {
		t.Fatalf("post-spawn uncertainty lost: %+v %v", got, err)
	}
}
