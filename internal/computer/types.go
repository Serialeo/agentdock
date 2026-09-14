// Package computer owns the private helper protocol and desktop execution state.
package computer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
)

// Owner comes only from the authenticated Runtime context, never tool arguments.
type Owner struct {
	WorkSession string `json:"work_session"`
	Target      string `json:"target"`
	Deployment  string `json:"deployment"`
	Revision    string `json:"revision"`
}
type Request struct {
	ID         string          `json:"id"`
	Tool       string          `json:"tool"`
	Owner      Owner           `json:"owner"`
	Args       json.RawMessage `json:"args,omitempty"`
	DeadlineMS int64           `json:"deadline_ms,omitempty"`
	CancelID   string          `json:"cancel_id,omitempty"`
}
type Reply struct {
	ID     string                `json:"id"`
	Result json.RawMessage       `json:"result,omitempty"`
	PNG    []byte                `json:"png,omitempty"`
	Error  *protocol.RemoteError `json:"error,omitempty"`
}

// Snapshot is helper-private. Target and native coordinates are never accepted from MCP.
type Snapshot struct {
	PNG                       []byte
	Width, Height             int
	Display, Target, Geometry string
	Transform                 [6]float64
	BoundWindow               bool
	TargetPID                 uint32
}
type Backend interface {
	Status(context.Context) (protocol.ComputerBackendStatus, error)
	Capture(context.Context, string, string, int) (Snapshot, error)
	Act(context.Context, Snapshot, protocol.ComputerAction) (protocol.ComputerInputResult, error)
}

func failure(code, message string) error {
	return &protocol.RemoteError{Code: code, Message: message, Category: "computer"}
}
func remoteError(err error) *protocol.RemoteError {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &protocol.RemoteError{Code: protocol.ErrorComputerSessionRevoked, Message: err.Error(), Category: "computer"}
	}
	if e, ok := err.(*protocol.RemoteError); ok {
		return e
	}
	return &protocol.RemoteError{Code: protocol.ErrorComputerHelperOffline, Message: err.Error(), Category: "computer"}
}
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("computer result: %v", err))
	}
	return b
}
