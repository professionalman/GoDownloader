package job

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

// asyncTransitionEngine is a test double capable of simulating asynchronous daemon state transitions.
type asyncTransitionEngine struct {
	mu sync.Mutex

	states       []string
	currentIndex int

	startDownloadCalled    int32
	stopDownloadCalled     int32
	setFilePrioritiesTimes int32

	prioritiesAppliedBeforeStart bool

	files []TorrentFile
}

func newAsyncTransitionEngine(states ...string) *asyncTransitionEngine {
	return &asyncTransitionEngine{
		states: states,
		files: []TorrentFile{
			{Index: 0, Path: "file1.iso", Size: 1000, Priority: PriorityNormal, Selected: true},
			{Index: 1, Path: "file2.iso", Size: 2000, Priority: PrioritySkip, Selected: false},
		},
	}
}

func (e *asyncTransitionEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{Pause: true, Resume: true, Cancel: true, Retry: true, FileSelection: true}
}

func (e *asyncTransitionEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	return j.EngineID, nil
}

func (e *asyncTransitionEngine) Pause(ctx context.Context, j *Job) error {
	atomic.AddInt32(&e.stopDownloadCalled, 1)
	e.mu.Lock()
	e.states = []string{"stoppedDL"}
	e.currentIndex = 0
	e.mu.Unlock()
	return nil
}

func (e *asyncTransitionEngine) Resume(ctx context.Context, j *Job) error {
	atomic.AddInt32(&e.startDownloadCalled, 1)
	return nil
}

func (e *asyncTransitionEngine) Cancel(ctx context.Context, j *Job) error {
	return nil
}

func (e *asyncTransitionEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	raw := "stoppedDL"
	if len(e.states) > 0 {
		if e.currentIndex < len(e.states) {
			raw = e.states[e.currentIndex]
			e.currentIndex++
		} else {
			raw = e.states[len(e.states)-1]
		}
	}

	st := StatusPaused
	switch raw {
	case "downloading", "forcedDL", "stalledDL", "allocating", "queuedDL", "checkingDL", "checkingResumeData", "moving":
		st = StatusDownloading
	case "uploading", "forcedUP", "stalledUP", "queuedUP", "checkingUP":
		st = StatusSeeding
	case "stoppedUP", "pausedUP":
		st = StatusCompleted
	case "stoppedDL", "pausedDL":
		st = StatusPaused
	default:
		st = StatusDownloading
	}

	return &EngineStatus{
		Status:              st,
		RawState:            raw,
		CompletedBytes:      250,
		TotalBytes:          1000,
		SpeedBytesPerSecond: 500,
		Progress:            25.0,
	}, nil
}

func (e *asyncTransitionEngine) AddMagnet(ctx context.Context, magnet, savePath, jobID string) (string, error) {
	return "hash1234", nil
}

func (e *asyncTransitionEngine) AddTorrentFile(ctx context.Context, filePath, savePath, jobID string) (string, error) {
	return "hash1234", nil
}

func (e *asyncTransitionEngine) GetTorrentOwnership(ctx context.Context, infoHash string) (*TorrentOwnership, error) {
	return nil, nil
}

func (e *asyncTransitionEngine) AdoptTorrent(ctx context.Context, infoHash, jobID string) error {
	return nil
}

func (e *asyncTransitionEngine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error) {
	return e.files, nil
}

func (e *asyncTransitionEngine) SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error {
	atomic.AddInt32(&e.setFilePrioritiesTimes, 1)
	if atomic.LoadInt32(&e.startDownloadCalled) == 0 {
		e.prioritiesAppliedBeforeStart = true
	}
	return nil
}

func (e *asyncTransitionEngine) StartDownload(ctx context.Context, infoHash string) error {
	atomic.AddInt32(&e.startDownloadCalled, 1)
	return nil
}

func (e *asyncTransitionEngine) StopDownload(ctx context.Context, infoHash string) error {
	atomic.AddInt32(&e.stopDownloadCalled, 1)
	e.mu.Lock()
	e.states = []string{"stoppedDL"}
	e.currentIndex = 0
	e.mu.Unlock()
	return nil
}

func (e *asyncTransitionEngine) RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error {
	return nil
}

func (e *asyncTransitionEngine) GetTorrentInfo(ctx context.Context, infoHash string) (*TorrentInfo, error) {
	return &TorrentInfo{Name: "test.iso", InfoHash: infoHash, TotalSize: 1000}, nil
}

