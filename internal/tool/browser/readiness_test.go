package browser

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadinessDirectProbeHelper(t *testing.T) {
	endpoint := os.Getenv("AGENTDOCK_TEST_PROBE_URL")
	if endpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := New(Config{CDPURL: endpoint}, nil).CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestReadinessUsesDirectConnectionAndBoundsResponses(t *testing.T) {
	for _, mode := range []string{"success", "error", "oversize", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer connection.Close()
				var request map[string]any
				if connection.ReadJSON(&request) != nil {
					return
				}
				switch mode {
				case "success":
					_ = connection.WriteJSON(map[string]any{"id": 1, "result": map[string]any{"product": "test"}})
				case "error":
					_ = connection.WriteJSON(map[string]any{"id": 1, "error": map[string]any{"code": -1}})
				case "oversize":
					_ = connection.WriteJSON(map[string]any{"id": 1, "result": map[string]any{"data": strings.Repeat("x", 128<<10)}})
				case "timeout":
					_ = connection.ReadJSON(&request)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := New(Config{CDPURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/test"}, nil).CheckReady(ctx)
			if (err == nil) != (mode == "success") {
				t.Fatalf("probe mode=%s err=%v", mode, err)
			}
		})
	}
	// A separate process is necessary because net/http caches proxy environment parsing.
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var host string
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
			host = network.IP.String()
			break
		}
	}
	if host == "" {
		t.Skip("no non-loopback interface for proxy regression")
	}
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		http.Error(w, "unexpected proxy", 502)
	}))
	defer proxy.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var req map[string]any
		if c.ReadJSON(&req) == nil {
			_ = c.WriteJSON(map[string]any{"id": 1, "result": map[string]any{"product": "direct"}})
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadinessDirectProbeHelper$")
	cmd.Env = append(os.Environ(), "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=", "http_proxy="+proxy.URL, "https_proxy="+proxy.URL, "no_proxy=", "AGENTDOCK_TEST_PROBE_URL="+fmt.Sprintf("ws://%s/devtools/browser/test", listener.Addr()))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direct probe: %v %s", err, output)
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("probe visited environmental proxy")
	}
}
