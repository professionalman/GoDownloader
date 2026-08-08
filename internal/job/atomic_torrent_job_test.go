package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type atomicFailingTorrentRepo struct {
	*fakeTorrentRepository
	failAtomicCreate error
	atomicCalls      int
}

func (r *atomicFailingTorrentRepo) CreateTorrentJobAtomic(ctx context.Context, j *Job, rec *TorrentJobRecord) error {
	r.atomicCalls++
	if r.failAtomicCreate != nil {
		return r.failAtomicCreate
	}
	return r.fakeTorrentRepository.CreateTorrentJobAtomic(ctx, j, rec)
}

func TestManager_CreateTorrentFromFile_AtomicFailureCleansUpDiskAndRollsBack(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := &atomicFailingTorrentRepo{
		fakeTorrentRepository: newFakeTorrentRepository(jobRepo),
		failAtomicCreate:      errors.New("simulated sqlite disk error on atomic insert"),
	}
	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	dataDir := t.TempDir()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo, dataDir)

	sub := bus.Subscribe()

	// Create a temporary valid dummy .torrent file to upload
	uploadPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(uploadPath, []byte("d8:announce12:http://test.come"), 0644); err != nil {
		t.Fatalf("failed to write dummy torrent file: %v", err)
	}

	_, err := mgr.CreateTorrentFromFile(context.Background(), uploadPath)
	if err == nil {
		t.Fatal("expected CreateTorrentFromFile to fail when atomic creation fails")
	}

	// 1. Verify error preserves the database error
	if !strings.Contains(err.Error(), "simulated sqlite disk error") {
		t.Fatalf("expected error to preserve DB error, got: %v", err)
	}

	// 2. Verify persisted .torrent file was removed from dataDir/torrents
	torrentsDir := filepath.Join(dataDir, "torrents")
	entries, _ := os.ReadDir(torrentsDir)
	if len(entries) != 0 {
		t.Fatalf("expected 0 persisted files in %s, found %d", torrentsDir, len(entries))
	}

	// 3. Verify zero jobs rows exist in repository
	if len(jobRepo.jobs) != 0 {
		t.Fatalf("expected 0 jobs in repository, found %d", len(jobRepo.jobs))
	}

	// 4. Verify zero torrent_jobs exist in repository
	if len(torrentRepo.torrentJobs) != 0 {
		t.Fatalf("expected 0 torrent_jobs in repository, found %d", len(torrentRepo.torrentJobs))
	}

	// 5. Verify EventJobCreated was NOT published
	select {
	case evt := <-sub:
		t.Fatalf("unexpected event published on failure: %+v", evt)
	default:
		// OK
	}

	// 6. Verify List and ListRecoverable return 0 jobs
	jobs, err := mgr.List(context.Background())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expected 0 jobs from List, got %d, err=%v", len(jobs), err)
	}
	recoverable, err := jobRepo.ListRecoverable(context.Background())
	if err != nil || len(recoverable) != 0 {
		t.Fatalf("expected 0 jobs from ListRecoverable, got %d, err=%v", len(recoverable), err)
	}
}

func TestManager_CreateTorrentFromFile_AtomicSuccessPersistsFileAndDB(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	dataDir := t.TempDir()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo, dataDir)

	sub := bus.Subscribe()

	uploadPath := filepath.Join(t.TempDir(), "success.torrent")
	content := []byte("d8:announce12:http://test.come")
	if err := os.WriteFile(uploadPath, content, 0644); err != nil {
		t.Fatalf("failed to write dummy torrent file: %v", err)
	}

	j, err := mgr.CreateTorrentFromFile(context.Background(), uploadPath)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile failed: %v", err)
	}

	// 1. Verify persisted file exists in dataDir/torrents
	expectedFile := filepath.Join(dataDir, "torrents", j.ID+".torrent")
	persistedData, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("persisted torrent file missing at %s: %v", expectedFile, err)
	}
	if string(persistedData) != string(content) {
		t.Fatal("persisted content mismatch")
	}

	// 2. Verify jobs row exists
	savedJob, err := jobRepo.GetByID(context.Background(), j.ID)
	if err != nil || savedJob == nil {
		t.Fatalf("saved job missing in jobRepo: %v", err)
	}

	// 3. Verify torrent_jobs row exists
	savedRec, err := torrentRepo.GetTorrentJob(context.Background(), j.ID)
	if err != nil || savedRec == nil {
		t.Fatalf("saved torrent record missing in torrentRepo: %v", err)
	}
	if savedRec.TorrentFilePath != expectedFile {
		t.Fatalf("saved TorrentFilePath mismatch: %s != %s", savedRec.TorrentFilePath, expectedFile)
	}

	// 4. Verify EventJobCreated was published
	select {
	case evt := <-sub:
		if evt.Type != EventJobCreated || evt.Job.ID != j.ID {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for EventJobCreated")
	}
}

func TestManager_CreateMagnet_AtomicFailureLeavesNoOrphan(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := &atomicFailingTorrentRepo{
		fakeTorrentRepository: newFakeTorrentRepository(jobRepo),
		failAtomicCreate:      errors.New("database locked"),
	}
	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	sub := bus.Subscribe()

	_, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:4444444444444444444444444444444444444444")
	if err == nil {
		t.Fatal("expected Create to fail on atomic DB error")
	}

	// Verify no orphan jobs
	if len(jobRepo.jobs) != 0 {
		t.Fatalf("expected 0 jobs in repo, got %d", len(jobRepo.jobs))
	}
	if len(torrentRepo.torrentJobs) != 0 {
		t.Fatalf("expected 0 torrent_jobs in repo, got %d", len(torrentRepo.torrentJobs))
	}

	// Verify no event published
	select {
	case evt := <-sub:
		t.Fatalf("unexpected event published: %+v", evt)
	default:
		// OK
	}
}

func TestMapStorageError_SanitizesSensitiveUnknownErrors(t *testing.T) {
	sensitiveErr := fmt.Errorf("storage drive IO error at /run/secrets/api_key.txt: disk timeout")
	mapped := mapStorageError(sensitiveErr)

	var appErr *AppError
	if !errors.As(mapped, &appErr) {
		t.Fatalf("expected *AppError, got %T: %v", mapped, mapped)
	}

	if appErr.Code != ErrInternalError {
		t.Fatalf("expected error code %s, got %s", ErrInternalError, appErr.Code)
	}

	// Must NOT leak sensitive path in message
	if strings.Contains(appErr.Message, "/run/secrets") || strings.Contains(appErr.Message, "api_key.txt") {
		t.Fatalf("sensitive details exposed in client message: %s", appErr.Message)
	}

	if appErr.Message != "an internal error occurred" {
		t.Fatalf("expected message 'an internal error occurred', got '%s'", appErr.Message)
	}
}