func (e *asyncTransitionEngine) GetRawState(ctx context.Context, infoHash string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.states) > 0 {
		return e.states[0], nil
	}
	return "stoppedDL", nil
}

func (e *asyncTransitionEngine) HealthCheck(ctx context.Context) error {
	return nil
}

// 5.A Start transition race:
// Initial engine state = stoppedDL -> first status poll = stoppedDL -> second status poll = downloading
// Expected: StartTorrent -> scheduler dispatch -> no PAUSED transition -> eventually DOWNLOADING -> activeJobs contains job -> progress monitor updates.
func TestTorrentSync_StartTransitionRace_AvoidsPausedSplitBrain(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// Sequence: stoppedDL on startup -> stoppedDL on first confirmation poll -> downloading on second poll
	eng := newAsyncTransitionEngine("stoppedDL", "stoppedDL", "downloading")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	var events []JobStatus
	var eventMu sync.Mutex
	sub := bus.Subscribe()
	go func() {
		for ev := range sub {
			if ev.Type == EventJobUpdated && ev.Job.ID != "" {
				eventMu.Lock()
				events = append(events, ev.Job.Status)
				eventMu.Unlock()
			}
		}
	}()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	monitor := NewMonitor(mgr, 100*time.Millisecond)
	monitor.Start(context.Background())
	defer monitor.Stop()

	j := &Job{
		ID:             "job_sync_a",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_a",
		Type:           TypeTorrent,
		Status:         StatusAwaitingSelection,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	// Wait for job to become DOWNLOADING
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := mgr.Get(context.Background(), j.ID)
		if current != nil && current.Status == StatusDownloading {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	current, _ := mgr.Get(context.Background(), j.ID)
	if current == nil || current.Status != StatusDownloading {
		t.Fatalf("expected job to reach StatusDownloading without Resume, got status=%v (error=%s)", current.Status, current.Error)
	}

	// Active jobs must contain this job
	active := mgr.GetActiveJobs()
	if _, ok := active[j.ID]; !ok {
		t.Fatalf("job %s must be present in activeJobs", j.ID)
	}

	// Verify no PAUSED event was emitted
	eventMu.Lock()
	for _, st := range events {
		if st == StatusPaused {
			t.Fatalf("job must NEVER transition to StatusPaused during asynchronous start confirmation, events: %v", events)
		}
	}
	eventMu.Unlock()
}

// 5.B Multiple transient stopped states: stoppedDL -> stoppedDL -> stalledDL -> DOWNLOADING
func TestTorrentSync_MultipleTransientStoppedStates_SuccessfullyReachesDownloading(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// Sequence: 3 stoppedDL states followed by stalledDL (active)
	eng := newAsyncTransitionEngine("stoppedDL", "stoppedDL", "stoppedDL", "stalledDL")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	j := &Job{
		ID:             "job_sync_b",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_b",
		Type:           TypeTorrent,
		Status:         StatusAwaitingSelection,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := mgr.Get(context.Background(), j.ID)
		if current != nil && current.Status == StatusDownloading {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	current, _ := mgr.Get(context.Background(), j.ID)
	if current == nil || current.Status != StatusDownloading {
		t.Fatalf("expected StatusDownloading after transient stalledDL, got %v", current.Status)
	}
}

// 5.C Start never leaves stoppedDL: fail closed
func TestTorrentSync_StartNeverLeavesStoppedDL_FailsClosed(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// Stays stoppedDL forever
	eng := newAsyncTransitionEngine("stoppedDL")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	j := &Job{
		ID:             "job_sync_c",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_c",
		Type:           TypeTorrent,
		Status:         StatusAwaitingSelection,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	// Must fail deterministically within confirmation timeout
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := mgr.Get(context.Background(), j.ID)
		if current != nil && current.Status == StatusFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	current, _ := mgr.Get(context.Background(), j.ID)
	if current == nil || current.Status != StatusFailed {
		t.Fatalf("expected job to fail closed when daemon never starts, got %v", current.Status)
	}

	active := mgr.GetActiveJobs()
	if _, ok := active[j.ID]; ok {
		t.Fatalf("failed job must not be in activeJobs")
	}
}

// 5.D Resume race: local PAUSED -> Resume called -> first engine state = stoppedDL -> later = downloading
func TestTorrentSync_ResumeRace_AutomaticallyBecomesDownloading(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// Initial paused state -> when resumed, first poll returns stoppedDL, next poll returns downloading
	eng := newAsyncTransitionEngine("stoppedDL", "downloading")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	j := &Job{
		ID:             "job_sync_d",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_d",
		Type:           TypeTorrent,
		Status:         StatusPaused,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})
	_ = torrentRepo.SaveTorrentFiles(context.Background(), j.ID, []TorrentFileRecord{
		{JobID: j.ID, FileIndex: 0, Size: 1000, Selected: true, Priority: "normal"},
		{JobID: j.ID, FileIndex: 1, Size: 2000, Selected: false, Priority: "skip"},
	})

	_, err := mgr.Resume(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := mgr.Get(context.Background(), j.ID)
		if current != nil && current.Status == StatusDownloading {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	current, _ := mgr.Get(context.Background(), j.ID)
	if current == nil || current.Status != StatusDownloading {
		t.Fatalf("expected resumed job to reach StatusDownloading without second Resume, got %v", current.Status)
	}
}

// 5.E Genuine Pause: DOWNLOADING -> user clicks Pause -> qBittorrent becomes stoppedDL -> GoDownloader PAUSED, removed from active monitoring
func TestTorrentSync_GenuinePause_TransitionsToPausedImmediately(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	eng := newAsyncTransitionEngine("downloading")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:             "job_sync_e",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_e",
		Type:           TypeTorrent,
		Status:         StatusDownloading,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})
	mgr.addActive(j)

	pausedJob, err := mgr.Pause(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("Pause failed: %v", err)
	}

	if pausedJob.Status != StatusPaused {
		t.Fatalf("expected StatusPaused, got %v", pausedJob.Status)
	}

	active := mgr.GetActiveJobs()
	if _, ok := active[j.ID]; ok {
		t.Fatalf("paused job must be removed from activeJobs")
	}

	if atomic.LoadInt32(&eng.stopDownloadCalled) < 1 {
		t.Fatalf("StopDownload must be called on genuine Pause")
	}
}

// 5.F Scheduler capacity: with max concurrency exhausted, StartTorrent persists QUEUED, StartDownload must NOT be called, qBittorrent remains stopped
func TestTorrentSync_SchedulerCapacityExhausted_RemainsQueuedAndStopped(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	eng := newAsyncTransitionEngine("stoppedDL")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	// Max concurrency = 0 (exhausted)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 0 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	j := &Job{
		ID:             "job_sync_f",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_f",
		Type:           TypeTorrent,
		Status:         StatusAwaitingSelection,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 1000})

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	current, _ := mgr.Get(context.Background(), j.ID)
	if current == nil || current.Status != StatusQueued {
		t.Fatalf("expected job to remain StatusQueued when capacity exhausted, got %v", current.Status)
	}

	if atomic.LoadInt32(&eng.startDownloadCalled) != 0 {
		t.Fatalf("StartDownload must NOT be called when scheduler capacity is exhausted, got %d calls", eng.startDownloadCalled)
	}
}

// 5.G Regression: selected TotalBytes remains selected payload only
// 5.H Regression: file priorities remain applied before qBittorrent is allowed to start
func TestTorrentSync_Regressions_PayloadSizeAndPriorityOrdering(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	eng := newAsyncTransitionEngine("downloading")
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)
	sched := NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, mgr.dispatchQueuedJob)
	mgr.SetScheduler(sched)
	mgr.SetQueueRepository(queueRepo)
	sched.Start(context.Background())
	defer sched.Stop()

	j := &Job{
		ID:             "job_sync_gh",
		Engine:         "qbittorrent",
		EngineID:       "hash_sync_gh",
		Type:           TypeTorrent,
		Status:         StatusAwaitingSelection,
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID, Name: "test.iso", TotalSize: 3000})

	// Select file 0 (size 1000) only, skip file 1 (size 2000)
	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := mgr.Get(context.Background(), j.ID)
		if current != nil && current.Status == StatusDownloading {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	current, _ := mgr.Get(context.Background(), j.ID)
	if current.TotalBytes != 1000 {
		t.Fatalf("selected TotalBytes must be 1000 (selected payload only), got %d", current.TotalBytes)
	}

	if !eng.prioritiesAppliedBeforeStart {
		t.Fatalf("file priorities must be applied to engine before StartDownload is called")
	}
}
