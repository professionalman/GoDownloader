package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/storage"
)

// Helper to create a test job in the repository.
func createDeleteTestJob(repo *fakeJobRepository, id string, status JobStatus, jobType, engine, engineID, destDir, finalPath, workDir string) *Job {
	j := &Job{
		ID:             id,
		Source:         "https://example.com/file.iso",
		Name:           "file.iso",
		Status:         status,
		Type:           jobType,
		Engine:         engine,
		EngineID:       engineID,
		DestinationDir: destDir,
		FinalPath:      finalPath,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	repo.jobs[id] = j
	return j
}

// 1. completed job + deleteFiles=false: DB record removed, user output remains
func TestDelete_Completed_KeepFiles(t *testing.T) {
	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "completed.iso")
	if err := os.WriteFile(destFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_comp_keep", StatusCompleted, TypeDownload, "aria2", "gid1", tempDir, destFile, "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, tempDir, nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Verify DB record removed
	if saved, _ := repo.GetByID(context.Background(), j.ID); saved != nil {
		t.Errorf("expected job to be deleted from repo, found: %+v", saved)
	}

	// Verify user output file remains
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("expected user output file to remain on disk: %v", err)
	}
}

// 2. completed job + deleteFiles=true: DB record removed, exact user output removed
func TestDelete_Completed_DeleteFiles(t *testing.T) {
	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "completed.iso")
	if err := os.WriteFile(destFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_comp_del", StatusCompleted, TypeDownload, "aria2", "gid2", tempDir, destFile, "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, tempDir, nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Verify DB record removed
	if saved, _ := repo.GetByID(context.Background(), j.ID); saved != nil {
		t.Errorf("expected job to be deleted from repo, found: %+v", saved)
	}

	// Verify user output file removed
	if _, err := os.Stat(destFile); !os.IsNotExist(err) {
		t.Errorf("expected user output file to be deleted from disk, but it still exists")
	}
}

// 3. cancelled job + deleteFiles=false: partial file remains, record removed
func TestDelete_Cancelled_KeepFiles(t *testing.T) {
	tempDir := t.TempDir()
	partialFile := filepath.Join(tempDir, "partial.iso")
	if err := os.WriteFile(partialFile, []byte("partial data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_canc_keep", StatusCancelled, TypeDownload, "aria2", "gid3", tempDir, partialFile, "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, tempDir, nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	if saved, _ := repo.GetByID(context.Background(), j.ID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
	if _, err := os.Stat(partialFile); err != nil {
		t.Errorf("expected partial file to remain on disk: %v", err)
	}
}

// 4. cancelled job + deleteFiles=true: owned partial file removed, record removed
func TestDelete_Cancelled_DeleteFiles(t *testing.T) {
	tempDir := t.TempDir()
	partialFile := filepath.Join(tempDir, "partial.iso")
	if err := os.WriteFile(partialFile, []byte("partial data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_canc_del", StatusCancelled, TypeDownload, "aria2", "gid4", tempDir, partialFile, "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, tempDir, nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	if saved, _ := repo.GetByID(context.Background(), j.ID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
	if _, err := os.Stat(partialFile); !os.IsNotExist(err) {
		t.Errorf("expected partial file to be deleted from disk")
	}
}

// 5. failed job delete: supported
func TestDelete_Failed_Job(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_fail", StatusFailed, TypeDownload, "aria2", "gid5", t.TempDir(), "", "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, t.TempDir(), nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
	if saved, _ := repo.GetByID(context.Background(), j.ID); saved != nil {
		t.Errorf("expected failed job to be deleted from repo")
	}
}

// 6. downloading job Delete: rejected, zero DB deletion, zero file deletion
func TestDelete_Downloading_Rejected(t *testing.T) {
	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "downloading.iso")
	if err := os.WriteFile(destFile, []byte("downloading data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_dl", StatusDownloading, TypeDownload, "aria2", "gid6", tempDir, destFile, "")

	bus := newFakeEventBus()
	eng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}
	m := NewManager(repo, reg, bus, tempDir, nil)

	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected delete on downloading job to be rejected")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrInvalidJobState {
		t.Errorf("expected ErrInvalidJobState, got: %v", err)
	}

	// Zero DB deletion
	if saved, _ := repo.GetByID(context.Background(), j.ID); saved == nil {
		t.Errorf("downloading job must remain in DB")
	}
	// Zero file deletion
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("downloading file must remain on disk: %v", err)
	}
}

// 7. paused job Delete: rejected
func TestDelete_Paused_Rejected(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_paused", StatusPaused, TypeDownload, "aria2", "gid7", t.TempDir(), "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected paused job delete to be rejected")
	}
}

