package job

import (
	"context"
	"time"

	"downloader/internal/networkpolicy"
)

// IJobRepository defines the persistence interface for jobs.
type IJobRepository interface {
	Create(ctx context.Context, j *Job) error
	Update(ctx context.Context, j *Job) error
	UpdateJobPriorityAndQueuePosition(ctx context.Context, jobID string, newPriority JobPriority, newPosition int64) error
	GetByID(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context) ([]Job, error)
	ListRecoverable(ctx context.Context) ([]Job, error)
	ListPendingEngineCleanups(ctx context.Context) ([]Job, error)
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
	Data any
}

// Event types.
const (
	EventJobCreated              = "job.created"
	EventJobUpdated              = "job.updated"
	EventJobCompleted            = "job.completed"
	EventJobFailed               = "job.failed"
	EventJobCancelled            = "job.cancelled"
	EventJobNetworkUpdated       = "job.network.updated"
	EventJobSeedingPolicyUpdated = "job.seeding_policy.updated"
	EventTrackerSourceUpdated    = "tracker_source.updated"
	EventTrackerSourceFailed     = "tracker_source.failed"
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
	FinalizeTorrent(ctx context.Context, j *Job, stopReason string) error
}

// TorrentJobRecord holds torrent-specific persistence data.
type TorrentJobRecord struct {
	JobID                   string
	InfoHash                string
	Name                    string
	TotalSize               int64
	SeedAfterComplete       bool
	TorrentFilePath         string
	SeedingPolicy           networkpolicy.SeedingPolicy
	SeedingStartedAt        *time.Time
	SeedingStopReason       string
	SeedingReconcilePending bool
	CustomTrackers          []string
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
