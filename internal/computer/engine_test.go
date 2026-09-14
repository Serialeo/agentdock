package computer

import (
	"context"
	"encoding/json"
	"errors"
	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/Serialeo/agentdock-protocol/mcpcontract"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBackend struct {
	clicks  atomic.Int32
	capture func(context.Context) (Snapshot, error)
	click   func(context.Context) (protocol.ComputerInputResult, error)
}

func (f *fakeBackend) Status(context.Context) (protocol.ComputerBackendStatus, error) {
	return protocol.ComputerBackendStatus{BackendState: "ready", DesktopState: "interactive", DesktopID: "desktop", Actions: actionNames(), Permissions: protocol.ComputerNativePermissions{Observe: "granted", Control: "granted"}}, nil
}
func (f *fakeBackend) Capture(ctx context.Context, display, window string, size int) (Snapshot, error) {
	if f.capture != nil {
		return f.capture(ctx)
	}
	return Snapshot{PNG: []byte("private screenshot"), Width: 100, Height: 80, Target: "external-app", Display: display, Geometry: "geometry", Transform: [6]float64{2, 0, 0, 2, -100, 0}}, nil
}
func (f *fakeBackend) Act(ctx context.Context, s Snapshot, action protocol.ComputerAction) (protocol.ComputerInputResult, error) {
	f.clicks.Add(1)
	if f.click != nil {
		return f.click(ctx)
	}
	return protocol.ComputerInputResult{Status: "accepted"}, nil
}

var testOwner = Owner{WorkSession: "work", Target: "target", Deployment: "deployment", Revision: "rev1"}

