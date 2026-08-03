package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type closureSeedingEngine struct {
	*fakeTorrentEngine
	applied      []networkpolicy.SeedingPolicy
	applyFailure int
}

func (e *closureSeedingEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{SeedingPolicy: true}
}

func (e *closureSeedingEngine) ApplySeedingPolicy(_ context.Context, _ *Job, policy networkpolicy.SeedingPolicy) error {
	e.applied = append(e.applied, policy)
	if e.applyFailure > 0 && len(e.applied) == e.applyFailure {
		return errors.New("share-limit application failed")
	}
	return nil
}

func setupSeedingPolicyJob(t *testing.T, status JobStatus) (*Manager, *fakeJobRepository, *fakeTorrentRepository, *fakeEventBus, *closureSeedingEngine, *Job) {
	t.Helper()
	manager, _, bus, cleanup, torrentEngine := setupManagerTest(t)
	t.Cleanup(cleanup)
	engine := &closureSeedingEngine{fakeTorrentEngine: torrentEngine}
	manager.engines.(*fakeEngineRegistry).engines["qbittorrent"] = engine
	repo := manager.repo.(*fakeJobRepository)
	torrentRepo := manager.torrentRepo.(*fakeTorrentRepository)
	now := time.Now()
	j := &Job{
		ID: "seeding-policy-job", Source: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name: "payload", Type: TypeTorrent, Engine: "qbittorrent", EngineID: "hash",
		Status: status, SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeUnlimited},
		SeedAfterComplete: true, DestinationDir: t.TempDir(), CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err := torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{
		JobID: j.ID, InfoHash: j.EngineID, SeedAfterComplete: true,
		SeedingPolicy: j.SeedingPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	return manager, repo, torrentRepo, bus, engine, j
}

func completedEventCount(bus *fakeEventBus) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	count := 0
	for _, event := range bus.events {
		if event.Type == EventJobCompleted {
			count++
		}
	}
	return count
}

func seedingPolicyUpdateEventCount(bus *fakeEventBus) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	count := 0
	for _, event := range bus.events {
		if event.Type == EventJobSeedingPolicyUpdated {
			count++
		}
	}
	return count
}

func TestUpdateSeedingPolicy_NoneDuringSeeding_StopFailurePreservesOldPolicy(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	removeCalls := 0
	engine.stopDownloadFunc = func(string) error { return errors.New("qB unavailable") }
	engine.removeTorrentFunc = func(string, bool) error {
		removeCalls++
		return nil
	}

	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrSeedingPolicyApplicationFailed {
		t.Fatalf("expected seeding application error, got %#v", err)
	}
	durable, _ := repo.GetByID(context.Background(), j.ID)
	record, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if durable.Status != StatusSeeding || durable.EngineCleanupPending {
		t.Fatalf("unexpected durable job after stop failure: %+v", durable)
	}
	if record.SeedingPolicy.Mode != networkpolicy.SeedingModeUnlimited || !record.SeedAfterComplete {
		t.Fatalf("old policy was not preserved: %+v", record)
	}
	if removeCalls != 0 || completedEventCount(bus) != 0 {
		t.Fatalf("removeCalls=%d completedEvents=%d", removeCalls, completedEventCount(bus))
	}
}

func TestUpdateSeedingPolicy_NoneDuringSeeding_UsesDurableCompletionFlow(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	order := []string{}
	engine.stopDownloadFunc = func(string) error {
		order = append(order, "stop")
		return nil
	}
	engine.removeTorrentFunc = func(string, bool) error {
		order = append(order, "remove")
		durable, _ := repo.GetByID(context.Background(), j.ID)
		if durable.Status != StatusCompleted || !durable.EngineCleanupPending {
			t.Fatalf("cleanup ran before durable completion: %+v", durable)
		}
		return nil
	}

	updated, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "stop" || order[1] != "remove" {
		t.Fatalf("unexpected lifecycle order: %v", order)
	}
	durable, _ := repo.GetByID(context.Background(), j.ID)
	record, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if durable.Status != StatusCompleted || durable.EngineCleanupPending || updated.Status != StatusCompleted {
		t.Fatalf("unexpected final job state: durable=%+v returned=%+v", durable, updated)
	}
	if record.SeedingStopReason != "policy_none" || record.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || record.SeedAfterComplete {
		t.Fatalf("unexpected final torrent state: %+v", record)
	}
	if completedEventCount(bus) != 1 {
		t.Fatalf("completed events=%d", completedEventCount(bus))
	}
}

