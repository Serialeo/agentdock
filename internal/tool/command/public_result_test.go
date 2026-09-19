package command

import (
	"reflect"
	"testing"
)

func TestCompactExecResultDropsInternalAndRedundantFields(t *testing.T) {
	raw := Result{
		"command_ok":         true,
		"deployment_id":      "deployment-a",
		"elapsed_ms":         55,
		"event_id":           "event-a",
		"exit_code":          0,
		"node_id":            "node-a",
		"outcome_state":      "completed",
		"project_id":         "project-a",
		"sandbox":            map[string]any{"enabled": false},
		"session_id":         "session-a",
		"status":             "exited",
		"stderr":             "",
		"stderr_total_bytes": 0,
		"stderr_truncated":   false,
		"stdout":             "result",
		"stdout_total_bytes": 6,
		"stdout_truncated":   false,
		"target_id":          "target-a",
		"timed_out":          false,
		"work_session_id":    "ws-a",
		"workdir":            "/tmp/project",
	}
	want := Result{"exit_code": 0, "stdout": "result"}
	if got := compactExecResult(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("compactExecResult() = %#v, want %#v", got, want)
	}
}

func TestCompactExecResultKeepsOnlyAsyncContinuationFields(t *testing.T) {
	raw := Result{
		"session_id":       "session-a",
		"status":           "running",
		"session_reason":   "foreground_threshold_exceeded",
		"observe_after_ms": 1000,
		"elapsed_ms":       5001,
		"event_id":         "event-a",
		"workdir":          "/tmp/project",
		"stdout":           "",
		"stderr":           "",
	}
	want := Result{
		"session_id": "session-a",
		"status":     "running",
	}
	if got := compactExecResult(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("compactExecResult() = %#v, want %#v", got, want)
	}
}

func TestCompactCommandOutputReportsTruncationOnlyWhenTrue(t *testing.T) {
	raw := Result{
		"stdout":               "tail",
		"stdout_truncated":     true,
		"stdout_total_bytes":   120000,
		"stdout_output_bytes":  4,
		"stdout_omitted_bytes": 119996,
		"stderr":               "",
		"stderr_truncated":     false,
		"stderr_total_bytes":   0,
		"timed_out":            false,
	}
	want := Result{
		"stdout":             "tail",
		"stdout_truncated":   true,
		"stdout_total_bytes": 120000,
	}
	if got := compactCommandOutput(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("compactCommandOutput() = %#v, want %#v", got, want)
	}
}

func TestCompactCommandOutputUsesConditionalFailureFields(t *testing.T) {
	timeout := Result{
		"timed_out":     true,
		"exit_code":     -1,
		"command_error": "signal: killed",
	}
	wantTimeout := Result{"timed_out": true}
	if got := compactCommandOutput(timeout); !reflect.DeepEqual(got, wantTimeout) {
		t.Fatalf("timeout projection = %#v, want %#v", got, wantTimeout)
	}

	exited := Result{
		"exit_code":     7,
		"stderr":        "failed",
		"command_error": "exit status 7",
	}
	wantExited := Result{"exit_code": 7, "stderr": "failed"}
	if got := compactCommandOutput(exited); !reflect.DeepEqual(got, wantExited) {
		t.Fatalf("exit failure projection = %#v, want %#v", got, wantExited)
	}

	preStart := Result{"command_error": "process never started"}
	if got := compactCommandOutput(preStart); !reflect.DeepEqual(got, preStart) {
		t.Fatalf("pre-start failure projection = %#v, want %#v", got, preStart)
	}
}

func TestCompactSessionCollectionDropsCountAndExecutionIdentity(t *testing.T) {
	raw := Result{
		"count": 1,
		"sessions": []map[string]any{{
			"session_id":      "session-a",
			"status":          "running",
			"elapsed_ms":      123,
			"target_id":       "target-a",
			"deployment_id":   "deployment-a",
			"work_session_id": "ws-a",
			"workdir":         "/tmp/project",
		}},
	}
	want := Result{"sessions": []map[string]any{{"session_id": "session-a", "status": "running"}}}
	if got := compactSessionCollection(raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("compactSessionCollection() = %#v, want %#v", got, want)
	}
}

func TestCompactExecResultPreservesUnknownOutcomeAndSignal(t *testing.T) {
	for _, status := range []string{"starting", "outcome_unknown", "interrupted"} {
		result := compactExecResult(Result{"status": status, "session_id": "recoverable"})
		if result["session_id"] != "recoverable" || result["status"] != status {
			t.Fatalf("lost recovery handle: %#v", result)
		}
	}
	result := compactExecResult(Result{"exit_code": -1, "status": "exited", "command_error": "signal: segmentation fault"})
	if result["command_error"] != "signal: segmentation fault" {
		t.Fatal("signal diagnosis lost")
	}
}
