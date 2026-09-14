package app

import (
	"context"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
)

func (r *Runtime) AgentDockContext(ctx context.Context) (Result, error) {
	return r.agentDockContext(ctx, false)
}

// AgentDockLocalContext 仅供 Nexus Bridge 使用。它不读取 Nexus 统一管理的
// Workflow/Recall，避免 fleet 聚合时按节点重复回灌共享上下文。
func (r *Runtime) AgentDockLocalContext(ctx context.Context) (Result, error) {
	return r.agentDockContext(ctx, true)
}

func (r *Runtime) agentDockContext(ctx context.Context, nexusLocalOnly bool) (Result, error) {
	skills, skillErr := r.skillCapabilityIndex()
	commonSkills, commonSkillErr := commonSkillCapabilityIndex(skills)
	contextResult := capabilityContext{
		Skills:            skills,
		CommonSkills:      commonSkills,
		DynamicMCP:        r.dynamicMCPCapabilityIndex(),
		WorkflowTemplates: []capabilityTemplateItem{},
	}
	if !nexusLocalOnly {
		// runtime 只保留模型操作主机所需的稳定环境事实；Nexus Bridge 已通过 Hello 持有这些节点事实，
		// 私有 context.local 不重复传输，避免两个来源长期漂移。
		contextResult.Runtime = &capabilityRuntimeContext{
			Version: buildinfo.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
			AgentDockHome: r.cfg.AgentDockHome, AgentDockDefaultDir: r.cfg.AgentDockDefaultDir,
			DefaultCWD: r.ws.DefaultDisplay(), PathModel: config.PathModel,
		}
	}
	if skillErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "skills", Message: "Skill 索引暂不可用。"})
	}
	if commonSkillErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "common_skills", Message: "通用 Skill 索引暂不可用。"})
	}

	if r.builtinAvailable("acp") {
		contextResult.ACP = &capabilityACPContext{
			Enabled:     true,
			Agent:       r.cfg.ACPAgentName,
			Description: "本机 Coding Agent 通道（Agent Client Protocol）。",
		}
	}

	if requiresNexus(r.cfg) && !nexusLocalOnly {
		templates, templateErr := r.templateCapabilityIndex(ctx)
		if templateErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "workflow_templates", Message: "工作流模板索引暂不可用。"})
		}
		memoryItems, memoryErr := r.memoryCapabilityIndex(ctx)
		if memoryErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "recall", Message: "记忆精简摘要暂不可用。"})
		}
		contextResult.WorkflowTemplates = templates
		contextResult.Recall = &capabilityRecallContext{Enabled: true, Items: memoryItems}
	}

	var result Result
	if err := remarshal(contextResult, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Runtime) agentDockContextTool(ctx context.Context, _ map[string]any) (Result, error) {
	return r.AgentDockContext(ctx)
}

type capabilityContext struct {
	Runtime           *capabilityRuntimeContext   `json:"runtime,omitempty"`
	Skills            []capabilitySkillItem       `json:"skills"`
	CommonSkills      *capabilityCommonSkillIndex `json:"common_skills,omitempty"`
	DynamicMCP        []capabilityDynamicMCPItem  `json:"dynamic_mcp"`
	ACP               *capabilityACPContext       `json:"acp,omitempty"`
	WorkflowTemplates []capabilityTemplateItem    `json:"workflow_templates"`
	Recall            *capabilityRecallContext    `json:"recall,omitempty"`
	Warnings          []capabilityWarning         `json:"warnings,omitempty"`
}

