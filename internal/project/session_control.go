package project

import "strings"

// UsesSessionControlExecution 让 Bridge 准备与 Runtime 刷新使用同一上下文路径。
// 这里只选择上下文，不授予权限；所属会话和当前动作权限仍由对应服务检查。
func UsesSessionControlExecution(tool string, args map[string]any) bool {
	switch tool {
	case "session_observe", "session_act", "acp_session", "acp_prompt", "acp_interaction":
		return true
	case "browser_session":
		action, _ := args["action"].(string)
		return strings.EqualFold(strings.TrimSpace(action), "close")
	default:
		return false
	}
}