func TestUpdateSeedingPolicy_NoneDuringSeeding_CompletionPersistenceFailure(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	torrentRepo.finalizeErr = errors.New("database unavailable")
	removeCalls := 0
	engine.removeTorrentFunc = func(string, bool) error {
		removeCalls++
		return nil
	}

	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrInternalError {
		t.Fatalf("expected internal persistence error, got %#v", err)
	}
	durable, _ := repo.GetByID(context.Background(), j.ID)
	if durable.Status == StatusCompleted || durable.EngineCleanupPending {
		t.Fatalf("database falsely completed torrent: %+v", durable)
	}
	if removeCalls != 0 || completedEventCount(bus) != 0 {
		t.Fatalf("removeCalls=%d completedEvents=%d", removeCalls, completedEventCount(bus))
	}
}

func TestUpdateSeedingPolicy_RejectsTerminalStates(t *testing.T) {
	for _, status := range []JobStatus{StatusCompleted, StatusFailed, StatusCancelled, StatusProcessing, StatusAnalyzing} {
		t.Run(string(status), func(t *testing.T) {
			manager, _, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, status)
			stopCalls := 0
			engine.stopDownloadFunc = func(string) error {
				stopCalls++
				return nil
			}
			before, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
			_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: floatPointer(1.5)})
			appErr, ok := err.(*AppError)
			if !ok || appErr.Code != ErrInvalidJobState {
				t.Fatalf("expected invalid state, got %#v", err)
			}
			after, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
			if stopCalls != 0 || len(engine.applied) != 0 || after.SeedingPolicy.Mode != before.SeedingPolicy.Mode {
				t.Fatalf("invalid state caused mutation: stop=%d applied=%v before=%+v after=%+v", stopCalls, engine.applied, before, after)
			}
		})
	}
}

func TestUpdateSeedingPolicy_LivePersistenceFailureRollsBack(t *testing.T) {
	manager, _, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	torrentRepo.updateErr = errors.New("database unavailable")
	next := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: floatPointer(1.5)}
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, next)
	if appErr, ok := err.(*AppError); !ok || appErr.Code != ErrInternalError {
		t.Fatalf("expected persistence error, got %#v", err)
	}
	if len(engine.applied) != 2 || engine.applied[0].Mode != networkpolicy.SeedingModeRatio || engine.applied[1].Mode != networkpolicy.SeedingModeUnlimited {
		t.Fatalf("expected apply then rollback, got %+v", engine.applied)
	}
}

func TestUpdateSeedingPolicy_LiveRollbackFailureIsAmbiguous(t *testing.T) {
	manager, _, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	torrentRepo.updateErr = errors.New("database unavailable")
	engine.applyFailure = 2
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatio, RatioLimit: floatPointer(1.5),
	})
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrSeedingPolicyStateAmbiguous {
		t.Fatalf("expected dedicated ambiguous-state error, got %#v", err)
	}
}

