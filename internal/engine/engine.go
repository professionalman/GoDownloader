package engine

import "downloader/internal/job"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus = job.EngineStatus

// IEngine defines the interface for a download engine.
type IEngine = job.IEngine

// IMediaAnalyzer is optionally implemented by engines that can extract media metadata.
type IMediaAnalyzer = job.IMediaAnalyzer

// IEngineRegistry manages available download engines and URL routing.
type IEngineRegistry = job.IEngineRegistry

// ITorrentEngine extends IEngine with torrent-specific operations.
type ITorrentEngine = job.ITorrentEngine

// IShutdownableEngine is optionally implemented by engines requiring cleanup on shutdown.
type IShutdownableEngine = job.IShutdownableEngine
