//go:build windows && agentdock_computer_native

package computer

import (
	"context"
	"encoding/binary"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"math"
	"unsafe"
)

func windowsKey(name string) (keyCode, error) {
	if key, ok := namedKey(name, "windows"); ok {
		return key, nil
	}
	chars := []rune(name)
	if len(chars) == 1 && chars[0] <= 0xffff {
		thread := ucall("GetWindowThreadProcessId", ucall("GetForegroundWindow"), 0)
		layout := ucall("GetKeyboardLayout", thread)
		value := uint16(ucall("VkKeyScanExW", uintptr(chars[0]), layout))
		if value != 0xffff {
			mods := uint64(0)
			if value&0x100 != 0 {
				mods |= modShift
			}
			if value&0x200 != 0 {
				mods |= modControl
			}
			if value&0x400 != 0 {
				mods |= modAlt
			}
			return keyCode{Code: value & 0xff, Implicit: mods}, nil
		}
	}
	return keyCode{}, failure(protocol.ErrorComputerInputRejected, fmt.Sprintf("unknown shortcut key %q; use text for Unicode text", name))
}

type windowsInputDriver struct {
	targetFocused    bool
	shot             Snapshot
	action           protocol.ComputerAction
	original, target uintptr
	display          winRect
	keys             []uint16
}

func (n *nativeBackend) Act(ctx context.Context, s Snapshot, a protocol.ComputerAction) (protocol.ComputerInputResult, error) {
	done := withDPI()
	defer done()
	batches, err := planAction(s, a, windowsKey)
	if err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	h, _, err := windowState()
	if err != nil {
		return protocol.ComputerInputResult{Status: "not_submitted"}, err
	}
	driver := &windowsInputDriver{shot: s, action: a, original: h, target: h, display: monitors()[s.Display]}
	if a.Point != nil {
		point := nativePoint(s, a.Point)
		packed := uintptr(uint64(uint32(int32(math.Round(point.X)))) | uint64(uint32(int32(math.Round(point.Y))))<<32)
		hit := ucall("WindowFromPoint", packed)
		if root := ucall("GetAncestor", hit, 2); root != 0 {
			driver.target = root
		}
	}
	for _, batch := range batches {
		for _, event := range batch.Events {
			if event.Kind == "key_down" {
				driver.keys = append(driver.keys, event.Key.Code)
			}
		}
	}
	return runInput(ctx, driver, batches)
}
func (d *windowsInputDriver) Check(ctx context.Context, continuing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !interactive() {
		return failure(protocol.ErrorComputerDesktopUnavailable, "desktop locked or disconnected")
	}
	display, ok := monitors()[d.shot.Display]
	if !ok || display != d.display {
		return failure(protocol.ErrorComputerObservationStale, "display geometry changed")
	}
	current, rect, err := windowState()
	if err != nil {
		return err
	}
	if !continuing {
		if geometry(display, current, rect) != d.shot.Geometry {
			return failure(protocol.ErrorComputerObservationStale, "focus or geometry changed since observation")
		}
		if d.shot.BoundWindow && d.target != current {
			return failure(protocol.ErrorComputerObservationStale, "point is outside the explicitly observed window")
		}
		for _, key := range []uintptr{1, 2, 4} {
			if ucall("GetAsyncKeyState", key)&0x8000 != 0 {
				return failure(protocol.ErrorComputerInputRejected, "release held mouse buttons before automated input")
			}
		}
		if d.action.Kind == "key" || d.action.Kind == "text" {
			check := append([]uint16{0x10, 0x11, 0x12, 0x5B, 0x5C}, d.keys...)
			for _, key := range check {
				if ucall("GetAsyncKeyState", uintptr(key))&0x8000 != 0 {
					return failure(protocol.ErrorComputerInputRejected, "release held shortcut keys before automated input")
				}
			}
		}
	} else {
		// Window dragging changes its rectangle; identity and display bounds remain the guard.
		if current == d.target {
			d.targetFocused = true
		}
		if d.targetFocused && current != d.target {
			return failure(protocol.ErrorComputerObservationStale, "target lost input focus")
		}
		if d.action.Point != nil && d.target != 0 && ucall("IsWindow", d.target) == 0 {
			return failure(protocol.ErrorComputerObservationStale, "pointer target no longer exists")
		}
		if current != d.target && current != d.original {
			return failure(protocol.ErrorComputerObservationStale, "input focus moved to an unrelated window")
		}
		if d.action.Kind == "drag" && ucall("IsWindow", d.target) == 0 {
			return failure(protocol.ErrorComputerObservationStale, "drag target no longer exists")
		}
	}
	return ctx.Err()
}