// 8. queued job Delete: rejected
func TestDelete_Queued_Rejected(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_queued", StatusQueued, TypeDownload, "aria2", "gid8", t.TempDir(), "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected queued job delete to be rejected")
	}
}

// 9. analyzing job Delete: rejected
func TestDelete_Analyzing_Rejected(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_analyzing", StatusAnalyzing, TypeTorrent, "qbittorrent", "", t.TempDir(), "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected analyzing job delete to be rejected")
	}
}

// 10. awaiting-selection torrent Delete: rejected until Cancelled
func TestDelete_AwaitingSelection_Rejected(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_awaiting", StatusAwaitingSelection, TypeTorrent, "qbittorrent", "hash10", t.TempDir(), "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected awaiting_selection job delete to be rejected")
	}
}

// 11. seeding job Delete: rejected until Stop/Cancel
func TestDelete_Seeding_Rejected(t *testing.T) {
	repo := newFakeJobRepository()
	j := createDeleteTestJob(repo, "job_seeding", StatusSeeding, TypeTorrent, "qbittorrent", "hash11", t.TempDir(), "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), j.ID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected seeding job delete to be rejected")
	}
}

// 12. completed torrent + deleteFiles=false: qBit torrent removed if present, destination data preserved, DB torrent rows + job row removed
func TestDelete_CompletedTorrent_KeepFiles(t *testing.T) {
	tempDir := t.TempDir()
	torrentFile := filepath.Join(tempDir, "video.mkv")
	if err := os.WriteFile(torrentFile, []byte("video data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_torrent_keep"
	infoHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, tempDir, torrentFile, "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{{JobID: jobID, FileIndex: 0, Path: "video.mkv", Size: 10, Selected: true}}

	var removeCalled int32
	var removeDeleteFiles bool
	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			atomic.AddInt32(&removeCalled, 1)
			removeDeleteFiles = delFiles
			return nil
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	if atomic.LoadInt32(&removeCalled) != 1 {
		t.Errorf("expected RemoveTorrent to be called exactly once, got %d", removeCalled)
	}
	if removeDeleteFiles != false {
		t.Errorf("expected RemoveTorrent delFiles=false, got %v", removeDeleteFiles)
	}

	// Destination data preserved
	if _, err := os.Stat(torrentFile); err != nil {
		t.Errorf("expected torrent file to remain on disk: %v", err)
	}
	// DB rows removed
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job row to be deleted")
	}
}

// 13. completed/cancelled torrent + deleteFiles=true: qBit torrent removed, owned files deleted, DB rows removed
func TestDelete_CompletedTorrent_DeleteFiles(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "torrent_dir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	torrentFile := filepath.Join(subDir, "video.mkv")
	if err := os.WriteFile(torrentFile, []byte("video data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_torrent_del"
	infoHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, tempDir, torrentFile, "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{{JobID: jobID, FileIndex: 0, Path: "torrent_dir/video.mkv", Size: 10, Selected: true}}

	var removeCalled int32
	var removeDelFiles bool
	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			atomic.AddInt32(&removeCalled, 1)
			removeDelFiles = delFiles
			return nil
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	if atomic.LoadInt32(&removeCalled) != 1 {
		t.Errorf("expected RemoveTorrent to be called once, got %d", removeCalled)
	}
	if removeDelFiles != false {
		t.Errorf("expected RemoveTorrent delFiles=false, got %v", removeDelFiles)
	}

	// Owned file removed
	if _, err := os.Stat(torrentFile); !os.IsNotExist(err) {
		t.Errorf("expected torrent file to be deleted")
	}
	// Empty parent directory safely removed
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Errorf("expected empty torrent subDir to be deleted")
	}
	// Destination root preserved
	if _, err := os.Stat(tempDir); err != nil {
		t.Errorf("destination root must NOT be deleted")
	}
}

// 14. qBit torrent already absent: deletion succeeds idempotently
func TestDelete_Torrent_AlreadyAbsent_Idempotent(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_torrent_absent"
	infoHash := "cccccccccccccccccccccccccccccccccccccccc"
	createDeleteTestJob(repo, jobID, StatusCancelled, TypeTorrent, "qbittorrent", infoHash, t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			return errors.New("torrent not found in qbittorrent")
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), t.TempDir(), torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("expected absent torrent deletion to succeed idempotently, got error: %v", err)
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
}

// 15. real qBit cleanup error: DB record remains
func TestDelete_Torrent_RealEngineError_RetainsDBRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_torrent_eng_err"
	infoHash := "dddddddddddddddddddddddddddddddddddddddd"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			return errors.New("connection reset by peer / daemon crashed")
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), t.TempDir(), torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected real engine error to be returned")
	}

	// DB record must remain so user can retry
	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when engine cleanup fails")
	}
}

