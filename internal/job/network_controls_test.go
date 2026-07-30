package job

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
	"downloader/internal/settings"
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

type applicationResultSettingsRepository struct {
	values map[string]string
}

func (r *applicationResultSettingsRepository) Get(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}
func (r *applicationResultSettingsRepository) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

type applicationResultAriaEngine struct {
	*fakeEngine
	applied int64
	err     error
}

func (e *applicationResultAriaEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{GlobalDownloadLimit: true, PerJobDownloadLimit: true}
}
func (e *applicationResultAriaEngine) GetGlobalDownloadLimit(context.Context) (int64, error) {
	return e.applied, nil
}
func (e *applicationResultAriaEngine) SetGlobalDownloadLimit(_ context.Context, value int64) error {
	if e.err != nil {
		return e.err
	}
	e.applied = value
	return nil
}

type applicationResultTorrentEngine struct {
	*fakeTorrentEngine
	applied int64
	err     error
}

func (e *applicationResultTorrentEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{PerJobDownloadLimit: true}
}
func (e *applicationResultTorrentEngine) GetDownloadLimit(context.Context, *Job) (int64, error) {
	return e.applied, nil
}
func (e *applicationResultTorrentEngine) SetDownloadLimit(_ context.Context, _ *Job, value int64) error {
	if e.err != nil {
		return e.err
	}
	e.applied = value
	return nil
}

type applicationResultMediaEngine struct{ *fakeEngine }

func (e *applicationResultMediaEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{
		PerJobDownloadLimit: true,
		StartupOnly:         map[string]bool{"downloadLimit": true},
	}
}

func TestNetworkSettingsApplicationResultsReportTruthfulEngineScope(t *testing.T) {
	manager, baseEngine, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	settingsRepo := &applicationResultSettingsRepository{values: map[string]string{}}
	settingsService := settings.NewSettingsService(settingsRepo, t.TempDir(), t.TempDir())
	manager.SetSettingsService(settingsService)
	var request settings.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(`{"network":{"globalDownloadLimitBytesPerSecond":2048}}`), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := settingsService.UpdatePowerSettings(context.Background(), &request); err != nil {
		t.Fatal(err)
	}

	aria := &applicationResultAriaEngine{fakeEngine: baseEngine}
	qb := &applicationResultTorrentEngine{fakeTorrentEngine: fakeTorrent}
	registry := manager.engines.(*fakeEngineRegistry)
	registry.engines["aria2"] = aria
	registry.engines["qbittorrent"] = qb
	registry.engines["ytdlp"] = &applicationResultMediaEngine{fakeEngine: &fakeEngine{}}

	repo := manager.repo.(*fakeJobRepository)
	now := time.Now()
	torrentJob := &Job{
		ID: "owned", Type: TypeTorrent, Engine: "qbittorrent", EngineID: "owned-hash",
		Status: StatusDownloading, CreatedAt: now, UpdatedAt: now,
		NetworkPolicy: networkpolicy.JobNetworkPolicy{},
	}
	if err := repo.Create(context.Background(), torrentJob); err != nil {
		t.Fatal(err)
	}

	results := manager.ReconcileNetworkPoliciesWithResults(context.Background())
	statuses := map[string]string{}
	for _, result := range results {
		statuses[result.Target] = result.Status
		if strings.Contains(result.Message, "database") || strings.Contains(result.Message, "RPC") {
			t.Fatalf("raw engine detail leaked: %+v", result)
		}
	}
	if statuses["settings"] != "persisted" || statuses["aria2"] != "applied" ||
		statuses["qbittorrent"] != "applied" || statuses["yt-dlp"] != "future_jobs_only" {
		t.Fatalf("unexpected application results: %+v", results)
	}
	if aria.applied != 2048 || qb.applied != 2048 {
		t.Fatalf("limits were not applied with truthful scope: aria=%d qb=%d", aria.applied, qb.applied)
	}
}

func TestNetworkSettingsApplicationResultsKeepUnavailableEnginesPending(t *testing.T) {
	manager, baseEngine, _, cleanup, _ := setupManagerTest(t)
	defer cleanup()
	settingsRepo := &applicationResultSettingsRepository{values: map[string]string{}}
	settingsService := settings.NewSettingsService(settingsRepo, t.TempDir(), t.TempDir())
	manager.SetSettingsService(settingsService)
	registry := manager.engines.(*fakeEngineRegistry)
	registry.engines["aria2"] = &applicationResultAriaEngine{fakeEngine: baseEngine, err: errors.New("secret RPC detail")}
	delete(registry.engines, "qbittorrent")
	delete(registry.engines, "ytdlp")

	results := manager.ReconcileNetworkPoliciesWithResults(context.Background())
	statuses := map[string]string{}
	for _, result := range results {
		statuses[result.Target] = result.Status
		if strings.Contains(result.Message, "secret RPC detail") {
			t.Fatalf("raw engine error leaked: %+v", result)
		}
	}
	if statuses["settings"] != "persisted" || statuses["aria2"] != "pending" ||
		statuses["qbittorrent"] != "unavailable" || statuses["yt-dlp"] != "unavailable" {
		t.Fatalf("unexpected unavailable results: %+v", results)
	}
}
