package computer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"golang.org/x/sys/windows"
	"image"
	"image/png"
	"math"
	"runtime"
	"strconv"
	"unsafe"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
var gdi32 = windows.NewLazySystemDLL("gdi32.dll")

func ucall(name string, args ...uintptr) uintptr {
	v, _, _ := user32.NewProc(name).Call(args...)
	return v
}
func gcall(name string, args ...uintptr) uintptr {
	v, _, _ := gdi32.NewProc(name).Call(args...)
	return v
}

type nativeBackend struct{}

func NewNativeBackend() (Backend, error) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return nil, failure(protocol.ErrorComputerUnsupported, "run the desktop Core in standard user mode for computer use")
	}
	return &nativeBackend{}, nil
}

type winRect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size          uint32
	Monitor, Work winRect
	Flags         uint32
}

func metric(n int) int { return int(int32(ucall("GetSystemMetrics", uintptr(n)))) }
func interactive() bool {
	var sid uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sid); err != nil || sid == 0 {
		return false
	}
	var statePtr uintptr
	var stateBytes uint32
	ok, _, _ := wtsapi32.NewProc("WTSQuerySessionInformationW").Call(0, uintptr(sid), 8, uintptr(unsafe.Pointer(&statePtr)), uintptr(unsafe.Pointer(&stateBytes)))
	if ok == 0 {
		return false
	}
	active := stateBytes >= 4 && *(*uint32)(unsafe.Pointer(statePtr)) == 0
	wtsapi32.NewProc("WTSFreeMemory").Call(statePtr)
	if !active {
		return false
	}
	d := ucall("OpenInputDesktop", 0, 0, 0x0001)
	if d == 0 {
		return false
	}
	defer ucall("CloseDesktop", d)
	var name [64]uint16
	var needed uint32
	if ucall("GetUserObjectInformationW", d, 2, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Sizeof(name)), uintptr(unsafe.Pointer(&needed))) == 0 {
		return false
	}
	return windows.UTF16ToString(name[:]) == "Default"
}

type monitorEntry struct {
	ID   string
	Rect winRect
}

// NewCallback slots cannot be freed on Windows; allocate this thunk once.
var monitorCallback = windows.NewCallback(func(h, dc, rect, param uintptr) uintptr {
	var info monitorInfo
	info.Size = uint32(unsafe.Sizeof(info))
	if ucall("GetMonitorInfoW", h, uintptr(unsafe.Pointer(&info))) != 0 {
		entries := (*[]monitorEntry)(unsafe.Pointer(param))
		*entries = append(*entries, monitorEntry{strconv.FormatUint(uint64(h), 10), info.Monitor})
	}
	return 1
})

func monitors() map[string]winRect {
	entries := []monitorEntry{}
	ucall("EnumDisplayMonitors", 0, 0, monitorCallback, uintptr(unsafe.Pointer(&entries)))
	runtime.KeepAlive(&entries)
	result := make(map[string]winRect, len(entries))
	for _, entry := range entries {
		result[entry.ID] = entry.Rect
	}
	return result
}

