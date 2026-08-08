package job

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/networkpolicy"

	"github.com/anacrolix/torrent/bencode"
)

func makeTestTorrentFileHelper(t *testing.T, dir, name string, length int64) (string, string) {
	t.Helper()
	infoMap := map[string]interface{}{
		"name":         name,
		"piece length": int64(262144),
		"pieces":       string(make([]byte, 20)),
		"length":       length,
	}
	infoBytes, err := bencode.Marshal(infoMap)
	if err != nil {
		t.Fatalf("marshal v1 info: %v", err)
	}
	h1 := sha1.Sum(infoBytes)
	v1Hex := strings.ToLower(hex.EncodeToString(h1[:]))

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		t.Fatalf("marshal v1 torrent: %v", err)
	}

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, torrentBytes, 0644); err != nil {
		t.Fatalf("write v1 torrent: %v", err)
	}
	return filePath, v1Hex
}

// 1. Torrent Retry DB update failure: zero metadata goroutine engine actions
func TestTorrentRetry_DBUpdateFailure_ZeroEngineActions(t *testing.T) {
	jobID := "job_retry_fail"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=retry.iso",
		Status:    StatusFailed,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	failRepo := &failingUpdateJobRepo{jobs: map[string]*Job{jobID: j}}

	var addMagnetCalled int32
	var addTorrentFileCalled int32
	var getOwnershipCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "1111111111111111111111111111111111111111", nil
		},
		addTorrentFileFunc: func(path string) (string, error) {
			atomic.AddInt32(&addTorrentFileCalled, 1)
			return "1111111111111111111111111111111111111111", nil
		},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			atomic.AddInt32(&getOwnershipCalled, 1)
			return nil, nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	torrentRepo := newFakeTorrentRepository(failRepo)
	manager := NewManager(failRepo, engines, bus, t.TempDir(), torrentRepo)

	_, err := manager.Retry(context.Background(), jobID)
	if err == nil {
		t.Fatalf("expected Retry to fail on DB update failure")
	}

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Errorf("expected 0 AddMagnet calls on retry DB update failure, got %d", addMagnetCalled)
	}
	if atomic.LoadInt32(&addTorrentFileCalled) != 0 {
		t.Errorf("expected 0 AddTorrentFile calls on retry DB update failure, got %d", addTorrentFileCalled)
	}
	if atomic.LoadInt32(&getOwnershipCalled) != 0 {
		t.Errorf("expected 0 GetTorrentOwnership calls on retry DB update failure, got %d", getOwnershipCalled)
	}
}

// 2. Graceful Stop during ANALYZING: qBittorrent torrent is NOT removed
func TestGracefulStop_DuringAnalyzing_DoesNotRemoveTorrent(t *testing.T) {
	jobID := "job_stop_analyzing"
	infoHash := "2222222222222222222222222222222222222222"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=test.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	var removeTorrentCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			// Metadata not yet ready to simulate in-flight acquisition
			return nil, errors.New("torrent not found")
		},
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			atomic.AddInt32(&removeTorrentCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	torrentRepo := newFakeTorrentRepository(repo)
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	// Launch metadata acquisition in background
	go manager.acquireTorrentMetadata(jobID, j.Source, "")

	time.Sleep(100 * time.Millisecond)

	// Perform graceful backend stop
	manager.Stop()

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&removeTorrentCalled) != 0 {
		t.Fatalf("expected RemoveTorrent to NOT be called on graceful stop during ANALYZING, got %d calls", removeTorrentCalled)
	}

	// Persisted state must remain ANALYZING
	saved, _ := repo.GetByID(context.Background(), jobID)
	if saved.Status != StatusAnalyzing {
		t.Errorf("expected job to remain ANALYZING on graceful stop, got %s", saved.Status)
	}
}

