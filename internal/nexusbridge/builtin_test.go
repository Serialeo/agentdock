package nexusbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/gorilla/websocket"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/builtin"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/mcp"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func TestBuiltinLocalChangePushesCompleteBridgeSnapshot(t *testing.T) {
	executable, _ := os.Executable()
	cfg := config.Config{AgentDockHome: t.TempDir(), AgentDockDefaultDir: t.TempDir(), BrowserExecutablePath: executable, Builtins: builtin.Choices{Browser: true}}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	messages := make(chan protocol.Message, 16)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		var hello protocol.Message
		if socket.ReadJSON(&hello) != nil {
			return
		}
		messages <- hello
		if socket.WriteJSON(protocol.Message{Type: protocol.MessageNodeReady, ProtocolVersion: protocol.ConnectionProtocolVersion, HeartbeatMS: 60000}) != nil {
			return
		}
		for {
			var message protocol.Message
			if socket.ReadJSON(&message) != nil {
				return
			}
			messages <- message
		}
	}))
	defer httpServer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := NewClient(Identity{Endpoint: httpServer.URL, DeviceID: "builtin-device", NodeID: "builtin-node", DeviceToken: "test"}, mcp.NewServer(runtime, cfg), runtime, publicartifacts.Store{}, &ConnectionState{})
	done := make(chan struct{})
	go func() { client.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("bridge failed to stop")
		}
	}()
	var first protocol.Message
	select {
	case first = <-messages:
	case <-time.After(3 * time.Second):
		t.Fatal("no hello")
	}
	if first.Hello == nil || len(first.Hello.Builtins) != 3 {
		t.Fatalf("missing capability states: %#v", first)
	}
	disabled := false
	if _, err := runtime.SetBuiltin(t.Context(), protocol.BuiltinUpdate{ID: "browser", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case message := <-messages:
			if message.Type != protocol.MessageNodeUpdated || message.Hello == nil {
				continue
			}
			state := message.Hello.Builtins[0]
			if state.Transitioning {
				continue
			}
			if state.Enabled || state.Available {
				t.Fatal("stale enabled snapshot")
			}
			for _, tool := range message.Hello.Tools {
				if tool.Name == "browser_session" {
					t.Fatal("disabled tool in bridge snapshot")
				}
			}
			if len(message.Hello.Capabilities) != len(message.Hello.Tools) {
				t.Fatal("names and descriptors split across snapshots")
			}
			return
		case <-timer.C:
			t.Fatal("local switch did not reach Bridge")
		}
	}
}
