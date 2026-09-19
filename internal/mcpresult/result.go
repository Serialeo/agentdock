package mcpresult

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	protocol "github.com/Serialeo/agentdock-protocol"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MaxSummaryRunes = 256

// Host 直接回传的交付标识，不进入模型的结构化业务结果。
const ProjectDeliveryMetaKey = protocol.ProjectContextDeliveryMetaKey

func Normalize(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tool result object: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// Build 接受 Runtime 对象和 Bridge MCP envelope，但绝不把 envelope 当作命令结果裁剪。
// 正文保存在 structuredContent；TextContent 是有界摘要，不再复制完整 JSON。
func Build(name string, value any, failed bool) (*mcpsdk.CallToolResult, error) {
	input, err := Normalize(value)
	if err != nil {
		return nil, err
	}
	response := &mcpsdk.CallToolResult{IsError: failed}
	payload := input
	_, hasStructured := input["structuredContent"]
	_, hasFlag := input["isError"].(bool)
	_, contentArray := input["content"].([]any)
	wrapped := contentArray && (hasStructured || hasFlag)
	if wrapped {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, response); err != nil {
			return nil, fmt.Errorf("decode Bridge tool result: %w", err)
		}
		response.IsError = response.IsError || failed
		// 未识别的上游工具或纯 content 返回不属于本服务可重写的结构化结果。
		if !hasStructured || !knownTool(name) {
			if hasStructured {
				response.StructuredContent = input["structuredContent"]
			}
			return response, nil
		}
		payload, err = Normalize(input["structuredContent"])
		if err != nil {
			return nil, err
		}
	}
	if name == "project_open" || name == "project_context" {
		revision, _ := payload["context_revision"].(string)
		identity := map[string]any{"work_session_id": payload["work_session_id"]}
		if target, ok := payload["target"].(map[string]any); ok {
			revision, _ = target["context_revision"].(string)
			identity["target_id"] = target["target_id"]
		}
		if revision != "" {
			identity["context_revision"] = revision
			if response.Meta == nil {
				response.Meta = mcpsdk.Meta{}
			}
			response.Meta[ProjectDeliveryMetaKey] = identity
		}
	}
	if name == "mcp_tool_call" {
		remote, ok := payload["result"].(map[string]any)
		if ok {
			// 上游 content 只在顶层传一次；structuredContent 不再嵌套文本/图片副本。
			if content, exists := remote["content"]; exists {
				encoded, err := json.Marshal(map[string]any{"content": content, "isError": truth(remote["isError"]), "_meta": remote["_meta"]})
				if err != nil {
					return nil, err
				}
				var upstream mcpsdk.CallToolResult
				if err := json.Unmarshal(encoded, &upstream); err != nil {
					return nil, fmt.Errorf("decode upstream MCP content: %w", err)
				}
				response.Content = upstream.Content
				response.IsError = response.IsError || upstream.IsError
				if response.Meta == nil {
					response.Meta = mcpsdk.Meta{}
				}
				for k, v := range upstream.Meta {
					response.Meta[k] = v
				}
				delete(remote, "content")
				delete(remote, "_meta")
			}
			response.IsError = response.IsError || truth(remote["isError"])
			response.Content = withoutSerializedDuplicate(response.Content, remote["structuredContent"])
		}
		response.StructuredContent = payload
		if len(response.Content) == 0 {
			response.Content = []mcpsdk.Content{&mcpsdk.TextContent{Text: Summary(name, payload, response.IsError)}}
		}
		return response, nil
	}
	if raw, ok := payload["_mcp_image_base64"].(string); ok && name == "view_image" {
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode tool image: %w", err)
		}
		mime, _ := payload["_mcp_image_mime_type"].(string)
		response.Content = []mcpsdk.Content{&mcpsdk.ImageContent{Data: data, MIMEType: mime}}
		remove(payload, "_mcp_image_base64", "_mcp_image_mime_type")
	}
	if ok, exists := payload["ok"].(bool); exists && !ok && knownTool(name) {
		response.IsError = true
	}
	if success, ok := payload["browser_ok"].(bool); ok && !success {
		response.IsError = true
	}
	if response.IsError {
		if strings.HasPrefix(name, "browser_") {
			payload = Project(name, payload)
		}
		payload = Error(payload)
	} else {
		payload = Project(name, payload)
	}
	response.StructuredContent = payload
	// 保留图像、音频和资源块；只替换本服务生成的文本副本。
	content := make([]mcpsdk.Content, 0, len(response.Content)+1)
	for _, item := range response.Content {
		if _, text := item.(*mcpsdk.TextContent); !text {
			content = append(content, item)
		}
	}
	if len(content) == 0 || name != "view_image" {
		content = append([]mcpsdk.Content{&mcpsdk.TextContent{Text: Summary(name, payload, response.IsError)}}, content...)
	}
	response.Content = content
	return response, nil
}

