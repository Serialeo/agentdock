package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

const journalOutputLimit = 64 << 10
const journalRecordLimit = 2 << 20
const maxJournalRecords = 4096

// Journal is an exclusive, crash-recoverable log of command facts. A record is
// the atomic unit: terminal state, bounded output and pending-report share one
// fsynced replacement. ACKs retain records and idempotency keys for later await.
type Journal struct {
	mu       sync.Mutex
	root     string
	lock     *os.File
	records  map[string]commandRecord
	requests map[string]string
	pending  map[string]commandRecord
	write    func(string, []byte, os.FileMode) error
	closed   bool
}

type commandRecord struct {
	Version       int                          `json:"schema_version"`
	ID            string                       `json:"command_session_id"`
	EventID       string                       `json:"event_id"`
	RequestID     string                       `json:"request_id,omitempty"`
	Digest        string                       `json:"arguments_digest"`
	Execution     session.ExecutionContext     `json:"execution"`
	State         protocol.CommandOutcomeState `json:"state"`
	ExitCode      *int                         `json:"exit_code,omitempty"`
	Stdout        string                       `json:"stdout,omitempty"`
	Stderr        string                       `json:"stderr,omitempty"`
	StdoutTotal   int64                        `json:"stdout_total_bytes"`
	StderrTotal   int64                        `json:"stderr_total_bytes"`
	CommandError  string                       `json:"command_error,omitempty"`
	TimedOut      bool                         `json:"timed_out,omitempty"`
	StartedAt     time.Time                    `json:"started_at"`
	FinishedAt    time.Time                    `json:"finished_at,omitempty"`
	UpdatedAt     time.Time                    `json:"updated_at"`
	PendingReport bool                         `json:"pending_report"`
}

func NewJournal(home string) (_ *Journal, returnErr error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("command journal home is required")
	}
	root := filepath.Join(home, "commands-v1")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("command journal must be a private real directory")
	}
	if err := securepath.EnsurePrivate(root); err != nil {
		return nil, err
	}
	lock, err := lockCommandJournal(filepath.Join(root, ".runtime.lock"))
	if err != nil {
		return nil, fmt.Errorf("command journal already in use or unavailable: %w", err)
	}
	j := &Journal{root: root, lock: lock, records: make(map[string]commandRecord), requests: make(map[string]string), pending: make(map[string]commandRecord), write: atomicfile.Write}
	defer func() {
		if returnErr != nil {
			_ = lock.Close()
		}
	}()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > journalRecordLimit {
			return nil, fmt.Errorf("invalid command journal record %s", entry.Name())
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, journalRecordLimit+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.Join(readErr, closeErr)
		}
		var record commandRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode command journal %s: %w", entry.Name(), err)
		}
		if record.Version != 1 || !validJournalID(record.ID) || entry.Name() != record.ID+".json" || record.EventID == "" || record.Digest == "" || record.StartedAt.IsZero() || (!record.State.Terminal() && record.State != protocol.CommandOutcomeStarting && record.State != protocol.CommandOutcomeRunning) {
			return nil, fmt.Errorf("invalid command journal identity/state in %s", entry.Name())
		}
		if len(record.Stdout) > journalOutputLimit || len(record.Stderr) > journalOutputLimit {
			return nil, fmt.Errorf("unbounded command journal output in %s", entry.Name())
		}
		if record.RequestID != "" {
			key := commandRequestKey(record.Execution, record.RequestID)
			if _, exists := j.requests[key]; exists {
				return nil, errors.New("duplicate durable command request identity")
			}
			j.requests[key] = record.ID
		}
		j.records[record.ID] = record
		if len(j.records) > maxJournalRecords {
			return nil, errors.New("command journal exceeds its record limit")
		}
	}
	// A prior process may have spawned a child, or died before spawn. Neither can
	// be inferred safely from a starting/running record; never resume or rerun it.
	for _, record := range j.records {
		if record.State.Terminal() {
			continue
		}
		record.State = protocol.CommandOutcomeUnknown
		record.ExitCode = nil
		record.FinishedAt = time.Now().UTC()
		record.UpdatedAt = record.FinishedAt
		record.PendingReport = record.Execution.WorkSessionID != ""
		record.CommandError = "AgentDock restarted before a durable terminal outcome; execution may have produced side effects and was not rerun"
		if err := j.persist(record); err != nil {
			return nil, fmt.Errorf("recover command outcome: %w", err)
		}
	}
	return j, nil
}

