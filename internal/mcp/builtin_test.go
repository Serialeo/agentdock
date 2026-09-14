package mcp

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
)

func TestBuiltinSwitchNotifiesConnectedHTTPClientAndRejectsCachedCalls(t *testing.T) {
	executable, _ := os.Executable()
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), BrowserExecutablePath: executable, Builtins: builtin.Choices{Browser: true}}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := NewServer(runtime, cfg)
	httpServer := httptest.NewServer(server.HTTPHandler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	notifications := make(chan struct{}, 16)
	ack := make(chan struct{}, 1)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "builtin-test", Version: "1"}, &mcpsdk.ClientOptions{ToolListChangedHandler: func(context.Context, *mcpsdk.ToolListChangedRequest) { notifications <- struct{}{} }})
	client.AddReceivingMiddleware(func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				select {
				case ack <- struct{}{}:
				default:
				}
			}
			return next(ctx, method, req)
		}
	})
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	select {
	case <-ack:
	case <-ctx.Done():
		t.Fatal("client did not subscribe")
	}
	assertListed := func(want bool) {
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
			t.Fatalf("browser listed=%v want %v", found, want)
		}
	}
	assertListed(true)
	for _, enabled := range []bool{false, true, false} {
		for len(notifications) > 0 {
			<-notifications
		}
		if _, err := runtime.SetBuiltin(ctx, protocol.BuiltinUpdate{ID: "browser", Enabled: &enabled}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-notifications:
		case <-ctx.Done():
			t.Fatal("no tools/list_changed notification")
		}
		assertListed(enabled)
	}
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "browser_session", Arguments: map[string]any{"action": "start"}})
	if err == nil && !result.IsError {
		t.Fatal("cached tool call bypassed disabled group")
	}
	if _, err := runtime.Call(ctx, "browser_session", nil); err == nil {
		t.Fatal("runtime bypassed disabled group")
	}
}
