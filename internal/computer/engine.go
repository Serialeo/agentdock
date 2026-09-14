package computer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/fs/securepath"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const leaseTTL = 15 * time.Second
const observationTTL = 30 * time.Second

type lease struct {
	Owner   Owner
	Session protocol.ComputerSession
	Until   time.Time
	Stopped bool
}
type evidence struct {
	Owner    Owner
	Metadata protocol.ComputerObservation
	Snapshot Snapshot
	Until    time.Time
}
type record struct {
	Owner  Owner                         `json:"owner"`
	Digest string                        `json:"digest"`
	Result protocol.ComputerActionResult `json:"result"`
}
type Engine struct {
	digestKey    []byte
	mu           sync.Mutex
	backend      Backend
	home, epoch  string
	enabled      bool
	closed       bool
	revoked      []Owner
	session      *lease
	observations map[string]evidence
	records      map[string]record
	sequence     uint64
	busy         bool
	activeCancel context.CancelFunc
}

func NewEngine(home string, backend Backend, enabled bool) (*Engine, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	if err := securepath.EnsurePrivate(home); err != nil {
		return nil, err
	}
	e := &Engine{backend: backend, home: home, epoch: id(), enabled: enabled, observations: map[string]evidence{}, records: map[string]record{}}
	key, keyErr := operationKey(home)
	if keyErr != nil {
		return nil, keyErr
	}
	e.digestKey = key
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(home, entry.Name()))
		if err != nil {
			return nil, err
		}
		var r record
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("read computer outcome %s: %w", entry.Name(), err)
		}
		if r.Result.State == "prepared" || r.Result.State == "executing" {
			r.Result.State = "outcome_unknown"
			r.Result.Effects = "unknown"
			r.Result.Input.Status = "unknown"
			if err = e.save(r); err != nil {
				return nil, err
			}
		}
		e.records[recordKey(r.Owner, r.Result.OperationID)] = r
	}
	return e, nil
}
func recordKey(owner Owner, operation string) string {
	h := sha256.Sum256(raw(struct {
		Owner     Owner
		Operation string
	}{owner, operation}))
	return hex.EncodeToString(h[:])
}
func (e *Engine) save(r record) error {
	name := filepath.Join(e.home, recordKey(r.Owner, r.Result.OperationID)+".json")
	f, err := os.CreateTemp(e.home, ".outcome-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(raw(r)); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = replaceFile(tmp, name); err != nil {
		return err
	}
	return syncDirectory(e.home)
}
func (e *Engine) cancelLocked() {
	if e.activeCancel != nil {
		e.activeCancel()
	}
	if e.session != nil {
		e.session.Stopped = true
	}
}
func (e *Engine) Close() { e.mu.Lock(); defer e.mu.Unlock(); e.closed = true; e.cancelLocked() }
func (e *Engine) Revoke(owner Owner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.revoked = append(e.revoked, owner)
	if e.session != nil && matchesOwner(e.session.Owner, owner) {
		e.cancelLocked()
	}
}
func matchesOwner(a, b Owner) bool {
	return (b.Target == "" || a.Target == b.Target) && (b.WorkSession == "" || a.WorkSession == b.WorkSession) && (b.Deployment == "" || a.Deployment == b.Deployment) && (b.Revision == "" || a.Revision == b.Revision)
}
func (e *Engine) denied(owner Owner) bool {
	for _, rule := range e.revoked {
		if matchesOwner(owner, rule) {
			return true
		}
	}
	return false
}
func (e *Engine) Maintain() {
	// 失去交互桌面即撤销租约，解锁不会恢复原来的输入资格。
	// 原生查询在状态锁外执行，stop 不等待捕获或系统查询。
	status, statusErr := e.backend.Status(context.Background())
	e.mu.Lock()
	defer e.mu.Unlock()
	if statusErr != nil || status.DesktopState != "interactive" {
		e.cancelLocked()
		clear(e.observations)
	}
	if e.session != nil && time.Now().After(e.session.Until) {
		e.cancelLocked()
	}
	// 本地停止文件只能由用户侧删除，MCP acquire/renew 无法解除停止锁存。
	if _, err := os.Stat(filepath.Join(e.home, "STOP")); err == nil {
		e.cancelLocked()
	}
	for key, ev := range e.observations {
		if time.Now().After(ev.Until) {
			delete(e.observations, key)
		}
	}
}
func (e *Engine) stoppedLocally() bool {
	_, err := os.Stat(filepath.Join(e.home, "STOP"))
	return err == nil
}
func (e *Engine) Call(ctx context.Context, req Request) (json.RawMessage, []byte, error) {
	switch req.Tool {
	case "hello":
		if _, err := e.backend.Status(ctx); err != nil {
			return nil, nil, err
		}
		return raw(map[string]any{"wire_version": protocol.ComputerWireVersion}), nil, nil
	case "revoke":
		e.Revoke(req.Owner)
		return raw(map[string]any{"revoked": true}), nil, nil
	case protocol.ToolComputerStatus:
		var a struct {
			Operation string `json:"operation_id"`
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return nil, nil, err
		}
		if a.Operation != "" {
			e.mu.Lock()
			defer e.mu.Unlock()
			r, ok := e.records[recordKey(req.Owner, a.Operation)]
			if !ok {
				return nil, nil, failure(protocol.ErrorComputerEvidenceExpired, "operation not found for this owner")
			}
			r.Result.ReplayedResult = true
			return raw(r.Result), nil, nil
		}
		status, err := e.backend.Status(ctx)
		if err != nil {
			return nil, nil, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		status.LocalControlEnabled = e.enabled
		status.StopLatched = e.stoppedLocally()
		return raw(status), nil, nil
	case protocol.ToolComputerSession:
		return e.sessionCall(ctx, req)
	case protocol.ToolComputerStop:
		return e.stop(req)
	case protocol.ToolComputerObserve:
		return e.observe(ctx, req)
	case protocol.ToolComputerAct:
		return e.act(ctx, req)
	default:
		return nil, nil, failure(protocol.ErrorComputerUnsupported, "unknown computer method")
	}
}
func (e *Engine) sessionCall(ctx context.Context, req Request) (json.RawMessage, []byte, error) {
	var a struct {
		Action  string `json:"action"`
		Desktop string `json:"desktop_id"`
		Session string `json:"session_id"`
	}
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return nil, nil, err
	}
	if a.Action == "acquire" {
		st, err := e.backend.Status(ctx)
		if err != nil {
			return nil, nil, err
		}
		if a.Desktop != st.DesktopID || st.DesktopState != "interactive" {
			return nil, nil, failure(protocol.ErrorComputerDesktopUnavailable, "desktop is not available")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, nil, failure(protocol.ErrorComputerHelperOffline, "helper is closing")
	}
	if e.denied(req.Owner) {
		return nil, nil, failure(protocol.ErrorComputerSessionRevoked, "owner authorization was revoked")
	}
	if !e.enabled || e.stoppedLocally() {
		return nil, nil, failure(protocol.ErrorComputerPermissionDenied, "enable local computer control and clear local stop before acquiring a session")
	}
	state := ""
	switch a.Action {
	case "acquire":
		if e.busy || (e.session != nil && !e.session.Stopped && time.Now().Before(e.session.Until)) {
			return nil, nil, failure(protocol.ErrorComputerDesktopBusy, "desktop already has a control session")
		}
		e.session = &lease{Owner: req.Owner, Until: time.Now().Add(leaseTTL), Session: protocol.ComputerSession{SessionID: id(), DesktopID: a.Desktop, DesktopEpoch: e.epoch}}
		state = "acquired"
	case "renew", "release":
		if err := e.validLease(req.Owner, a.Session); err != nil {
			return nil, nil, err
		}
		if a.Action == "release" {
			e.cancelLocked()
			state = "released"
		} else {
			e.session.Until = time.Now().Add(leaseTTL)
			state = "renewed"
		}
	default:
		return nil, nil, failure(protocol.ErrorComputerInputRejected, "invalid session action")
	}
	e.session.Session.ExpiresAt = e.session.Until.UTC().Format(time.RFC3339Nano)
	return raw(map[string]any{"state": state, "session": e.session.Session}), nil, nil
}
func (e *Engine) validLease(owner Owner, session string) error {
	if e.denied(owner) || e.session == nil || e.session.Owner != owner || e.session.Session.SessionID != session || e.session.Stopped || time.Now().After(e.session.Until) {
		return failure(protocol.ErrorComputerSessionRevoked, "session is stopped, expired or belongs to another owner")
	}
	return nil
}
func (e *Engine) stop(req Request) (json.RawMessage, []byte, error) {
	var a struct {
		Session   string `json:"session_id"`
		Operation string `json:"operation_id"`
	}
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return nil, nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.Owner != req.Owner || e.session.Session.SessionID != a.Session {
		return nil, nil, failure(protocol.ErrorComputerSessionRevoked, "session not found for this owner")
	}
	state := "stopped"
	if e.session.Stopped {
		state = "already_stopped"
	}
	e.cancelLocked()
	if e.busy {
		state = "outcome_unknown"
	}
	result := map[string]any{"session_id": a.Session, "state": state}
	if a.Operation != "" {
		result["operation_id"] = a.Operation
	}
	return raw(result), nil, nil
}
func (e *Engine) begin(ctx context.Context) (context.Context, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, nil, failure(protocol.ErrorComputerHelperOffline, "helper is closing")
	}
	if e.busy {
		return nil, nil, failure(protocol.ErrorComputerDesktopBusy, "another desktop operation is in progress")
	}
	e.busy = true
	ctx, cancel := context.WithCancel(ctx)
	e.activeCancel = cancel
	return ctx, func() { cancel(); e.mu.Lock(); e.busy = false; e.activeCancel = nil; e.mu.Unlock() }, nil
}
func (e *Engine) capture(ctx context.Context, owner Owner, desktop, display, window string, size int) (protocol.ComputerObservation, []byte, error) {
	e.mu.Lock()
	denied := e.denied(owner)
	enabled := e.enabled
	e.mu.Unlock()
	if denied {
		return protocol.ComputerObservation{}, nil, failure(protocol.ErrorComputerSessionRevoked, "owner authorization was revoked")
	}
	if !enabled {
		return protocol.ComputerObservation{}, nil, failure(protocol.ErrorComputerPermissionDenied, "local computer access is disabled")
	}
	st, err := e.backend.Status(ctx)
	if err != nil {
		return protocol.ComputerObservation{}, nil, err
	}
	if desktop != st.DesktopID || st.DesktopState != "interactive" {
		return protocol.ComputerObservation{}, nil, failure(protocol.ErrorComputerDesktopUnavailable, "desktop is unavailable")
	}
	if size <= 0 {
		size = 1600
	}
	if size > 2560 {
		size = 2560
	} // 调整传输偏好，不因用户请求大图拒绝截图。
	shot, err := e.backend.Capture(ctx, display, window, size)
	if err != nil {
		return protocol.ComputerObservation{}, nil, err
	}
	if err = ctx.Err(); err != nil {
		return protocol.ComputerObservation{}, nil, err
	}
	if len(shot.PNG) == 0 || shot.Width <= 0 || shot.Height <= 0 {
		return protocol.ComputerObservation{}, nil, failure(protocol.ErrorComputerInputRejected, "native capture returned no image")
	}
	shot, err = fitImage(ctx, shot, imageBudget)
	if err != nil {
		return protocol.ComputerObservation{}, nil, err
	}
	h := sha256.Sum256(shot.PNG)
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.denied(owner) || ctx.Err() != nil {
		return protocol.ComputerObservation{}, nil, failure(protocol.ErrorComputerSessionRevoked, "observation cancelled by authorization change")
	}
	meta := protocol.ComputerObservation{ObservationID: id(), DesktopID: desktop, DisplayID: display, WindowID: shot.Target, DesktopEpoch: e.epoch, GeometryRevision: shot.Geometry, InputSequence: e.sequence, CapturedAt: now.UTC().Format(time.RFC3339Nano), ExpiresAt: now.Add(observationTTL).UTC().Format(time.RFC3339Nano), Width: shot.Width, Height: shot.Height, SHA256: hex.EncodeToString(h[:]), ImageToNative: shot.Transform}
	// 截图仅随本次响应传输；日志只存元数据，绝不将桌面图放进公共 Artifact。
	png := shot.PNG
	shot.PNG = nil
	e.observations[meta.ObservationID] = evidence{owner, meta, shot, now.Add(observationTTL)}
	return meta, png, nil
}
func (e *Engine) observe(ctx context.Context, req Request) (json.RawMessage, []byte, error) {
	var a struct {
		Desktop string `json:"desktop_id"`
		Display string `json:"display_id"`
		Window  string `json:"window_id"`
		Size    int    `json:"max_size"`
		After   string `json:"after_operation_id"`
	}
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return nil, nil, err
	}
	ctx, end, err := e.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer end()
	if a.After != "" {
		e.mu.Lock()
		r, ok := e.records[recordKey(req.Owner, a.After)]
		e.mu.Unlock()
		if !ok || r.Result.State == "prepared" || r.Result.State == "executing" {
			return nil, nil, failure(protocol.ErrorComputerEvidenceExpired, "no completed operation for this owner")
		}
	}
	meta, png, err := e.capture(ctx, req.Owner, a.Desktop, a.Display, a.Window, a.Size)
	if err != nil {
		return nil, nil, err
	}
	return raw(meta), png, nil
}
func (e *Engine) act(ctx context.Context, req Request) (json.RawMessage, []byte, error) {
	var a struct {
		Session     string                  `json:"session_id"`
		Operation   string                  `json:"operation_id"`
		Observation string                  `json:"observation_id"`
		Action      protocol.ComputerAction `json:"action"`
		Timeout     int                     `json:"timeout_ms"`
	}
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return nil, nil, err
	}
	var validationErr error
	a.Action, validationErr = normalizeAction(a.Action)
	if validationErr != nil {
		return nil, nil, validationErr
	}
	if a.Operation == "" {
		return nil, nil, failure(protocol.ErrorComputerInputRejected, "operation_id is required")
	}
	h := hmac.New(sha256.New, e.digestKey)
	h.Write(raw(struct {
		Session, Observation string
		Action               protocol.ComputerAction
	}{a.Session, a.Observation, a.Action}))
	digest := hex.EncodeToString(h.Sum(nil))
	key := recordKey(req.Owner, a.Operation)
	e.mu.Lock()
	prior, exists := e.records[key]
	e.mu.Unlock()
	if exists {
		if prior.Digest != digest {
			return nil, nil, failure(protocol.ErrorComputerOperationConflict, "operation_id was already used for different input")
		}
		prior.Result.ReplayedResult = true
		return raw(prior.Result), nil, nil
	}
	if a.Timeout <= 0 || a.Timeout > 15000 {
		a.Timeout = 15000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Millisecond)
	defer cancel()
	ctx, end, err := e.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer end()
	e.mu.Lock()
	// begin 后再次查重，避免两个并发请求都越过首次查询。
	if prior, ok := e.records[key]; ok {
		e.mu.Unlock()
		if prior.Digest != digest {
			return nil, nil, failure(protocol.ErrorComputerOperationConflict, "operation_id conflict")
		}
		prior.Result.ReplayedResult = true
		return raw(prior.Result), nil, nil
	}
	if err = e.validLease(req.Owner, a.Session); err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	ev, ok := e.observations[a.Observation]
	if !ok || ev.Owner != req.Owner || time.Now().After(ev.Until) || ev.Metadata.DesktopEpoch != e.epoch || ev.Metadata.InputSequence != e.sequence {
		e.mu.Unlock()
		return nil, nil, failure(protocol.ErrorComputerObservationStale, "capture a fresh observation for this owner before input")
	}
	if boundsErr := validateActionBounds(a.Action, ev.Metadata.Width, ev.Metadata.Height); boundsErr != nil {
		e.mu.Unlock()
		return nil, nil, boundsErr
	}
	if e.stoppedLocally() {
		e.cancelLocked()
		e.mu.Unlock()
		return nil, nil, failure(protocol.ErrorComputerSessionRevoked, "local stop is latched")
	}
	r := record{Owner: req.Owner, Digest: digest, Result: protocol.ComputerActionResult{OperationID: a.Operation, State: "executing", Effects: "none", Input: protocol.ComputerInputResult{Status: "not_submitted"}, ObservationStatus: "not_requested", Verification: "not_checked"}}
	e.records[key] = r
	until := e.session.Until
	e.mu.Unlock()
	// 持久化不占用状态锁；停止不应等待 fsync。写盘返回后再次确认租约。
	if err = e.save(r); err != nil {
		e.mu.Lock()
		delete(e.records, key)
		e.mu.Unlock()
		return nil, nil, fmt.Errorf("persist input intent: %w", err)
	}
	e.mu.Lock()
	actionErr := e.validLease(req.Owner, a.Session)
	if actionErr == nil {
		actionErr = ctx.Err()
	}
	if actionErr == nil && e.stoppedLocally() {
		e.cancelLocked()
		actionErr = failure(protocol.ErrorComputerSessionRevoked, "local stop is latched")
	}
	e.sequence++
	e.mu.Unlock()
	input := protocol.ComputerInputResult{Status: "not_submitted"}
	inputCtx, inputCancel := context.WithDeadline(ctx, until)
	defer inputCancel()
	if actionErr == nil {
		input, actionErr = e.backend.Act(inputCtx, ev.Snapshot, a.Action)
	}
	r.Result.Input = input
	r.Result.State = "finished"
	switch input.Status {
	case "accepted":
		r.Result.Effects = "applied"
	case "submitted", "unknown":
		r.Result.Effects = "unknown"
	case "partial":
		r.Result.Effects = "partial"
	default:
		r.Result.Effects = "none"
	}
	if actionErr != nil {
		r.Result.ErrorCode = remoteError(actionErr).Code
		r.Result.ErrorMessage = actionErr.Error()
		if errors.Is(actionErr, context.Canceled) || errors.Is(actionErr, context.DeadlineExceeded) {
			r.Result.State = "cancelled"
		}
		if input.Status == "unknown" || input.Status == "partial" {
			r.Result.State = "outcome_unknown"
		}
	}
	// 先落盘输入结果，后取新截图。截图失败不得导致原点击重试。
	e.mu.Lock()
	e.records[key] = r
	e.mu.Unlock()
	err = e.save(r)
	if err != nil {
		return nil, nil, failure(protocol.ErrorComputerExecutionUnknown, "input returned but durable outcome could not be saved; do not repeat this operation")
	}
	if actionErr != nil && input.Status == "not_submitted" {
		return raw(r.Result), nil, nil
	}
	r.Result.ObservationStatus = "unavailable"
	meta, png, captureErr := e.capture(ctx, req.Owner, ev.Metadata.DesktopID, ev.Metadata.DisplayID, "", 1600)
	if captureErr == nil {
		r.Result.Observation = &meta
		r.Result.ObservationStatus = "captured"
	}
	e.mu.Lock()
	e.records[key] = r
	e.mu.Unlock()
	err = e.save(r)
	if err != nil {
		return nil, nil, failure(protocol.ErrorComputerExecutionUnknown, "could not persist final observation metadata; query operation status")
	}
	return raw(r.Result), png, nil
}

func (e *Engine) SetEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = enabled
	if !enabled || e.stoppedLocally() {
		e.cancelLocked()
	}
}
