package app

import (
	"context"
	"strings"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

type projectCapability uint8

const (
	projectCapabilityNone projectCapability = iota
	projectCapabilityFileRead
	projectCapabilityFileWrite
	projectCapabilityShell
	projectCapabilityBrowser
	projectCapabilityBrowserSessionControl
	projectCapabilityDynamicMCP
	projectCapabilityACP
	projectCapabilityCommandSession
	projectCapabilityRemoved
)

func requiredProjectCapability(name string, args map[string]any) projectCapability {
	switch name {
	case "read_file", "list_dir", "search_text":
		return projectCapabilityFileRead
	case "file_edit":
		return projectCapabilityFileWrite
	case "exec_command":
		return projectCapabilityShell
	case "session_observe", "session_act":
		return projectCapabilityCommandSession
	case "browser_session":
		action, _ := args["action"].(string)
		switch strings.ToLower(strings.TrimSpace(action)) {
		case "close":
			return projectCapabilityBrowserSessionControl
		case "cleanup_stale":
			return projectCapabilityRemoved
		default:
			return projectCapabilityBrowser
		}
	case "browser_act", "browser_snapshot":
		return projectCapabilityBrowser
	case "mcp_manage", "mcp_tool_search", "mcp_tool_inspect", "mcp_tool_call":
		return projectCapabilityDynamicMCP
	case "acp_session", "acp_prompt", "acp_interaction":
		return projectCapabilityACP
	case "view_image":
		if path, _ := args["path"].(string); strings.TrimSpace(path) != "" {
			return projectCapabilityFileRead
		}
	}
	return projectCapabilityNone
}

func (r *Runtime) authorizeProjectTool(ctx context.Context, name string, args map[string]any) error {
	capability := requiredProjectCapability(name, args)
	if capability == projectCapabilityNone {
		return nil
	}
	execution, ok := projectstate.ExecutionFromContext(ctx)
	if !ok {
		return toolErrorDetails(
			protocol.ErrorExecutionContextRequired,
			"Project execution context is required for this tool",
			"authorization",
			map[string]any{"tool": name},
		)
	}
	permissions := execution.Permissions
	if permissions.FullAccess {
		switch capability {
		case projectCapabilityRemoved:
			// Explicitly retired actions stay retired even when the Node is configured
			// for Full Access.
		default:
			return nil
		}
	}
	allowed := false
	switch capability {
	case projectCapabilityFileRead:
		allowed = permissions.Files == protocol.FileCapabilityReadOnly || permissions.Files == protocol.FileCapabilityReadWrite
	case projectCapabilityFileWrite:
		allowed = permissions.Files == protocol.FileCapabilityReadWrite
	case projectCapabilityShell:
		allowed = permissions.Shell
	case projectCapabilityBrowser:
		allowed = permissions.Browser
	case projectCapabilityDynamicMCP:
		allowed = permissions.DynamicMCP
	case projectCapabilityACP, projectCapabilityCommandSession, projectCapabilityBrowserSessionControl:
		// Session ownership and action-specific current-permission checks are handled
		// by the ACP/command services so stale owners can still observe/terminate old work.
		return nil
	case projectCapabilityRemoved:
		message := "tool is not available in Project execution"
		if name == "browser_session" {
			message = "browser_session cleanup_stale is not available in Project execution"
		}
		return toolErrorDetails(
			protocol.ErrorCapabilityDenied,
			message,
			"authorization",
			map[string]any{"tool": name, "target_id": execution.Target.TargetID},
		)
	}
	if allowed {
		return nil
	}
	return toolErrorDetails(
		protocol.ErrorCapabilityDenied,
		"Project Deployment capability denies this tool",
		"authorization",
		map[string]any{"tool": name, "target_id": execution.Target.TargetID, "deployment_id": execution.Deployment.ID},
	)
}
