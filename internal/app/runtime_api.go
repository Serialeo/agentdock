package app

import (
	"context"
	"strings"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

const runtimeAPISource = "agentdock-api"

func (r *Runtime) RuntimeStatus() Result {
	tools := r.ToolNames()
	return Result{
		"ok":                    true,
		"source":                runtimeAPISource,
		"service":               config.ServerName,
		"version":               buildinfo.Version,
		"agentdock_home":        r.cfg.AgentDockHome,
		"agentdock_default_dir": r.cfg.AgentDockDefaultDir,
		"path_model":            config.PathModel,
		"auth_enabled":          r.cfg.AuthRequired(),
		"builtins":              r.BuiltinCapabilities(),
		"memory_enabled":        r.cfg.NexusEndpoint != "",
		"nexus_enabled":         strings.TrimSpace(r.cfg.NexusEndpoint) != "",
		"tool_count":            len(tools),
		"tools":                 tools,
	}
}

func (r *Runtime) RuntimeSkills() (Result, error) {
	return r.skills.RuntimeSkills()
}

func (r *Runtime) RuntimeSkill(skill string) (Result, error) {
	return r.skills.RuntimeSkill(skill)
}

func (r *Runtime) RuntimeSkillFiles(skill string) (Result, error) {
	return r.skills.RuntimeSkillFiles(skill)
}

func (r *Runtime) RuntimeSkillFile(skill, relativePath string) (Result, error) {
	return r.skills.RuntimeSkillFile(skill, relativePath)
}

func (r *Runtime) RuntimeBrowseFiles(ctx context.Context, request toolfile.BrowseRequest) (Result, error) {
	return r.files.BrowseDir(ctx, request)
}

func (r *Runtime) RuntimeSkillManage(ctx context.Context, request toolskill.PackageRequest) (Result, error) {
	switch request.Action {
	case "activate", "rollback", "env_set", "env_unset", "env_list":
	default:
		return nil, toolError("RUNTIME_SKILL_ACTION_UNSUPPORTED", "Skill action is not available through the Runtime settings API", "validation")
	}
	return r.skills.Package(ctx, request)
}

func (r *Runtime) RuntimeTasks(options taskstate.ListOptions) (Result, error) {
	return r.taskTools.RuntimeTasks(options)
}

func (r *Runtime) RuntimeTask(id string) (Result, error) {
	return r.taskTools.RuntimeTask(id)
}

func (r *Runtime) RuntimeTaskDelete(id string) (Result, error) {
	return r.taskTools.RuntimeTaskDelete(id)
}

func (r *Runtime) RuntimeCapabilities(ctx context.Context, refresh bool) (Result, error) {
	result, err := r.AgentDockContext(ctx)
	if err != nil {
		return nil, err
	}
	result["source"] = runtimeAPISource
	return result, nil
}

func (r *Runtime) RuntimeMCPServers(ctx context.Context) (Result, error) {
	return r.runtimeMCPManage(ctx, map[string]any{"action": "list"})
}

func (r *Runtime) RuntimeMCPServer(ctx context.Context, name string) (Result, error) {
	return r.runtimeMCPManage(ctx, map[string]any{"action": "inspect", "name": name})
}

func (r *Runtime) RuntimeMCPManage(ctx context.Context, args map[string]any) (Result, error) {
	return r.runtimeMCPManage(ctx, args)
}

func (r *Runtime) runtimeMCPManage(ctx context.Context, args map[string]any) (Result, error) {
	if err := r.validateToolArguments(toolmcp.ToolManage, args); err != nil {
		return nil, err
	}
	var request toolmcp.ManageRequest
	if err := decodeToolInput("mcp_manage", args, &request); err != nil {
		return nil, err
	}
	result, err := r.dynamicMCP.Manage(ctx, request)
	if err != nil {
		return nil, err
	}
	result["ok"] = true
	result["source"] = runtimeAPISource
	return result, nil
}
