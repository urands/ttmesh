package api

import "context"

// Executor executes tasks of a certain kind and emits results.
type Executor interface {
    // CanHandle returns true if this executor can process given task name.
    CanHandle(name string) bool

    // Execute runs the task and returns a channel of results.
    // For unary tasks, implementations may send a single result then close.
    Execute(ctx context.Context, req TaskRequest) (<-chan TaskResult, error)
}
