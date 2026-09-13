package runtimeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/uvwt/agentdock/internal/app"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

// MethodAllowed 返回指定 Runtime API 路径允许当前方法与否。
// HTTP 入口用它生成 405，其他传输也复用同一份方法契约。
func MethodAllowed(method, path string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	cleanPath := strings.TrimSuffix(strings.TrimSpace(path), "/")
	if method == http.MethodGet {
		return true
	}
	if method == http.MethodDelete {
		_, ok := runtimeTaskID(cleanPath)
		return ok
	}
	return method == http.MethodPost && (cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" || cleanPath == "/internal/runtime/evolve" || cleanPath == "/internal/runtime/skills/manage")
}

func AllowHeader(path string) string {
	cleanPath := strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, ok := runtimeTaskID(cleanPath); ok {
		return "GET, DELETE"
	}
	if cleanPath == "/internal/runtime/capabilities" || cleanPath == "/internal/runtime/mcp" {
		return "GET, POST"
	}
	if cleanPath == "/internal/runtime/skills/manage" {
		return "POST"
	}
	if cleanPath == "/internal/runtime/evolve" {
		return "POST"
	}
	return "GET"
}

// Dispatch 只负责 Runtime API 的路由、参数校验与应用能力调用，不感知 HTTP request/response。
func Dispatch(ctx context.Context, runtime Runtime, request Request) (map[string]any, error) {
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	path := strings.TrimSuffix(strings.TrimSpace(request.Path), "/")
	if !MethodAllowed(method, path) {
		return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime API route not found", Category: "not_found"}
	}

	taskID, isTaskPath := runtimeTaskID(path)
	switch {
	case path == "/internal/runtime/status":
		return map[string]any(runtime.RuntimeStatus()), nil
	case path == "/internal/runtime/capabilities":
		refresh := strings.EqualFold(request.queryValue("refresh"), "true") || method == http.MethodPost
		result, err := runtime.RuntimeCapabilities(ctx, refresh)
		return map[string]any(result), err
	case path == "/internal/runtime/files":
		browseRequest, err := decodeRuntimeBrowseRequest(request)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeBrowseFiles(ctx, browseRequest)
		return map[string]any(result), err
	case path == "/internal/runtime/skills":
		result, err := runtime.RuntimeSkills()
		return map[string]any(result), err
	case path == "/internal/runtime/skills/manage" && method == http.MethodPost:
		request, err := decodeRuntimeSkillManageRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeSkillManage(ctx, request)
		return map[string]any(result), err
	case strings.HasPrefix(path, "/internal/runtime/skills/"):
		skill, filePath, action, ok := runtimeSkillRoute(path)
		if !ok {
			return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime Skill API route not found", Category: "not_found"}
		}
		switch action {
		case "detail":
			result, err := runtime.RuntimeSkill(skill)
			return map[string]any(result), err
		case "files":
			result, err := runtime.RuntimeSkillFiles(skill)
			return map[string]any(result), err
		case "file":
			result, err := runtime.RuntimeSkillFile(skill, filePath)
			return map[string]any(result), err
		default:
			return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime Skill API route not found", Category: "not_found"}
		}
	case path == "/internal/runtime/evolve" && method == http.MethodPost:
		args, err := decodeRuntimeEvolutionRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeEvolve(ctx, args)
		return map[string]any(result), err
	case path == "/internal/runtime/mcp" && method == http.MethodPost:
		args, err := decodeRuntimeMCPRequest(request.Body)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeMCPManage(ctx, args)
		return map[string]any(result), err
	case path == "/internal/runtime/mcp":
		result, err := runtime.RuntimeMCPServers(ctx)
		return map[string]any(result), err
	case strings.HasPrefix(path, "/internal/runtime/mcp/"):
		name, ok := runtimeMCPName(path)
		if !ok {
			return nil, &app.ToolError{Code: "MCP_NAME_REQUIRED", Message: "dynamic MCP server name is required", Category: "validation"}
		}
		result, err := runtime.RuntimeMCPServer(ctx, name)
		return map[string]any(result), err
	case path == "/internal/runtime/tasks":
		options, err := decodeRuntimeTaskQuery(request)
		if err != nil {
			return nil, err
		}
		result, err := runtime.RuntimeTasks(options)
		return map[string]any(result), err
	case isTaskPath && method == http.MethodDelete:
		result, err := runtime.RuntimeTaskDelete(taskID)
		return map[string]any(result), err
	case isTaskPath:
		result, err := runtime.RuntimeTask(taskID)
		return map[string]any(result), err
	default:
		return nil, &app.ToolError{Code: "NOT_FOUND", Message: "runtime API route not found", Category: "not_found"}
	}
}

func decodeRuntimeBrowseRequest(request Request) (toolfile.BrowseRequest, error) {
	for key, values := range request.Query {
		switch key {
		case "path", "offset", "limit", "include_hidden":
		default:
			return toolfile.BrowseRequest{}, &app.ToolError{Code: "INVALID_FILE_BROWSE_QUERY", Message: "unsupported file browse query parameter", Category: "validation"}
		}
		if len(values) > 1 {
			return toolfile.BrowseRequest{}, &app.ToolError{Code: "INVALID_FILE_BROWSE_QUERY", Message: "file browse query parameters must not be repeated", Category: "validation"}
		}
	}

	browse := toolfile.BrowseRequest{Path: request.queryValue("path")}
	if raw := strings.TrimSpace(request.queryValue("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > toolfile.MaxBrowseOffset {
			return toolfile.BrowseRequest{}, &app.ToolError{Code: "INVALID_FILE_BROWSE_QUERY", Message: "offset must be a bounded non-negative integer", Category: "validation"}
		}
		browse.Offset = &value
	}
	if raw := strings.TrimSpace(request.queryValue("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > toolfile.MaxBrowseLimit {
			return toolfile.BrowseRequest{}, &app.ToolError{Code: "INVALID_FILE_BROWSE_QUERY", Message: "limit is outside the allowed range", Category: "validation"}
		}
		browse.Limit = &value
	}
	if raw := strings.TrimSpace(request.queryValue("include_hidden")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return toolfile.BrowseRequest{}, &app.ToolError{Code: "INVALID_FILE_BROWSE_QUERY", Message: "include_hidden must be a boolean", Category: "validation"}
		}
		browse.IncludeHidden = value
	}
	return browse, nil
}

