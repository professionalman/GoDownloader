package job_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/networkpolicy"
	"downloader/internal/storage"
)

type governorTestEngine struct {
	startedJobs sync.Map // id -> bool
	failStart   atomic.Bool
	failPause   atomic.Bool
}

func (e *governorTestEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{}
}

func (e *governorTestEngine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	if e.failStart.Load() {
		return "", errors.New("engine start deliberate test failure")
	}
	e.startedJobs.Store(j.ID, true)
	return "engine-" + j.ID, nil
}

func (e *governorTestEngine) Pause(ctx context.Context, j *job.Job) error {
	if e.failPause.Load() {
		return errors.New("engine pause deliberate test failure")
	}
	e.startedJobs.Delete(j.ID)
	return nil
}

func (e *governorTestEngine) Resume(ctx context.Context, j *job.Job) error {
	e.startedJobs.Store(j.ID, true)
	return nil
}

func (e *governorTestEngine) Cancel(ctx context.Context, j *job.Job) error {
	e.startedJobs.Delete(j.ID)
	return nil
}

func (e *governorTestEngine) Status(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
	return &job.EngineStatus{Status: job.StatusDownloading}, nil
}

func setupGovernorTestEnv(t *testing.T, cfg job.ResourceGovernorConfig) (
	*database.DB,
	*job.Manager,
	*database.SQLiteJobRepository,
	*database.SQLiteQueueRepository,
	*job.Scheduler,
	*job.ResourceGovernor,
	*governorTestEngine,
	string,
) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_governor.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	storageSvc := storage.NewStorageService(nil, nil, nil, tempDir, tempDir)

	downloadDir := filepath.Join(tempDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		t.Fatalf("failed to create downloadDir: %v", err)
	}

	bus := events.NewInMemoryBus()
	engReg := engine.NewRegistry()
	fakeEng := &governorTestEngine{}
	engReg.Register("ytdlp", fakeEng)
	engReg.Register("aria2", fakeEng)
	engReg.Register("qbittorrent", fakeEng)
	engReg.Register("fake", fakeEng)

	mgr := job.NewManager(jobRepo, engReg, bus, downloadDir, nil, tempDir)
	mgr.SetStorageService(storageSvc)
	mgr.SetExecutionRepository(execRepo)
	mgr.SetQueueRepository(queueRepo)

	gov := job.NewResourceGovernor(cfg)
	mgr.SetResourceGovernor(gov)

	limitFn := func(ctx context.Context) int {
		return cfg.GlobalTransferLimit
	}
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return mgr.DispatchQueuedJob(ctx, qj)
	}

	sched := job.NewScheduler(jobRepo, queueRepo, limitFn, dispatchFn)
	sched.SetEventBus(bus)
	sched.SetResourceGovernor(gov)
	mgr.SetScheduler(sched)

	return db, mgr, jobRepo, queueRepo, sched, gov, fakeEng, dbPath
}

// 1. Global transfer limit adherence: limit=3, 5 jobs queued -> exactly 3 start
func TestSchedulerGovernor_GlobalTransferLimitAdherence(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 3,
		EngineLimits: map[string]int{
			"aria2": 5,
			"ytdlp": 5,
		},
	}
	db, _, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	for i := 1; i <= 5; i++ {
		eng := "aria2"
		if i%2 == 0 {
			eng = "ytdlp"
		}
		jID := fmt.Sprintf("j-global-%d", i)
		j := &job.Job{ID: jID, Status: job.StatusQueued, Engine: eng, CreatedAt: t0, UpdatedAt: t0}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      jID,
			Position:   int64(i),
			Action:     job.QueueActionStart,
			EnqueuedAt: t0,
			UpdatedAt:  t0,
		})
	}

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	// Wait bounded duration for scheduler to process
	var activeTransfers int
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		snap := gov.Snapshot()
		activeTransfers = snap.ActiveTransfers
		if activeTransfers == 3 {
			break
		}
	}

	if activeTransfers != 3 {
		t.Fatalf("expected exactly 3 active transfers under global limit 3, got %d", activeTransfers)
	}

	// Verify remaining 2 jobs remain queued
	runnable, err := queueRepo.ListRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("ListRunnable: %v", err)
	}
	if len(runnable) != 2 {
		t.Fatalf("expected 2 jobs to remain queued, got %d", len(runnable))
	}
}