// 3. Restart with ANALYZING magnet + existing qBit object:
// same torrent reused, no duplicate, eventually AwaitingSelection
func TestRestartRecovery_AnalyzingMagnet_ExistingQBitObject(t *testing.T) {
	jobID := "job_restart_mag"
	infoHash := "3333333333333333333333333333333333333333"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=ubuntu.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{
		JobID:    jobID,
		InfoHash: infoHash,
	}

	var addMagnetCalled int32
	var stopDownloadCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  true,
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return infoHash, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{
				Name:      "ubuntu.iso",
				InfoHash:  infoHash,
				TotalSize: 1024 * 1024 * 1024,
			}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "ubuntu.iso", Size: 1024 * 1024 * 1024, Priority: PriorityNormal, Selected: true},
			}, nil
		},
		stopDownloadFunc: func(hash string) error {
			atomic.AddInt32(&stopDownloadCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	// Simulate restart recovery
	manager.recover(context.Background())

	// Wait for metadata goroutine to complete
	deadline := time.Now().Add(2 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusAwaitingSelection {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach AwaitingSelection, current status=%v, error=%v", saved.Status, saved.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Fatalf("expected AddMagnet to NOT be called for existing same-job torrent, called %d times", addMagnetCalled)
	}

	if atomic.LoadInt32(&stopDownloadCalled) < 1 {
		t.Fatalf("expected StopDownload to be called to ensure stopped state before AwaitingSelection")
	}
}

// 4. Restart with ANALYZING uploaded .torrent + existing qBit object:
// same behavior, no duplicate
func TestRestartRecovery_AnalyzingUploadedTorrent_ExistingQBitObject(t *testing.T) {
	tmpDir := t.TempDir()
	torrentFilePath, infoHash := makeTestTorrentFileHelper(t, tmpDir, "linux.iso", 2048*1024)

	jobID := "job_restart_file"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "torrent://linux.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{
		JobID:           jobID,
		TorrentFilePath: torrentFilePath,
		InfoHash:        infoHash,
	}

	var addTorrentFileCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  true,
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		addTorrentFileFunc: func(path string) (string, error) {
			atomic.AddInt32(&addTorrentFileCalled, 1)
			return infoHash, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{
				Name:      "linux.iso",
				InfoHash:  infoHash,
				TotalSize: 2048 * 1024,
			}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "linux.iso", Size: 2048 * 1024, Priority: PriorityNormal, Selected: true},
			}, nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	manager.recover(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusAwaitingSelection {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach AwaitingSelection, current status=%v, error=%v", saved.Status, saved.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&addTorrentFileCalled) != 0 {
		t.Fatalf("expected AddTorrentFile to NOT be called for existing torrent, called %d times", addTorrentFileCalled)
	}
}

// 5. Restart with ANALYZING job and qBit object absent:
// safely re-add, eventually AwaitingSelection
func TestRestartRecovery_AnalyzingJob_QBitObjectAbsent(t *testing.T) {
	jobID := "job_restart_absent"
	infoHash := "5555555555555555555555555555555555555555"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=readd.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{
		JobID:    jobID,
		InfoHash: infoHash,
	}

	var addMagnetCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  true,
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if atomic.LoadInt32(&addMagnetCalled) == 0 {
				return nil, nil // absent initially
			}
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return infoHash, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{
				Name:      "readd.iso",
				InfoHash:  infoHash,
				TotalSize: 5000,
			}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "readd.iso", Size: 5000, Priority: PriorityNormal, Selected: true},
			}, nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	manager.recover(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusAwaitingSelection {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach AwaitingSelection, current status=%v, error=%v", saved.Status, saved.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&addMagnetCalled) != 1 {
		t.Fatalf("expected AddMagnet to be called exactly once to re-add absent torrent, got %d", addMagnetCalled)
	}
}