func validJournalID(id string) bool {
	if !strings.HasPrefix(id, "session-") || len(id) != len("session-")+24 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "session-"))
	return err == nil
}

func commandRequestKey(execution session.ExecutionContext, requestID string) string {
	data, _ := json.Marshal([]string{execution.WorkSessionID, execution.TargetID, execution.ProjectID, execution.DeploymentID, requestID})
	return string(data)
}

func (j *Journal) persist(record commandRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	j.pending[record.ID] = record
	if err := j.write(filepath.Join(j.root, record.ID+".json"), data, 0600); err != nil {
		return err
	}
	j.records[record.ID] = record
	delete(j.pending, record.ID)
	return nil
}

func (j *Journal) flush() error {
	if j.closed {
		return errors.New("command journal is closed")
	}
	for _, record := range j.pending {
		if err := j.persist(record); err != nil {
			return fmt.Errorf("persist command outcome %s: %w", record.ID, err)
		}
	}
	return nil
}

func (j *Journal) lookup(execution session.ExecutionContext, requestID, digest string) (commandRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.flush(); err != nil {
		return commandRecord{}, false, err
	}
	return j.lookupLocked(execution, requestID, digest)
}

func (j *Journal) lookupLocked(execution session.ExecutionContext, requestID, digest string) (commandRecord, bool, error) {
	if requestID == "" {
		return commandRecord{}, false, nil
	}
	id, exists := j.requests[commandRequestKey(execution, requestID)]
	if !exists {
		return commandRecord{}, false, nil
	}
	record := j.records[id]
	if record.Digest != digest {
		return commandRecord{}, false, toolError("COMMAND_REQUEST_CONFLICT", "request_id already identifies a command with different arguments or execution context", "conflict")
	}
	return record, true, nil
}

func (j *Journal) begin(execution session.ExecutionContext, requestID, digest string) (commandRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.flush(); err != nil {
		return commandRecord{}, false, err
	}
	if old, exists, err := j.lookupLocked(execution, requestID, digest); err != nil || exists {
		return old, exists, err
	}
	if len(j.records) >= maxJournalRecords {
		return commandRecord{}, false, toolError("COMMAND_JOURNAL_FULL", "durable command journal is full; existing outcomes remain readable and no command was started", "resource_limit")
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return commandRecord{}, false, err
	}
	now := time.Now().UTC()
	record := commandRecord{Version: 1, ID: "session-" + hex.EncodeToString(raw), EventID: "command-event-" + hex.EncodeToString(raw), RequestID: requestID, Digest: digest, Execution: execution, State: protocol.CommandOutcomeStarting, StartedAt: now, UpdatedAt: now}
	// Reserve the key even if fsync reports an error after rename. No process is
	// started on failure; retries must first reconcile this durable identity.
	if requestID != "" {
		j.requests[commandRequestKey(execution, requestID)] = record.ID
	}
	if err := j.persist(record); err != nil {
		// The caller will not spawn. Keep a terminal nonexecution fact in memory so
		// later reconciliation cannot expose a phantom running command indefinitely.
		record.State = protocol.CommandOutcomeFailed
		record.CommandError = "command was not started because its initial durable record could not be committed"
		record.FinishedAt = time.Now().UTC()
		record.UpdatedAt = record.FinishedAt
		record.PendingReport = record.Execution.WorkSessionID != ""
		j.pending[record.ID] = record
		return record, false, err
	}
	return record, false, nil
}

