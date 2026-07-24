package engine

import "downloader/internal/job"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus = job.EngineStatus

// Engine defines the interface for a download engine.
type Engine = job.Engine

// MediaAnalyzer is optionally implemented by engines that can extract media metadata.
type MediaAnalyzer = job.MediaAnalyzer

// EngineRegistry manages available download engines and URL routing.
type EngineRegistry = job.EngineRegistry