// 6. User Cancel while ANALYZING: qBit torrent removed/cancelled as expected
func TestUserCancel_DuringAnalyzing_RemovesTorrent(t *testing.T) {
	jobID := "job_cancel_analyzing"
	infoHash := "6666666666666666666666666666666666666666"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=cancel.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{
		JobID:    jobID,
		InfoHash: infoHash,
	}

	var cancelCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{
			cancelFunc: func(ctx context.Context, j *Job) error {
				atomic.AddInt32(&cancelCalled, 1)
				return nil
			},
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	cancelledJob, err := manager.Cancel(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	if cancelledJob.Status != StatusCancelled {
		t.Errorf("expected job to be StatusCancelled, got %s", cancelledJob.Status)
	}

	if atomic.LoadInt32(&cancelCalled) < 1 {
		t.Fatalf("expected engine Cancel to be called for analyzing torrent job")
	}
}

// 7. Priority verification success: exact priorities read back, StartDownload allowed
func TestPriorityVerification_Success(t *testing.T) {
	jobID := "job_prio_success"
	infoHash := "7777777777777777777777777777777777777777"
	j := &Job{
		ID:             jobID,
		Type:           TypeTorrent,
		EngineID:       infoHash,
		Source:         "magnet:?xt=urn:btih:" + infoHash,
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		TorrentInfo: &TorrentInfo{
			Name:      "prio.iso",
			InfoHash:  infoHash,
			TotalSize: 3000,
		},
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash, Name: "prio.iso", TotalSize: 3000}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Size: 1000, Selected: true, Priority: "normal"},
		{JobID: jobID, FileIndex: 1, Size: 2000, Selected: false, Priority: "skip"},
	}

	var startDownloadCalled int32
	var setFilePrioritiesCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  false,
		setPrioritiesFunc: func(hash string) error {
			atomic.AddInt32(&setFilePrioritiesCalled, 1)
			return nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "file1.bin", Size: 1000, Priority: PriorityNormal, Selected: true},
				{Index: 1, Path: "file2.bin", Size: 2000, Priority: PrioritySkip, Selected: false},
			}, nil
		},
		startDownloadFunc: func(hash string) error {
			atomic.AddInt32(&startDownloadCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	queueRepo := &fakeQueueRepo{}
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)
	manager.SetQueueRepository(queueRepo)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	_, err := manager.StartTorrentWithPolicy(context.Background(), jobID, selections, policy)
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	if atomic.LoadInt32(&setFilePrioritiesCalled) != 1 {
		t.Errorf("expected SetFilePriorities to be called once, got %d", setFilePrioritiesCalled)
	}

	// Without scheduler, fallback runs StartDownload after verification
	if atomic.LoadInt32(&startDownloadCalled) != 1 {
		t.Errorf("expected StartDownload to be called after verified priorities, got %d", startDownloadCalled)
	}
}

// 8. Priority mismatch: fail closed, StartDownload == 0 calls, torrent stopped
func TestPriorityVerification_Mismatch_FailsClosed(t *testing.T) {
	jobID := "job_prio_mismatch"
	infoHash := "8888888888888888888888888888888888888888"
	j := &Job{
		ID:             jobID,
		Type:           TypeTorrent,
		EngineID:       infoHash,
		Source:         "magnet:?xt=urn:btih:" + infoHash,
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		TorrentInfo: &TorrentInfo{
			Name:      "mismatch.iso",
			InfoHash:  infoHash,
			TotalSize: 3000,
		},
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash, Name: "mismatch.iso", TotalSize: 3000}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Size: 1000, Selected: true, Priority: "normal"},
		{JobID: jobID, FileIndex: 1, Size: 2000, Selected: false, Priority: "skip"},
	}

	var startDownloadCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  true,
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			// Returns mismatch: file 1 is still normal / selected instead of skip
			return []TorrentFile{
				{Index: 0, Path: "file1.bin", Size: 1000, Priority: PriorityNormal, Selected: true},
				{Index: 1, Path: "file2.bin", Size: 2000, Priority: PriorityNormal, Selected: true},
			}, nil
		},
		startDownloadFunc: func(hash string) error {
			atomic.AddInt32(&startDownloadCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	queueRepo := &fakeQueueRepo{}
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)
	manager.SetQueueRepository(queueRepo)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	_, err := manager.StartTorrentWithPolicy(ctx, jobID, selections, policy)
	if err == nil {
		t.Fatalf("expected StartTorrentWithPolicy to fail on priority mismatch")
	}

	if atomic.LoadInt32(&startDownloadCalled) != 0 {
		t.Fatalf("expected ZERO calls to StartDownload on priority verification failure, got %d", startDownloadCalled)
	}

	// Verify zero queue entries created
	if len(queueRepo.entries) != 0 {
		t.Fatalf("expected zero queue entries on priority mismatch, got %d", len(queueRepo.entries))
	}
}