func TestUpdateSeedingPolicy_UsesDurableOldPolicyForRollback(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusSeeding)
	ratio := 1.5
	record, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	record = cloneTorrentRecord(record)
	record.SeedingPolicy = networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratio}
	record.SeedAfterComplete = true
	if err := torrentRepo.UpdateTorrentJob(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	stale, _ := repo.GetByID(context.Background(), j.ID)
	stale.SeedingPolicy = networkpolicy.SeedingPolicy{}
	stale.SeedAfterComplete = false
	if err := repo.Update(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	active := *j
	active.SeedingPolicy = cloneSeedingPolicy(record.SeedingPolicy)
	manager.addActive(&active)

	torrentRepo.updateErr = errors.New("database unavailable")
	duration := int64(7200)
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeDuration, TimeLimitSeconds: &duration,
	})
	if appErr, ok := err.(*AppError); !ok || appErr.Code != ErrInternalError {
		t.Fatalf("expected persistence error, got %#v", err)
	}
	if len(engine.applied) != 2 ||
		engine.applied[0].Mode != networkpolicy.SeedingModeDuration ||
		engine.applied[0].TimeLimitSeconds == nil || *engine.applied[0].TimeLimitSeconds != 7200 ||
		engine.applied[1].Mode != networkpolicy.SeedingModeRatio ||
		engine.applied[1].RatioLimit == nil || *engine.applied[1].RatioLimit != 1.5 {
		t.Fatalf("rollback did not use authoritative policy: %+v", engine.applied)
	}
	durable, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	activeAfter := manager.GetActiveJobs()[j.ID]
	if durable.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || *durable.SeedingPolicy.RatioLimit != 1.5 {
		t.Fatalf("durable policy changed: %+v", durable.SeedingPolicy)
	}
	if activeAfter.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || *activeAfter.SeedingPolicy.RatioLimit != 1.5 {
		t.Fatalf("active policy changed: %+v", activeAfter.SeedingPolicy)
	}
	if seedingPolicyUpdateEventCount(bus) != 0 {
		t.Fatal("failed persistence published a successful policy event")
	}
}

func TestUpdateSeedingPolicy_SynchronizesActiveJob(t *testing.T) {
	manager, _, torrentRepo, _, _, j := setupSeedingPolicyJob(t, StatusDownloading)
	startedAt := time.Now().Add(-time.Hour)
	record, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	record = cloneTorrentRecord(record)
	record.SeedingStartedAt = &startedAt
	record.SeedingStopReason = "previous"
	if err := torrentRepo.UpdateTorrentJob(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	manager.addActive(j)

	updated, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatal(err)
	}
	durable, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	active := manager.GetActiveJobs()[j.ID]
	if durable.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || durable.SeedAfterComplete {
		t.Fatalf("durable policy was not updated: %+v", durable)
	}
	if active.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || active.SeedAfterComplete {
		t.Fatalf("active policy was not synchronized: %+v", active)
	}
	if updated.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || updated.SeedAfterComplete {
		t.Fatalf("returned policy was not synchronized: %+v", updated)
	}
	if active.SeedingStartedAt == nil || updated.SeedingStartedAt == nil ||
		!active.SeedingStartedAt.Equal(startedAt) || !updated.SeedingStartedAt.Equal(startedAt) ||
		active.SeedingStopReason != "previous" || updated.SeedingStopReason != "previous" {
		t.Fatalf("authoritative lifecycle fields were not preserved: active=%+v returned=%+v", active, updated)
	}
}

func TestUpdatedPolicyControlsCompletionLifecycle(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusDownloading)
	manager.addActive(j)
	removeCalls := 0
	engine.removeTorrentFunc = func(string, bool) error {
		removeCalls++
		return nil
	}
	if _, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}); err != nil {
		t.Fatal(err)
	}

	active := manager.GetActiveJobs()[j.ID]
	manager.UpdateJobFromEngine(context.Background(), active, &EngineStatus{
		Status: StatusSeeding, Progress: 100,
	}, true)

	durableJob, _ := repo.GetByID(context.Background(), j.ID)
	durableTorrent, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if durableJob.Status != StatusCompleted || durableJob.EngineCleanupPending {
		t.Fatalf("updated none policy entered seeding instead of durable completion: %+v", durableJob)
	}
	if durableTorrent.SeedingPolicy.Mode != networkpolicy.SeedingModeNone ||
		durableTorrent.SeedingStopReason != "policy_none" || removeCalls != 1 {
		t.Fatalf("unexpected torrent completion state: record=%+v removes=%d", durableTorrent, removeCalls)
	}
	if _, exists := manager.GetActiveJobs()[j.ID]; exists {
		t.Fatal("completed torrent remained active")
	}
	if completedEventCount(bus) != 1 {
		t.Fatalf("completed events=%d", completedEventCount(bus))
	}
}