// 16. filesystem delete error: DB record remains
func TestDelete_FilesystemError_RetainsDBRecord(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "protected_dest")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_fs_err"
	infoHash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	j := createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, destDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{{JobID: jobID, FileIndex: 0, Path: "../../../etc/shadow", Size: 10, Selected: true}}

	torrentEng := &fakeTorrentEngine{fakeEngine: &fakeEngine{}}
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), destDir, torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected filesystem/safety error on traversal path")
	}

	// DB record must remain
	if saved, _ := repo.GetByID(context.Background(), j.ID); saved == nil {
		t.Errorf("job must remain in DB when file cleanup fails")
	}
}

// 17. DB transaction failure: rollback all DB row deletion, retry remains possible
func TestDelete_DBTransactionFailure_RetainsRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_tx_fail"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeDownload, "aria2", "gid17", t.TempDir(), "", "")
	repo.deleteErr = errors.New("simulated SQLite deadlock / transaction rollback")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, newFakeEventBus(), t.TempDir(), nil)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected DB error to be returned")
	}

	// Fix DB error and verify retry succeeds
	repo.deleteErr = nil
	err = m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("expected retry to succeed after DB recovery, got: %v", err)
	}
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job to be deleted after retry")
	}
}

// 18. path traversal torrent entry: ../../something MUST be rejected, outside file untouched
func TestDelete_PathTraversal_Rejected_OutsideFileUntouched(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := filepath.Join(tempDir, "outside")
	destDir := filepath.Join(tempDir, "destination")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	outsideFile := filepath.Join(outsideDir, "critical_system_file.txt")
	if err := os.WriteFile(outsideFile, []byte("do not touch"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_traversal"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash18", destDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash18"}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{{JobID: jobID, FileIndex: 0, Path: "../outside/critical_system_file.txt", Size: 10, Selected: true}}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), destDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected delete to reject path traversal")
	}

	// Outside file must be untouched
	if _, err := os.Stat(outsideFile); err != nil {
		t.Errorf("outside file was wrongly deleted: %v", err)
	}
}

// 19. DestinationDir root safety: implementation must never delete shared download root
func TestDelete_DestinationRootSafety_NeverDeletesRoot(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "shared_downloads")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_root_safety"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash19", destDir, destDir, "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash19"}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), destDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Destination root must remain intact
	if _, err := os.Stat(destDir); err != nil {
		t.Errorf("destination root was wrongly deleted: %v", err)
	}
}

// 20. unrelated file next to torrent: deleteFiles=true leaves unrelated file untouched
func TestDelete_UnrelatedFileNextToTorrent_Untouched(t *testing.T) {
	tempDir := t.TempDir()
	torrentFile := filepath.Join(tempDir, "torrent_movie.mp4")
	unrelatedFile := filepath.Join(tempDir, "other_user_doc.pdf")
	if err := os.WriteFile(torrentFile, []byte("movie"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedFile, []byte("important doc"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_unrelated"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash20", tempDir, torrentFile, "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash20"}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{{JobID: jobID, FileIndex: 0, Path: "torrent_movie.mp4", Size: 5, Selected: true}}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), tempDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Torrent file deleted
	if _, err := os.Stat(torrentFile); !os.IsNotExist(err) {
		t.Errorf("expected torrent file to be deleted")
	}
	// Unrelated file untouched
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Errorf("unrelated file was deleted: %v", err)
	}
}