type capabilityRuntimeContext struct {
	Version             string `json:"version"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	AgentDockHome       string `json:"agentdock_home"`
	AgentDockDefaultDir string `json:"agentdock_default_dir"`
	DefaultCWD          string `json:"default_cwd"`
	PathModel           string `json:"path_model"`
}

type capabilitySkillItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
	Bundled     bool   `json:"bundled,omitempty"`
}

type capabilityCommonSkillIndex struct {
	Root      string                      `json:"root"`
	Total     int                         `json:"total"`
	Effective int                         `json:"effective"`
	Shadowed  int                         `json:"shadowed"`
	Truncated bool                        `json:"truncated"`
	Items     []capabilityCommonSkillItem `json:"items"`
}

type capabilityCommonSkillItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	File        string `json:"file"`
}

type capabilityDynamicMCPItem struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	ToolCount     int    `json:"tool_count"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

type capabilityACPContext struct {
	Enabled     bool   `json:"enabled"`
	Agent       string `json:"agent"`
	Description string `json:"description"`
}

type capabilityTemplateItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type capabilityMemoryItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type capabilityRecallContext struct {
	Enabled bool                   `json:"enabled"`
	Items   []capabilityMemoryItem `json:"items"`
}

type capabilityWarning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type capabilityTemplateList struct {
	Templates []capabilityTemplateListItem `json:"templates"`
}

type capabilityTemplateListItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type capabilityRecallContextIndexResponse struct {
	ContextIndex capabilityRecallContextIndex `json:"context_index"`
}

type capabilityRecallContextIndex struct {
	Items     []capabilityRecallIndexItem `json:"items"`
	Truncated bool                        `json:"truncated"`
}

type capabilityRecallIndexItem struct {
	Kind     string   `json:"kind"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Keywords []string `json:"keywords"`
	Aliases  []string `json:"aliases"`
	Tags     []string `json:"tags"`
	CardType string   `json:"card_type"`
}

func (r *Runtime) dynamicMCPCapabilityIndex() []capabilityDynamicMCPItem {
	servers := r.dynamicMCP.CapabilityItems()
	items := make([]capabilityDynamicMCPItem, 0, len(servers))
	for _, server := range servers {
		items = append(items, capabilityDynamicMCPItem{
			Name:          server.Name,
			Description:   truncateString(strings.TrimSpace(server.Description), 160),
			Status:        server.Status,
			ToolCount:     server.ToolCount,
			LastErrorCode: server.LastErrorCode,
		})
	}
	return items
}

func (r *Runtime) skillCapabilityIndex() ([]capabilitySkillItem, error) {
	skillItems, err := r.skills.CapabilityItems()
	if err != nil {
		return []capabilitySkillItem{}, err
	}
	items := make([]capabilitySkillItem, 0, len(skillItems))
	for _, skill := range skillItems {
		items = append(items, capabilitySkillItem{
			Name:        skill.Name,
			Description: truncateString(strings.TrimSpace(skill.Description), 160),
			File:        skill.File,
			Bundled:     skill.Bundled,
		})
	}
	return items, nil
}

func (r *Runtime) templateCapabilityIndex(ctx context.Context) ([]capabilityTemplateItem, error) {
	result, err := r.taskTools.WorkflowManage(ctx, tooltask.WorkflowRequest{Action: "list", TemplateStatus: "active"})
	if err != nil {
		return []capabilityTemplateItem{}, err
	}
	var listed capabilityTemplateList
	if err := remarshal(result, &listed); err != nil {
		return []capabilityTemplateItem{}, err
	}
	items := make([]capabilityTemplateItem, 0, len(listed.Templates))
	for _, listedItem := range listed.Templates {
		name := strings.TrimSpace(listedItem.ID)
		if name == "" {
			continue
		}
		items = append(items, capabilityTemplateItem{Name: name, Description: truncateString(strings.TrimSpace(listedItem.Title), 160)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (r *Runtime) memoryCapabilityIndex(ctx context.Context) ([]capabilityMemoryItem, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(capMaxInt(1000, capMinInt(config.RecallTimeoutMS, 5000)))*time.Millisecond)
	defer cancel()
	result, err := r.recall.ContextIndex(ctx, 3000)
	if err != nil {
		return []capabilityMemoryItem{}, err
	}
	var response capabilityRecallContextIndexResponse
	if err := remarshal(result, &response); err != nil {
		return []capabilityMemoryItem{}, err
	}
	items := make([]capabilityMemoryItem, 0, len(response.ContextIndex.Items))
	seen := make(map[string]struct{}, len(response.ContextIndex.Items))
	for _, item := range response.ContextIndex.Items {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		items = append(items, capabilityMemoryItem{Name: path, Description: recallIndexDescription(item)})
	}
	return items, nil
}

func recallIndexDescription(item capabilityRecallIndexItem) string {
	if summary := strings.TrimSpace(item.Summary); summary != "" {
		if title := strings.TrimSpace(item.Title); title != "" {
			return truncateString(title+" — "+summary, 360)
		}
		return truncateString(summary, 360)
	}
	parts := []string{}
	if title := strings.TrimSpace(item.Title); title != "" {
		parts = append(parts, title)
	}
	if kind := strings.TrimSpace(item.Kind); kind != "" {
		parts = append(parts, kind)
	}
	if cardType := strings.TrimSpace(item.CardType); cardType != "" {
		parts = append(parts, cardType)
	}
	labels := append(append(append([]string{}, item.Keywords...), item.Aliases...), item.Tags...)
	if len(labels) > 0 {
		parts = append(parts, strings.Join(labels, ", "))
	}
	return truncateString(strings.Join(parts, " · "), 360)
}

func capMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func capMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