func call(t *testing.T, e *Engine, tool string, args any) (map[string]any, []byte, error) {
	t.Helper()
	b, png, err := e.Call(context.Background(), Request{Tool: tool, Owner: testOwner, Args: raw(args)})
	var result map[string]any
	if err == nil {
		if err = json.Unmarshal(b, &result); err != nil {
			t.Fatal(err)
		}
		schema, _ := mcpcontract.ComputerOutputSchema(tool)
		validator, compileErr := toolcontract.CompileInputSchema(schema)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		if validationErr := validator.Validate(result); validationErr != nil {
			t.Fatalf("%s output: %v: %s", tool, validationErr, b)
		}
	}
	return result, png, err
}
func setup(t *testing.T, f *fakeBackend) (*Engine, string, string) {
	t.Helper()
	e, err := NewEngine(t.TempDir(), f, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	r, _, err := call(t, e, protocol.ToolComputerSession, map[string]any{"action": "acquire", "desktop_id": "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	session := r["session"].(map[string]any)["session_id"].(string)
	r, _, err = call(t, e, protocol.ToolComputerObserve, map[string]any{"desktop_id": "desktop", "display_id": "display"})
	if err != nil {
		t.Fatal(err)
	}
	return e, session, r["observation_id"].(string)
}
func action(session, observation, operation string) map[string]any {
	return map[string]any{"session_id": session, "observation_id": observation, "operation_id": operation, "action": map[string]any{"kind": "click", "point": map[string]any{"x": 20, "y": 30}}}
}
func TestOperationIsNeverReplayedAfterResponseLossOrRestart(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	args := action(s, o, "operation/用户 #1")
	first, png, err := call(t, e, protocol.ToolComputerAct, args)
	if err != nil {
		t.Fatal(err)
	}
	if first["effects"] != "applied" || len(png) == 0 {
		t.Fatalf("missing result/evidence: %v", first)
	}
	second, png, err := call(t, e, protocol.ToolComputerAct, args)
	if err != nil || second["replayed_result"] != true || len(png) != 0 || f.clicks.Load() != 1 {
		t.Fatalf("input replayed: %v %v %d", second, err, f.clicks.Load())
	}
	restarted, err := NewEngine(e.home, f, true)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recovered, _, err := call(t, restarted, protocol.ToolComputerAct, args)
	if err != nil || recovered["replayed_result"] != true || f.clicks.Load() != 1 {
		t.Fatalf("restart replayed input: %v %v", recovered, err)
	}
	args["action"] = map[string]any{"kind": "click", "point": map[string]any{"x": 21, "y": 30}}
	if _, _, err = call(t, restarted, protocol.ToolComputerAct, args); remoteError(err).Code != protocol.ErrorComputerOperationConflict {
		t.Fatalf("conflict: %v", err)
	}
	files, err := os.ReadDir(e.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		b, _ := os.ReadFile(filepath.Join(e.home, file.Name()))
		if string(b) == "private screenshot" {
			t.Fatal("screenshot persisted in journal")
		}
	}
}
func TestStopDoesNotWaitForCaptureAndNeverResumes(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	started := make(chan struct{})
	f.click = func(ctx context.Context) (protocol.ComputerInputResult, error) {
		close(started)
		<-ctx.Done()
		return protocol.ComputerInputResult{Status: "not_submitted"}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, _, err := call(t, e, protocol.ToolComputerAct, action(s, o, "cancelled")); done <- err }()
	<-started
	before := time.Now()
	r, _, err := call(t, e, protocol.ToolComputerStop, map[string]any{"session_id": s})
	if err != nil || time.Since(before) > time.Second || r["state"] != "outcome_unknown" {
		t.Fatalf("stop blocked: %v %v", r, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("input did not cancel")
	}
	if _, _, err = call(t, e, protocol.ToolComputerAct, action(s, o, "new")); err == nil {
		t.Fatal("stopped lease resumed")
	}
	if f.clicks.Load() != 1 {
		t.Fatalf("extra native call: %d", f.clicks.Load())
	}
}
func TestPostInputCaptureFailurePreservesOutcome(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	f.capture = func(context.Context) (Snapshot, error) { return Snapshot{}, errors.New("capture offline") }
	r, _, err := call(t, e, protocol.ToolComputerAct, action(s, o, "clicked"))
	if err != nil || r["effects"] != "applied" || r["observation_status"] != "unavailable" {
		t.Fatalf("%v %v", r, err)
	}
	if _, _, err = call(t, e, protocol.ToolComputerAct, action(s, o, "clicked")); err != nil || f.clicks.Load() != 1 {
		t.Fatal("capture failure replayed click")
	}
}
func TestObservationAndHistoryAreOwnerBound(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	wrong := testOwner
	wrong.WorkSession = "other"
	if _, _, err := e.Call(context.Background(), Request{Owner: wrong, Tool: protocol.ToolComputerAct, Args: raw(action(s, o, "stolen"))}); err == nil {
		t.Fatal("foreign owner used session")
	}
	_, _, err := call(t, e, protocol.ToolComputerAct, action(s, o, "saved"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.Call(context.Background(), Request{Owner: wrong, Tool: protocol.ToolComputerStatus, Args: raw(map[string]any{"operation_id": "saved"})}); err == nil {
		t.Fatal("foreign owner read history")
	}
	e.Revoke(Owner{Target: testOwner.Target})
	if _, _, err = call(t, e, protocol.ToolComputerStatus, map[string]any{"operation_id": "saved"}); err != nil {
		t.Fatal("owner lost historical result after revoke")
	}
	if _, _, err = call(t, e, protocol.ToolComputerSession, map[string]any{"action": "acquire", "desktop_id": "desktop"}); err == nil {
		t.Fatal("revoked owner acquired a new lease")
	}
}
func TestLeaseExpiryAndBoundsRejectBeforeNativeInput(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	args := action(s, o, "outside")
	args["action"] = map[string]any{"kind": "click", "point": map[string]any{"x": 100, "y": 0}}
	if _, _, err := call(t, e, protocol.ToolComputerAct, args); err == nil {
		t.Fatal("right boundary accepted")
	}
	e.mu.Lock()
	e.session.Until = time.Now().Add(-time.Second)
	e.mu.Unlock()
	e.Maintain()
	if _, _, err := call(t, e, protocol.ToolComputerAct, action(s, o, "expired")); err == nil {
		t.Fatal("expired lease accepted")
	}
	if f.clicks.Load() != 0 {
		t.Fatal("native input reached")
	}
}
func TestCrashWithExecutingRecordIsUnknownAndNeverReissued(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	args := action(s, o, "crash")
	_, _, err := call(t, e, protocol.ToolComputerAct, args)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	key := recordKey(testOwner, "crash")
	r := e.records[key]
	r.Result.State = "executing"
	if err = e.save(r); err != nil {
		t.Fatal(err)
	}
	e.mu.Unlock()
	restored, err := NewEngine(e.home, f, true)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, _, err := call(t, restored, protocol.ToolComputerAct, args)
	if err != nil || got["state"] != "outcome_unknown" || got["effects"] != "unknown" || f.clicks.Load() != 1 {
		t.Fatalf("%v %v", got, err)
	}
}
func TestNativeRefusalExplainsWhyAndIsRemembered(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	f.click = func(context.Context) (protocol.ComputerInputResult, error) {
		return protocol.ComputerInputResult{Status: "not_submitted"}, failure(protocol.ErrorComputerObservationStale, "target focus changed")
	}
	got, _, err := call(t, e, protocol.ToolComputerAct, action(s, o, "refused"))
	if err != nil || got["error_code"] != protocol.ErrorComputerObservationStale || got["effects"] != "none" {
		t.Fatalf("%v %v", got, err)
	}
}

type lockableBackend struct {
	fakeBackend
	locked atomic.Bool
}

func (b *lockableBackend) Status(ctx context.Context) (protocol.ComputerBackendStatus, error) {
	s, e := b.fakeBackend.Status(ctx)
	if b.locked.Load() {
		s.DesktopState = "locked"
	}
	return s, e
}
func TestUnlockDoesNotRestoreOldLeaseOrObservation(t *testing.T) {
	f := &lockableBackend{}
	e, err := NewEngine(t.TempDir(), f, true)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	got, _, err := call(t, e, protocol.ToolComputerSession, map[string]any{"action": "acquire", "desktop_id": "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	session := got["session"].(map[string]any)["session_id"].(string)
	got, _, err = call(t, e, protocol.ToolComputerObserve, map[string]any{"desktop_id": "desktop", "display_id": "display"})
	if err != nil {
		t.Fatal(err)
	}
	observation := got["observation_id"].(string)
	f.locked.Store(true)
	e.Maintain()
	f.locked.Store(false)
	e.Maintain()
	if _, _, err = call(t, e, protocol.ToolComputerAct, action(session, observation, "after-unlock")); err == nil {
		t.Fatal("unlock restored old input authorization")
	}
	if f.clicks.Load() != 0 {
		t.Fatal("input submitted after unlock")
	}
}