// 2. Per-engine limit adherence: global=10, ytdlp=2, 4 ytdlp jobs queued -> exactly 2 start
func TestSchedulerGovernor_PerEngineLimitAdherence(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 10,
		EngineLimits: map[string]int{
			"ytdlp": 2,
			"aria2": 5,
		},
	}
	db, _, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	for i := 1; i <= 4; i++ {
		jID := fmt.Sprintf("j-yt-%d", i)
		j := &job.Job{ID: jID, Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      jID,
			Position:   int64(i),
			Action:     job.QueueActionStart,
			EnqueuedAt: t0,
			UpdatedAt:  t0,
		})
	}

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	snap := gov.Snapshot()
	if snap.ActivePerEngine["ytdlp"] != 2 {
		t.Fatalf("expected 2 active ytdlp jobs under engine limit 2, got %d", snap.ActivePerEngine["ytdlp"])
	}
	if snap.ActiveTransfers != 2 {
		t.Fatalf("expected 2 total active transfers, got %d", snap.ActiveTransfers)
	}

	runnable, err := queueRepo.ListRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("ListRunnable: %v", err)
	}
	if len(runnable) != 2 {
		t.Fatalf("expected 2 ytdlp jobs to remain queued, got %d", len(runnable))
	}
}

// 3. Multi-engine concurrent execution within global limit
func TestSchedulerGovernor_MultiEngineConcurrentWithinGlobalLimit(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 3,
		EngineLimits: map[string]int{
			"ytdlp": 2,
			"aria2": 2,
		},
	}
	db, _, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	// Enqueue 2 ytdlp and 2 aria2
	jobs := []struct {
		id  string
		eng string
		pos int64
	}{
		{"yt-1", "ytdlp", 1},
		{"yt-2", "ytdlp", 2},
		{"aria-1", "aria2", 3},
		{"aria-2", "aria2", 4},
	}

	for _, it := range jobs {
		j := &job.Job{ID: it.id, Status: job.StatusQueued, Engine: it.eng, CreatedAt: t0, UpdatedAt: t0}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      it.id,
			Position:   it.pos,
			Action:     job.QueueActionStart,
			EnqueuedAt: t0,
			UpdatedAt:  t0,
		})
	}

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(150 * time.Millisecond)

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 3 {
		t.Fatalf("expected exactly 3 active transfers under global limit 3, got %d", snap.ActiveTransfers)
	}
	// Exactly 2 ytdlp and 1 aria2 (or vice-versa depending on order)
	if snap.ActivePerEngine["ytdlp"] != 2 || snap.ActivePerEngine["aria2"] != 1 {
		t.Fatalf("expected 2 ytdlp and 1 aria2 active, got ytdlp=%d, aria2=%d", snap.ActivePerEngine["ytdlp"], snap.ActivePerEngine["aria2"])
	}
}

