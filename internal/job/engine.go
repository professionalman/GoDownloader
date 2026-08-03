package job

import "context"

import "downloader/internal/networkpolicy"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus struct {
	Status JobStatus

	// RawState contains the raw unmapped state from the underlying engine (e.g. metaDL, pausedDL, stoppedDL).
	RawState string

	TotalBytes     int64
	CompletedBytes int64

	SpeedBytesPerSecond int64
	ETASeconds          int64

	Progress float64

	Error              string
	FileName           string
	OutputPath         string
	UploadSpeed        int64
	Uploaded           int64
	Ratio              float64
	Seeders            int
	Leechers           int
	SeedingTimeSeconds int64
	TorrentPrivate     *bool
}

// IEngine defines the interface for a download engine.
type IEngine interface {
	Capabilities() networkpolicy.EngineCapabilities
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

type IGlobalDownloadLimitController interface {
	GetGlobalDownloadLimit(ctx context.Context) (int64, error)
	SetGlobalDownloadLimit(ctx context.Context, bytesPerSecond int64) error
}

type IDownloadLimitController interface {
	GetDownloadLimit(ctx context.Context, j *Job) (int64, error)
	SetDownloadLimit(ctx context.Context, j *Job, bytesPerSecond int64) error
}

type IUploadLimitController interface {
	GetUploadLimit(ctx context.Context, j *Job) (int64, error)
	SetUploadLimit(ctx context.Context, j *Job, bytesPerSecond int64) error
}

type ITrackerController interface {
	GetTrackers(ctx context.Context, j *Job) ([]networkpolicy.Tracker, error)
	AddTrackers(ctx context.Context, j *Job, trackers []string) error
	GetTorrentPrivacy(ctx context.Context, j *Job) (bool, error)
}

type ISeedingPolicyController interface {
	ApplySeedingPolicy(ctx context.Context, j *Job, policy networkpolicy.SeedingPolicy) error
}

type TorrentOwnership struct {
	Hash     string
	Category string
	Tags     []string
}

type IManagedTorrentProxyController interface {
	ListTorrentOwnership(ctx context.Context) ([]TorrentOwnership, error)
	ApplyManagedProxy(ctx context.Context, policy *networkpolicy.RuntimePolicy) error
}

type INetworkMediaAnalyzer interface {
	AnalyzeWithPolicy(ctx context.Context, url string, policy *networkpolicy.RuntimePolicy) (*MediaInfo, error)
}

// IMediaAnalyzer is optionally implemented by engines that can extract media metadata.
type IMediaAnalyzer interface {
	// Analyze extracts media information from a URL without downloading.
	Analyze(ctx context.Context, url string) (*MediaInfo, error)
}

// ICleanupableEngine is optionally implemented by engines that maintain in-memory tracking state after terminal status.
type ICleanupableEngine interface {
	Cleanup(jobID string)
}

// IEngineRegistry manages available download engines and URL routing.
type IEngineRegistry interface {
	// Get returns the engine registered under the given name.
	Get(name string) (IEngine, bool)

	// Detect determines which engine should handle the given URL.
	Detect(url string) string
}

// ITorrentEngine extends IEngine with torrent-specific operations.
type ITorrentEngine interface {
	IEngine

	// AddMagnet adds a magnet URI and returns the info hash.
	AddMagnet(ctx context.Context, magnet, savePath, jobID string) (infoHash string, err error)

	// AddTorrentFile adds a .torrent file and returns the info hash.
	AddTorrentFile(ctx context.Context, filePath, savePath, jobID string) (infoHash string, err error)

	// GetFiles returns the normalized file list for a torrent.
	GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error)

	// SetFilePriorities applies file selections to a torrent.
	SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error

	// StartDownload starts/resumes a torrent.
	StartDownload(ctx context.Context, infoHash string) error

	// StopDownload stops/pauses a torrent.
	StopDownload(ctx context.Context, infoHash string) error

	// RemoveTorrent removes a torrent from the engine.
	RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error

	// GetTorrentInfo returns normalized torrent metadata.
	GetTorrentInfo(ctx context.Context, infoHash string) (*TorrentInfo, error)

	// HealthCheck verifies the engine is reachable and operational.
	HealthCheck(ctx context.Context) error
}

// IShutdownableEngine is optionally implemented by engines that require cleanup on backend shutdown.
type IShutdownableEngine interface {
	Shutdown()
}

// ITorrentRawStateProvider is optionally implemented by torrent engines to provide raw qBittorrent state strings.
type ITorrentRawStateProvider interface {
	GetRawState(ctx context.Context, infoHash string) (string, error)
}
