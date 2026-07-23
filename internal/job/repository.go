package job

import "context"

// JobRepository defines the persistence interface for jobs.
type JobRepository interface {
	Create(ctx context.Context, j *Job) error
	Update(ctx context.Context, j *Job) error
	GetByID(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context) ([]Job, error)
	ListRecoverable(ctx context.Context) ([]Job, error)
}

// EventBus defines the interface for publishing and subscribing to events.
type EventBus interface {
	Publish(event Event)
	Subscribe() <-chan Event
	Unsubscribe(ch <-chan Event)
}

// Event represents an internal event published by the job system.
type Event struct {
	Type string
	Job  Job
}

// Event types.
const (
	EventJobCreated   = "job.created"
	EventJobUpdated   = "job.updated"
	EventJobCompleted = "job.completed"
	EventJobFailed    = "job.failed"
	EventJobCancelled = "job.cancelled"
)