func outputTail(text string, limit int) string {
	text = strings.ToValidUTF8(text, "�")
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

func (j *Journal) update(id string, mutate func(*commandRecord)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("command journal is closed")
	}
	record, exists := j.pending[id]
	if !exists {
		record, exists = j.records[id]
	}
	if !exists {
		return errors.New("durable command record is missing")
	}
	if record.State.Terminal() {
		return nil
	}
	mutate(&record)
	record.UpdatedAt = time.Now().UTC()
	return j.persist(record)
}

func (j *Journal) running(id string) error {
	return j.update(id, func(r *commandRecord) { r.State = protocol.CommandOutcomeRunning })
}

func (j *Journal) output(id string, stderr bool, data []byte) error {
	return j.update(id, func(r *commandRecord) {
		if stderr {
			r.StderrTotal += int64(len(data))
			r.Stderr = outputTail(r.Stderr+string(data), journalOutputLimit)
		} else {
			r.StdoutTotal += int64(len(data))
			r.Stdout = outputTail(r.Stdout+string(data), journalOutputLimit)
		}
	})
}

func (j *Journal) finish(id string, completion session.Completion) error {
	return j.update(id, func(r *commandRecord) {
		r.State = protocol.CommandOutcomeCompleted
		if completion.ExitCode != 0 || completion.Error != nil {
			r.State = protocol.CommandOutcomeFailed
		}
		if completion.Cancelled && !completion.TimedOut {
			r.State = protocol.CommandOutcomeInterrupted
		}
		code := completion.ExitCode
		r.ExitCode = &code
		r.Stdout = outputTail(completion.Stdout, journalOutputLimit)
		r.Stderr = outputTail(completion.Stderr, journalOutputLimit)
		r.StdoutTotal = int64(completion.StdoutTotalBytes)
		r.StderrTotal = int64(completion.StderrTotalBytes)
		r.FinishedAt = completion.FinishedAt.UTC()
		r.TimedOut = completion.TimedOut
		if completion.Error != nil {
			r.CommandError = completion.Error.Error()
		}
		r.PendingReport = r.Execution.WorkSessionID != ""
	})
}

func (j *Journal) startFailed(id string, failure error) error {
	return j.update(id, func(r *commandRecord) {
		r.State = protocol.CommandOutcomeFailed
		r.CommandError = "command process could not be started: " + failure.Error()
		var startError *session.StartError
		if errors.As(failure, &startError) && startError.ProcessStarted {
			r.State = protocol.CommandOutcomeUnknown
			r.CommandError = "command process started but startup control failed; side effects are unknown and the command was not rerun: " + failure.Error()
		}
		r.FinishedAt = time.Now().UTC()
		r.PendingReport = r.Execution.WorkSessionID != ""
	})
}

func (j *Journal) get(id string) (commandRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.flush(); err != nil {
		return commandRecord{}, false, err
	}
	record, exists := j.records[id]
	return record, exists, nil
}

func (j *Journal) list() ([]commandRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.flush(); err != nil {
		return nil, err
	}
	records := make([]commandRecord, 0, len(j.records))
	for _, record := range j.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, k int) bool {
		if records[i].StartedAt.Equal(records[k].StartedAt) {
			return records[i].ID < records[k].ID
		}
		return records[i].StartedAt.Before(records[k].StartedAt)
	})
	return records, nil
}

func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	err := j.flush()
	j.closed = true
	return errors.Join(err, j.lock.Close())
}