// 4. Head-of-line non-blocking bypass:
// Blocked high-ranked job allows lower-ranked job with available engine capacity to dispatch
func TestSchedulerGovernor_HeadOfLineNonBlockingBypass(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 5,
		EngineLimits: map[string]int{
			"ytdlp": 2,
			"aria2": 2,
		},
	}
	db, _, jobRepo, queueRepo, sched, _, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	// 1. Enqueue 2 ytdlp jobs at positions 1 and 2
	j1 := &job.Job{ID: "j-yt-1", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j-yt-2", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// 2. Enqueue 3rd ytdlp job at position 3 (will be blocked because ytdlp limit=2)
	j3 := &job.Job{ID: "j-yt-3", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j3)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j3.ID, Position: 3, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// 3. Enqueue 4th job with engine aria2 at position 4 (aria2 capacity is free: 0/2, global is free: 2/5)
	j4 := &job.Job{ID: "j-aria-4", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j4)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j4.ID, Position: 4, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	// Wait for dispatches
	time.Sleep(150 * time.Millisecond)

	// j-yt-1, j-yt-2, and j-aria-4 should be downloading!
	j1Check, _ := jobRepo.GetByID(ctx, j1.ID)
	j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
	j3Check, _ := jobRepo.GetByID(ctx, j3.ID)
	j4Check, _ := jobRepo.GetByID(ctx, j4.ID)

	if j1Check.Status != job.StatusDownloading {
		t.Fatalf("expected j-yt-1 downloading, got %s", j1Check.Status)
	}
	if j2Check.Status != job.StatusDownloading {
		t.Fatalf("expected j-yt-2 downloading, got %s", j2Check.Status)
	}
	// j3 MUST remain QUEUED because ytdlp engine capacity is saturated
	if j3Check.Status != job.StatusQueued {
		t.Fatalf("expected j-yt-3 to remain QUEUED, got %s", j3Check.Status)
	}
	// j4 MUST be DOWNLOADING because it bypassed blocked j3 without head-of-line blocking!
	if j4Check.Status != job.StatusDownloading {
		t.Fatalf("expected j-aria-4 to be DOWNLOADING (bypassed blocked j-yt-3), got %s", j4Check.Status)
	}

	// Verify j3 is still in the queue at position 3
	entry3, err := queueRepo.Get(ctx, j3.ID)
	if err != nil || entry3 == nil {
		t.Fatalf("expected j-yt-3 still in queue, got err=%v", err)
	}
	if entry3.Position != 3 {
		t.Fatalf("expected j-yt-3 queue position to remain 3, got %d", entry3.Position)
	}
}

// 5. Run Now job respects hard resource limit
func TestSchedulerGovernor_RunNowRespectsHardResourceLimit(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 2,
		EngineLimits: map[string]int{
			"ytdlp": 2,
		},
	}
	db, mgr, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	// Fill capacity with 2 jobs
	j1 := &job.Job{ID: "j1", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j2", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Third job
	j3 := &job.Job{ID: "j3", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j3)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j3.ID, Position: 3, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 2 {
		t.Fatalf("expected 2 active transfers, got %d", snap.ActiveTransfers)
	}

	// Apply Run Now to j3
	_, err := mgr.RunNow(ctx, j3.ID)
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}

	// Kick scheduler
	sched.Kick()
	time.Sleep(100 * time.Millisecond)

	// j3 MUST NOT dispatch because capacity is 2/2 (hard limit)
	j3Check, _ := jobRepo.GetByID(ctx, j3.ID)
	if j3Check.Status != job.StatusQueued {
		t.Fatalf("expected j3 to remain QUEUED despite Run Now when capacity is full, got %s", j3Check.Status)
	}

	// Pause j1 -> frees 1 slot
	_, err = mgr.Pause(ctx, j1.ID)
	if err != nil {
		t.Fatalf("pause j1 failed: %v", err)
	}

	// Scheduler automatically kicks on Pause; wait for j3 dispatch
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		j3Check, _ = jobRepo.GetByID(ctx, j3.ID)
		if j3Check.Status == job.StatusDownloading {
			break
		}
	}

	if j3Check.Status != job.StatusDownloading {
		t.Fatalf("expected j3 to be dispatched once capacity was freed, got %s", j3Check.Status)
	}
}

