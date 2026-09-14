package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// CheckReady probes the default backend only; per-call CDP endpoints are validated by start.
// Local browsers are launched only by browser_session; remote CDP must answer a read-only handshake.
func (s *Service) CheckReady(ctx context.Context) error {
	endpoint, _, err := s.resolveCDPConnection(ctx, StartRequest{})
	if err != nil {
		return err
	}
	if endpoint == "" {
		_, err = FindExecutable(s.cfg.ExecutablePath, BrowserAuto)
		return err
	}
	endpoint, err = resolveCDPWebSocket(ctx, endpoint, 5*time.Second)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{Proxy: nil, HandshakeTimeout: 5 * time.Second}
	connection, response, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return fmt.Errorf("CDP backend is not reachable: %w", err)
	}
	defer connection.Close()
	connection.SetReadLimit(64 << 10)
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := connection.WriteJSON(map[string]any{"id": 1, "method": "Browser.getVersion"}); err != nil {
		return err
	}
	for {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := connection.ReadJSON(&reply); err != nil {
			return fmt.Errorf("CDP readiness handshake: %w", err)
		}
		if reply.ID != 1 {
			continue
		}
		if len(reply.Error) > 0 || len(reply.Result) == 0 {
			return errors.New("CDP backend rejected Browser.getVersion")
		}
		return nil
	}
}
