package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/desktopruntime"
)

func TestStdioBuiltinHelper(t *testing.T) {
	if os.Getenv("AGENTDOCK_STDIO_TEST_HELPER") != "1" {
		return
	}
	if err := runServer(context.Background(), []string{"--stdio"}, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestStdioFreshHomeCanManageBuiltinsAndRestart(t *testing.T) {
	home, err := os.MkdirTemp("", "stdio-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	cwd := t.TempDir()
	root := filepath.Join(home, "runtime", "stdio")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := func() *mcpsdk.ClientSession {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStdioBuiltinHelper$")
		cmd.Env = append(os.Environ(), "AGENTDOCK_STDIO_TEST_HELPER=1", "AGENTDOCK_HOME="+home, "AGENTDOCK_DEFAULT_DIR="+cwd,
			"AGENTDOCK_RUNTIME_ROOT="+filepath.Join(t.TempDir(), "unrelated-desktop"))
		cmd.Stderr = os.Stderr
		session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
		if err != nil {
			t.Fatal(err)
		}
		// MCP 与本地控制是独立监听器；MCP 初始化完成不代表 control socket 已绑定。
		for {
			var status bytes.Buffer
			if err := desktopruntime.RunBuiltinCommand(ctx, []string{"list", "--runtime-root", root}, &status, os.Stderr); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				_ = session.Close()
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
		return session
	}
	checkBrowser := func(session *mcpsdk.ClientSession, want bool) {
		t.Helper()
		result, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range result.Tools {
			if tool.Name == "browser_session" {
				found = true
			}
		}
		if found != want {
			t.Fatalf("browser=%v want %v", found, want)
		}
	}
	first := start()
	defer first.Close()
	checkBrowser(first, false)
	var output bytes.Buffer
	// The CLI reaches the stdio instance's isolated endpoint, not inherited desktop runtime root.
	if err := desktopruntime.RunBuiltinCommand(ctx, []string{"set", "--runtime-root", root, "--id", "browser", "--enabled=true"}, &output, os.Stderr); err != nil {
		t.Fatal(err)
	}
	checkBrowser(first, true)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := start()
	defer second.Close()
	checkBrowser(second, true)
	output.Reset()
	if err := desktopruntime.RunBuiltinCommand(ctx, []string{"set", "--runtime-root", root, "--id", "browser", "--enabled=false"}, &output, os.Stderr); err != nil {
		t.Fatal(err)
	}
	checkBrowser(second, false)
	result, err := second.CallTool(ctx, &mcpsdk.CallToolParams{Name: "browser_session", Arguments: map[string]any{"action": "start", "cdp_url": "http://127.0.0.1:9222"}})
	if err == nil && !result.IsError {
		t.Fatal("disabled cached call accepted")
	}
}