func TestUpdatedRatioPolicyUsedWhenDownloadCompletes(t *testing.T) {
	manager, repo, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusDownloading)
	manager.addActive(j)
	stopCalls := 0
	removeCalls := 0
	engine.stopDownloadFunc = func(string) error {
		stopCalls++
		return nil
	}
	engine.removeTorrentFunc = func(string, bool) error {
		removeCalls++
		return nil
	}
	ratio := 1.5
	if _, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratio,
	}); err != nil {
		t.Fatal(err)
	}

	active := manager.GetActiveJobs()[j.ID]
	manager.UpdateJobFromEngine(context.Background(), active, &EngineStatus{
		Status: StatusSeeding, Progress: 100, Ratio: 1.0, SeedingTimeSeconds: 60,
	}, true)
	seedingJob, _ := repo.GetByID(context.Background(), j.ID)
	active = manager.GetActiveJobs()[j.ID]
	if seedingJob.Status != StatusSeeding || active.Status != StatusSeeding ||
		active.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio ||
		active.SeedingPolicy.RatioLimit == nil || *active.SeedingPolicy.RatioLimit != 1.5 {
		t.Fatalf("updated ratio policy did not control seeding entry: durable=%+v active=%+v", seedingJob, active)
	}

	manager.UpdateJobFromEngine(context.Background(), active, &EngineStatus{
		Status: StatusSeeding, Progress: 100, Ratio: 1.5, SeedingTimeSeconds: 120,
	}, true)
	completedJob, _ := repo.GetByID(context.Background(), j.ID)
	completedTorrent, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if completedJob.Status != StatusCompleted || completedJob.EngineCleanupPending ||
		completedTorrent.SeedingStopReason != "ratio" || stopCalls != 1 || removeCalls != 1 {
		t.Fatalf("ratio threshold did not use hardened completion: job=%+v torrent=%+v stop=%d remove=%d",
			completedJob, completedTorrent, stopCalls, removeCalls)
	}
	if completedEventCount(bus) != 1 {
		t.Fatalf("completed events=%d", completedEventCount(bus))
	}
}

func TestFailedPolicyPersistenceDoesNotMutateActiveJob(t *testing.T) {
	manager, _, torrentRepo, bus, engine, j := setupSeedingPolicyJob(t, StatusDownloading)
	manager.addActive(j)
	torrentRepo.updateErr = errors.New("database unavailable")
	ratio := 2.0
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratio,
	})
	if appErr, ok := err.(*AppError); !ok || appErr.Code != ErrInternalError {
		t.Fatalf("expected persistence error, got %#v", err)
	}
	active := manager.GetActiveJobs()[j.ID]
	durable, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if active.SeedingPolicy.Mode != networkpolicy.SeedingModeUnlimited || !active.SeedAfterComplete {
		t.Fatalf("active policy changed before durable persistence: %+v", active)
	}
	if durable.SeedingPolicy.Mode != networkpolicy.SeedingModeUnlimited || !durable.SeedAfterComplete {
		t.Fatalf("durable policy changed on failure: %+v", durable)
	}
	if len(engine.applied) != 2 || engine.applied[1].Mode != networkpolicy.SeedingModeUnlimited {
		t.Fatalf("external policy was not rolled back: %+v", engine.applied)
	}
	if seedingPolicyUpdateEventCount(bus) != 0 {
		t.Fatal("failed persistence published a successful policy event")
	}
}

