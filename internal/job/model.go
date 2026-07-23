package job

import "time"

// JobStatus represents the state of a download job.
type JobStatus string

const (
	StatusQueued      JobStatus = "queued"
	StatusDownloading JobStatus = "downloading"
	StatusPaused      JobStatus = "paused"
	StatusCompleted   JobStatus = "completed"
	StatusFailed      JobStatus = "failed"
	StatusCancelled   JobStatus = "cancelled"
)

// Job represents a single download task.
type Job struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Name   string `json:"name"`

	Status JobStatus `json:"status"`

	Progress       float64 `json:"progress"`
	TotalBytes     int64   `json:"totalBytes"`
	CompletedBytes int64   `json:"completedBytes"`

	SpeedBytesPerSecond int64 `json:"speedBytesPerSecond"`
	ETASeconds          int64 `json:"etaSeconds"`

	Error string `json:"error,omitempty"`

	Engine   string `json:"engine"`
	EngineID string `json:"-"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
