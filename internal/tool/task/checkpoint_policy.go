package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/nexusclient"
)

type checkpointPolicy struct {
	Source      string   `json:"source"`
	Version     string   `json:"version"`
	Enforcement string   `json:"enforcement"`
	Rules       []string `json:"rules"`
	Prompt      string   `json:"prompt"`
	Warning     string   `json:"warning,omitempty"`
}

// 任务生命周期规则独立于 Project Prompt；每次交付当前版本，不从旧节点指令隐式恢复。
func defaultCheckpointPolicy() checkpointPolicy {
	return checkpointPolicy{
		Source:      "agentdock/task_manage",
		Version:     "1",
		Enforcement: "caller_driven",
		Prompt:      "Call task_manage checkpoint when a recoverable milestone is reached, during long steps when useful progress is made, and before pausing or handing off. Do not defer all progress records until final_review. Include completed work, evidence or artifact references, remaining risks or blockers, and the next action in summary so another session can resume.",
		Rules: []string{
			"Every checkpoint requires task_id and a nonempty summary. The prompt guides checkpoint content and timing; it does not change required fields or task state constraints.",
			"For progress without a step transition, supply task_id and summary only. This also works for tasks created without steps. Use step_id with status, or completed_step_ids/current_step_id, only when updating step state.",
			"Read the saved task with get before continuing; resume blocked tasks before checkpointing. All steps must be completed and final_review must pass before complete. Checkpoints are caller-submitted; the server does not schedule them or verify their frequency.",
		},
	}
}

// 提示词属于任务服务；各调用通道都拿到同一策略，不依赖网关或项目 AGENTS.md 注入。
func (s *Service) currentCheckpointPolicy(ctx context.Context) (checkpointPolicy, error) {
	policy := defaultCheckpointPolicy()
	if err := ctx.Err(); err != nil {
		return policy, err
	}
	cfg := s.config()
	if strings.TrimSpace(cfg.NexusEndpoint) == "" {
		return policy, nil
	}
	prompt, revision, err := loadNexusCheckpointPrompt(ctx, cfg)
	if err != nil {
		if ctx.Err() != nil {
			return policy, ctx.Err()
		}
		// 离线时仍允许恢复本地任务，但必须明确交付失败和当前使用的内置规则。
		policy.Warning = "Nexus checkpoint prompt unavailable; using built-in checkpoint guidance: " + err.Error()
		return policy, nil
	}
	policy.Source, policy.Version, policy.Prompt = "nexusdock/checkpoint", revision, prompt
	return policy, nil
}

func loadNexusCheckpointPrompt(ctx context.Context, cfg config.Config) (string, string, error) {
	if strings.TrimSpace(cfg.NexusDeviceToken) == "" {
		return "", "", errors.New("paired Device Token is unavailable")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := nexusclient.New(cfg.NexusEndpoint, cfg.NexusDeviceToken)
	response, err := client.Do(requestCtx, http.MethodGet, "/v1/settings/checkpoint", nil)
	if err != nil {
		return "", "", fmt.Errorf("fetch checkpoint prompt: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("checkpoint prompt returned HTTP %d", response.StatusCode)
	}
	data, err := nexusclient.ReadBoundedBody(response.Body, 128*1024)
	if err != nil {
		return "", "", err
	}
	var result struct {
		OK       bool `json:"ok"`
		Settings struct {
			Prompt   string `json:"prompt"`
			Revision string `json:"revision"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", "", errors.New("checkpoint prompt response is not valid JSON")
	}
	if !result.OK || strings.TrimSpace(result.Settings.Prompt) == "" || len(result.Settings.Prompt) > 8*1024 || !utf8.ValidString(result.Settings.Prompt) || strings.TrimSpace(result.Settings.Revision) == "" {
		return "", "", errors.New("checkpoint prompt response is incomplete or exceeds its size limit")
	}
	return result.Settings.Prompt, result.Settings.Revision, nil
}
