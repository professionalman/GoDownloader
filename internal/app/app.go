package app

import (
	"context"
	"sync"

	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/mediaauth"
	"downloader/internal/process"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/toolmanager"
	"downloader/internal/tracker"
)

// App represents the unified, host-agnostic application runtime.
// It owns the database, repositories, secure store, background services,
// engines, process supervision, event bus, job manager, and scheduler.
type App struct {
	cfg               *config.Config
	db                *database.DB
	repo              *database.SQLiteJobRepository
	queueRepo         *database.SQLiteQueueRepository
	settingsRepo      *database.SQLiteSettingsRepository
	secretRepo        *database.SQLiteSecretRepository
	trackerRepo       *database.SQLiteTrackerRepository
	catRepo           *storage.SQLiteCategoryRepository
	execRepo          *database.SQLiteExecutionRepository
	torrentRepo       *database.SQLiteTorrentRepository

	keyMgr            *securestore.MasterKeyManager
	secretStore       *securestore.Store
	settingsService   *settings.SettingsService

	toolMgr           *toolmanager.Manager
	storageService    *storage.StorageService
	mediaAuthService  *mediaauth.Service
	processSupervisor *process.Supervisor

	registry          *engine.Registry
	bus               *events.InMemoryBus
	trackerService    *tracker.Service

	manager           *job.Manager
	resourceGovernor  *job.ResourceGovernor
	scheduler         *job.Scheduler

	// Lifecycle state coordination
	mu            sync.Mutex
	started       bool
	stopped       bool
	stopping      bool
	shutdownDone  chan struct{}
	trackerCancel context.CancelFunc
	trackerDone   chan struct{}
	shutdownErr   error
}

// Option configures an App instance during construction.
type Option func(*options)

type options struct {
	dbPath string
}

// WithDBPath overrides the default SQLite database path ("./downloader.db").
// Provided as a lightweight seam for isolated testing.
func WithDBPath(path string) Option {
	return func(o *options) {
		o.dbPath = path
	}
}

// Manager returns the core job Manager required by the host.
func (a *App) Manager() *job.Manager {
	return a.manager
}

// Settings returns the SettingsService required by the host.
func (a *App) Settings() *settings.SettingsService {
	return a.settingsService
}

// Categories returns the CategoryRepository required by the host router.
func (a *App) Categories() storage.ICategoryRepository {
	return a.catRepo
}

// Tracker returns the Tracker Service required by the host router.
func (a *App) Tracker() *tracker.Service {
	return a.trackerService
}

// MediaAuth returns the MediaAuthService required by the host router.
func (a *App) MediaAuth() *mediaauth.Service {
	return a.mediaAuthService
}

// EventBus returns the application event bus required by host event adapters.
func (a *App) EventBus() *events.InMemoryBus {
	return a.bus
}
