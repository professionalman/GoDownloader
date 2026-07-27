package job

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// JobStatus represents the state of a download job.
type JobStatus string

const (
	StatusQueued            JobStatus = "queued"
	StatusDownloading       JobStatus = "downloading"
	StatusPaused            JobStatus = "paused"
	StatusCompleted         JobStatus = "completed"
	StatusFailed            JobStatus = "failed"
	StatusCancelled         JobStatus = "cancelled"
	StatusAnalyzing         JobStatus = "analyzing"
	StatusProcessing        JobStatus = "processing"
	StatusAwaitingSelection JobStatus = "awaiting_selection"
	StatusSeeding           JobStatus = "seeding"
)

// JobType classifies the download engine strategy.
const (
	TypeDownload = "download" // Direct file download via aria2
	TypeMedia    = "media"    // Media extraction via yt-dlp
	TypeTorrent  = "torrent"  // Torrent download via qBittorrent
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

// TorrentFilePriority represents the priority level for a torrent file.
type TorrentFilePriority string

const (
	PrioritySkip    TorrentFilePriority = "skip"
	PriorityNormal  TorrentFilePriority = "normal"
	PriorityHigh    TorrentFilePriority = "high"
	PriorityMaximum TorrentFilePriority = "maximum"
)

// ValidPriority returns true if the priority value is valid.
func ValidPriority(p TorrentFilePriority) bool {
	switch p {
	case PrioritySkip, PriorityNormal, PriorityHigh, PriorityMaximum:
		return true
	}
	return false
}

// TorrentInfo holds normalized torrent metadata owned by GoDownloader.
type TorrentInfo struct {
	Name        string  `json:"name"`
	InfoHash    string  `json:"infoHash"`
	TotalSize   int64   `json:"totalSize"`
	Seeders     int     `json:"seeders"`
	Leechers    int     `json:"leechers"`
	Uploaded    int64   `json:"uploaded"`
	UploadSpeed int64   `json:"uploadSpeed"`
	Ratio       float64 `json:"ratio"`
}

// TorrentFile represents a single file within a torrent.
type TorrentFile struct {
	Index    int                 `json:"index"`
	Path     string              `json:"path"`
	Size     int64               `json:"size"`
	Progress float64             `json:"progress"`
	Priority TorrentFilePriority `json:"priority"`
	Selected bool                `json:"selected"`
}

// TorrentFileSelection represents a user's file selection with priority.
type TorrentFileSelection struct {
	Index    int                 `json:"index"`
	Priority TorrentFilePriority `json:"priority"`
}

// ExtractMagnetHash extracts and normalizes the 40-character lowercase hex info hash from a magnet URI string.
func ExtractMagnetHash(magnet string) (string, error) {
	lower := strings.ToLower(magnet)
	const prefix = "urn:btih:"
	idx := strings.Index(lower, prefix)
	if idx == -1 {
		return "", fmt.Errorf("invalid magnet link: missing btih")
	}

	hashPart := magnet[idx+len(prefix):]
	ampIdx := strings.Index(hashPart, "&")
	if ampIdx != -1 {
		hashPart = hashPart[:ampIdx]
	}

	// 40-character hex BTIH
	if len(hashPart) == 40 {
		for _, c := range hashPart {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "", fmt.Errorf("invalid 40-character info hash in magnet link: not valid hex")
			}
		}
		return strings.ToLower(hashPart), nil
	}

	// 32-character Base32 BTIH
	if len(hashPart) == 32 {
		upperHash := strings.ToUpper(hashPart)
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(upperHash)
		if err != nil || len(decoded) != 20 {
			return "", fmt.Errorf("invalid 32-character base32 info hash in magnet link: %v", err)
		}
		return hex.EncodeToString(decoded), nil
	}

	return "", fmt.Errorf("invalid info hash length in magnet link")
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

	TorrentInfo       *TorrentInfo `json:"torrentInfo,omitempty"`
	SeedAfterComplete bool         `json:"seedAfterComplete,omitempty"`

	Priority JobPriority `json:"priority"`
	BatchID  string      `json:"batchId,omitempty"`

	CategoryID     string                 `json:"categoryId,omitempty"`
	DestinationDir string                 `json:"destinationDir"`
	WorkDir        string                 `json:"workDir,omitempty"`
	ConflictPolicy FilenameConflictPolicy `json:"conflictPolicy"`
	FinalPath      string                 `json:"finalPath,omitempty"`

	EngineCleanupPending bool `json:"engineCleanupPending,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FilenameConflictPolicy defines the strategy when a destination file already exists.
type FilenameConflictPolicy string

const (
	ConflictPolicyRename        FilenameConflictPolicy = "rename"
	ConflictPolicyOverwrite     FilenameConflictPolicy = "overwrite"
	ConflictPolicyFail          FilenameConflictPolicy = "fail"
	ConflictPolicyEngineManaged FilenameConflictPolicy = "engine_managed"
)

// JobPriority represents the queue priority level for a download job.
type JobPriority string

const (
	JobPriorityLow    JobPriority = "low"
	JobPriorityNormal JobPriority = "normal"
	JobPriorityHigh   JobPriority = "high"
)

// ValidJobPriority checks if a job priority string is valid.
func ValidJobPriority(p JobPriority) bool {
	switch p {
	case JobPriorityLow, JobPriorityNormal, JobPriorityHigh:
		return true
	}
	return false
}

// QueueAction defines whether a queued job requires a fresh engine start or a resume operation.
type QueueAction string

const (
	QueueActionStart  QueueAction = "start"
	QueueActionResume QueueAction = "resume"
)

// QueueEntry represents a persistent row in job_queue.
type QueueEntry struct {
	JobID      string      `json:"jobId"`
	Position   int64       `json:"position"`
	Action     QueueAction `json:"action"`
	EnqueuedAt time.Time   `json:"enqueuedAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

// QueuedJob combines a Job with its QueueEntry and derived runtime waiting reason.
type QueuedJob struct {
	JobID         string      `json:"jobId"`
	Position      int64       `json:"position"`
	Action        QueueAction `json:"action"`
	EnqueuedAt    time.Time   `json:"enqueuedAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
	WaitingReason string      `json:"waitingReason,omitempty"`
	Job           Job         `json:"job"`
}

// BatchInput defines a single job input inside a batch submission.
type BatchInput struct {
	Source         string                 `json:"source"`
	Priority       JobPriority            `json:"priority,omitempty"`
	CategoryID     string                 `json:"categoryId,omitempty"`
	DestinationDir string                 `json:"destinationDir,omitempty"`
	ConflictPolicy FilenameConflictPolicy `json:"conflictPolicy,omitempty"`
}

// CreateBatchRequest represents the POST /jobs/batch body.
type CreateBatchRequest struct {
	Inputs         []BatchInput           `json:"inputs,omitempty"`
	Sources        []string               `json:"sources,omitempty"`
	Priority       JobPriority            `json:"priority,omitempty"`
	CategoryID     string                 `json:"categoryId,omitempty"`
	DestinationDir string                 `json:"destinationDir,omitempty"`
	ConflictPolicy FilenameConflictPolicy `json:"conflictPolicy,omitempty"`
}

// BatchItemResult represents the outcome for one item in a batch request.
type BatchItemResult struct {
	Index int       `json:"index"`
	Job   *Job      `json:"job,omitempty"`
	Error *AppError `json:"error,omitempty"`
}

// CreateBatchResponse is returned by POST /jobs/batch.
type CreateBatchResponse struct {
	BatchID string            `json:"batchId"`
	Created int               `json:"created"`
	Failed  int               `json:"failed"`
	Items   []BatchItemResult `json:"items"`
}

// BulkActionRequest represents the POST /jobs/bulk body.
type BulkActionRequest struct {
	JobIDs []string `json:"jobIds"`
	Action string   `json:"action"` // pause, resume, cancel, retry
}

// BulkItemResult represents the result for a single job in a bulk operation.
type BulkItemResult struct {
	JobID   string    `json:"jobId"`
	Success bool      `json:"success"`
	Job     *Job      `json:"job,omitempty"`
	Error   *AppError `json:"error,omitempty"`
}

// BulkActionResponse is returned by POST /jobs/bulk.
type BulkActionResponse struct {
	Action    string           `json:"action"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
	Results   []BulkItemResult `json:"results"`
}

// QueueSnapshot represents the response for GET /queue.
type QueueSnapshot struct {
	MaxConcurrentDownloads int         `json:"maxConcurrentDownloads"`
	RunningDownloads       int         `json:"runningDownloads"`
	QueuedDownloads        int         `json:"queuedDownloads"`
	PausedDownloads        int         `json:"pausedDownloads"`
	Items                  []QueuedJob `json:"items"`
}

// QueueReorderRequest represents the body for PUT /queue/reorder.
type QueueReorderRequest struct {
	Priority JobPriority `json:"priority"`
	JobIDs   []string    `json:"jobIds"`
}

// SetPriorityRequest represents the body for PUT /jobs/{id}/priority.
type SetPriorityRequest struct {
	Priority JobPriority `json:"priority"`
}
