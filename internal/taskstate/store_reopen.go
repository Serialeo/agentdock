package taskstate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func invalidateReview(task *Task, stepID, reason string, now time.Time) error {
	if task.FinalReview == nil {
		return nil
	}
	if len(task.ReviewHistory) >= maxTaskEvents {
		return errors.New("task review history limit reached")
	}
	task.ReviewHistory = append(task.ReviewHistory, ReviewInvalidation{Review: *task.FinalReview, StepID: stepID, Reason: reason, InvalidatedAt: now})
	task.FinalReview = nil
	task.EvolutionCandidates = nil
	return nil
}

// Reopen 是唯一允许已完成步骤回到执行中的入口；归档任务仍不可变。
func (s *Store) Reopen(id, stepID, reason string) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if err := requireActive(task); err != nil {
			return err
		}
		stepID, reason = strings.TrimSpace(stepID), strings.TrimSpace(reason)
		if stepID == "" || reason == "" {
			return errors.New("step_id and reopen_reason are required")
		}
		if err := validateTextLimit("reopen reason", reason, maxTaskSummaryBytes); err != nil {
			return err
		}
		index := -1
		for i, step := range task.Steps {
			if step.ID == stepID {
				index = i
			}
			if step.Status == StepInProgress {
				return fmt.Errorf("step %s is already in progress", step.ID)
			}
		}
		if index < 0 {
			return fmt.Errorf("task step %s not found", stepID)
		}
		step := &task.Steps[index]
		if step.Status != StepCompleted {
			return errors.New("only completed steps can be reopened")
		}
		if len(task.StepRevisions) >= maxTaskEvents {
			return errors.New("task step revision history limit reached")
		}
		if err := invalidateReview(task, stepID, reason, now); err != nil {
			return err
		}
		step.Status = StepInProgress
		step.Revision++
		task.StepRevisions = append(task.StepRevisions, StepRevision{StepID: stepID, Revision: step.Revision, Reason: reason, CreatedAt: now})
		step.UpdatedAt = now
		task.Phase = step.Phase
		task.Summary = reason
		task.EvolutionCandidates = nil
		appendTaskEvent(task, Event{Type: "step_reopened", Summary: stepID + ": " + reason, CreatedAt: now})
		return nil
	})
}

func normalizeConditionEvidence(task *Task, input []ConditionEvidence) ([]ConditionEvidence, error) {
	if len(input) > maxTaskReviewItems {
		return nil, errors.New("too many condition evidence entries")
	}
	known := make(map[string]bool, len(task.Conditions))
	for _, condition := range task.Conditions {
		known[condition.ID] = true
	}
	out := make([]ConditionEvidence, 0, len(input))
	for _, item := range input {
		item.ConditionID = strings.TrimSpace(item.ConditionID)
		item.Summary = strings.TrimSpace(item.Summary)
		item.EvidenceRef = strings.TrimSpace(item.EvidenceRef)
		if !known[item.ConditionID] {
			return nil, fmt.Errorf("unknown completion condition %q", item.ConditionID)
		}
		if item.Summary == "" {
			return nil, errors.New("condition evidence summary is required")
		}
		if item.Source != "" && item.Source != "self_reported" {
			return nil, errors.New("caller evidence must be self_reported; machine_verified requires a trusted verifier")
		}
		if err := validateTextLimit("evidence summary", item.Summary, maxTaskReviewItemBytes); err != nil {
			return nil, err
		}
		if err := validateTextLimit("evidence reference", item.EvidenceRef, maxTaskReviewItemBytes); err != nil {
			return nil, err
		}
		item.Source = "self_reported"
		out = append(out, item)
	}
	return out, nil
}