func TestPolicyPointerFieldsAreCopied(t *testing.T) {
	manager, _, torrentRepo, bus, _, j := setupSeedingPolicyJob(t, StatusDownloading)
	manager.addActive(j)
	ratio := 1.5
	source := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratio}
	updated, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	active := manager.GetActiveJobs()[j.ID]
	durable, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	bus.mu.Lock()
	event := bus.events[len(bus.events)-1]
	bus.mu.Unlock()
	eventData, ok := event.Data.(map[string]any)
	if !ok {
		t.Fatalf("event data has unexpected type: %T", event.Data)
	}
	eventPolicy, ok := eventData["seedingPolicy"].(networkpolicy.SeedingPolicy)
	if !ok {
		t.Fatalf("event policy has unexpected type: %T", eventData["seedingPolicy"])
	}

	ratio = 9
	if *updated.SeedingPolicy.RatioLimit != 1.5 ||
		*active.SeedingPolicy.RatioLimit != 1.5 ||
		*durable.SeedingPolicy.RatioLimit != 1.5 ||
		*event.Job.SeedingPolicy.RatioLimit != 1.5 ||
		*eventPolicy.RatioLimit != 1.5 {
		t.Fatalf("source pointer mutation leaked: returned=%v active=%v durable=%v event=%v eventData=%v",
			*updated.SeedingPolicy.RatioLimit, *active.SeedingPolicy.RatioLimit,
			*durable.SeedingPolicy.RatioLimit, *event.Job.SeedingPolicy.RatioLimit, *eventPolicy.RatioLimit)
	}
	if source.RatioLimit == updated.SeedingPolicy.RatioLimit ||
		source.RatioLimit == active.SeedingPolicy.RatioLimit ||
		source.RatioLimit == durable.SeedingPolicy.RatioLimit ||
		updated.SeedingPolicy.RatioLimit == active.SeedingPolicy.RatioLimit ||
		updated.SeedingPolicy.RatioLimit == durable.SeedingPolicy.RatioLimit ||
		active.SeedingPolicy.RatioLimit == durable.SeedingPolicy.RatioLimit {
		t.Fatal("policy pointer fields are aliased across source, API, active, or repository state")
	}
}

func floatPointer(value float64) *float64 {
	return &value
}

func TestQueuedTorrentPolicyUpdateIsHydratedBeforeDispatch(t *testing.T) {
	manager, repo, torrentRepo, _, _, j := setupSeedingPolicyJob(t, StatusQueued)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	manager.SetQueueRepository(queueRepo)
	_ = queueRepo.Enqueue(context.Background(), &QueueEntry{JobID: j.ID, Action: QueueActionStart, Position: 1})

	// Update seeding policy to none while QUEUED
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("UpdateSeedingPolicy failed: %v", err)
	}

	// Verify durable torrent record is updated
	durableRec, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if durableRec.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || durableRec.SeedAfterComplete {
		t.Fatalf("expected durable policy mode none, got %+v", durableRec)
	}

	// Dispatch queued job
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	if err := manager.DispatchQueuedJob(context.Background(), qj); err != nil {
		t.Fatalf("DispatchQueuedJob failed: %v", err)
	}

	// Verify activeJobs copy is hydrated with policy=none
	activeJobs := manager.GetActiveJobs()
	activeJ, ok := activeJobs[j.ID]
	if !ok {
		t.Fatalf("expected job %s to be in activeJobs", j.ID)
	}
	if activeJ.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || activeJ.SeedAfterComplete {
		t.Fatalf("expected activeJob to have policy mode none, got %+v", activeJ)
	}

	// Simulate engine completion
	status := &EngineStatus{Status: StatusCompleted, Progress: 100}
	manager.UpdateJobFromEngine(context.Background(), activeJ, status, true)

	// Verify job reaches COMPLETED without entering SEEDING
	finalJ, _ := repo.GetByID(context.Background(), j.ID)
	if finalJ.Status != StatusCompleted {
		t.Fatalf("expected job to reach StatusCompleted, got %s", finalJ.Status)
	}
}

