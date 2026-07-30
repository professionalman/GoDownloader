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

func floatPointer(value float64) *float64 {
	return &value
}
