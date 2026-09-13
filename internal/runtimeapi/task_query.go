package runtimeapi

import (
	"strconv"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/taskstate"
)

func decodeRuntimeTaskQuery(request Request) (taskstate.ListOptions, error) {
	for key, values := range request.Query {
		switch key {
		case "status", "q", "time_field", "from", "to", "offset", "limit":
		default:
			return taskstate.ListOptions{}, invalidTaskQuery("unsupported task query parameter: " + key)
		}
		if len(values) != 1 {
			return taskstate.ListOptions{}, invalidTaskQuery("task query parameters must have exactly one value: " + key)
		}
	}
	options := taskstate.ListOptions{
		Status: taskstate.Status(request.queryValue("status")), Query: request.queryValue("q"),
		TimeField: request.queryValue("time_field"), Limit: 50,
	}
	if raw, present := request.Query["limit"]; present {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value < 1 || value > 200 {
			return taskstate.ListOptions{}, &app.ToolError{
				Code: "INVALID_LIMIT", Message: "limit must be an integer between 1 and 200", Category: "validation",
				Details: map[string]any{"limit": raw[0], "minimum": 1, "maximum": 200},
			}
		}
		options.Limit = value
	}
	if raw, present := request.Query["offset"]; present {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value < 0 {
			return taskstate.ListOptions{}, invalidTaskQuery("offset must be a non-negative integer")
		}
		options.Offset = value
	}
	for _, boundary := range []struct {
		key         string
		destination **time.Time
	}{{"from", &options.From}, {"to", &options.To}} {
		if raw, present := request.Query[boundary.key]; present {
			value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw[0]))
			if err != nil {
				return taskstate.ListOptions{}, invalidTaskQuery(boundary.key + " must be an RFC3339 timestamp with a timezone")
			}
			*boundary.destination = &value
		}
	}
	options, err := taskstate.NormalizeListOptions(options)
	if err != nil {
		queryErr := err.(*taskstate.ListQueryError)
		return taskstate.ListOptions{}, &app.ToolError{Code: queryErr.Code, Message: queryErr.Message, Category: "validation"}
	}
	return options, nil
}

func invalidTaskQuery(message string) error {
	return &app.ToolError{Code: "INVALID_TASK_QUERY", Message: message, Category: "validation"}
}
