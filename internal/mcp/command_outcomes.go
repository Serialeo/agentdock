package mcp

import (
	"context"
	"errors"

	protocol "github.com/Serialeo/agentdock-protocol"
)

// These operations belong to the authenticated private Bridge, not MCP tools.
func (s *Server) ReadCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesReadRequest) (protocol.CommandOutcomesReadResult, error) {
	if s == nil || s.runtime == nil {
		return protocol.CommandOutcomesReadResult{}, errors.New("runtime unavailable")
	}
	return s.runtime.ReadCommandOutcomes(ctx, request)
}
func (s *Server) AckCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesAckRequest) (protocol.CommandOutcomesAckResult, error) {
	if s == nil || s.runtime == nil {
		return protocol.CommandOutcomesAckResult{}, errors.New("runtime unavailable")
	}
	return s.runtime.AckCommandOutcomes(ctx, request)
}