// 6. Media processing retains transfer capacity:
// yt-dlp holds its transfer lease conservatively for its complete process lifetime
// (downloading + FFmpeg processing) until terminal exit, preventing process explosion.
func TestSchedulerGovernor_MediaProcessingRetainsTransferCapacity(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 1,
		EngineLimits: map[string]int{
			"ytdlp": 1,
		},
	}
	db, mgr, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	j1 := &job.Job{ID: "j1", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j2", Status: job.StatusQueued, Engine: "ytdlp", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	// j1 is downloading
	j1Check, _ := jobRepo.GetByID(ctx, j1.ID)
	if j1Check.Status != job.StatusDownloading {
		t.Fatalf("expected j1 downloading, got %s", j1Check.Status)
	}

	// Update j1 status to PROCESSING via UpdateJobFromEngine
	mgr.UpdateJobFromEngine(ctx, j1Check, &job.EngineStatus{
		Status: job.StatusProcessing,
	}, true)

	// Governor should still show ActiveTransfers=1 (j1 retains transfer permit throughout processing)
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer while processing, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["ytdlp"] != 1 {
		t.Errorf("expected 1 active ytdlp while processing, got %d", snap.ActivePerEngine["ytdlp"])
	}

	// j2 MUST remain QUEUED because j1 holds the transfer permit until process exit
	time.Sleep(100 * time.Millisecond)
	j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
	if j2Check.Status != job.StatusQueued {
		t.Fatalf("expected j2 to remain queued while j1 is processing, got %s", j2Check.Status)
	}

	// Mark j1 complete (terminal exit)
	mgr.UpdateJobFromEngine(ctx, j1Check, &job.EngineStatus{
		Status:     job.StatusCompleted,
		TotalBytes: 1000,
	}, true)

	// Now j2 dispatches
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		j2Check, _ = jobRepo.GetByID(ctx, j2.ID)
		if j2Check.Status == job.StatusDownloading {
			break
		}
	}

	if j2Check == nil || j2Check.Status != job.StatusDownloading {
		t.Fatalf("expected j2 to be admitted after j1 completed, got status=%v", j2Check)
	}

	snap = gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer (j2), got %d", snap.ActiveTransfers)
	}
}

// 6b. Torrent entering seeding frees transfer capacity:
// When a torrent finishes downloading and enters seeding, the transfer permit is released,
// kicking the scheduler so the next queued download can dispatch while seeding continues.
func TestSchedulerGovernor_TorrentSeedingFreesTransferCapacity(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 1,
	}
	db, mgr, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	// tor-1 is an active downloading torrent
	j1 := &job.Job{
		ID:                "tor-1",
		Type:              job.TypeTorrent,
		Status:            job.StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "hash-tor-1",
		CreatedAt:         t0,
		UpdatedAt:         t0,
		SeedAfterComplete: true,
	}
	jobRepo.Create(ctx, j1)
	if _, err := gov.TryAcquire(j1.ID, job.ResourceRequirements{Engine: j1.Engine}); err != nil {
		t.Fatalf("acquire j1: %v", err)
	}

	// down-2 is queued
	j2 := &job.Job{ID: "down-2", Type: job.TypeDownload, Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	// Verify down-2 remains queued because tor-1 holds global transfer limit (1/1)
	j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
	if j2Check.Status != job.StatusQueued {
		t.Fatalf("expected down-2 to remain queued while tor-1 is downloading, got %s", j2Check.Status)
	}

	// tor-1 completes downloading and enters seeding
	mgr.UpdateJobFromEngine(ctx, j1, &job.EngineStatus{
		Status:      job.StatusSeeding,
		UploadSpeed: 500,
		TotalBytes:  5000,
	}, true)

	// Governor should show tor-1 released its transfer permit, allowing down-2 to dispatch
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		j2Check, _ = jobRepo.GetByID(ctx, j2.ID)
		if j2Check.Status == job.StatusDownloading {
			break
		}
	}

	if j2Check == nil || j2Check.Status != job.StatusDownloading {
		t.Fatalf("expected down-2 to be admitted after tor-1 entered seeding, got status=%v", j2Check)
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer (down-2), got %d", snap.ActiveTransfers)
	}
}