func TestQueuedTorrentRatioPolicyControlsPostDispatchCompletion(t *testing.T) {
	manager, repo, _, _, engine, j := setupSeedingPolicyJob(t, StatusQueued)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	manager.SetQueueRepository(queueRepo)
	_ = queueRepo.Enqueue(context.Background(), &QueueEntry{JobID: j.ID, Action: QueueActionStart, Position: 1})

	ratioLimit := 1.5
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratioLimit})
	if err != nil {
		t.Fatalf("UpdateSeedingPolicy failed: %v", err)
	}

	// Dispatch
	if err := manager.DispatchQueuedJob(context.Background(), &QueuedJob{JobID: j.ID, Action: QueueActionStart}); err != nil {
		t.Fatalf("DispatchQueuedJob failed: %v", err)
	}

	activeJ := manager.GetActiveJobs()[j.ID]
	if activeJ.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || activeJ.SeedingPolicy.RatioLimit == nil || *activeJ.SeedingPolicy.RatioLimit != 1.5 {
		t.Fatalf("expected activeJob to have ratio policy 1.5, got %+v", activeJ)
	}

	// Engine completes with ratio < 1.5
	engine.statusFunc = func(ctx context.Context, job *Job) (*EngineStatus, error) {
		return &EngineStatus{Status: StatusSeeding, Progress: 100, Ratio: 0.5}, nil
	}
	manager.UpdateJobFromEngine(context.Background(), activeJ, &EngineStatus{Status: StatusCompleted, Progress: 100, Ratio: 0.5}, true)

	seedingJ, _ := repo.GetByID(context.Background(), j.ID)
	if seedingJ.Status != StatusSeeding {
		t.Fatalf("expected job to enter StatusSeeding when ratio < 1.5, got %s", seedingJ.Status)
	}

	// Engine ratio reaches 1.5 -> threshold reached -> stopSeeding -> COMPLETED
	engine.statusFunc = func(ctx context.Context, job *Job) (*EngineStatus, error) {
		return &EngineStatus{Status: StatusSeeding, Progress: 100, Ratio: 1.5}, nil
	}
	manager.UpdateJobFromEngine(context.Background(), activeJ, &EngineStatus{Status: StatusSeeding, Progress: 100, Ratio: 1.5}, true)

	completedJ, _ := repo.GetByID(context.Background(), j.ID)
	if completedJ.Status != StatusCompleted {
		t.Fatalf("expected job to reach StatusCompleted when ratio reached 1.5, got %s", completedJ.Status)
	}
}

func TestPausedTorrentPolicyUpdateIsHydratedOnResumeDispatch(t *testing.T) {
	manager, repo, _, _, _, j := setupSeedingPolicyJob(t, StatusPaused)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	manager.SetQueueRepository(queueRepo)

	// Update policy while PAUSED
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("UpdateSeedingPolicy failed: %v", err)
	}

	// Dispatch queued resume
	if err := manager.DispatchQueuedJob(context.Background(), &QueuedJob{JobID: j.ID, Action: QueueActionResume}); err != nil {
		t.Fatalf("DispatchQueuedJob failed: %v", err)
	}

	activeJ := manager.GetActiveJobs()[j.ID]
	if activeJ == nil || activeJ.SeedingPolicy.Mode != networkpolicy.SeedingModeNone {
		t.Fatalf("expected activeJob to be hydrated with policy=none on resume, got %+v", activeJ)
	}

	// Simulate engine completion
	manager.UpdateJobFromEngine(context.Background(), activeJ, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)
	finalJ, _ := repo.GetByID(context.Background(), j.ID)
	if finalJ.Status != StatusCompleted {
		t.Fatalf("expected job to reach StatusCompleted without seeding, got %s", finalJ.Status)
	}
}

