package api

import "context"

// Scheduler accepts tasks and routes them to executors across the mesh.
type Scheduler interface {
    // Submit enqueues a task for execution and returns a stream of results.
    Submit(ctx context.Context, req TaskRequest) (<-chan TaskResult, error)
}
