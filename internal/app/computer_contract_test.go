package app

import (
	"encoding/json"
	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/Serialeo/agentdock-protocol/mcpcontract"
	projectstate "github.com/uvwt/agentdock/internal/project"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
	"math"
	"testing"
)

func TestComputerContractsCompileAndRemainUnpublished(t *testing.T) {
	for _, name := range mcpcontract.ComputerToolNames() {
		input, ok := mcpcontract.ComputerInputSchema(name)
		if !ok {
			t.Fatal(name)
		}
		if _, err := toolcontract.CompileInputSchema(input); err != nil {
			t.Fatalf("%s input: %v", name, err)
		}
		output, ok := mcpcontract.ComputerOutputSchema(name)
		if !ok {
			t.Fatal(name)
		}
		if _, err := toolcontract.CompileInputSchema(output); err != nil {
			t.Fatalf("%s output: %v", name, err)
		}
		if _, exists := toolSpecByName(name); exists {
			t.Fatalf("P1 advertised unfinished native backend: %s", name)
		}
		if mcpcontract.IsCanonicalTool(name) {
			t.Fatalf("Node-owned tool made canonical: %s", name)
		}
	}
}
func TestComputerActContractRejectsUnsafeShapes(t *testing.T) {
	schema, _ := mcpcontract.ComputerInputSchema(protocol.ToolComputerAct)
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	point := map[string]any{"x": 10, "y": 20}
	for _, action := range []map[string]any{
		{"kind": "click", "point": point}, {"kind": "move", "point": point},
		{"kind": "scroll", "point": point, "delta_x": 0, "delta_y": 120},
		{"kind": "key", "keys": []string{"CTRL", "A"}}, {"kind": "text", "text": "hello"},
		{"kind": "drag", "point": point, "to": point, "duration_ms": 100},
	} {
		args := map[string]any{"session_id": "cs-1", "operation_id": "op-1", "observation_id": "obs-1", "action": action}
		if err := validator.Validate(args); err != nil {
			t.Fatalf("valid action %v: %v", action, err)
		}
	}
	for _, action := range []map[string]any{
		{"kind": "click"}, {"kind": "click", "point": point, "count": 0},
		{"kind": "click", "point": point, "script": "ignored?"},
		{"kind": "click", "point": map[string]any{"x": -1, "y": 0}},
		{"kind": "click", "point": map[string]any{"x": math.NaN(), "y": 0}},
		{"kind": "text", "text": ""}, {"kind": "key", "keys": []string{}},
		{"kind": "drag", "point": point, "to": point, "duration_ms": 3001},
		{"kind": "key_down", "keys": []string{"CTRL"}},
	} {
		args := map[string]any{"session_id": "cs-1", "operation_id": "op-1", "observation_id": "obs-1", "action": action}
		if validator.Validate(args) == nil {
			t.Fatalf("invalid action accepted: %v", action)
		}
	}
	for _, field := range []string{"permissions", "image_to_native", "owner", "grant", "working_folder", "node_id"} {
		args := map[string]any{"session_id": "cs-1", "operation_id": "op-1", "observation_id": "obs-1", "action": map[string]any{"kind": "click", "point": point}, field: "override"}
		if validator.Validate(args) == nil {
			t.Fatalf("model override accepted: %s", field)
		}
	}
}
func TestComputerSessionContractHasActionSpecificFields(t *testing.T) {
	schema, _ := mcpcontract.ComputerInputSchema(protocol.ToolComputerSession)
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"action":"acquire","desktop_id":"desktop-1"}`, true},
		{`{"action":"renew","session_id":"cs-1"}`, true},
		{`{"action":"release","session_id":"cs-1"}`, true},
		{`{"action":"acquire","session_id":"cs-1"}`, false},
		{`{"action":"renew","desktop_id":"desktop-1"}`, false},
		{`{"action":"renew","session_id":"cs-1","desktop_id":"desktop-1"}`, false},
		{`{"action":"acquire","desktop_id":"display/left + 2"}`, true},
	} {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.body), &args)
		if got := validator.Validate(args) == nil; got != tc.want {
			t.Fatalf("%s got valid=%v", tc.body, got)
		}
	}
}
func TestComputerAuthorizationUsesDeploymentPolicy(t *testing.T) {
	r := &Runtime{}
	for _, level := range []protocol.ComputerPermission{protocol.ComputerPermissionNone, protocol.ComputerPermissionObserve, protocol.ComputerPermissionControl} {
		p := protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone, Computer: level}
		ctx := projectAuthorizationContextForTest("target-1", p)
		for _, name := range mcpcontract.ComputerToolNames() {
			err := r.authorizeProjectTool(ctx, name, nil)
			if got := err == nil; got != p.AllowsComputerTool(name) {
				t.Fatalf("%s/%s: %v", level, name, err)
			}
		}
	}
}

func TestComputerHistoricalRuntimeRefreshRetainsOwnerBoundary(t *testing.T) {
	r, root := newCodeToolsRuntime(t)
	defer r.Close()
	ctx := projectContextForTest(t, r, root, protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone, Computer: protocol.ComputerPermissionNone})
	r.RevokeProjectTarget("test-target")
	for _, tc := range []struct {
		name    string
		args    map[string]any
		allowed bool
	}{
		{protocol.ToolComputerStop, map[string]any{"session_id": "cs-1"}, true},
		{protocol.ToolComputerStatus, map[string]any{"operation_id": "op-1"}, true},
		{protocol.ToolComputerStatus, nil, false},
		{protocol.ToolComputerStatus, map[string]any{"operation_id": "  "}, false},
		{protocol.ToolComputerObserve, nil, false}, {protocol.ToolComputerSession, map[string]any{"action": "release"}, false}, {protocol.ToolComputerAct, nil, false},
	} {
		_, err := r.refreshPreparedProjectExecution(ctx, tc.name, tc.args)
		if (err == nil) != tc.allowed {
			t.Fatalf("revoked %s %v: %v", tc.name, tc.args, err)
		}
	}
	execution, _ := projectstate.ExecutionFromContext(ctx)
	execution.Context.WorkSessionID = "different-owner"
	wrongOwner := projectstate.WithExecution(ctx, execution)
	if _, err := r.refreshPreparedProjectExecution(wrongOwner, protocol.ToolComputerStatus, map[string]any{"operation_id": "op-1"}); err == nil {
		t.Fatal("historical query crossed WorkSession owner boundary")
	}
}
func TestComputerDeploymentCannotEnableUnfinishedBackend(t *testing.T) {
	r, _ := newCodeToolsRuntime(t)
	defer r.Close()
	for _, full := range []bool{false, true} {
		_, err := r.ApplyProjectDeployment(protocol.Deployment{Permissions: protocol.DeploymentPermissions{Files: protocol.FileCapabilityNone, Computer: protocol.ComputerPermissionControl, FullAccess: full}})
		requireAppToolErrorCode(t, err, protocol.ErrorComputerUnsupported)
	}
}

func TestComputerContractAllowsOpaqueIDsAndBackendPreferences(t *testing.T) {
	schema, _ := mcpcontract.ComputerInputSchema(protocol.ToolComputerObserve)
	validator, err := toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(map[string]any{"desktop_id": "用户桌面 / 1", "display_id": "Display #2 (Retina)", "max_size": 4096}); err != nil {
		t.Fatalf("ordinary display ID or size preference rejected: %v", err)
	}
	schema, _ = mcpcontract.ComputerInputSchema(protocol.ToolComputerAct)
	validator, err = toolcontract.CompileInputSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []map[string]any{
		{"kind": "key", "keys": []string{"⌘", "Arrow-Left"}},
		{"kind": "click", "point": map[string]any{"x": 0, "y": 0}, "count": 3},
		{"kind": "move", "point": map[string]any{"x": 40000, "y": 1}},
	} {
		if err := validator.Validate(map[string]any{"session_id": "opaque/session 1", "operation_id": "request + 2", "observation_id": "observation #3", "timeout_ms": 60000, "action": action}); err != nil {
			t.Fatalf("backend-resolved value rejected before execution: %v", err)
		}
	}
}