func TestPausedTorrentRatioPolicySurvivesResume(t *testing.T) {
	manager, repo, _, _, engine, j := setupSeedingPolicyJob(t, StatusPaused)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	manager.SetQueueRepository(queueRepo)

	ratioLimit := 2.0
	_, err := manager.UpdateSeedingPolicy(context.Background(), j.ID, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratioLimit})
	if err != nil {
		t.Fatalf("UpdateSeedingPolicy failed: %v", err)
	}

	if err := manager.DispatchQueuedJob(context.Background(), &QueuedJob{JobID: j.ID, Action: QueueActionResume}); err != nil {
		t.Fatalf("DispatchQueuedJob failed: %v", err)
	}

	activeJ := manager.GetActiveJobs()[j.ID]
	if activeJ.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || activeJ.SeedingPolicy.RatioLimit == nil || *activeJ.SeedingPolicy.RatioLimit != 2.0 {
		t.Fatalf("expected activeJob to have ratio policy 2.0, got %+v", activeJ)
	}

	// Engine completes with ratio 2.0 -> reaches COMPLETED directly
	engine.statusFunc = func(ctx context.Context, job *Job) (*EngineStatus, error) {
		return &EngineStatus{Status: StatusSeeding, Progress: 100, Ratio: 2.0}, nil
	}
	manager.UpdateJobFromEngine(context.Background(), activeJ, &EngineStatus{Status: StatusCompleted, Progress: 100, Ratio: 2.0}, true)

	finalJ, _ := repo.GetByID(context.Background(), j.ID)
	if finalJ.Status != StatusCompleted {
		t.Fatalf("expected job to reach StatusCompleted when ratio reached 2.0, got %s", finalJ.Status)
	}
}

func TestTorrentDispatch_FailsClosedOnHydrationFailure(t *testing.T) {
	manager, repo, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, StatusQueued)
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	manager.SetQueueRepository(queueRepo)
	_ = queueRepo.Enqueue(context.Background(), &QueueEntry{JobID: j.ID, Action: QueueActionStart, Position: 1})

	// Delete the torrent_jobs record so hydration fails
	_ = torrentRepo.DeleteTorrentJob(context.Background(), j.ID)

	startCalled := false
	engine.startDownloadFunc = func(string) error {
		startCalled = true
		return nil
	}

	err := manager.DispatchQueuedJob(context.Background(), &QueuedJob{JobID: j.ID, Action: QueueActionStart})
	if err == nil {
		t.Fatalf("expected DispatchQueuedJob to fail when torrent record is missing")
	}

	if startCalled {
		t.Fatalf("expected StartDownload to NOT be called on engine when hydration fails")
	}

	if activeJ := manager.GetActiveJobs()[j.ID]; activeJ != nil {
		t.Fatalf("expected job to NOT be inserted into activeJobs when hydration fails")
	}

	// Verify job status remains QUEUED in repository
	inDB, _ := repo.GetByID(context.Background(), j.ID)
	if inDB.Status != StatusQueued {
		t.Fatalf("expected job status to remain StatusQueued, got %s", inDB.Status)
	}
}

func TestRecovery_HydratesTorrentPolicyFromTorrentRepo(t *testing.T) {
	manager, repo, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, StatusDownloading)

	// Set generic jobs row to stale policy none
	j.SeedingPolicy = networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	j.SeedAfterComplete = false
	_ = repo.Update(context.Background(), j)

	// Set torrent_jobs row to ratio 1.5
	ratioLimit := 1.5
	rec, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	rec.SeedingPolicy = networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratioLimit}
	rec.SeedAfterComplete = true
	_ = torrentRepo.UpdateTorrentJob(context.Background(), rec)

	engine.statusFunc = func(ctx context.Context, job *Job) (*EngineStatus, error) {
		return &EngineStatus{Status: StatusDownloading, Progress: 50}, nil
	}

	// Perform recovery
	manager.recover(context.Background())

	// Verify active runtime job has ratio 1.5
	activeJ := manager.GetActiveJobs()[j.ID]
	if activeJ == nil {
		t.Fatalf("expected job %s to be active after recovery", j.ID)
	}
	if activeJ.SeedingPolicy.Mode != networkpolicy.SeedingModeRatio || activeJ.SeedingPolicy.RatioLimit == nil || *activeJ.SeedingPolicy.RatioLimit != 1.5 || !activeJ.SeedAfterComplete {
		t.Fatalf("expected recovered activeJob to be hydrated with ratio policy 1.5, got %+v", activeJ)
	}
}