func (r commandRecord) outcome() protocol.CommandOutcome {
	stdout := outputTail(r.Stdout, protocol.MaxCommandOutcomeOutputBytes/2)
	stderr := outputTail(r.Stderr, protocol.MaxCommandOutcomeOutputBytes/2)
	out := protocol.CommandOutcome{EventID: r.EventID, CommandSessionID: r.ID, State: r.State, ExitCode: r.ExitCode, Stdout: stdout, Stderr: stderr, StdoutDroppedBytes: max(0, r.StdoutTotal-int64(len(stdout))), StderrDroppedBytes: max(0, r.StderrTotal-int64(len(stderr))), Workdir: r.Execution.Workdir, CommandError: r.CommandError, TimedOut: r.TimedOut, StartedAt: r.StartedAt.Format(time.RFC3339Nano), UpdatedAt: r.UpdatedAt.Format(time.RFC3339Nano), PendingReport: r.PendingReport, ClientRequestID: r.RequestID}
	out.ExecutionContext = protocol.ExecutionContext{WorkSessionID: r.Execution.WorkSessionID, TargetID: r.Execution.TargetID, ProjectID: r.Execution.ProjectID, DeploymentID: r.Execution.DeploymentID, DeploymentRevision: r.Execution.DeploymentRevision, ContextRevision: r.Execution.ContextRevision}
	if !r.FinishedAt.IsZero() {
		out.FinishedAt = r.FinishedAt.Format(time.RFC3339Nano)
	}
	out.OutputTruncated = out.StdoutDroppedBytes > 0 || out.StderrDroppedBytes > 0
	return out
}

func (j *Journal) ReadOutcomes(ctx context.Context, request protocol.CommandOutcomesReadRequest) (protocol.CommandOutcomesReadResult, error) {
	result := protocol.CommandOutcomesReadResult{Outcomes: []protocol.CommandOutcome{}}
	if request.Limit < 0 || request.Limit > protocol.MaxCommandOutcomesPerRead || len(request.CommandSessionIDs) > protocol.MaxCommandOutcomesPerRead {
		return result, errors.New("command outcome read exceeds its bound")
	}
	limit := request.Limit
	if limit == 0 {
		limit = protocol.DefaultCommandOutcomesPerRead
	}
	selected := make(map[string]bool, len(request.CommandSessionIDs))
	for _, id := range request.CommandSessionIDs {
		if !validJournalID(id) {
			return result, errors.New("invalid command session id")
		}
		selected[id] = true
	}
	records, err := j.list()
	if err != nil {
		return result, err
	}
	bytes := 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if record.Execution.WorkSessionID == "" {
			continue
		}
		if len(selected) > 0 {
			if !selected[record.ID] {
				continue
			}
		} else if request.PendingOnly && (!record.State.Terminal() || !record.PendingReport) {
			continue
		}
		outcome := record.outcome()
		encoded, err := json.Marshal(outcome)
		if err != nil {
			return result, err
		}
		if len(result.Outcomes) >= limit || bytes+len(encoded) > protocol.MaxCommandOutcomeBatchBytes-256 {
			result.HasMore = true
			break
		}
		bytes += len(encoded)
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}

func (j *Journal) AckOutcomes(ctx context.Context, request protocol.CommandOutcomesAckRequest) (protocol.CommandOutcomesAckResult, error) {
	result := protocol.CommandOutcomesAckResult{AcknowledgedEventIDs: []string{}}
	if len(request.EventIDs) == 0 || len(request.EventIDs) > protocol.MaxCommandOutcomesPerRead {
		return result, errors.New("command outcome ack requires 1 to 128 event ids")
	}
	selected := make(map[string]bool, len(request.EventIDs))
	for _, id := range request.EventIDs {
		if len(id) > 128 || id == "" {
			return result, errors.New("invalid command event id")
		}
		selected[id] = true
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.flush(); err != nil {
		return result, err
	}
	for _, record := range j.records {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !selected[record.EventID] || record.Execution.WorkSessionID == "" || !record.State.Terminal() {
			continue
		}
		if record.PendingReport {
			record.PendingReport = false
			if err := j.persist(record); err != nil {
				return result, err
			}
		}
		result.AcknowledgedEventIDs = append(result.AcknowledgedEventIDs, record.EventID)
	}
	sort.Strings(result.AcknowledgedEventIDs)
	return result, nil
}
