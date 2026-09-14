//go:build windows

package desktopcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNamedPipeShutdownBeforeConnectionStarts(t *testing.T) {
	name, err := windows.UTF16PtrFromString(endpointPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	// 已取消的上下文覆盖 ready 通知与真正开始同步 I/O 之间的竞争窗口。
	for i := 0; i < 100; i++ {
		pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			1, 1024, 1024, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		done := make(chan error, 1)
		go func() { done <- connectNamedPipe(ctx, pipe) }()
		select {
		case err := <-done:
			_ = windows.CloseHandle(pipe)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("shutdown returned %v", err)
			}
		case <-time.After(2 * time.Second):
			_ = windows.CloseHandle(pipe)
			t.Fatal("shutdown waited for a client after cancellation")
		}
	}
}