// 21. workdir safety: only marked/validated workdir can be recursively cleaned
func TestDelete_WorkDirSafety(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "media_work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	jobID := "job_workdir"
	// Write valid marker
	markerPath := filepath.Join(workDir, storage.WorkDirMarkerFilename)
	if err := os.WriteFile(markerPath, []byte(jobID), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeMedia, "ytdlp", "", tempDir, "", workDir)

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"ytdlp": &fakeEngine{}}}, newFakeEventBus(), tempDir, nil)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// WorkDir should be cleaned up
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected validated workdir to be cleaned up")
	}
}

// 22. uploaded .torrent internal copy: removed when job is deleted even with deleteFiles=false
func TestDelete_UploadedTorrentCopy_RemovedEvenWhenKeepFiles(t *testing.T) {
	tempDir := t.TempDir()
	torrentUploadCopy := filepath.Join(tempDir, "godownloader_upload_abc.torrent")
	if err := os.WriteFile(torrentUploadCopy, []byte("d8:announce...e"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_torrent_copy"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash22", tempDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash22", TorrentFilePath: torrentUploadCopy}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeTorrentEngine{fakeEngine: &fakeEngine{}}}}, newFakeEventBus(), tempDir, torrentRepo)
	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Uploaded temp .torrent copy removed
	if _, err := os.Stat(torrentUploadCopy); !os.IsNotExist(err) {
		t.Errorf("expected uploaded .torrent copy to be cleaned up")
	}
}

// 23. EventJobDeleted: emitted only after durable DB deletion succeeds
func TestDelete_EventJobDeleted_EmittedAfterDBSuccess(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_evt_del"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeDownload, "aria2", "gid23", t.TempDir(), "", "")

	bus := newFakeEventBus()
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, bus, t.TempDir(), nil)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	select {
	case evt := <-bus.ch:
		if evt.Type != EventJobDeleted {
			t.Errorf("expected event type %s, got %s", EventJobDeleted, evt.Type)
		}
		if evt.Job.ID != jobID {
			t.Errorf("expected event job ID %s, got %s", jobID, evt.Job.ID)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for EventJobDeleted")
	}
}

// 24. deleting a nonexistent job: correct not-found behavior
func TestDelete_NonexistentJob_ReturnsNotFound(t *testing.T) {
	repo := newFakeJobRepository()
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, newFakeEventBus(), t.TempDir(), nil)

	err := m.Delete(context.Background(), "nonexistent_job_123", DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected error when deleting nonexistent job")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound, got: %v", err)
	}
}

// 25. Regression test: DestinationDir/file.iso existed before download, actual output file.1.iso, FinalPath empty, Name file.iso
// Delete with deleteFiles=true MUST NEVER remove the original file.iso when exact path cannot be proven.
func TestDelete_DirectDownload_NoUnsafePathGuessing(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := filepath.Join(tempDir, "file.iso")
	if err := os.WriteFile(originalFile, []byte("pre-existing original file"), 0644); err != nil {
		t.Fatal(err)
	}
	actualAria2Output := filepath.Join(tempDir, "file.1.iso")
	if err := os.WriteFile(actualAria2Output, []byte("aria2 renamed partial data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_aria2_guess_test"
	// FinalPath is empty, Name is file.iso, engine status is not available / cannot prove path
	createDeleteTestJob(repo, jobID, StatusCancelled, TypeDownload, "aria2", "", tempDir, "", "")

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": &fakeEngine{}}}, newFakeEventBus(), tempDir, nil)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected delete to fail safely with ErrStorageError when output path cannot be proven")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrStorageError {
		t.Errorf("expected ErrStorageError, got: %v", err)
	}

	// CRITICAL: original file.iso MUST NEVER be deleted!
	if _, err := os.Stat(originalFile); err != nil {
		t.Fatalf("CRITICAL BUG: pre-existing file.iso was deleted by unsafe path guessing!")
	}
	// DB record must remain
	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when deleteFiles fails safely")
	}

	// Record-only delete MUST still succeed
	err = m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err != nil {
		t.Fatalf("expected record-only delete to succeed, got: %v", err)
	}
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("job should be deleted after record-only delete")
	}
	// original file still intact
	if _, err := os.Stat(originalFile); err != nil {
		t.Errorf("original file was removed during record-only delete: %v", err)
	}
}

