package app

import (
	"context"

	protocol "github.com/Serialeo/agentdock-protocol"
)

func (r *Runtime) ReadCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesReadRequest) (protocol.CommandOutcomesReadResult, error) {
	return r.command.ReadCommandOutcomes(ctx, request)
}
func (r *Runtime) AckCommandOutcomes(ctx context.Context, request protocol.CommandOutcomesAckRequest) (protocol.CommandOutcomesAckResult, error) {
	return r.command.AckCommandOutcomes(ctx, request)
}