func TestSchedulerGovernor_NoOpReleaseDoesNotRedundantKick(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 2,
	}
	db, _, _, _, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()

	// Calling release on unheld jobID returns false
	released := gov.Release("non-existent-job")
	if released {
		t.Fatal("expected Release on unheld job to return false")
	}

	// Double release is idempotent
	gov.TryAcquire("held-job", job.ResourceRequirements{Engine: "aria2"})
	first := gov.Release("held-job")
	if !first {
		t.Fatal("expected first Release to return true")
	}
	second := gov.Release("held-job")
	if second {
		t.Fatal("expected second Release to return false")
	}
	_ = sched
}

// 7. Job completion releases capacity
func TestSchedulerGovernor_JobCompletionReleasesCapacity(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 1,
		EngineLimits: map[string]int{
			"aria2": 1,
		},
	}
	db, mgr, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	j1 := &job.Job{ID: "j1", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j2", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	j1Check, _ := jobRepo.GetByID(ctx, j1.ID)
	if j1Check.Status != job.StatusDownloading {
		t.Fatalf("expected j1 downloading, got %s", j1Check.Status)
	}

	// Mark j1 complete
	mgr.UpdateJobFromEngine(ctx, j1Check, &job.EngineStatus{
		Status:     job.StatusCompleted,
		TotalBytes: 1000,
	}, true)

	// Wait for j2 to dispatch
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
		if j2Check.Status == job.StatusDownloading {
			break
		}
	}

	j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
	if j2Check.Status != job.StatusDownloading {
		t.Fatalf("expected j2 downloading after j1 completion, got %s", j2Check.Status)
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer, got %d", snap.ActiveTransfers)
	}
}

// 8. Pause and cancel release capacity
func TestSchedulerGovernor_PauseAndCancelReleaseCapacity(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 1,
		EngineLimits: map[string]int{
			"aria2": 1,
		},
	}
	db, mgr, jobRepo, queueRepo, sched, gov, _, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	j1 := &job.Job{ID: "j1", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j2", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	// Pause j1 -> j2 starts
	mgr.Pause(ctx, j1.ID)

	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
		j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
		if j2Check.Status == job.StatusDownloading {
			break
		}
	}

	j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
	if j2Check.Status != job.StatusDownloading {
		t.Fatalf("expected j2 downloading after j1 pause, got %s", j2Check.Status)
	}

	// Cancel j2 -> capacity drops to 0
	mgr.Cancel(ctx, j2.ID)
	time.Sleep(50 * time.Millisecond)

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 0 {
		t.Errorf("expected 0 active transfers after cancelling j2, got %d", snap.ActiveTransfers)
	}
}

