package project

import "testing"

func TestSessionControlExecutionClassification(t *testing.T) {
	for _, test := range []struct {
		tool, action string
		want         bool
	}{
		{"session_observe", "status", true}, {"session_act", "kill", true},
		{"session_act", "write", true}, // 服务还须验证当前 stdin 权限。
		{"acp_session", "new", true}, {"acp_prompt", "start", true},
		{"acp_interaction", "cancel", true},
		{"browser_session", "close", true}, {"browser_session", " CLOSE ", true},
		{"browser_session", "start", false}, {"browser_session", "cleanup_stale", false},
		{"browser_session", "", false}, {"browser_act", "close", false},
		{"browser_snapshot", "close", false}, {"exec_command", "", false},
	} {
		t.Run(test.tool+"/"+test.action, func(t *testing.T) {
			if got := UsesSessionControlExecution(test.tool, map[string]any{"action": test.action, "close_after": true}); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
	if UsesSessionControlExecution("browser_session", nil) {
		t.Fatal("missing action allowed")
	}
}