// 26. Direct download: authoritative OutputPath resolved from engine status
func TestDelete_DirectDownload_AuthoritativeOutputPathFromEngine(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := filepath.Join(tempDir, "file.iso")
	if err := os.WriteFile(originalFile, []byte("pre-existing original file"), 0644); err != nil {
		t.Fatal(err)
	}
	actualOutput := filepath.Join(tempDir, "file.1.iso")
	if err := os.WriteFile(actualOutput, []byte("aria2 output data"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_aria2_status_path"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeDownload, "aria2", "gid26", tempDir, "", "")

	// Fake engine whose Status returns exact OutputPath: file.1.iso
	eng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{
				Status:     StatusCompleted,
				OutputPath: actualOutput,
			}, nil
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"aria2": eng}}, newFakeEventBus(), tempDir, nil)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Proven output file.1.iso is deleted
	if _, err := os.Stat(actualOutput); !os.IsNotExist(err) {
		t.Errorf("expected actual output file.1.iso to be deleted")
	}
	// Pre-existing file.iso is untouched
	if _, err := os.Stat(originalFile); err != nil {
		t.Errorf("pre-existing file.iso was wrongly deleted: %v", err)
	}
	// DB record removed
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
}

// 27. Torrent repository GetTorrentJob error retains DB record
func TestDelete_TorrentRepo_GetTorrentJobError_RetainsDBRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_t_rec_err"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash27", t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.getErr = errors.New("simulated SQLite read failure")

	torrentEng := &fakeTorrentEngine{fakeEngine: &fakeEngine{}}
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), t.TempDir(), torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected error on GetTorrentJob failure")
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when torrent repository read fails")
	}
}

// 28. Torrent repository GetTorrentFiles error retains DB record
func TestDelete_TorrentRepo_GetTorrentFilesError_RetainsDBRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_t_files_err"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash28", t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash28"}
	torrentRepo.getFilesErr = errors.New("simulated SQLite files read failure")

	torrentEng := &fakeTorrentEngine{fakeEngine: &fakeEngine{}}
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), t.TempDir(), torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected error on GetTorrentFiles failure")
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when torrent files repository read fails")
	}
}

// 29. Torrent engine unavailable: returns ErrEngineError and retains DB record
func TestDelete_TorrentEngineUnavailable_RetainsDBRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_t_no_eng"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash29", t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash29"}

	// Empty engine registry (qbittorrent unavailable)
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{}}, newFakeEventBus(), t.TempDir(), torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected error when torrent engine is unavailable")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrEngineError {
		t.Errorf("expected ErrEngineError, got: %v", err)
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when torrent engine is unavailable")
	}
}

// 30. Torrent engine wrong interface: returns ErrEngineError and retains DB record
func TestDelete_TorrentEngineWrongInterface_RetainsDBRecord(t *testing.T) {
	repo := newFakeJobRepository()
	jobID := "job_t_wrong_iface"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash30", t.TempDir(), "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash30"}

	// Registered engine does NOT implement ITorrentEngine
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": &fakeEngine{}}}, newFakeEventBus(), t.TempDir(), torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected error when registered engine has wrong interface")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrEngineError {
		t.Errorf("expected ErrEngineError, got: %v", err)
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when torrent engine interface is invalid")
	}
}

// 31. Local fallback preserves unselected/pre-existing torrent files
func TestDelete_Torrent_LocalFallback_PreservesUnselectedFiles(t *testing.T) {
	tempDir := t.TempDir()
	selectedFile := filepath.Join(tempDir, "movie.mkv")
	skippedPreExistingFile := filepath.Join(tempDir, "cover.jpg")
	if err := os.WriteFile(selectedFile, []byte("movie data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skippedPreExistingFile, []byte("pre-existing cover"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_unselected_preserve"
	infoHash := "hash31"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, tempDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Path: "movie.mkv", Size: 1000, Selected: true},
		{JobID: jobID, FileIndex: 1, Path: "cover.jpg", Size: 50, Selected: false}, // SKIPPED / unselected
	}

	// qBittorrent already absent
	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			return errors.New("torrent not found")
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// Selected file deleted
	if _, err := os.Stat(selectedFile); !os.IsNotExist(err) {
		t.Errorf("expected selected movie.mkv to be deleted")
	}
	// Skipped file MUST remain untouched!
	if _, err := os.Stat(skippedPreExistingFile); err != nil {
		t.Errorf("skipped/pre-existing cover.jpg was wrongly deleted: %v", err)
	}
	// DB record removed
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
}

