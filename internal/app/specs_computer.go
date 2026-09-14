package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/Serialeo/agentdock-protocol/mcpcontract"
	"github.com/uvwt/agentdock/internal/computer"
	"github.com/uvwt/agentdock/internal/config"
	projectstate "github.com/uvwt/agentdock/internal/project"
	"time"
)

func computerToolSpecs() []ToolSpec {
	descriptions := map[string]string{
		protocol.ToolComputerStatus:  "Read native desktop availability and supported actions, or reread a saved operation_id without replaying input.",
		protocol.ToolComputerSession: "Acquire, renew or release a 15-second desktop control lease. Local user opt-in and Deployment control permission are required.",
		protocol.ToolComputerObserve: "Capture a display as a private MCP image with observation_id and image pixel coordinates. Does not focus or acquire control. max_size is a preference.",
		protocol.ToolComputerAct:     "Perform a supported action using a fresh observation and control session. This backend implements a single click. Reuse operation_id only to retrieve the same result; never retry uncertain input with a new ID. OS input submission is not business success.",
		protocol.ToolComputerStop:    "Stop a desktop control session immediately. In-flight OS input may already have occurred; query its operation_id. Stopped sessions never resume.",
	}
	specs := make([]ToolSpec, 0, 5)
	for _, name := range mcpcontract.ComputerToolNames() {
		name := name
		annotations := mutatingToolAnnotations(true, true)
		if name == protocol.ToolComputerStatus || name == protocol.ToolComputerObserve {
			annotations = readOnlyToolAnnotations(true)
		}
		specs = append(specs, ToolSpec{Name: name, Title: name, Description: descriptions[name], Annotations: annotations,
			Availability: func(cfg config.Config) bool { return cfg.ComputerAvailable },
			Contract: func(name string, cfg config.Config) (ToolContract, bool) {
				return staticToolContract(name, mcpcontract.ComputerInputSchema, mcpcontract.ComputerOutputSchema)
			},
			Handler: func(ctx context.Context, r *Runtime, args map[string]any) (Result, error) {
				return r.callComputer(ctx, name, args)
			},
		})
	}
	return specs
}
func (r *Runtime) callComputer(ctx context.Context, name string, args map[string]any) (Result, error) {
	if r.computer == nil {
		return nil, toolError(protocol.ErrorComputerUnsupported, "native computer helper is unavailable", "computer")
	}
	owner := computer.Owner{}
	if execution, ok := projectstate.ExecutionFromContext(ctx); ok {
		owner = computer.Owner{WorkSession: execution.Context.WorkSessionID, Target: execution.Target.TargetID, Deployment: execution.Target.DeploymentID, Revision: execution.Target.DeploymentRevision}
	} else {
		hash := sha256.Sum256([]byte(r.cfg.AgentDockHome))
		owner = computer.Owner{WorkSession: "local:" + hex.EncodeToString(hash[:]), Target: "local"}
	}
	if args == nil {
		args = map[string]any{}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	reply, err := r.computer.Call(ctx, computer.Request{Tool: name, Owner: owner, Args: encoded})
	if err != nil {
		var remote *protocol.RemoteError
		if errors.As(err, &remote) {
			return nil, toolErrorDetails(remote.Code, remote.Message, remote.Category, remote.Details)
		}
		return nil, toolError(protocol.ErrorComputerExecutionUnknown, err.Error(), "computer")
	}
	var result Result
	if err = json.Unmarshal(reply.Result, &result); err != nil {
		return nil, err
	}
	if len(reply.PNG) > 0 {
		result["_mcp_image_base64"] = base64.StdEncoding.EncodeToString(reply.PNG)
		result["_mcp_image_mime_type"] = "image/png"
	}
	return result, nil
}
