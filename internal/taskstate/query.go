package taskstate

import (
	"strings"
	"time"
)

type ListOptions struct {
	Status    Status
	Query     string
	TimeField string
	From      *time.Time
	To        *time.Time
	Offset    int
	Limit     int
}

type TaskCounts struct {
	All       int `json:"all"`
	Active    int `json:"active"`
	Blocked   int `json:"blocked"`
	Completed int `json:"completed"`
}

type ListPage struct {
	Tasks   []Task
	Total   int
	Offset  int
	Limit   int
	HasMore bool
	Counts  TaskCounts
}

type ListQueryError struct {
	Code    string
	Message string
}

func (e *ListQueryError) Error() string { return e.Message }

// NormalizeListOptions 在访问任务目录前校验范围，避免错误查询触发全量状态读取。
func NormalizeListOptions(options ListOptions) (ListOptions, error) {
	options.Status = Status(strings.ToLower(strings.TrimSpace(string(options.Status))))
	if options.Status == "all" {
		options.Status = ""
	}
	switch options.Status {
	case "", StatusActive, StatusBlocked, StatusCompleted:
	default:
		return ListOptions{}, &ListQueryError{Code: "INVALID_STATUS", Message: "status must be all, active, blocked or completed"}
	}
	options.Query = strings.ToLower(strings.TrimSpace(options.Query))
	options.TimeField = strings.TrimSpace(options.TimeField)
	if options.TimeField == "" {
		options.TimeField = "updated_at"
	}
	if options.TimeField != "updated_at" && options.TimeField != "created_at" {
		return ListOptions{}, &ListQueryError{Code: "INVALID_TASK_QUERY", Message: "time_field must be updated_at or created_at"}
	}
	if options.From != nil && options.To != nil && !options.From.Before(*options.To) {
		return ListOptions{}, &ListQueryError{Code: "INVALID_TASK_QUERY", Message: "from must be earlier than to"}
	}
	if options.Offset < 0 {
		return ListOptions{}, &ListQueryError{Code: "INVALID_TASK_QUERY", Message: "offset must be a non-negative integer"}
	}
	if options.Limit == 0 {
		options.Limit = 50
	}
	if options.Limit < 1 || options.Limit > 200 {
		return ListOptions{}, &ListQueryError{Code: "INVALID_LIMIT", Message: "limit must be an integer between 1 and 200"}
	}
	return options, nil
}

func taskListTime(task Task, field string) time.Time {
	if field == "created_at" {
		return task.CreatedAt
	}
	return task.UpdatedAt
}

func taskMatchesListQuery(task Task, options ListOptions) bool {
	stamp := taskListTime(task, options.TimeField)
	if options.From != nil && stamp.Before(*options.From) {
		return false
	}
	if options.To != nil && !stamp.Before(*options.To) {
		return false
	}
	if options.Query == "" {
		return true
	}
	// 当前步骤的选择与 Runtime 列表一致：优先进行中的步骤，其次第一个待执行步骤。
	currentStep := ""
	foundCurrent := false
	for _, step := range task.Steps {
		if step.Status == StepInProgress {
			currentStep = step.Title
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		for _, step := range task.Steps {
			if step.Status == StepPending {
				currentStep = step.Title
				break
			}
		}
	}
	text := strings.Join([]string{task.ID, task.Title, task.Goal, string(task.Status), task.Summary, task.Blocker, currentStep}, " ")
	return strings.Contains(strings.ToLower(text), options.Query)
}
