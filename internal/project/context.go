package project

import "context"

type executionContextKey struct{}

func WithExecution(ctx context.Context, execution Execution) context.Context {
	return context.WithValue(ctx, executionContextKey{}, execution)
}

func ExecutionFromContext(ctx context.Context) (Execution, bool) {
	if ctx == nil {
		return Execution{}, false
	}
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}
