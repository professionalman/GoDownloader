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