// 9. Priority visibility delayed: polling eventually verifies without arbitrary sleep
func TestPriorityVerification_DelayedVisibility(t *testing.T) {
	jobID := "job_prio_delayed"
	infoHash := "9999999999999999999999999999999999999999"
	j := &Job{
		ID:             jobID,
		Type:           TypeTorrent,
		EngineID:       infoHash,
		Source:         "magnet:?xt=urn:btih:" + infoHash,
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		TorrentInfo: &TorrentInfo{
			Name:      "delayed.iso",
			InfoHash:  infoHash,
			TotalSize: 3000,
		},
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash, Name: "delayed.iso", TotalSize: 3000}
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Size: 1000, Selected: true, Priority: "normal"},
		{JobID: jobID, FileIndex: 1, Size: 2000, Selected: false, Priority: "skip"},
	}

	var pollCount int32
	var startDownloadCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  false,
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			count := atomic.AddInt32(&pollCount, 1)
			if count < 3 {
				// Old stale priorities initially
				return []TorrentFile{
					{Index: 0, Path: "file1.bin", Size: 1000, Priority: PriorityNormal, Selected: true},
					{Index: 1, Path: "file2.bin", Size: 2000, Priority: PriorityNormal, Selected: true},
				}, nil
			}
			// Updated priorities on 3rd poll
			return []TorrentFile{
				{Index: 0, Path: "file1.bin", Size: 1000, Priority: PriorityNormal, Selected: true},
				{Index: 1, Path: "file2.bin", Size: 2000, Priority: PrioritySkip, Selected: false},
			}, nil
		},
		startDownloadFunc: func(hash string) error {
			atomic.AddInt32(&startDownloadCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	queueRepo := &fakeQueueRepo{}
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)
	manager.SetQueueRepository(queueRepo)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	_, err := manager.StartTorrentWithPolicy(context.Background(), jobID, selections, policy)
	if err != nil {
		t.Fatalf("StartTorrentWithPolicy failed: %v", err)
	}

	if atomic.LoadInt32(&pollCount) < 3 {
		t.Fatalf("expected at least 3 poll queries, got %d", pollCount)
	}

	if atomic.LoadInt32(&startDownloadCalled) != 1 {
		t.Fatalf("expected StartDownload to be called after delayed priority confirmation, got %d", startDownloadCalled)
	}
}

// 10. TotalBytes repair persistence failure: StartDownload == 0, deterministic persistence state
func TestTotalBytesRepair_PersistenceFailure_FailsDispatch(t *testing.T) {
	jobID := "job_repair_fail"
	infoHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	j := &Job{
		ID:             jobID,
		Type:           TypeTorrent,
		EngineID:       infoHash,
		Source:         "magnet:?xt=urn:btih:" + infoHash,
		Status:         StatusQueued,
		TotalBytes:     9999999, // Inaccurate TotalBytes
		Engine:         "qbittorrent",
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	failRepo := &failingUpdateJobRepo{jobs: map[string]*Job{jobID: j}}

	torrentRepo := newFakeTorrentRepository(failRepo)
	torrentRepo.torrentFiles[jobID] = []TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Size: 1000, Selected: true, Priority: "normal"},
	}

	var startDownloadCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		startDownloadFunc: func(hash string) error {
			atomic.AddInt32(&startDownloadCalled, 1)
			return nil
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	queueRepo := &fakeQueueRepo{
		entries: map[string]*QueueEntry{
			jobID: {JobID: jobID, Position: 1, Action: QueueActionStart},
		},
	}

	manager := NewManager(failRepo, engines, bus, t.TempDir(), torrentRepo)
	manager.SetQueueRepository(queueRepo)

	qj := &QueuedJob{
		JobID:    jobID,
		Position: 1,
		Action:   QueueActionStart,
	}

	err := manager.dispatchQueuedJob(context.Background(), qj)
	if err == nil {
		t.Fatalf("expected dispatchQueuedJob to fail when TotalBytes repair persistence fails")
	}

	if atomic.LoadInt32(&startDownloadCalled) != 0 {
		t.Fatalf("expected ZERO calls to StartDownload on repair persistence failure, got %d", startDownloadCalled)
	}
}

