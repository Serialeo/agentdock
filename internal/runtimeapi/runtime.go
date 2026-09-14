package runtimeapi

import (
	"context"
	"encoding/json"
	"net/url"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/taskstate"
	toolfile "github.com/uvwt/agentdock/internal/tool/file"
	toolskill "github.com/uvwt/agentdock/internal/tool/skill"
)

// Runtime 定义 Runtime API 路由真正需要的应用能力。
// HTTP、Nexus Bridge 等传输层都只依赖这份传输无关契约。
type Runtime interface {
	RuntimeBuiltins() app.Result
	SetBuiltin(context.Context, protocol.BuiltinUpdate) (app.Result, error)
	RuntimeStatus() app.Result
	RuntimeSkills() (app.Result, error)
	RuntimeSkill(skill string) (app.Result, error)
	RuntimeSkillFiles(skill string) (app.Result, error)
	RuntimeSkillFile(skill, path string) (app.Result, error)
	RuntimeSkillManage(context.Context, toolskill.PackageRequest) (app.Result, error)
	RuntimeBrowseFiles(context.Context, toolfile.BrowseRequest) (app.Result, error)
	RuntimeTasks(taskstate.ListOptions) (app.Result, error)
	RuntimeTask(id string) (app.Result, error)
	RuntimeTaskDelete(id string) (app.Result, error)
	RuntimeCapabilities(context.Context, bool) (app.Result, error)
	RuntimeMCPServers(context.Context) (app.Result, error)
	RuntimeMCPServer(context.Context, string) (app.Result, error)
	RuntimeMCPManage(context.Context, map[string]any) (app.Result, error)
	RuntimeEvolve(context.Context, map[string]any) (app.Result, error)
}

// Request 是 HTTP 与 Nexus Bridge 共用的 Runtime API 请求表示。
type Request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  url.Values      `json:"query,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (r Request) queryValue(key string) string {
	if r.Query == nil {
		return ""
	}
	return r.Query.Get(key)
}
