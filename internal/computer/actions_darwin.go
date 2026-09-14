//go:build darwin && cgo && agentdock_computer_native

package computer

/*
#include "native_darwin.h"
*/
import "C"
import (
	"context"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
)

type darwinInputDriver struct {
	shot      C.ADShot
	action    protocol.ComputerAction
	target    C.uint64_t
	targetPID C.int32_t
	keys      []uint16
	point     protocol.ComputerPoint
}

func (n *nativeBackend) Act(ctx context.Context, s Snapshot, a protocol.ComputerAction) (protocol.ComputerInputResult, error) {
	batches, err := planAction(s, a, macKey)
	if err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	var x, y, w, h, wx, wy, ww, wh float64
	var target uint64
	if _, err = fmt.Sscanf(s.Geometry, "%g,%g,%g,%g/%d/%g,%g,%g,%g", &x, &y, &w, &h, &target, &wx, &wy, &ww, &wh); err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	var display uint32
	if _, err = fmt.Sscan(s.Display, &display); err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	driver := &darwinInputDriver{shot: C.ADShot{display: C.uint32_t(display), target: C.uint64_t(target), pid: C.int32_t(s.TargetPID), x: C.double(x), y: C.double(y), w: C.double(w), h: C.double(h), wx: C.double(wx), wy: C.double(wy), ww: C.double(ww), wh: C.double(wh)}, action: a}
	if a.Point != nil {
		point := nativePoint(s, a.Point)
		driver.point = point
		driver.target = C.ad_target(C.double(point.X), C.double(point.Y), &driver.targetPID)
	}
	for _, batch := range batches {
		for _, event := range batch.Events {
			if event.Kind == "key_down" {
				driver.keys = append(driver.keys, event.Key.Code)
			}
		}
	}
	// Encode explicit-window scope in the driver, never infer it from foreground metadata.
	if s.BoundWindow {
		driver.shot.bound = 1
	}
	return runInput(ctx, driver, batches)
}
func (d *darwinInputDriver) Check(ctx context.Context, continuing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pointer := C.int(0)
	x, y := C.double(0), C.double(0)
	if d.action.Point != nil {
		pointer = 1
		x = C.double(d.point.X)
		y = C.double(d.point.Y)
	}
	next := C.int(0)
	if continuing {
		next = 1
	}
	code := C.ad_check(&d.shot, next, pointer, d.target, d.targetPID, x, y)
	if code != 0 {
		return nativeErr(code)
	}
	if !continuing && (d.action.Kind == "key" || d.action.Kind == "text") {
		keys := append([]uint16{55, 54, 56, 60, 59, 62, 58, 61, 63}, d.keys...)
		for _, key := range keys {
			if C.ad_pressed(C.uint16_t(key)) != 0 {
				return failure(protocol.ErrorComputerInputRejected, "release held shortcut keys before automated input")
			}
		}
	}
	return ctx.Err()
}
func (d *darwinInputDriver) Send(events []inputEvent) (submission, error) {
	native := make([]C.ADInput, len(events))
	kinds := map[string]int{"move": 1, "drag": 2, "button_down": 3, "button_up": 4, "wheel": 5, "key_down": 6, "key_up": 7, "text": 8}
	requested := 0
	for i, event := range events {
		e := &native[i]
		e.kind = C.int(kinds[event.Kind])
		e.x = C.double(event.Point.X)
		e.y = C.double(event.Point.Y)
		e.count = C.int(event.Count)
		e.code = C.uint16_t(event.Key.Code)
		e.flags = C.uint64_t(event.Flags)
		e.dx = C.int(event.DX)
		e.dy = C.int(event.DY)
		if event.Button == "right" {
			e.button = 1
		} else if event.Button == "middle" {
			e.button = 2
		}
		for j, unit := range event.Text {
			e.text[j] = C.uint16_t(unit)
		}
		e.length = C.int(len(event.Text))
		requested++
		if event.Kind == "text" {
			requested++
		}
	}
	if len(native) == 0 {
		return submission{}, nil
	}
	var sent C.int
	code := C.ad_send(&native[0], C.int(len(native)), &sent)
	report := submission{Sent: int(sent), Requested: requested, Known: false}
	if code != 0 {
		return report, nativeErr(code)
	}
	return report, nil
}