// DPI context belongs to a thread. Keep all coordinate-sensitive calls on it.
func withDPI() func() {
	runtime.LockOSThread()
	old := ucall("SetThreadDpiAwarenessContext", ^uintptr(3))
	return func() {
		if old != 0 {
			ucall("SetThreadDpiAwarenessContext", old)
		}
		runtime.UnlockOSThread()
	}
}
func (n *nativeBackend) Status(ctx context.Context) (protocol.ComputerBackendStatus, error) {
	done := withDPI()
	defer done()
	s := protocol.ComputerBackendStatus{BackendState: "ready", DesktopState: "interactive", DesktopID: "windows/default", Actions: actionNames(), Permissions: protocol.ComputerNativePermissions{Observe: "granted", Control: "granted"}}
	if !interactive() {
		s.DesktopState = "locked"
		s.Permissions.Observe = "unavailable"
		s.Permissions.Control = "unavailable"
		return s, nil
	}
	for id, r := range monitors() {
		s.Displays = append(s.Displays, protocol.ComputerDisplay{DisplayID: id, Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top), Rotation: monitorRotation(id)})
	}
	return s, ctx.Err()
}
func windowState() (uintptr, winRect, error) {
	h := ucall("GetForegroundWindow")
	var r winRect
	if h == 0 || ucall("GetWindowRect", h, uintptr(unsafe.Pointer(&r))) == 0 {
		return 0, r, failure(protocol.ErrorComputerDesktopUnavailable, "no foreground target window")
	}
	return h, r, nil
}
func geometry(display winRect, h uintptr, window winRect) string {
	return fmt.Sprintf("%d,%d,%d,%d/%d/%d,%d,%d,%d", display.Left, display.Top, display.Right, display.Bottom, h, window.Left, window.Top, window.Right, window.Bottom)
}
func (n *nativeBackend) Capture(ctx context.Context, display, window string, size int) (Snapshot, error) {
	done := withDPI()
	defer done()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if !interactive() {
		return Snapshot{}, failure(protocol.ErrorComputerDesktopUnavailable, "interactive desktop unavailable")
	}
	r, ok := monitors()[display]
	if !ok {
		return Snapshot{}, failure(protocol.ErrorComputerObservationStale, "display is no longer present")
	}
	h, wr, err := windowState()
	if err != nil {
		return Snapshot{}, err
	}
	target := strconv.FormatUint(uint64(h), 10)
	if window != "" && window != target {
		return Snapshot{}, failure(protocol.ErrorComputerObservationStale, "requested window is not the foreground target")
	}
	sw, sh := int(r.Right-r.Left), int(r.Bottom-r.Top)
	scale := math.Min(1, float64(size)/float64(max(sw, sh)))
	w, height := max(1, int(float64(sw)*scale)), max(1, int(float64(sh)*scale))
	dc := ucall("GetDC", 0)
	if dc == 0 {
		return Snapshot{}, fmt.Errorf("GetDC failed")
	}
	defer ucall("ReleaseDC", 0, dc)
	mem := gcall("CreateCompatibleDC", dc)
	if mem == 0 {
		return Snapshot{}, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer gcall("DeleteDC", mem)
	bmp := gcall("CreateCompatibleBitmap", dc, uintptr(w), uintptr(height))
	if bmp == 0 {
		return Snapshot{}, fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer gcall("DeleteObject", bmp)
	old := gcall("SelectObject", mem, bmp)
	defer gcall("SelectObject", mem, old)
	gcall("SetStretchBltMode", mem, 4)
	if gcall("StretchBlt", mem, 0, 0, uintptr(w), uintptr(height), dc, uintptr(r.Left), uintptr(r.Top), uintptr(sw), uintptr(sh), 0x40CC0020) == 0 {
		return Snapshot{}, fmt.Errorf("desktop capture failed")
	}
	// GetDIBits requires bitmap deselected from any DC.
	gcall("SelectObject", mem, old)
	type header struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}
	info := header{Size: 40, Width: int32(w), Height: -int32(height), Planes: 1, BitCount: 32}
	pixels := make([]byte, w*height*4)
	if gcall("GetDIBits", dc, bmp, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0) != uintptr(height) {
		return Snapshot{}, fmt.Errorf("GetDIBits failed")
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 255
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, height)}); err != nil {
		return Snapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if !interactive() {
		return Snapshot{}, failure(protocol.ErrorComputerDesktopUnavailable, "desktop changed during capture")
	}
	now, nowRect, err := windowState()
	if err != nil {
		return Snapshot{}, err
	}
	if now != h || nowRect != wr {
		return Snapshot{}, failure(protocol.ErrorComputerObservationStale, "target changed during capture; observe again")
	}
	return Snapshot{BoundWindow: window != "", PNG: encoded.Bytes(), Width: w, Height: height, Display: display, Target: target, Geometry: geometry(r, h, wr), Transform: [6]float64{float64(sw) / float64(w), 0, 0, float64(sh) / float64(height), float64(r.Left), float64(r.Top)}}, nil
}
func monitorRotation(id string) int {
	handle, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	var info struct {
		Size          uint32
		Monitor, Work winRect
		Flags         uint32
		Device        [32]uint16
	}
	info.Size = uint32(unsafe.Sizeof(info))
	if ucall("GetMonitorInfoW", uintptr(handle), uintptr(unsafe.Pointer(&info))) == 0 {
		return 0
	}
	var mode [220]byte
	binary.LittleEndian.PutUint16(mode[68:70], 220)
	if ucall("EnumDisplaySettingsW", uintptr(unsafe.Pointer(&info.Device[0])), 0xffffffff, uintptr(unsafe.Pointer(&mode[0]))) == 0 {
		return 0
	}
	return int(binary.LittleEndian.Uint32(mode[84:88])) * 90
}
