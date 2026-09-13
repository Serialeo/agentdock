package command

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func commandIdentity(ctx context.Context) session.ExecutionContext {
	execution, scoped := projectExecution(ctx)
	if !scoped {
		return session.ExecutionContext{}
	}
	return session.ExecutionContext{WorkSessionID: execution.Target.WorkSessionID, TargetID: execution.Target.TargetID, ProjectID: execution.Target.ProjectID, DeploymentID: execution.Target.DeploymentID, NodeID: execution.Deployment.NodeID, DeploymentRevision: execution.Target.DeploymentRevision, ContextRevision: execution.Target.ContextRevision}
}

func commandDigest(ctx context.Context, request ExecRequest) (string, error) {
	// Observation preferences do not change which command was submitted. Digest
	// only caller-supplied execution arguments and the trusted execution identity;
	// never persist the command text, stdin or secret environment values.
	request.RequestID = ""
	request.YieldTimeMS = nil
	request.MaxOutputBytes = nil
	request.ExecutionMode = ""
	timeout, err := commandTimeout(request.TimeoutMS)
	if err != nil {
		return "", err
	}
	timeoutMS := int(timeout / time.Millisecond)
	request.TimeoutMS = &timeoutMS
	data, err := json.Marshal(struct {
		Request   ExecRequest
		Execution session.ExecutionContext
	}{request, commandIdentity(ctx)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

func durableFailure(err error) error {
	if _, ok := err.(*ToolError); ok {
		return err
	}
	return toolErrorDetails("COMMAND_JOURNAL_UNAVAILABLE", "durable command state could not be read or written", "storage", map[string]any{"reason": err.Error()})
}

func (s *Service) durableSessionResult(ctx context.Context, id string, maxBytes int) (Result, error) {
	if s.journal == nil {
		return nil, toolError("SESSION_NOT_FOUND", "session not found", "not_found")
	}
	record, exists, err := s.journal.get(id)
	if err != nil {
		return nil, durableFailure(err)
	}
	if !exists {
		return nil, toolError("SESSION_NOT_FOUND", "session not found", "not_found")
	}
	if execution, scoped := projectExecution(ctx); scoped && !sessionOwnedByExecution(execution, record.Execution) {
		return nil, toolError("SESSION_TARGET_DENIED", "command session does not belong to the current Project Target", "authorization")
	}
	return record.result(maxBytes), nil
}

func (r commandRecord) result(maxBytes int) Result {
	stdout := outputTail(r.Stdout, maxBytes)
	stderr := outputTail(r.Stderr, maxBytes)
	elapsed := time.Since(r.StartedAt)
	if !r.FinishedAt.IsZero() {
		elapsed = r.FinishedAt.Sub(r.StartedAt)
	}
	status := "running"
	if r.State.Terminal() {
		status = "exited"
		if r.TimedOut {
			status = "timeout"
		}
		if r.State == protocol.CommandOutcomeInterrupted {
			status = "killed"
		}
		if r.State == protocol.CommandOutcomeUnknown {
			status = "outcome_unknown"
		}
	}
	result := Result{"session_id": r.ID, "event_id": r.EventID, "status": status, "outcome_state": string(r.State), "stdout": stdout, "stderr": stderr, "elapsed_ms": elapsed.Milliseconds(), "timed_out": r.TimedOut, "workdir": r.Execution.Workdir, "stdout_output_bytes": len(stdout), "stderr_output_bytes": len(stderr), "stdout_total_bytes": r.StdoutTotal, "stderr_total_bytes": r.StderrTotal, "stdout_dropped_bytes": max(0, r.StdoutTotal-int64(len(r.Stdout))), "stderr_dropped_bytes": max(0, r.StderrTotal-int64(len(r.Stderr))), "stdout_omitted_bytes": len(r.Stdout) - len(stdout), "stderr_omitted_bytes": len(r.Stderr) - len(stderr), "stdout_truncated": r.StdoutTotal > int64(len(stdout)), "stderr_truncated": r.StderrTotal > int64(len(stderr))}
	if r.RequestID != "" {
		result["request_id"] = r.RequestID
	}
	if r.State.Terminal() {
		result["command_ok"] = r.State == protocol.CommandOutcomeCompleted && r.ExitCode != nil && *r.ExitCode == 0 && !r.TimedOut
	}
	if r.ExitCode != nil {
		result["exit_code"] = *r.ExitCode
	}
	if r.CommandError != "" {
		result["command_error"] = r.CommandError
	}
	if r.Execution.WorkSessionID != "" {
		result["work_session_id"] = r.Execution.WorkSessionID
		result["target_id"] = r.Execution.TargetID
		result["project_id"] = r.Execution.ProjectID
		result["deployment_id"] = r.Execution.DeploymentID
		result["node_id"] = r.Execution.NodeID
	}
	return result
}

func (s *Service) ReadCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesReadRequest) (protocol.CommandOutcomesReadResult, error) {
	if s.journal == nil {
		return protocol.CommandOutcomesReadResult{}, fmt.Errorf("durable command outcomes unavailable")
	}
	return s.journal.ReadOutcomes(ctx, request)
}
func (s *Service) AckCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesAckRequest) (protocol.CommandOutcomesAckResult, error) {
	if s.journal == nil {
		return protocol.CommandOutcomesAckResult{}, fmt.Errorf("durable command outcomes unavailable")
	}
	return s.journal.AckOutcomes(ctx, request)
}

func validateCommandRequestID(id string) error {
	if len(id) > 128 || id != strings.TrimSpace(id) || strings.ContainsAny(id, "\x00\r\n") {
		return toolError("INVALID_ARGUMENT", "request_id must be a nonblank stable identifier of at most 128 bytes", "validation")
	}
	return nil
}
