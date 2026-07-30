package job

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type failingUpdateJobRepository struct {
	IJobRepository
	failUpdateCount int
	updateCalls     int
}

func (r *failingUpdateJobRepository) Update(ctx context.Context, j *Job) error {
	r.updateCalls++
	if r.failUpdateCount > 0 {
		r.failUpdateCount--
		return fmt.Errorf("%w: simulated persistence failure", ErrDispatchPersistenceFailed)
	}
	return r.IJobRepository.Update(ctx, j)
}

func setupReconciliationTestEnv(t *testing.T, initialStatus JobStatus, policy networkpolicy.SeedingPolicy) (*Manager, *Scheduler, *failingUpdateJobRepository, *fakeTorrentRepository, *fakeTorrentEngine, *Job) {
	t.Helper()
	jobRepo := newFakeJobRepository()
	failRepo := &failingUpdateJobRepository{IJobRepository: jobRepo}
	torrentRepo := newFakeTorrentRepository(jobRepo)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	bus := newFakeEventBus()
	reg := &fakeEngineRegistry{engines: make(map[string]IEngine)}

	downloadDir := t.TempDir()
	dataDir := t.TempDir()

	mgr := NewManager(failRepo, reg, bus, downloadDir, torrentRepo, dataDir)
	mgr.queueRepo = queueRepo

	sched := NewScheduler(
		failRepo,
		queueRepo,
		func(ctx context.Context) int { return 5 },
		mgr.dispatchQueuedJob,
	)
	mgr.SetScheduler(sched)

	seedAfter := policy.Mode != networkpolicy.SeedingModeNone

	j := &Job{
		ID:                "rec-torrent-job-1",
		Source:            "magnet:?xt=urn:btih:rec1111222233334444555566667777",
		Name:              "reconcile-torrent",
		Status:            initialStatus,
		Type:              TypeTorrent,
		Engine:            "qbittorrent",
		DestinationDir:    downloadDir,
		SeedAfterComplete: seedAfter,
		SeedingPolicy:     policy,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if initialStatus == StatusPaused {
		j.EngineID = "rec1111222233334444555566667777"
	}
	_ = jobRepo.Create(context.Background(), j)

	rec := &TorrentJobRecord{
		JobID:             j.ID,
		InfoHash:          "rec1111222233334444555566667777",
		SeedAfterComplete: seedAfter,
		SeedingPolicy:     policy,
	}
	_ = torrentRepo.CreateTorrentJob(context.Background(), rec)

	fakeEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{
				Status:         StatusDownloading,
				Progress:       50.0,
				TotalBytes:     1000,
				CompletedBytes: 500,
			}, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			return "rec1111222233334444555566667777", nil
		},
		startDownloadFunc: func(hash string) error {
			return nil
		},
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return nil
		},
	}
	reg.engines["qbittorrent"] = fakeEng

	return mgr, sched, failRepo, torrentRepo, fakeEng, j
}

// Test A: Queued Start Persistence Failure Reconciliation
func TestSchedulerReconciliation_HydratesTorrentAfterStartPersistenceFailure(t *testing.T) {
	ratioVal := 1.5
	policy := networkpolicy.SeedingPolicy{
		Mode:       networkpolicy.SeedingModeRatio,
		RatioLimit: &ratioVal,
	}
	mgr, sched, failRepo, _, _, j := setupReconciliationTestEnv(t, StatusQueued, policy)
	ctx := context.Background()

	// Cause generic jobs Update to fail on dispatch persistence write
	failRepo.failUpdateCount = 1

	// Dispatch queued job directly
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	err := mgr.dispatchQueuedJob(ctx, qj)
	if !errors.Is(err, ErrDispatchPersistenceFailed) {
		t.Fatalf("expected ErrDispatchPersistenceFailed, got: %v", err)
	}

	// Mark reservation dirty for external execution reconciliation
	sched.markReservationDirtyExternal(j.ID, QueueActionStart, "rec1111222233334444555566667777")
	if !sched.hasUnresolvedReconciliations() {
		t.Fatal("expected unresolved reconciliations")
	}

	// Run reconciliation
	sched.reconcileAll(ctx)

	if sched.hasUnresolvedReconciliations() {
		t.Error("expected reconciliation to resolve reservation")
	}

	// Verify activeJobs contains hydrated job
	active := mgr.GetActiveJobs()
	act, ok := active[j.ID]
	if !ok {
		t.Fatalf("expected job %s to be in activeJobs after reconciliation", j.ID)
	}
	if act.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || act.SeedingPolicy.RatioLimit == nil || *act.SeedingPolicy.RatioLimit != 1.5 {
		t.Errorf("expected active job to have hydrated ratio policy 1.5, got %+v", act.SeedingPolicy)
	}
	if !act.SeedAfterComplete {
		t.Error("expected SeedAfterComplete=true on reconciled active job")
	}
}

