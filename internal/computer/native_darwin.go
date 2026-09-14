//go:build darwin && cgo && agentdock_computer_native

package computer

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices -framework ScreenCaptureKit -framework CoreImage -framework CoreMedia -framework CoreVideo
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"
import (
	"context"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"path/filepath"
	"strconv"
	"unsafe"
)

type nativeBackend struct{}

func NewNativeBackend() (Backend, error) { return &nativeBackend{}, nil }
func RequestNativePermissions()          { C.ad_permissions() }
func nativeErr(code C.int) error {
	switch code {
	case 1:
		return failure(protocol.ErrorComputerDesktopUnavailable, "desktop locked or disconnected")
	case 2:
		return failure(protocol.ErrorComputerPermissionDenied, "grant permission to the installed AgentDock Computer helper")
	case 3:
		return failure(protocol.ErrorComputerObservationStale, "target focus, geometry or display changed; observe again")
	default:
		return failure(protocol.ErrorComputerInputRejected, "native desktop operation failed")
	}
}
func (n *nativeBackend) Status(ctx context.Context) (protocol.ComputerBackendStatus, error) {
	var observe, control C.int
	ready := C.ad_status(&observe, &control)
	s := protocol.ComputerBackendStatus{BackendState: "ready", DesktopState: "interactive", DesktopID: "macos/console", Actions: actionNames(), Permissions: protocol.ComputerNativePermissions{Observe: "denied", Control: "denied"}}
	if observe != 0 {
		s.Permissions.Observe = "granted"
	}
	if control != 0 {
		s.Permissions.Control = "granted"
	}
	if ready == 0 {
		s.DesktopState = "locked"
	}
	var ids [64]C.uint32_t
	count := int(C.ad_displays(&ids[0], 64))
	for _, id := range ids[:count] {
		var w, h, r C.int
		C.ad_display(id, &w, &h, &r)
		s.Displays = append(s.Displays, protocol.ComputerDisplay{DisplayID: strconv.FormatUint(uint64(id), 10), Width: int(w), Height: int(h), Rotation: int(r)})
	}
	return s, ctx.Err()
}
func shotGeometry(s C.ADShot) string {
	return fmt.Sprintf("%g,%g,%g,%g/%d/%g,%g,%g,%g", float64(s.x), float64(s.y), float64(s.w), float64(s.h), uint64(s.target), float64(s.wx), float64(s.wy), float64(s.ww), float64(s.wh))
}
func (n *nativeBackend) Capture(ctx context.Context, display, window string, size int) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	id, err := strconv.ParseUint(display, 10, 32)
	if err != nil {
		return Snapshot{}, failure(protocol.ErrorComputerObservationStale, "display no longer exists")
	}
	var shot C.ADShot
	code := C.ad_capture(C.uint32_t(id), C.int(size), &shot)
	if code != 0 {
		return Snapshot{}, nativeErr(code)
	}
	defer C.free(shot.png)
	if err = ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	target := strconv.FormatUint(uint64(shot.target), 10)
	if window != "" && window != target {
		return Snapshot{}, failure(protocol.ErrorComputerObservationStale, "requested window is not foreground")
	}
	return Snapshot{BoundWindow: window != "", TargetPID: uint32(shot.pid), PNG: C.GoBytes(shot.png, C.int(shot.length)), Width: int(shot.width), Height: int(shot.height), Display: display, Target: target, Geometry: shotGeometry(shot), Transform: [6]float64{float64(shot.w) / float64(shot.width), 0, 0, float64(shot.h) / float64(shot.height), float64(shot.x), float64(shot.y)}}, nil
}
func platformConfigDir() (string, error) {
	home := C.ad_user_home()
	if home == nil {
		return "", fmt.Errorf("resolve OS user home")
	}
	defer C.free(unsafe.Pointer(home))
	return filepath.Join(C.GoString(home), "Library", "Application Support"), nil
}

// RunMain keeps Cocoa on the initial OS thread and serves private IPC on Go workers.
func RunMain(run func() error) error {
	done := make(chan error, 1)
	go func() { done <- run(); C.ad_stop_loop() }()
	C.ad_run_loop()
	return <-done
}