// 8b. Failed pause safety for aria2 and qbittorrent:
// If engine pause fails, the job remains active and governor capacity remains held.
// Only after successful pause is the transfer lease released.
func TestSchedulerGovernor_FailedPauseRetainsLease(t *testing.T) {
	for _, engName := range []string{"aria2", "qbittorrent"} {
		t.Run(engName, func(t *testing.T) {
			cfg := job.ResourceGovernorConfig{
				GlobalTransferLimit: 1,
				EngineLimits: map[string]int{
					engName: 1,
				},
			}
			db, mgr, jobRepo, queueRepo, sched, gov, fakeEng, _ := setupGovernorTestEnv(t, cfg)
			defer db.Close()
			ctx := context.Background()
			t0 := time.Now()

			j1 := &job.Job{ID: "j1-" + engName, Status: job.StatusQueued, Engine: engName, CreatedAt: t0, UpdatedAt: t0}
			j2 := &job.Job{ID: "j2-" + engName, Status: job.StatusQueued, Engine: engName, CreatedAt: t0, UpdatedAt: t0}
			jobRepo.Create(ctx, j1)
			jobRepo.Create(ctx, j2)
			queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
			queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

			sched.Start(ctx)
			defer sched.Stop()
			sched.Kick()

			time.Sleep(100 * time.Millisecond)

			// j1 must be downloading and hold governor transfer lease
			j1Check, _ := jobRepo.GetByID(ctx, j1.ID)
			if j1Check.Status != job.StatusDownloading {
				t.Fatalf("expected j1 downloading, got %s", j1Check.Status)
			}
			snap := gov.Snapshot()
			if snap.ActiveTransfers != 1 || snap.ActivePerEngine[engName] != 1 {
				t.Fatalf("expected 1 active transfer held before pause attempt, got %+v", snap)
			}

			// Simulate engine pause failure
			fakeEng.failPause.Store(true)

			_, pauseErr := mgr.Pause(ctx, j1.ID)
			if pauseErr == nil {
				t.Fatal("expected Pause to return error when engine fails pause")
			}

			// Job must remain downloading in DB
			j1Check, _ = jobRepo.GetByID(ctx, j1.ID)
			if j1Check.Status != job.StatusDownloading {
				t.Fatalf("expected j1 to remain downloading after failed pause, got %s", j1Check.Status)
			}

			// CRITICAL: Governor usage must still be held (1/1)
			snap = gov.Snapshot()
			if snap.ActiveTransfers != 1 {
				t.Fatalf("expected ActiveTransfers to remain 1 after failed pause, got %d", snap.ActiveTransfers)
			}
			if snap.ActivePerEngine[engName] != 1 {
				t.Fatalf("expected ActivePerEngine[%s] to remain 1 after failed pause, got %d", engName, snap.ActivePerEngine[engName])
			}

			// Queued job j2 must NOT be dispatched
			time.Sleep(100 * time.Millisecond)
			j2Check, _ := jobRepo.GetByID(ctx, j2.ID)
			if j2Check.Status != job.StatusQueued {
				t.Fatalf("expected j2 to remain queued while j1 pause failed, got %s", j2Check.Status)
			}

			// Now allow pause to succeed
			fakeEng.failPause.Store(false)

			_, pauseErr = mgr.Pause(ctx, j1.ID)
			if pauseErr != nil {
				t.Fatalf("expected Pause to succeed, got %v", pauseErr)
			}

			// After successful pause, j1 is paused and j2 is dispatched
			for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(20 * time.Millisecond) {
				j2Check, _ = jobRepo.GetByID(ctx, j2.ID)
				if j2Check.Status == job.StatusDownloading {
					break
				}
			}

			if j2Check == nil || j2Check.Status != job.StatusDownloading {
				t.Fatalf("expected j2 downloading after j1 successfully paused, got status=%v", j2Check)
			}

			snap = gov.Snapshot()
			if snap.ActiveTransfers != 1 {
				t.Errorf("expected 1 active transfer (held by j2), got %d", snap.ActiveTransfers)
			}
		})
	}
}

