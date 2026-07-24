package engine

import "downloader/internal/job"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus = job.EngineStatus

// IEngine defines the interface for a download engine.
type IEngine = job.IEngine
type Engine = job.IEngine

// IMediaAnalyzer is optionally implemented by engines that can extract media metadata.
type IMediaAnalyzer = job.IMediaAnalyzer
type MediaAnalyzer = job.IMediaAnalyzer

// IEngineRegistry manages available download engines and URL routing.
type IEngineRegistry = job.IEngineRegistry
type EngineRegistry = job.IEngineRegistry

// ITorrentEngine extends IEngine with torrent-specific operations.
type ITorrentEngine = job.ITorrentEngine
type TorrentEngine = job.ITorrentEngine