type windowsInput struct {
	Type    uint32
	Padding uint32
	Body    [32]byte
}

func mousePacket(flags uint32, p protocol.ComputerPoint, data int) windowsInput {
	var packet windowsInput
	vx, vy, vw, vh := metric(76), metric(77), metric(78), metric(79)
	x := math.Max(float64(vx), math.Min(float64(vx+vw-1), p.X))
	y := math.Max(float64(vy), math.Min(float64(vy+vh-1), p.Y))
	if flags&1 != 0 {
		binary.LittleEndian.PutUint32(packet.Body[0:4], uint32(int32(math.Round((x-float64(vx))*65535/float64(max(1, vw-1))))))
		binary.LittleEndian.PutUint32(packet.Body[4:8], uint32(int32(math.Round((y-float64(vy))*65535/float64(max(1, vh-1))))))
		flags |= 0xC000
	}
	binary.LittleEndian.PutUint32(packet.Body[8:12], uint32(int32(data)))
	binary.LittleEndian.PutUint32(packet.Body[12:16], flags)
	return packet
}
func keyboardPacket(key keyCode, scan uint16, flags uint32) windowsInput {
	packet := windowsInput{Type: 1}
	if key.Extended {
		flags |= 1
	}
	binary.LittleEndian.PutUint16(packet.Body[0:2], key.Code)
	binary.LittleEndian.PutUint16(packet.Body[2:4], scan)
	binary.LittleEndian.PutUint32(packet.Body[4:8], flags)
	return packet
}
func mouseButtonFlags(button string, up bool) uint32 {
	base := uint32(2)
	switch button {
	case "right":
		base = 8
	case "middle":
		base = 32
	}
	if up {
		base *= 2
	}
	return base
}
func rawWindowsSend(packets []windowsInput) int {
	if len(packets) == 0 {
		return 0
	}
	return int(ucall("SendInput", uintptr(len(packets)), uintptr(unsafe.Pointer(&packets[0])), unsafe.Sizeof(packets[0])))
}
func (d *windowsInputDriver) Send(events []inputEvent) (submission, error) {
	packets := []windowsInput{}
	ends := []int{}
	for _, event := range events {
		switch event.Kind {
		case "move", "drag":
			packets = append(packets, mousePacket(1, event.Point, 0))
		case "button_down", "button_up":
			packets = append(packets, mousePacket(mouseButtonFlags(event.Button, event.Kind == "button_up"), event.Point, 0))
		case "wheel":
			if event.DX != 0 {
				packets = append(packets, mousePacket(0x1000, event.Point, event.DX))
			}
			if event.DY != 0 {
				packets = append(packets, mousePacket(0x800, event.Point, event.DY))
			}
		case "key_down":
			packets = append(packets, keyboardPacket(event.Key, 0, 0))
		case "key_up":
			packets = append(packets, keyboardPacket(event.Key, 0, 2))
		case "text":
			for _, unit := range event.Text {
				packets = append(packets, keyboardPacket(keyCode{}, unit, 4), keyboardPacket(keyCode{}, unit, 6))
			}
		}
		ends = append(ends, len(packets))
	}
	accepted := rawWindowsSend(packets)
	report := submission{Requested: len(packets), Accepted: accepted, Known: true}
	for _, end := range ends {
		if end <= accepted {
			report.Sent++
		}
	}
	if accepted != len(packets) {
		// If failure splits a Unicode pair, release that packet immediately. General
		// key/button pairs are tracked by runInput using the accepted logical prefix.
		if accepted > 0 {
			last := packets[accepted-1]
			if last.Type == 1 {
				flags := binary.LittleEndian.Uint32(last.Body[4:8])
				if flags&4 != 0 && flags&2 == 0 {
					binary.LittleEndian.PutUint32(last.Body[4:8], flags|2)
					if rawWindowsSend([]windowsInput{last}) != 1 {
						return report, failure(protocol.ErrorComputerExecutionUnknown, "Unicode input was partially accepted and key-up could not be confirmed")
					}
				}
			}
		}
		return report, failure(protocol.ErrorComputerInputRejected, "Windows did not accept all events; check target elevation and desktop availability")
	}
	return report, nil
}