// 11. Existing same-job torrent with metadata: cannot reach AwaitingSelection unless stopped state is verified
func TestExistingSameJobTorrent_MustVerifyStoppedState(t *testing.T) {
	jobID := "job_stop_verify_fail"
	infoHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=unstopped.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		isStopped:  false, // Never transitions to stopped
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "unstopped.iso", InfoHash: infoHash, TotalSize: 4000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "unstopped.iso", Size: 4000, Priority: PriorityNormal, Selected: true},
			}, nil
		},
		stopDownloadFunc: func(hash string) error {
			return errors.New("daemon busy")
		},
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	go manager.acquireTorrentMetadata(jobID, j.Source, "")

	deadline := time.Now().Add(6 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not fail closed on unstopped state, status=%v", saved.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}

	saved, _ := repo.GetByID(context.Background(), jobID)
	if saved.Status == StatusAwaitingSelection {
		t.Fatalf("job MUST NOT reach StatusAwaitingSelection when stopped verification fails")
	}
}

// 12. Existing same-job magnet still fetching metadata: StopDownload must NOT be called
// before metadata is ready.
// Strengthened: after StopDownload, metadata can never arrive — so premature stop causes
// test timeout / failure.
func TestExistingMagnet_StillFetchingMetadata_NotPrematurelyStopped(t *testing.T) {
	jobID := "job_mag_metadata_fetch"
	infoHash := "cccccccccccccccccccccccccccccccccccccccc"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=fetch.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}

	var metadataFetchPolls int32
	var mu sync.Mutex
	isStoppedState := false
	metadataKilled := false // once StopDownload is called, metadata can never arrive

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{jobID}}, nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			mu.Lock()
			killed := metadataKilled
			mu.Unlock()
			if killed {
				// Metadata can never complete after StopDownload — qBittorrent killed DHT/peers
				return &TorrentInfo{Name: "", InfoHash: infoHash, TotalSize: 0}, nil
			}
			count := atomic.AddInt32(&metadataFetchPolls, 1)
			if count < 3 {
				// Metadata still pending from DHT/peers
				return &TorrentInfo{Name: "", InfoHash: infoHash, TotalSize: 0}, nil
			}
			return &TorrentInfo{Name: "fetch.iso", InfoHash: infoHash, TotalSize: 6000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			mu.Lock()
			killed := metadataKilled
			mu.Unlock()
			if killed {
				return nil, errors.New("no files yet — metadata killed by StopDownload")
			}
			count := atomic.LoadInt32(&metadataFetchPolls)
			if count < 3 {
				return nil, errors.New("no files yet")
			}
			return []TorrentFile{
				{Index: 0, Path: "fetch.iso", Size: 6000, Priority: PriorityNormal, Selected: true},
			}, nil
		},
		stopDownloadFunc: func(hash string) error {
			mu.Lock()
			isStoppedState = true
			metadataKilled = true // key: premature stop prevents metadata from ever arriving
			mu.Unlock()
			return nil
		},
	}

	torrentEng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		mu.Lock()
		st := "downloading"
		if isStoppedState {
			st = "stoppedDL"
		}
		mu.Unlock()
		return &EngineStatus{Status: StatusDownloading, RawState: st}, nil
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	go manager.acquireTorrentMetadata(jobID, j.Source, "")

	deadline := time.Now().Add(6 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusAwaitingSelection {
			break
		}
		if saved != nil && saved.Status == StatusFailed {
			t.Fatalf("job FAILED — premature StopDownload killed metadata acquisition: %s", saved.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach AwaitingSelection (premature StopDownload killed metadata), current status=%v, error=%v", saved.Status, saved.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&metadataFetchPolls) < 3 {
		t.Fatalf("expected metadata polling to continue until metadata arrives, got %d polls", metadataFetchPolls)
	}
}

// 13. Orphan-magnet metadata-incomplete: AdoptTorrent must NOT stop the torrent.
// After adoption, metadata acquisition must continue to completion.
func TestOrphanMagnet_MetadataIncomplete_AdoptDoesNotStop(t *testing.T) {
	jobID := "job_orphan_mag_adopt"
	infoHash := "dddddddddddddddddddddddddddddddddddddddd"
	j := &Job{
		ID:        jobID,
		Type:      TypeTorrent,
		Source:    "magnet:?xt=urn:btih:" + infoHash + "&dn=orphan.iso",
		Status:    StatusAnalyzing,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	repo := newFakeJobRepository()
	repo.jobs[jobID] = j

	torrentRepo := newFakeTorrentRepository(repo)
	torrentRepo.torrentJobs[jobID] = &TorrentJobRecord{JobID: jobID, InfoHash: infoHash}

	var metadataFetchPolls int32
	var mu sync.Mutex
	isStoppedState := false
	metadataKilled := false
	var adoptCalled int32

	torrentEng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			// Orphan: godownloader category but tagged with a stale/other job
			return &TorrentOwnership{Hash: infoHash, Category: "godownloader", Tags: []string{"job_stale_old"}}, nil
		},
		adoptTorrentFunc: func(hash, jid string) error {
			atomic.AddInt32(&adoptCalled, 1)
			return nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			mu.Lock()
			killed := metadataKilled
			mu.Unlock()
			if killed {
				return &TorrentInfo{Name: "", InfoHash: infoHash, TotalSize: 0}, nil
			}
			count := atomic.AddInt32(&metadataFetchPolls, 1)
			if count < 3 {
				return &TorrentInfo{Name: "", InfoHash: infoHash, TotalSize: 0}, nil
			}
			return &TorrentInfo{Name: "orphan.iso", InfoHash: infoHash, TotalSize: 8000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			mu.Lock()
			killed := metadataKilled
			mu.Unlock()
			if killed {
				return nil, errors.New("no files — metadata killed by premature stop")
			}
			count := atomic.LoadInt32(&metadataFetchPolls)
			if count < 3 {
				return nil, errors.New("no files yet")
			}
			return []TorrentFile{
				{Index: 0, Path: "orphan.iso", Size: 8000, Priority: PriorityNormal, Selected: true},
			}, nil
		},
		stopDownloadFunc: func(hash string) error {
			mu.Lock()
			isStoppedState = true
			metadataKilled = true // premature stop prevents metadata from completing
			mu.Unlock()
			return nil
		},
	}

	torrentEng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		mu.Lock()
		st := "downloading"
		if isStoppedState {
			st = "stoppedDL"
		}
		mu.Unlock()
		return &EngineStatus{Status: StatusDownloading, RawState: st}, nil
	}

	engines := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": torrentEng}}
	bus := newFakeEventBus()
	manager := NewManager(repo, engines, bus, t.TempDir(), torrentRepo)

	// The stale job must not exist in the repo, so the ownership check falls through to adopt
	go manager.acquireTorrentMetadata(jobID, j.Source, "")

	deadline := time.Now().Add(6 * time.Second)
	for {
		saved, _ := repo.GetByID(context.Background(), jobID)
		if saved != nil && saved.Status == StatusAwaitingSelection {
			break
		}
		if saved != nil && saved.Status == StatusFailed {
			t.Fatalf("job FAILED — premature stop during adopt killed metadata: %s", saved.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach AwaitingSelection (premature stop killed metadata), current status=%v, error=%v", saved.Status, saved.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if atomic.LoadInt32(&adoptCalled) != 1 {
		t.Fatalf("expected AdoptTorrent to be called exactly once, got %d", atomic.LoadInt32(&adoptCalled))
	}
	if atomic.LoadInt32(&metadataFetchPolls) < 3 {
		t.Fatalf("expected metadata polling to continue until metadata arrives after adoption, got %d polls", metadataFetchPolls)
	}
}