// Test B: Policy None Reconciliation
func TestSchedulerReconciliation_HydratesNonePolicyBeforeActivation(t *testing.T) {
	policy := networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeNone,
	}
	mgr, sched, failRepo, _, _, j := setupReconciliationTestEnv(t, StatusQueued, policy)
	ctx := context.Background()

	failRepo.failUpdateCount = 1

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.dispatchQueuedJob(ctx, qj)
	sched.markReservationDirtyExternal(j.ID, QueueActionStart, "rec1111222233334444555566667777")

	sched.reconcileAll(ctx)

	active := mgr.GetActiveJobs()
	act, ok := active[j.ID]
	if !ok {
		t.Fatalf("expected job %s in activeJobs", j.ID)
	}
	if act.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || act.SeedAfterComplete {
		t.Errorf("expected policy none and SeedAfterComplete=false, got %+v (SeedAfterComplete=%v)", act.SeedingPolicy, act.SeedAfterComplete)
	}

	// Simulate engine completion
	mgr.UpdateJobFromEngine(ctx, act, &EngineStatus{
		Status:         StatusCompleted,
		Progress:       100.0,
		TotalBytes:     1000,
		CompletedBytes: 1000,
	}, true)

	updated, _ := failRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected job with policy none to transition directly to COMPLETED, got %s", updated.Status)
	}
}

// Test C: Paused Resume Persistence Failure
func TestSchedulerReconciliation_HydratesTorrentAfterResumePersistenceFailure(t *testing.T) {
	ratioVal := 2.0
	policy := networkpolicy.SeedingPolicy{
		Mode:       networkpolicy.SeedingModeRatio,
		RatioLimit: &ratioVal,
	}
	mgr, sched, failRepo, _, _, j := setupReconciliationTestEnv(t, StatusPaused, policy)
	ctx := context.Background()

	failRepo.failUpdateCount = 1

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionResume}
	err := mgr.dispatchQueuedJob(ctx, qj)
	if !errors.Is(err, ErrDispatchPersistenceFailed) {
		t.Fatalf("expected ErrDispatchPersistenceFailed, got %v", err)
	}

	sched.markReservationDirtyExternal(j.ID, QueueActionResume, "rec1111222233334444555566667777")
	sched.reconcileAll(ctx)

	active := mgr.GetActiveJobs()
	act, ok := active[j.ID]
	if !ok {
		t.Fatalf("expected job %s in activeJobs after resume reconciliation", j.ID)
	}
	if act.SeedingPolicy.RatioLimit == nil || *act.SeedingPolicy.RatioLimit != 2.0 {
		t.Errorf("expected ratio limit 2.0 on resumed active job, got %+v", act.SeedingPolicy)
	}
}

// Test D: Reconciled SEEDING State
func TestSchedulerReconciliation_HydratesTorrentWhenEngineReportsSeeding(t *testing.T) {
	ratioVal := 1.5
	policy := networkpolicy.SeedingPolicy{
		Mode:       networkpolicy.SeedingModeRatio,
		RatioLimit: &ratioVal,
	}
	mgr, sched, failRepo, _, fakeEng, j := setupReconciliationTestEnv(t, StatusQueued, policy)
	ctx := context.Background()

	failRepo.failUpdateCount = 1

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.dispatchQueuedJob(ctx, qj)

	// Engine is already seeding when reconciliation queries engine status
	fakeEng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusSeeding,
			Progress:       100.0,
			TotalBytes:     1000,
			CompletedBytes: 1000,
		}, nil
	}

	sched.markReservationDirtyExternal(j.ID, QueueActionStart, "rec1111222233334444555566667777")
	sched.reconcileAll(ctx)

	active := mgr.GetActiveJobs()
	act, ok := active[j.ID]
	if !ok {
		t.Fatalf("expected job %s in activeJobs", j.ID)
	}
	if act.Status != StatusSeeding {
		t.Errorf("expected StatusSeeding, got %s", act.Status)
	}
	if act.SeedingPolicy.RatioLimit == nil || *act.SeedingPolicy.RatioLimit != 1.5 {
		t.Errorf("expected ratio policy 1.5 on reconciled seeding job, got %+v", act.SeedingPolicy)
	}
}