func Summary(name string, payload map[string]any, failed bool) string {
	text := name + " result"
	if failed {
		text = name + " failed"
		if message, ok := payload["error"].(string); ok && message != "" {
			text = message
		}
		if detail, ok := payload["error"].(map[string]any); ok {
			if message, ok := detail["message"].(string); ok && message != "" {
				text = message
			}
		}
	} else if name == "file_edit" {
		if summary, ok := payload["summary"].(string); ok && summary != "" {
			text = summary
		}
	} else if name == "exec_command" || name == "session_observe" || name == "session_act" {
		switch {
		case payload["status"] != nil:
			text = "command " + fmt.Sprint(payload["status"])
		case truth(payload["timed_out"]):
			text = "command timed out"
		case payload["command_error"] != nil:
			text = "command failed: " + fmt.Sprint(payload["command_error"])
		case payload["exit_code"] != nil:
			text = "command exited: " + fmt.Sprint(payload["exit_code"])
		}
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > MaxSummaryRunes {
		return string(runes[:MaxSummaryRunes]) + "…"
	}
	return string(runes)
}

// 固定规则放在工具定义中只交付一次，而不是每个 checkpoint/get/resume 回包重复。
func Description(name, description string) string {
	const orientation = " Save a checkpoint at recoverable milestones, during long steps, and before handoff. Include work, evidence, blockers and next action. Summary-only checkpoints need task_id and summary. Read saved state before continuing; resume blocked tasks, complete all steps, and pass final_review before complete. Checkpoints are caller-driven, not scheduled."
	if name == "task_manage" && !strings.Contains(description, orientation) {
		description += orientation
	}
	return description
}

// 仅移除与 structuredContent 完全等价、且没有独立注解的 JSON 文本副本。
// 独立解释文本、注解以及图片/音频/资源块必须原样保留。
func withoutSerializedDuplicate(content []mcpsdk.Content, structured any) []mcpsdk.Content {
	if structured == nil {
		return content
	}
	canonical, err := json.Marshal(structured)
	if err != nil {
		return content
	}
	out := make([]mcpsdk.Content, 0, len(content))
	for _, item := range content {
		text, ok := item.(*mcpsdk.TextContent)
		duplicate := false
		if ok && text.Annotations == nil && len(text.Meta) == 0 && json.Valid([]byte(text.Text)) {
			decoder := json.NewDecoder(strings.NewReader(text.Text))
			decoder.UseNumber()
			var decoded any
			if decoder.Decode(&decoded) == nil {
				encoded, err := json.Marshal(decoded)
				duplicate = err == nil && bytes.Equal(encoded, canonical)
			}
		}
		if !duplicate {
			out = append(out, item)
		}
	}
	return out
}
func knownTool(name string) bool {
	if _, ok := removed[name]; ok {
		return true
	}
	switch name {
	case "file_edit", "list_dir", "exec_command", "session_observe", "session_act", "mcp_tool_call", "mcp_tool_inspect", "view_image", "browser_session", "browser_act", "browser_snapshot", "agentdock_context", "project_open", "project_context", "evolve":
		return true
	}
	return false
}
