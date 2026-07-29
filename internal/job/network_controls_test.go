package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type fakeLimitEngine struct {
	*fakeEngine
	values       []int64
	failOnSecond bool
}

func (f *fakeLimitEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{PerJobDownloadLimit: true}
}
func (f *fakeLimitEngine) GetDownloadLimit(context.Context, *Job) (int64, error) { return 0, nil }
func (f *fakeLimitEngine) SetDownloadLimit(_ context.Context, _ *Job, value int64) error {
	f.values = append(f.values, value)
	if f.failOnSecond && len(f.values) == 2 {
		return errors.New("rollback failed")
	}
	return nil
}

func TestLiveLimitPersistenceFailureRollsBackExternalState(t *testing.T) {
	manager, _, _, cleanup, _ := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()
	repo := manager.repo.(*fakeJobRepository)
	engine := &fakeLimitEngine{fakeEngine: &fakeEngine{}}
	manager.engines.(*fakeEngineRegistry).engines["aria2"] = engine
	oldLimit := int64(100)
	j := &Job{
		ID: "limit-job", Source: "https://example.com", Engine: "aria2", EngineID: "gid",
		Type: TypeDownload, Status: StatusDownloading,
		NetworkPolicy:                        networkpolicy.JobNetworkPolicy{DownloadLimitBytesPerSecond: oldLimit},
		EffectiveDownloadLimitBytesPerSecond: oldLimit,
		CreatedAt:                            time.Now(), UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatal(err)
	}
	repo.updateErr = errors.New("database unavailable")
	newLimit := int64(200)
	if _, err := manager.UpdateNetworkLimits(ctx, j.ID, NetworkLimitUpdate{DownloadLimitBytesPerSecond: &newLimit}); err == nil {
		t.Fatal("expected persistence error")
	}
	if len(engine.values) != 2 || engine.values[0] != 200 || engine.values[1] != 100 {
		t.Fatalf("expected external apply then rollback, got %v", engine.values)
	}
}

func TestLiveLimitRollbackFailureReturnsAmbiguousState(t *testing.T) {
	manager, _, _, cleanup, _ := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()
	repo := manager.repo.(*fakeJobRepository)
	engine := &fakeLimitEngine{fakeEngine: &fakeEngine{}, failOnSecond: true}
	manager.engines.(*fakeEngineRegistry).engines["aria2"] = engine
	j := &Job{
		ID: "ambiguous-job", Source: "https://example.com", Engine: "aria2", EngineID: "gid",
		Type: TypeDownload, Status: StatusDownloading,
		NetworkPolicy:                        networkpolicy.JobNetworkPolicy{DownloadLimitBytesPerSecond: 100},
		EffectiveDownloadLimitBytesPerSecond: 100,
		CreatedAt:                            time.Now(), UpdatedAt: time.Now(),
	}
	_ = repo.Create(ctx, j)
	repo.updateErr = errors.New("database unavailable")
	value := int64(200)
	_, err := manager.UpdateNetworkLimits(ctx, j.ID, NetworkLimitUpdate{DownloadLimitBytesPerSecond: &value})
	appError, ok := err.(*AppError)
	if !ok || appError.Code != ErrNetworkSettingStateAmbiguous {
		t.Fatalf("expected ambiguous-state error, got %#v", err)
	}
}
