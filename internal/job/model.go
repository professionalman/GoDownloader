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
	StatusAnalyzing   JobStatus = "analyzing"
	StatusProcessing  JobStatus = "processing"
)

// JobType classifies the download engine strategy.
const (
	TypeDownload = "download" // Direct file download via aria2
	TypeMedia    = "media"    // Media extraction via yt-dlp
)

// MediaFormat describes a single available format from a media source.
type MediaFormat struct {
	FormatID   string  `json:"formatId"`
	Extension  string  `json:"ext"`
	Resolution string  `json:"resolution"`
	FileSize   int64   `json:"fileSize"`
	VCodec     string  `json:"vcodec"`
	ACodec     string  `json:"acodec"`
	FPS        float64 `json:"fps"`
	Quality    string  `json:"quality"`
	Note       string  `json:"note"`
}

// MediaInfo holds metadata extracted from a media source by yt-dlp.
type MediaInfo struct {
	Title       string        `json:"title"`
	Duration    float64       `json:"duration"`
	Thumbnail   string        `json:"thumbnail"`
	URL         string        `json:"url"`
	Formats     []MediaFormat `json:"formats"`
	SelectedFmt string        `json:"selectedFormat,omitempty"`
}

// Job represents a single download task.
type Job struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Name   string `json:"name"`

	Status JobStatus `json:"status"`
	Type   string    `json:"type"`

	Progress       float64 `json:"progress"`
	TotalBytes     int64   `json:"totalBytes"`
	CompletedBytes int64   `json:"completedBytes"`

	SpeedBytesPerSecond int64 `json:"speedBytesPerSecond"`
	ETASeconds          int64 `json:"etaSeconds"`

	Error string `json:"error,omitempty"`

	Engine   string `json:"engine"`
	EngineID string `json:"-"`

	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