type runtimeSkillManageRequest struct {
	Action  string  `json:"action"`
	Skill   string  `json:"skill"`
	Version string  `json:"version,omitempty"`
	Key     string  `json:"key,omitempty"`
	Value   *string `json:"value,omitempty"`
}

func decodeRuntimeSkillManageRequest(body []byte) (toolskill.PackageRequest, error) {
	if len(body) > 64*1024 {
		return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "Skill settings request body is too large", Category: "validation"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request runtimeSkillManageRequest
	if err := decoder.Decode(&request); err != nil {
		return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "invalid Skill settings request body", Category: "validation"}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "request body must contain exactly one JSON value", Category: "validation"}
	}
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.Skill = strings.TrimSpace(request.Skill)
	request.Version = strings.TrimSpace(request.Version)
	request.Key = strings.TrimSpace(request.Key)
	if request.Skill == "" {
		return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "skill is required", Category: "validation"}
	}
	switch request.Action {
	case "activate":
		if request.Version == "" || request.Key != "" || request.Value != nil {
			return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "activate requires version and does not accept environment fields", Category: "validation"}
		}
	case "rollback":
		if request.Version != "" || request.Key != "" || request.Value != nil {
			return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "rollback accepts only skill", Category: "validation"}
		}
	case "env_list":
		if request.Version != "" || request.Key != "" || request.Value != nil {
			return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "env_list accepts only skill", Category: "validation"}
		}
	case "env_set":
		if request.Key == "" || request.Value == nil || request.Version != "" {
			return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "env_set requires key and an explicit value", Category: "validation"}
		}
	case "env_unset":
		if request.Key == "" || request.Value != nil || request.Version != "" {
			return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "env_unset requires key and no value", Category: "validation"}
		}
	default:
		return toolskill.PackageRequest{}, &app.ToolError{Code: "INVALID_SKILL_REQUEST", Message: "action must be activate, rollback, env_list, env_set, or env_unset", Category: "validation"}
	}
	return toolskill.PackageRequest{Action: request.Action, Skill: request.Skill, Version: request.Version, Key: request.Key, Value: request.Value}, nil
}