func TestHydrateTorrentState_PointerIndependence(t *testing.T) {
	manager, _, torrentRepo, _, _, j := setupSeedingPolicyJob(t, StatusQueued)
	ratioVal := 1.5
	timeVal := int64(3600)
	nowVal := time.Now()
	expectedTime := nowVal

	rec, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	rec.SeedingPolicy = networkpolicy.SeedingPolicy{
		Mode:             networkpolicy.SeedingModeRatioOrDuration,
		RatioLimit:       &ratioVal,
		TimeLimitSeconds: &timeVal,
	}
	rec.SeedingStartedAt = &nowVal
	_ = torrentRepo.UpdateTorrentJob(context.Background(), rec)

	if err := manager.hydrateTorrentState(context.Background(), j); err != nil {
		t.Fatalf("hydrateTorrentState failed: %v", err)
	}

	// Mutate local variables
	ratioVal = 9.9
	timeVal = 9999
	nowVal = nowVal.Add(10 * time.Hour)

	if *j.SeedingPolicy.RatioLimit != 1.5 || *j.SeedingPolicy.TimeLimitSeconds != 3600 || j.SeedingStartedAt == nil || !j.SeedingStartedAt.Equal(expectedTime) {
		t.Fatalf("pointer fields in hydrated job were mutated when local source variables changed: %+v", j)
	}
}

func TestStartTorrentWithPolicy_ModeNone_DoesNotCallApplySeedingPolicyAndProceeds(t *testing.T) {
	manager, _, torrentRepo, _, engine, j := setupSeedingPolicyJob(t, StatusAwaitingSelection)
	engine.getFilesFunc = func(hash string) ([]TorrentFile, error) {
		return []TorrentFile{{Index: 0, Path: "file1.mp4", Size: 1024, Priority: PriorityNormal, Selected: true}}, nil
	}

	selections := []TorrentFileSelection{{Index: 0, Priority: PriorityNormal}}
	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}

	startedJ, err := manager.StartTorrentWithPolicy(context.Background(), j.ID, selections, policy)
	if err != nil {
		t.Fatalf("expected StartTorrentWithPolicy to succeed for mode none, got %v", err)
	}

	if startedJ.Status != StatusQueued && startedJ.Status != StatusDownloading {
		t.Fatalf("expected started job status to be queued or downloading, got %s", startedJ.Status)
	}

	if startedJ.SeedAfterComplete {
		t.Fatalf("expected SeedAfterComplete=false for mode none, got true")
	}

	durableRecord, _ := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if durableRecord.SeedingPolicy.Mode != networkpolicy.SeedingModeNone || durableRecord.SeedAfterComplete {
		t.Fatalf("expected persisted record to have mode none and SeedAfterComplete=false, got %+v", durableRecord)
	}
}

func TestStartTorrentWithPolicy_SeedingPolicyFailure_DoesNotEnqueueOrLoseSelections(t *testing.T) {
	manager, repo, _, _, engine, j := setupSeedingPolicyJob(t, StatusAwaitingSelection)
	engine.getFilesFunc = func(hash string) ([]TorrentFile, error) {
		return []TorrentFile{{Index: 0, Path: "file1.mp4", Size: 1024, Priority: PriorityNormal, Selected: true}}, nil
	}
	engine.applyFailure = 1

	selections := []TorrentFileSelection{{Index: 0, Priority: PriorityNormal}}
	ratioVal := 1.5
	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratioVal}

	_, err := manager.StartTorrentWithPolicy(context.Background(), j.ID, selections, policy)
	if err == nil {
		t.Fatal("expected StartTorrentWithPolicy to fail when ApplySeedingPolicy returns error")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrNetworkSettingApplicationFailed {
		t.Fatalf("expected ErrNetworkSettingApplicationFailed, got %#v", err)
	}

	// Verify job remains in StatusAwaitingSelection in DB
	durableJob, _ := repo.GetByID(context.Background(), j.ID)
	if durableJob.Status != StatusAwaitingSelection {
		t.Fatalf("expected job to remain StatusAwaitingSelection on seeding failure, got %s", durableJob.Status)
	}

	// Verify job was NOT enqueued
	if activeJ := manager.GetActiveJobs()[j.ID]; activeJ != nil && activeJ.Status != StatusAwaitingSelection {
		t.Fatalf("expected job to NOT be active/enqueued on seeding failure, got status %s", activeJ.Status)
	}
}