// 32. Path validation occurs BEFORE any destructive external side effects
func TestDelete_Torrent_PathValidationBeforeDestructiveSideEffects(t *testing.T) {
	tempDir := t.TempDir()
	repo := newFakeJobRepository()
	jobID := "job_path_val_pre"
	infoHash := "hash32"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, tempDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Path: "../../../outside/file.txt", Size: 100, Selected: true},
	}

	var removeCalled int32
	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			atomic.AddInt32(&removeCalled, 1)
			return nil
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err == nil {
		t.Fatalf("expected delete to fail on path traversal")
	}

	// External engine must NOT have been called
	if atomic.LoadInt32(&removeCalled) != 0 {
		t.Errorf("RemoveTorrent was called despite pre-validation failure")
	}
	// DB record must remain
	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB after pre-validation failure")
	}
}

// 33. Internal cleanup error on .torrent file removal retains DB record
func TestDelete_TorrentFileRemovalError_RetainsDBRecord(t *testing.T) {
	tempDir := t.TempDir()
	// Create a directory where TorrentFilePath is expected to be a file, so os.Remove fails
	badTorrentPath := filepath.Join(tempDir, "cannot_remove_dir.torrent")
	if err := os.MkdirAll(filepath.Join(badTorrentPath, "child"), 0755); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_torrent_file_rm_err"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", "hash33", tempDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: "hash33", TorrentFilePath: badTorrentPath}

	torrentEng := &fakeTorrentEngine{fakeEngine: &fakeEngine{}}
	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: false})
	if err == nil {
		t.Fatalf("expected error on non-removable torrent file")
	}

	if saved, _ := repo.GetByID(context.Background(), jobID); saved == nil {
		t.Errorf("job must remain in DB when internal .torrent file cleanup fails")
	}
}

// 34. Regression test: with qBit PRESENT, deleteFiles=true MUST pass delFiles=false to RemoveTorrent,
// delete selected files locally, preserve unselected files, and remove DB record.
func TestDelete_Torrent_QBitPresent_PreservesUnselectedFiles_PassesFalseToEngine(t *testing.T) {
	tempDir := t.TempDir()
	selectedFile := filepath.Join(tempDir, "movie.mkv")
	skippedPreExistingFile := filepath.Join(tempDir, "cover.jpg")
	if err := os.WriteFile(selectedFile, []byte("movie data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skippedPreExistingFile, []byte("pre-existing cover"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeJobRepository()
	jobID := "job_qbit_present_unselected"
	infoHash := "hash34"
	createDeleteTestJob(repo, jobID, StatusCompleted, TypeTorrent, "qbittorrent", infoHash, tempDir, "", "")

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Path: "movie.mkv", Size: 1000, Selected: true},
		{JobID: jobID, FileIndex: 1, Path: "cover.jpg", Size: 50, Selected: false}, // UNSELECTED / skipped
	}

	var removeCalled int32
	var removeDelFiles bool
	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		removeTorrentFunc: func(hash string, delFiles bool) error {
			atomic.AddInt32(&removeCalled, 1)
			removeDelFiles = delFiles
			return nil
		},
	}

	m := NewManager(repo, &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}, newFakeEventBus(), tempDir, torrentRepo)

	err := m.Delete(context.Background(), jobID, DeleteJobOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}

	// 1. RemoveTorrent called exactly once with delFiles == false
	if atomic.LoadInt32(&removeCalled) != 1 {
		t.Errorf("expected RemoveTorrent to be called exactly once, got %d", removeCalled)
	}
	if removeDelFiles != false {
		t.Errorf("expected RemoveTorrent to receive delFiles=false, got %v", removeDelFiles)
	}

	// 2. Selected movie.mkv deleted locally by GoDownloader
	if _, err := os.Stat(selectedFile); !os.IsNotExist(err) {
		t.Errorf("expected selected movie.mkv to be deleted")
	}

	// 3. Unselected cover.jpg remains intact
	if _, err := os.Stat(skippedPreExistingFile); err != nil {
		t.Errorf("unselected/pre-existing cover.jpg was wrongly deleted: %v", err)
	}

	// 4. DB record removed
	if saved, _ := repo.GetByID(context.Background(), jobID); saved != nil {
		t.Errorf("expected job to be deleted from repo")
	}
}
