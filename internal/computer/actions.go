package computer

import (
	"context"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"math"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	modShift uint64 = 1 << iota
	modControl
	modAlt
	modMeta
	modFn
)

type keyCode struct {
	Code               uint16
	Modifier, Implicit uint64
	Extended, Shift    bool
}
type inputEvent struct {
	Kind   string
	Point  protocol.ComputerPoint
	Button string
	Count  int
	DX, DY int
	Key    keyCode
	Flags  uint64
	Text   []uint16
}
type inputBatch struct {
	Delay  time.Duration
	Events []inputEvent
}
type submission struct {
	Sent, Requested, Accepted int
	Known                     bool
}
type inputDriver interface {
	Check(context.Context, bool) error
	Send([]inputEvent) (submission, error)
}

func actionNames() []string { return []string{"move", "click", "drag", "scroll", "key", "text"} }
func normalizeAction(a protocol.ComputerAction) (protocol.ComputerAction, error) {
	switch a.Kind {
	case "move", "click", "drag", "scroll":
		if a.Point == nil {
			return a, failure(protocol.ErrorComputerInputRejected, "pointer action requires point")
		}
		if a.Kind == "click" || a.Kind == "drag" {
			if a.Button == "" {
				a.Button = "left"
			}
			if a.Button != "left" && a.Button != "right" && a.Button != "middle" {
				return a, failure(protocol.ErrorComputerInputRejected, "unknown mouse button")
			}
		}
		if a.Kind == "click" {
			if a.Count == 0 {
				a.Count = 1
			}
			if a.Count < 1 || a.Count > 3 {
				return a, failure(protocol.ErrorComputerInputRejected, "click count must be 1, 2 or 3")
			}
		}
		if a.Kind == "drag" && (a.To == nil || a.DurationMS < 1 || a.DurationMS > 3000) {
			return a, failure(protocol.ErrorComputerInputRejected, "drag requires destination and duration_ms between 1 and 3000")
		}
		if a.Kind == "scroll" && (a.DeltaX == nil || a.DeltaY == nil) {
			return a, failure(protocol.ErrorComputerInputRejected, "scroll requires both deltas")
		}
	case "key":
		keys, err := normalizeKeys(a.Keys)
		if err != nil {
			return a, err
		}
		a.Keys = keys
	case "text":
		if a.Text == "" {
			return a, failure(protocol.ErrorComputerInputRejected, "text is empty")
		}
	default:
		return a, failure(protocol.ErrorComputerUnsupported, "unknown native action")
	}
	return a, nil
}
func validateActionBounds(a protocol.ComputerAction, w, h int) error {
	for _, p := range []*protocol.ComputerPoint{a.Point, a.To} {
		if p == nil {
			continue
		}
		if math.IsNaN(p.X) || math.IsInf(p.X, 0) || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) || p.X < 0 || p.Y < 0 || p.X >= float64(w) || p.Y >= float64(h) {
			return failure(protocol.ErrorComputerInputRejected, "point is outside the observed image")
		}
	}
	return nil
}
func nativePoint(s Snapshot, p *protocol.ComputerPoint) protocol.ComputerPoint {
	return protocol.ComputerPoint{X: s.Transform[0]*p.X + s.Transform[2]*p.Y + s.Transform[4], Y: s.Transform[1]*p.X + s.Transform[3]*p.Y + s.Transform[5]}
}
func planAction(s Snapshot, a protocol.ComputerAction, resolve func(string) (keyCode, error)) ([]inputBatch, error) {
	var batches []inputBatch
	add := func(delay time.Duration, events ...inputEvent) { batches = append(batches, inputBatch{delay, events}) }
	var p protocol.ComputerPoint
	if a.Point != nil {
		p = nativePoint(s, a.Point)
	}
	switch a.Kind {
	case "move":
		add(0, inputEvent{Kind: "move", Point: p})
	case "click":
		for i := 1; i <= a.Count; i++ {
			delay := time.Duration(0)
			if i > 1 {
				delay = 60 * time.Millisecond
			}
			add(delay, inputEvent{Kind: "move", Point: p}, inputEvent{Kind: "button_down", Point: p, Button: a.Button, Count: i}, inputEvent{Kind: "button_up", Point: p, Button: a.Button, Count: i})
		}
	case "scroll":
		add(0, inputEvent{Kind: "move", Point: p}, inputEvent{Kind: "wheel", Point: p, DX: *a.DeltaX, DY: *a.DeltaY})
	case "drag":
		to := nativePoint(s, a.To)
		add(0, inputEvent{Kind: "move", Point: p}, inputEvent{Kind: "button_down", Point: p, Button: a.Button, Count: 1})
		steps := max(1, (a.DurationMS+15)/16)
		previous := time.Duration(0)
		for i := 1; i <= steps; i++ {
			at := time.Duration(a.DurationMS) * time.Millisecond * time.Duration(i) / time.Duration(steps)
			fraction := float64(i) / float64(steps)
			point := protocol.ComputerPoint{X: p.X + (to.X-p.X)*fraction, Y: p.Y + (to.Y-p.Y)*fraction}
			add(at-previous, inputEvent{Kind: "drag", Point: point, Button: a.Button})
			previous = at
		}
		add(0, inputEvent{Kind: "button_up", Point: to, Button: a.Button, Count: 1})
	case "key":
		var keys []keyCode
		seen := map[uint16]bool{}
		implicit := uint64(0)
		for _, name := range a.Keys {
			k, err := resolve(name)
			if err != nil {
				return nil, err
			}
			implicit |= k.Implicit
			if k.Shift {
				implicit |= modShift
			}
			if !seen[k.Code] {
				keys = append(keys, k)
				seen[k.Code] = true
			}
		}
		for _, mod := range []struct {
			Name string
			Flag uint64
		}{{"shift", modShift}, {"control", modControl}, {"alt", modAlt}, {"meta", modMeta}} {
			if implicit&mod.Flag != 0 {
				k, err := resolve(mod.Name)
				if err != nil {
					return nil, err
				}
				if !seen[k.Code] {
					keys = append(keys, k)
					seen[k.Code] = true
				}
			}
		}
		// 修饰键先按下，释放顺序相反；别名或传参顺序不改变快捷键含义。
		ordered := make([]keyCode, 0, len(keys))
		for _, k := range keys {
			if k.Modifier != 0 {
				ordered = append(ordered, k)
			}
		}
		for _, k := range keys {
			if k.Modifier == 0 {
				ordered = append(ordered, k)
			}
		}
		flags := uint64(0)
		events := []inputEvent{}
		for _, k := range ordered {
			flags |= k.Modifier
			events = append(events, inputEvent{Kind: "key_down", Key: k, Flags: flags})
		}
		for i := len(ordered) - 1; i >= 0; i-- {
			k := ordered[i]
			flags &^= k.Modifier
			events = append(events, inputEvent{Kind: "key_up", Key: k, Flags: flags})
		}
		add(0, events...)
	case "text":
		text := strings.ReplaceAll(strings.ReplaceAll(a.Text, "\r\n", "\n"), "\r", "\n")
		events := []inputEvent{}
		for _, r := range text {
			if r == '\n' || r == '\t' {
				name := "enter"
				if r == '\t' {
					name = "tab"
				}
				k, err := resolve(name)
				if err != nil {
					return nil, err
				}
				events = append(events, inputEvent{Kind: "key_down", Key: k}, inputEvent{Kind: "key_up", Key: k})
			} else {
				events = append(events, inputEvent{Kind: "text", Text: utf16.Encode([]rune{r})})
			}
			if len(events) >= 32 {
				add(0, events...)
				events = nil
			}
		}
		if len(events) > 0 {
			add(0, events...)
		}
	}
	return batches, nil
}
func runInput(ctx context.Context, driver inputDriver, batches []inputBatch) (result protocol.ComputerInputResult, err error) {
	result.Status = "not_submitted"
	held := []inputEvent{}
	sent, requested, accepted := 0, 0, 0
	known := true
	var cursor protocol.ComputerPoint
	apply := func(event inputEvent) {
		if event.Kind == "move" || event.Kind == "drag" || event.Kind == "button_down" || event.Kind == "button_up" {
			cursor = event.Point
		}
		if event.Kind == "button_down" || event.Kind == "key_down" {
			held = append(held, event)
		}
		if event.Kind == "button_up" || event.Kind == "key_up" {
			for i := len(held) - 1; i >= 0; i-- {
				if (event.Kind == "button_up" && held[i].Kind == "button_down" && held[i].Button == event.Button) || (event.Kind == "key_up" && held[i].Kind == "key_down" && held[i].Key.Code == event.Key.Code) {
					held = append(held[:i], held[i+1:]...)
					break
				}
			}
		}
	}
	defer func() {
		// 取消只中断新输入；释放不使用已取消的 context，不释放用户原本按住的键。
		if len(held) > 0 {
			releases := []inputEvent{}
			flags := uint64(0)
			for _, h := range held {
				flags |= h.Key.Modifier
			}
			for i := len(held) - 1; i >= 0; i-- {
				event := held[i]
				if event.Kind == "button_down" {
					event.Kind = "button_up"
					event.Point = cursor
				} else {
					event.Kind = "key_up"
					flags &^= event.Key.Modifier
					event.Flags = flags
				}
				releases = append(releases, event)
			}
			if _, releaseErr := driver.Send(releases); releaseErr != nil {
				err = failure(protocol.ErrorComputerExecutionUnknown, fmt.Sprintf("%v; release held input: %v", err, releaseErr))
			}
		}
		if requested > 0 {
			result.RequestedEvents = &requested
			if known {
				result.AcceptedEvents = &accepted
			}
		}
		switch {
		case err != nil && (sent > 0 || accepted > 0):
			result.Status = "partial"
		case err != nil && requested > 0:
			result.Status = "rejected"
		case err != nil:
			result.Status = "not_submitted"
		case known:
			result.Status = "accepted"
		default:
			result.Status = "submitted"
		}
	}()
	for i, batch := range batches {
		if batch.Delay > 0 {
			timer := time.NewTimer(batch.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return result, ctx.Err()
			case <-timer.C:
			}
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = driver.Check(ctx, i > 0); err != nil {
			return result, err
		}
		report, sendErr := driver.Send(batch.Events)
		requested += report.Requested
		accepted += report.Accepted
		known = known && report.Known
		for _, event := range batch.Events[:min(report.Sent, len(batch.Events))] {
			apply(event)
		}
		sent += report.Sent
		if sendErr != nil {
			return result, sendErr
		}
	}
	return result, nil
}
