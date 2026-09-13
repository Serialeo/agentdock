package nexusbridge

import (
	"context"
	"encoding/json"

	protocol "github.com/Serialeo/agentdock-protocol"
)

func (c *Client) commandOutcomes(ctx context.Context, operation string, arguments json.RawMessage) (map[string]any, error) {
	node, supported := c.node.(commandOutcomeAPI)
	if !supported {
		return nil, &protocol.RemoteError{Code: "BRIDGE_CAPABILITY_UNAVAILABLE", Message: "durable command outcomes are not supported", Category: "capability"}
	}
	var result any
	var err error
	if operation == protocol.OperationCommandOutcomesRead {
		var request protocol.CommandOutcomesReadRequest
		if err = decodeStrictBridgeArguments(arguments, &request); err == nil {
			result, err = node.ReadCommandOutcomes(ctx, request)
		}
	} else {
		var request protocol.CommandOutcomesAckRequest
		if err = decodeStrictBridgeArguments(arguments, &request); err == nil {
			result, err = node.AckCommandOutcomes(ctx, request)
		}
	}
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	err = json.Unmarshal(data, &response)
	return response, err
}