// 9. Dispatch failure releases governor lease without leaking
func TestSchedulerGovernor_DispatchFailureReleasesLease(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 1,
		EngineLimits: map[string]int{
			"aria2": 1,
		},
	}
	db, _, jobRepo, queueRepo, sched, gov, fakeEng, _ := setupGovernorTestEnv(t, cfg)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	fakeEng.failStart.Store(true)

	j1 := &job.Job{ID: "j1", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(100 * time.Millisecond)

	j1Check, _ := jobRepo.GetByID(ctx, j1.ID)
	if j1Check.Status != job.StatusFailed {
		t.Fatalf("expected j1 to be FAILED, got %s", j1Check.Status)
	}

	// Lease must have been released upon dispatch failure
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 0 {
		t.Fatalf("expected 0 active transfers after dispatch failure, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 0 {
		t.Fatalf("expected 0 active aria2 after dispatch failure, got %d", snap.ActivePerEngine["aria2"])
	}
}

// 10. Startup recovery reconstruction:
// Active jobs in DB reconstructed into Governor on restart
func TestSchedulerGovernor_StartupRecoveryReconstruction(t *testing.T) {
	cfg := job.ResourceGovernorConfig{
		GlobalTransferLimit: 2,
		EngineLimits: map[string]int{
			"aria2": 2,
		},
	}
	db, _, jobRepo, _, _, _, _, dbPath := setupGovernorTestEnv(t, cfg)
	ctx := context.Background()
	t0 := time.Now()

	// Create 2 downloading jobs in DB
	j1 := &job.Job{ID: "rec-1", Status: job.StatusDownloading, Engine: "aria2", EngineID: "eng-rec-1", CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "rec-2", Status: job.StatusDownloading, Engine: "aria2", EngineID: "eng-rec-2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)

	// Close DB
	db.Close()

	// Reopen DB and restart manager
	db2, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db2.Close()

	jobRepo2 := database.NewSQLiteJobRepository(db2)
	queueRepo2 := database.NewSQLiteQueueRepository(db2)
	engReg2 := engine.NewRegistry()
	fakeEng2 := &governorTestEngine{}
	engReg2.Register("aria2", fakeEng2)

	mgr2 := job.NewManager(jobRepo2, engReg2, events.NewInMemoryBus(), t.TempDir(), nil, t.TempDir())
	mgr2.SetQueueRepository(queueRepo2)

	gov2 := job.NewResourceGovernor(cfg)
	mgr2.SetResourceGovernor(gov2)

	limitFn := func(ctx context.Context) int { return cfg.GlobalTransferLimit }
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return mgr2.DispatchQueuedJob(ctx, qj)
	}
	sched2 := job.NewScheduler(jobRepo2, queueRepo2, limitFn, dispatchFn)
	sched2.SetResourceGovernor(gov2)
	mgr2.SetScheduler(sched2)

	// Run startup background tasks (which includes recovery and governor reconstruction)
	mgr2.StartBackgroundTasks(ctx)
	defer mgr2.Stop()

	// Assert Governor reconstructed the 2 active downloading jobs
	snap := gov2.Snapshot()
	if snap.ActiveTransfers != 2 {
		t.Fatalf("expected 2 active transfers reconstructed, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 2 {
		t.Fatalf("expected 2 active aria2 reconstructed, got %d", snap.ActivePerEngine["aria2"])
	}

	// Enqueue a 3rd job: cannot be admitted because global capacity is 2/2
	j3 := &job.Job{ID: "rec-3", Status: job.StatusQueued, Engine: "aria2", CreatedAt: t0, UpdatedAt: t0}
	jobRepo2.Create(ctx, j3)
	queueRepo2.Enqueue(ctx, &job.QueueEntry{JobID: j3.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	sched2.Kick()
	time.Sleep(100 * time.Millisecond)

	j3Check, _ := jobRepo2.GetByID(ctx, j3.ID)
	if j3Check.Status != job.StatusQueued {
		t.Fatalf("expected j3 to remain QUEUED under full capacity after restart, got %s", j3Check.Status)
	}
}

// 11. Backward compatibility: governor == nil fallback operates normally
func TestSchedulerGovernor_NilGovernorBackwardCompatibility(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_nil_gov.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	t0 := time.Now()
	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	engReg := engine.NewRegistry()
	fakeEng := &governorTestEngine{}
	engReg.Register("fake", fakeEng)

	mgr := job.NewManager(jobRepo, engReg, events.NewInMemoryBus(), tempDir, nil, tempDir)
	mgr.SetQueueRepository(queueRepo)
	// governor is intentionally NIL

	limitFn := func(ctx context.Context) int { return 2 }
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return mgr.DispatchQueuedJob(ctx, qj)
	}

	sched := job.NewScheduler(jobRepo, queueRepo, limitFn, dispatchFn)
	mgr.SetScheduler(sched)
	// sched.governor is NIL

	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("nil-gov-%d", i)
		j := &job.Job{ID: id, Status: job.StatusQueued, Engine: "fake", CreatedAt: t0, UpdatedAt: t0}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: id, Position: int64(i), Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	}

	sched.Start(ctx)
	defer sched.Stop()
	sched.Kick()

	time.Sleep(150 * time.Millisecond)

	// Exactly 2 jobs should start under limit=2
	downloading, err := jobRepo.CountDownloading(ctx)
	if err != nil {
		t.Fatalf("CountDownloading: %v", err)
	}
	if downloading != 2 {
		t.Fatalf("expected 2 downloading jobs under nil governor, got %d", downloading)
	}
}
