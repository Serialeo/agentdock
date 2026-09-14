package computer

import (
	"context"
	"errors"
	protocol "github.com/Serialeo/agentdock-protocol"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

type recordingDriver struct {
	events []inputEvent
	check  func(bool) error
	send   func([]inputEvent) (submission, error)
}

func (d *recordingDriver) Check(ctx context.Context, continuing bool) error {
	if d.check != nil {
		return d.check(continuing)
	}
	return ctx.Err()
}
func (d *recordingDriver) Send(events []inputEvent) (submission, error) {
	d.events = append(d.events, events...)
	if d.send != nil {
		return d.send(events)
	}
	return submission{Sent: len(events), Requested: len(events), Accepted: len(events), Known: true}, nil
}
func planForTest(t *testing.T, a protocol.ComputerAction) []inputBatch {
	t.Helper()
	a, err := normalizeAction(a)
	if err != nil {
		t.Fatal(err)
	}
	batches, err := planAction(Snapshot{Width: 100, Height: 80, Transform: [6]float64{2, 0, 0, 2, -100, 10}}, a, macKey)
	if err != nil {
		t.Fatal(err)
	}
	return batches
}
func TestDragCancellationReleasesAtLastSubmittedPosition(t *testing.T) {
	plan := planForTest(t, protocol.ComputerAction{Kind: "drag", Point: &protocol.ComputerPoint{X: 10, Y: 10}, To: &protocol.ComputerPoint{X: 80, Y: 60}, DurationMS: 500})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver := &recordingDriver{}
	driver.send = func(events []inputEvent) (submission, error) {
		if events[len(events)-1].Kind == "drag" {
			cancel()
		}
		return submission{Sent: len(events), Requested: len(events), Accepted: len(events), Known: true}, nil
	}
	result, err := runInput(ctx, driver, plan)
	if !errors.Is(err, context.Canceled) || result.Status != "partial" {
		t.Fatalf("%+v %v", result, err)
	}
	events := driver.events
	last := events[len(events)-1]
	prior := events[len(events)-2]
	if last.Kind != "button_up" || last.Point != prior.Point || last.Point == (protocol.ComputerPoint{X: 60, Y: 130}) {
		t.Fatalf("cancel jumped to destination or left button down: %#v", events)
	}
	for i, event := range events {
		if event.Kind == "button_up" && i != len(events)-1 {
			t.Fatal("input resumed after release")
		}
	}
}
func TestDragPlanMovesWindowWithoutRecheckingInitialRectangle(t *testing.T) {
	plan := planForTest(t, protocol.ComputerAction{Kind: "drag", Point: &protocol.ComputerPoint{X: 10, Y: 10}, To: &protocol.ComputerPoint{X: 80, Y: 60}, DurationMS: 32})
	checks := []bool{}
	driver := &recordingDriver{check: func(continuing bool) error { checks = append(checks, continuing); return nil }}
	result, err := runInput(context.Background(), driver, plan)
	if err != nil || result.Status != "accepted" {
		t.Fatal(result, err)
	}
	if checks[0] || !checks[1] || !checks[len(checks)-1] {
		t.Fatal("drag did not distinguish initial geometry from continuation")
	}
	last := driver.events[len(driver.events)-1]
	if last.Kind != "button_up" || last.Point != (protocol.ComputerPoint{X: 60, Y: 130}) {
		t.Fatal("wrong final native drag position")
	}
}
func TestShortcutAliasesAndReverseRelease(t *testing.T) {
	for _, keys := range [][]string{{"⌘", "Shift", "s"}, {"Command+Shift+S"}, {"S", "cmd", "shift", "cmd"}} {
		plan := planForTest(t, protocol.ComputerAction{Kind: "key", Keys: keys})
		events := plan[0].Events
		if len(events) != 6 || events[0].Kind != "key_down" || events[0].Key.Modifier == 0 || events[2].Key.Code != 1 || events[2].Flags != modMeta|modShift {
			t.Fatalf("wrong chord for %v: %#v", keys, events)
		}
		for i := 0; i < 3; i++ {
			if events[5-i].Kind != "key_up" || events[5-i].Key.Code != events[i].Key.Code {
				t.Fatal("shortcut released in wrong order")
			}
		}
		if events[5].Flags != 0 {
			t.Fatal("modifier flags stuck")
		}
	}
	keys, err := normalizeKeys([]string{"CTRL", "Arrow-Left"})
	if err != nil || !reflect.DeepEqual(keys, []string{"control", "left"}) {
		t.Fatal(keys, err)
	}
}
func TestPartialChordReleasesOnlyAcceptedKeys(t *testing.T) {
	plan := planForTest(t, protocol.ComputerAction{Kind: "key", Keys: []string{"cmd", "shift", "s"}})
	driver := &recordingDriver{}
	first := true
	driver.send = func(events []inputEvent) (submission, error) {
		if first {
			first = false
			return submission{Sent: 2, Requested: len(events), Accepted: 2, Known: true}, errors.New("native partial")
		}
		return submission{Sent: len(events), Requested: len(events), Accepted: len(events), Known: true}, nil
	}
	result, err := runInput(context.Background(), driver, plan)
	if err == nil || result.Status != "partial" {
		t.Fatal(result, err)
	}
	release := driver.events[len(plan[0].Events):]
	if len(release) != 2 || release[0].Key.Code != 56 || release[1].Key.Code != 55 || release[1].Flags != 0 {
		t.Fatalf("wrong accepted-prefix cleanup: %#v", release)
	}
}
func TestUnicodeTextAndLineBreaksUsePairedBatches(t *testing.T) {
	input := "中文🙂\r\n\t" + strings.Repeat("hello", 20)
	plan := planForTest(t, protocol.ComputerAction{Kind: "text", Text: input})
	var runes []rune
	enters, tabs := 0, 0
	for _, batch := range plan {
		if len(batch.Events) > 33 {
			t.Fatal("text cancellation batch too large")
		}
		for i, event := range batch.Events {
			switch event.Kind {
			case "text":
				runes = append(runes, utf16.Decode(event.Text)...)
			case "key_down":
				if i+1 >= len(batch.Events) || batch.Events[i+1].Kind != "key_up" {
					t.Fatal("newline/tab down split across batches")
				}
				if event.Key.Code == 36 {
					enters++
				} else if event.Key.Code == 48 {
					tabs++
				}
			}
		}
	}
	if string(runes) != "中文🙂"+strings.Repeat("hello", 20) || enters != 1 || tabs != 1 {
		t.Fatal("Unicode or CRLF changed")
	}
}
func TestMultiClickScrollMoveAndBounds(t *testing.T) {
	for count := 1; count <= 3; count++ {
		plan := planForTest(t, protocol.ComputerAction{Kind: "click", Count: count, Point: &protocol.ComputerPoint{X: 2, Y: 3}})
		if len(plan) != count {
			t.Fatal("lost multi-click")
		}
		for i, batch := range plan {
			if batch.Events[1].Count != i+1 || batch.Events[2].Kind != "button_up" {
				t.Fatal("click state or pairing wrong")
			}
		}
	}
	dx, dy := 120, -240
	plan := planForTest(t, protocol.ComputerAction{Kind: "scroll", Point: &protocol.ComputerPoint{}, DeltaX: &dx, DeltaY: &dy})
	if plan[0].Events[1].DX != dx || plan[0].Events[1].DY != dy {
		t.Fatal("scroll axes lost")
	}
	if err := validateActionBounds(protocol.ComputerAction{Kind: "drag", Point: &protocol.ComputerPoint{}, To: &protocol.ComputerPoint{X: 100, Y: 0}}, 100, 80); err == nil {
		t.Fatal("drag destination escaped screenshot")
	}
	if err := validateActionBounds(protocol.ComputerAction{Kind: "key", Keys: []string{"alt", "f4"}}, 100, 80); err != nil {
		t.Fatal("keyboard incorrectly requires pointer")
	}
}
func TestTextFlowsThroughEngineAndNeverPersistsPlaintext(t *testing.T) {
	f := &fakeBackend{}
	e, s, o := setup(t, f)
	args := map[string]any{"session_id": s, "observation_id": o, "operation_id": "typed", "action": map[string]any{"kind": "text", "text": "test-private-文本🙂"}}
	got, _, err := call(t, e, protocol.ToolComputerAct, args)
	if err != nil || got["effects"] != "applied" {
		t.Fatal(got, err)
	}
	got, _, err = call(t, e, protocol.ToolComputerAct, args)
	if err != nil || got["replayed_result"] != true || f.clicks.Load() != 1 {
		t.Fatal("text was replayed", got, err)
	}
	key := recordKey(testOwner, "typed")
	e.mu.Lock()
	record := e.records[key]
	e.mu.Unlock()
	if strings.Contains(string(raw(record)), "test-private") {
		t.Fatal("typed text leaked into journal")
	}
}
func TestCancellationBeforeInputSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	driver := &recordingDriver{}
	_, err := runInput(ctx, driver, planForTest(t, protocol.ComputerAction{Kind: "key", Keys: []string{"ctrl", "a"}}))
	if !errors.Is(err, context.Canceled) || len(driver.events) != 0 {
		t.Fatal("cancelled request submitted keys")
	}
}
