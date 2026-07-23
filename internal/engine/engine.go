package engine

import "downloader/internal/job"

// EngineStatus holds the normalized status returned by a download engine.
type EngineStatus = job.EngineStatus

// Engine defines the interface for a download engine.
type Engine = job.Engine
