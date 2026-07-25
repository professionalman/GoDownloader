package job

import "context"

// IQueueRepository defines the persistence interface for the job queue.
type IQueueRepository interface {
	Enqueue(ctx context.Context, entry *QueueEntry) error
	Get(ctx context.Context, jobID string) (*QueueEntry, error)
	Delete(ctx context.Context, jobID string) error

	NextRunnable(ctx context.Context) (*QueuedJob, error)
	List(ctx context.Context) ([]QueuedJob, error)

	NextPosition(ctx context.Context, priority JobPriority) (int64, error)

	Reorder(
		ctx context.Context,
		priority JobPriority,
		orderedJobIDs []string,
	) error
}
