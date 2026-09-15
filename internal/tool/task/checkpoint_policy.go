package task

type checkpointPolicy struct {
	Source      string   `json:"source"`
	Version     string   `json:"version"`
	Enforcement string   `json:"enforcement"`
	Rules       []string `json:"rules"`
}

// 任务生命周期规则独立于 Project Prompt；每次交付当前版本，不从旧节点指令隐式恢复。
func defaultCheckpointPolicy() checkpointPolicy {
	return checkpointPolicy{
		Source:      "agentdock/task_manage",
		Version:     "1",
		Enforcement: "caller_driven",
		Rules: []string{
			"Call task_manage checkpoint when a recoverable milestone is reached, during long steps when useful progress is made, and before pausing or handing off. Do not defer all progress records until final_review.",
			"Include completed work, evidence or artifact references, remaining risks or blockers, and the next action in summary so another session can resume.",
			"For progress without a step transition, supply task_id and summary only. This also works for tasks created without steps. Use step_id with status, or completed_step_ids/current_step_id, only when updating step state.",
			"Read the saved task with get before continuing; resume blocked tasks before checkpointing. All steps must be completed and final_review must pass before complete. Checkpoints are caller-submitted; the server does not schedule them or verify their frequency.",
		},
	}
}
