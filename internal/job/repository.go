package job

import "context"

// IJobRepository defines the persistence interface for jobs.
type IJobRepository interface {
	Create(ctx context.Context, j *Job) error
	Update(ctx context.Context, j *Job) error
	GetByID(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context) ([]Job, error)
	ListRecoverable(ctx context.Context) ([]Job, error)
	CountDownloading(ctx context.Context) (int, error)
}

// IEventBus defines the interface for publishing and subscribing to events.
type IEventBus interface {
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

// ITorrentRepository defines the persistence interface for torrent-specific data.
type ITorrentRepository interface {
	CreateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error
	GetTorrentJob(ctx context.Context, jobID string) (*TorrentJobRecord, error)
	UpdateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error
	DeleteTorrentJob(ctx context.Context, jobID string) error
	GetTorrentJobByInfoHash(ctx context.Context, infoHash string) (*TorrentJobRecord, error)
	GetActiveTorrentJobByInfoHash(ctx context.Context, infoHash string) (*TorrentJobRecord, error)
	SaveTorrentFiles(ctx context.Context, jobID string, files []TorrentFileRecord) error
	GetTorrentFiles(ctx context.Context, jobID string) ([]TorrentFileRecord, error)
	UpdateTorrentFileSelections(ctx context.Context, jobID string, selections []TorrentFileRecord) error
}

// TorrentJobRecord holds torrent-specific persistence data.
type TorrentJobRecord struct {
	JobID             string
	InfoHash          string
	Name              string
	TotalSize         int64
	SeedAfterComplete bool
	TorrentFilePath   string
}

// TorrentFileRecord holds a single torrent file's persistence data.
type TorrentFileRecord struct {
	JobID     string
	FileIndex int
	Path      string
	Size      int64
	Selected  bool
	Priority  string
}
