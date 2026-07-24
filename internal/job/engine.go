package job

import "context"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus struct {
	Status JobStatus

	TotalBytes     int64
	CompletedBytes int64

	SpeedBytesPerSecond int64
	ETASeconds          int64

	Progress float64

	Error    string
	FileName string
}

// Engine defines the interface for a download engine.
type Engine interface {
	// Start begins a new download and returns the engine-specific ID.
	Start(ctx context.Context, j *Job, downloadDir string) (engineID string, err error)

	// Pause pauses an active download.
	Pause(ctx context.Context, j *Job) error

	// Resume resumes a paused download.
	Resume(ctx context.Context, j *Job) error

	// Cancel cancels a download.
	Cancel(ctx context.Context, j *Job) error

	// Status retrieves the current status of a download.
	Status(ctx context.Context, j *Job) (*EngineStatus, error)
}

// MediaAnalyzer is optionally implemented by engines that can extract media metadata.
type MediaAnalyzer interface {
	// Analyze extracts media information from a URL without downloading.
	Analyze(ctx context.Context, url string) (*MediaInfo, error)
}

// EngineRegistry manages available download engines and URL routing.
type EngineRegistry interface {
	// Get returns the engine registered under the given name.
	Get(name string) (Engine, bool)

	// Detect determines which engine should handle the given URL.
	Detect(url string) string
}