type runtimeMCPRequest struct {
	Action      string            `json:"action"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Cwd         string            `json:"cwd"`
	HeaderEnv   map[string]string `json:"header_env"`
	EnvFromEnv  map[string]string `json:"env_from_env"`
	Enabled     *bool             `json:"enabled"`
	TimeoutMS   int               `json:"timeout_ms"`
	Key         string            `json:"key"`
	Value       *string           `json:"value"`
}

var runtimeMCPManageActions = map[string]bool{
	"add": true, "remove": true, "enable": true, "disable": true,
	"env_set": true, "env_unset": true, "env_list": true, "refresh": true,
}

func decodeRuntimeMCPRequest(body []byte) (map[string]any, error) {
	if len(body) > 64*1024 {
		return nil, runtimeMCPRequestError("MCP request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request runtimeMCPRequest
	if err := decoder.Decode(&request); err != nil {
		return nil, runtimeMCPRequestError("invalid MCP request body")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, runtimeMCPRequestError("request body must contain exactly one JSON value")
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	if action == "" {
		return nil, &app.ToolError{Code: "MCP_ACTION_REQUIRED", Message: "dynamic MCP action is required", Category: "validation"}
	}
	if !runtimeMCPManageActions[action] {
		return nil, &app.ToolError{Code: "MCP_ACTION_UNSUPPORTED", Message: "dynamic MCP action is not available through the Runtime API", Category: "validation"}
	}
	args := map[string]any{"action": action}
	// Runtime API 与模型工具最终进入同一份公共契约。只转发请求中真正提供的可选字段，
	// 避免 Go 零值被解释成 schema 中有语义的空 enum 值或未声明允许的 null。
	if request.Name != "" {
		args["name"] = request.Name
	}
	if request.Description != "" {
		args["description"] = request.Description
	}
	if request.Transport != "" {
		args["transport"] = request.Transport
	}
	if request.URL != "" {
		args["url"] = request.URL
	}
	if request.Command != "" {
		args["command"] = request.Command
	}
	if request.Cwd != "" {
		args["cwd"] = request.Cwd
	}
	if request.Key != "" {
		args["key"] = request.Key
	}
	if request.Args != nil {
		args["args"] = request.Args
	}
	if request.HeaderEnv != nil {
		args["header_env"] = request.HeaderEnv
	}
	if request.EnvFromEnv != nil {
		args["env_from_env"] = request.EnvFromEnv
	}
	if request.Value != nil {
		args["value"] = *request.Value
	}
	if request.Enabled != nil {
		args["enabled"] = *request.Enabled
	}
	if request.TimeoutMS > 0 {
		args["timeout_ms"] = request.TimeoutMS
	}
	return args, nil
}

func runtimeMCPRequestError(message string) error {
	return &app.ToolError{Code: "INVALID_MCP_REQUEST", Message: message, Category: "validation"}
}

func runtimeSkillRoute(path string) (skill, filePath, action string, ok bool) {
	const prefix = "/internal/runtime/skills/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", "", "", false
	}
	skill = parts[0]
	switch {
	case len(parts) == 1:
		return skill, "", "detail", true
	case len(parts) == 2 && parts[1] == "files":
		return skill, "", "files", true
	case len(parts) >= 3 && parts[1] == "files":
		filePath = strings.Join(parts[2:], "/")
		if strings.TrimSpace(filePath) == "" {
			return "", "", "", false
		}
		return skill, filePath, "file", true
	default:
		return "", "", "", false
	}
}

func runtimeMCPName(path string) (string, bool) {
	const prefix = "/internal/runtime/mcp/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func runtimeTaskID(path string) (string, bool) {
	const prefix = "/internal/runtime/tasks/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(path, prefix)
	if strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}