// Test E: Reconciliation Hydration Failure
func TestSchedulerReconciliation_HydrationFailureKeepsReservationPending(t *testing.T) {
	ratioVal := 1.5
	policy := networkpolicy.SeedingPolicy{
		Mode:       networkpolicy.SeedingModeRatio,
		RatioLimit: &ratioVal,
	}
	mgr, sched, failRepo, torrentRepo, _, j := setupReconciliationTestEnv(t, StatusQueued, policy)
	ctx := context.Background()

	failRepo.failUpdateCount = 1

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.dispatchQueuedJob(ctx, qj)

	// Delete torrent record to simulate missing torrent_jobs record / hydration failure
	_ = torrentRepo.DeleteTorrentJob(ctx, j.ID)

	sched.markReservationDirtyExternal(j.ID, QueueActionStart, "rec1111222233334444555566667777")
	sched.reconcileAll(ctx)

	// Reservation must remain dirty / pending
	if !sched.hasUnresolvedReconciliations() {
		t.Error("expected unresolved reconciliations to remain true on hydration failure")
	}

	// Job must not be added to activeJobs
	active := mgr.GetActiveJobs()
	if _, ok := active[j.ID]; ok {
		t.Error("job must NOT be added to activeJobs when hydration fails")
	}

	// Now recreate the torrent_jobs record to restore hydration capability
	_ = torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:             j.ID,
		InfoHash:          "rec1111222233334444555566667777",
		SeedAfterComplete: true,
		SeedingPolicy:     policy,
	})

	// Retry reconciliation
	sched.reconcileAll(ctx)

	if sched.hasUnresolvedReconciliations() {
		t.Error("expected reconciliation to succeed after torrent record restored")
	}

	active = mgr.GetActiveJobs()
	if _, ok := active[j.ID]; !ok {
		t.Error("expected job in activeJobs after successful reconciliation retry")
	}
}

// Test F: Non-Torrent Regression
func TestSchedulerReconciliation_NonTorrentActivationUnaffected(t *testing.T) {
	jobRepo := newFakeJobRepository()
	failRepo := &failingUpdateJobRepository{IJobRepository: jobRepo}
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	bus := newFakeEventBus()
	reg := &fakeEngineRegistry{engines: make(map[string]IEngine)}

	downloadDir := t.TempDir()
	dataDir := t.TempDir()

	mgr := NewManager(failRepo, reg, bus, downloadDir, nil, dataDir)
	mgr.queueRepo = queueRepo

	sched := NewScheduler(
		failRepo,
		queueRepo,
		func(ctx context.Context) int { return 5 },
		mgr.dispatchQueuedJob,
	)
	mgr.SetScheduler(sched)

	j := &Job{
		ID:             "direct-rec-1",
		Source:         "https://example.com/file.bin",
		Name:           "direct-rec",
		Status:         StatusQueued,
		Type:           TypeDownload,
		Engine:         "aria2",
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	fakeEng := &fakeEngine{
		startFunc: func(ctx context.Context, j *Job, dir string) (string, error) {
			return "gid-direct-1", nil
		},
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{
				Status:         StatusDownloading,
				Progress:       25.0,
				TotalBytes:     1000,
				CompletedBytes: 250,
			}, nil
		},
	}
	reg.engines["aria2"] = fakeEng

	failRepo.failUpdateCount = 1

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.dispatchQueuedJob(context.Background(), qj)

	sched.markReservationDirtyExternal(j.ID, QueueActionStart, "gid-direct-1")
	sched.reconcileAll(context.Background())

	if sched.hasUnresolvedReconciliations() {
		t.Error("expected non-torrent reconciliation to succeed")
	}

	active := mgr.GetActiveJobs()
	if _, ok := active[j.ID]; !ok {
		t.Error("expected non-torrent job in activeJobs")
	}
}
