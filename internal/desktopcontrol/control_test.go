package desktopcontrol

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestHandleMessage(t *testing.T) {
	responseData := handleMessage(context.Background(), []byte(`{"id":"1","method":"ping"}`), func(_ context.Context, request Request) (any, error) {
		if request.Method != "ping" {
			t.Fatalf("unexpected method: %s", request.Method)
		}
		return map[string]bool{"ready": true}, nil
	})
	var reply response
	if err := jsonUnmarshal(responseData, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != nil || string(reply.Result) != `{"ready":true}` {
		t.Fatalf("unexpected response: %s", responseData)
	}
}

func TestUnixRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping local IPC test")
	}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, root, func(_ context.Context, request Request) (any, error) {
			if request.Method == "fail" {
				return nil, errors.New("expected failure")
			}
			return map[string]string{"method": request.Method}, nil
		})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var result map[string]string
		err := Call(context.Background(), root, "ping", nil, &result)
		if err == nil {
			if result["method"] != "ping" {
				t.Fatalf("unexpected result: %#v", result)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("IPC server did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IPC server did not stop after cancellation")
	}
}

func TestSecondServerCannotTakeOverEndpoint(t *testing.T) {
	// macOS 的系统临时目录前缀较长，避免测试名耗尽 Unix socket 路径上限。
	root, err := os.MkdirTemp("", "ctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, root, func(context.Context, Request) (any, error) { return "original", nil })
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var value string
		if Call(ctx, root, "ping", nil, &value) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := Serve(ctx, root, func(context.Context, Request) (any, error) { return "replacement", nil }); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	var value string
	if err := Call(ctx, root, "ping", nil, &value); err != nil || value != "original" {
		t.Fatalf("original endpoint lost: %q %v", value, err)
	}
}
