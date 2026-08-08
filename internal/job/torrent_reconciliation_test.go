package job

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// Helper to create a bencoded torrent file buffer
func makeTestTorrentV1(name string, length int64) ([]byte, string, error) {
	infoMap := map[string]interface{}{
		"name":         name,
		"piece length": int64(262144),
		"pieces":       string(make([]byte, 20)),
		"length":       length,
	}
	infoBytes, err := bencode.Marshal(infoMap)
	if err != nil {
		return nil, "", err
	}
	h1 := sha1.Sum(infoBytes)
	infoHash := hex.EncodeToString(h1[:])

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, "", err
	}
	return torrentBytes, strings.ToLower(infoHash), nil
}

func makeTestTorrentV2(name string, length int64) ([]byte, string, error) {
	infoMap := map[string]interface{}{
		"name":         name,
		"piece length": int64(262144),
		"meta version": int64(2),
		"file tree": map[string]interface{}{
			name: map[string]interface{}{
				"": map[string]interface{}{
					"length":      length,
					"pieces root": string(make([]byte, 32)),
				},
			},
		},
	}
	infoBytes, err := bencode.Marshal(infoMap)
	if err != nil {
		return nil, "", err
	}
	h2 := sha256.Sum256(infoBytes)
	infoHash := hex.EncodeToString(h2[:])

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, "", err
	}
	return torrentBytes, strings.ToLower(infoHash), nil
}

func makeTestTorrentHybrid(name string, length int64) ([]byte, string, error) {
	infoMap := map[string]interface{}{
		"name":         name,
		"piece length": int64(262144),
		"pieces":       string(make([]byte, 20)),
		"meta version": int64(2),
		"file tree": map[string]interface{}{
			name: map[string]interface{}{
				"": map[string]interface{}{
					"length":      length,
					"pieces root": string(make([]byte, 32)),
				},
			},
		},
	}
	infoBytes, err := bencode.Marshal(infoMap)
	if err != nil {
		return nil, "", err
	}
	h1 := sha1.Sum(infoBytes)
	infoHash := hex.EncodeToString(h1[:])

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, "", err
	}
	return torrentBytes, strings.ToLower(infoHash), nil
}

// 9. Test .torrent hash extraction fixtures with deterministic expected hashes
func TestExtractTorrentInfoHash_Fixtures(t *testing.T) {
	// V1
	v1Bytes, expectedV1, err := makeTestTorrentV1("ubuntu-22.04.iso", 1024*1024*1024)
	if err != nil {
		t.Fatalf("failed to make v1 torrent: %v", err)
	}
	gotV1, err := ExtractTorrentInfoHash(v1Bytes)
	if err != nil {
		t.Fatalf("ExtractTorrentInfoHash(v1) failed: %v", err)
	}
	if gotV1 != expectedV1 {
		t.Fatalf("v1 info hash mismatch: got %s, want %s", gotV1, expectedV1)
	}

	// V2
	v2Bytes, expectedV2, err := makeTestTorrentV2("v2-archive.iso", 2048*1024*1024)
	if err != nil {
		t.Fatalf("failed to make v2 torrent: %v", err)
	}
	gotV2, err := ExtractTorrentInfoHash(v2Bytes)
	if err != nil {
		t.Fatalf("ExtractTorrentInfoHash(v2) failed: %v", err)
	}
	if gotV2 != expectedV2 {
		t.Fatalf("v2 info hash mismatch: got %s, want %s", gotV2, expectedV2)
	}

	// Hybrid (should resolve to v1 SHA-1 hash for qBittorrent compatibility)
	hybridBytes, expectedHybrid, err := makeTestTorrentHybrid("hybrid.iso", 512*1024*1024)
	if err != nil {
		t.Fatalf("failed to make hybrid torrent: %v", err)
	}
	gotHybrid, err := ExtractTorrentInfoHash(hybridBytes)
	if err != nil {
		t.Fatalf("ExtractTorrentInfoHash(hybrid) failed: %v", err)
	}
	if gotHybrid != expectedHybrid {
		t.Fatalf("hybrid info hash mismatch: got %s, want %s", gotHybrid, expectedHybrid)
	}
}

// 1. Magnet hash absent in qBittorrent: AddMagnet called normally
func TestTorrentReconciliation_MagnetHashAbsent_CallsAddMagnet(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			return nil, nil // Hash absent
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "1111111111111111111111111111111111111111", nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "test.iso", TotalSize: 1000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "test.iso", Size: 1000, Selected: true}}, nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}

	// Wait for metadata acquisition
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if atomic.LoadInt32(&addMagnetCalled) != 1 {
		t.Fatalf("expected AddMagnet to be called 1 time, got %d", addMagnetCalled)
	}
}

// 2 & 3. Same-job hash already exists: AddMagnet NOT called, existing torrent reused, retry succeeds without 409
func TestTorrentReconciliation_SameJobExisting_ReusesTorrentWithoutCallingAdd(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	var stopDownloadCalled int32

	targetHash := "2222222222222222222222222222222222222222"
	var currentJobID string

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if hash == targetHash {
				return &TorrentOwnership{
					Hash:     targetHash,
					Category: "godownloader",
					Tags:     []string{currentJobID}, // Tagged with current job ID
				}, nil
			}
			return nil, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "", errors.New("failed to add magnet, status: 409")
		},
		stopDownloadFunc: func(hash string) error {
			atomic.AddInt32(&stopDownloadCalled, 1)
			return nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "existing.iso", TotalSize: 2000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "existing.iso", Size: 2000, Selected: true}}, nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:"+targetHash)
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}
	currentJobID = j.ID

	// Wait for metadata acquisition
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusAwaitingSelection {
		t.Fatalf("expected StatusAwaitingSelection, got %s (err=%s)", jobObj.Status, jobObj.Error)
	}

	// Verify AddMagnet was NOT called (avoiding 409)
	if atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Fatalf("expected AddMagnet to NOT be called for same-job existing torrent, called %d times", addMagnetCalled)
	}

	// Verify stopDownload was called for safety before file selection
	if atomic.LoadInt32(&stopDownloadCalled) < 1 {
		t.Fatalf("expected StopDownload to be called, got %d", stopDownloadCalled)
	}
}

// 4 & 10 & 11. Orphan godownloader torrent: adopted safely, stopped before selection, no AddMagnet, never deletes files
func TestTorrentReconciliation_OrphanGodownloader_AdoptedSafely(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	var adoptCalled int32
	var removeCalled int32

	targetHash := "3333333333333333333333333333333333333333"

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if hash == targetHash {
				return &TorrentOwnership{
					Hash:     targetHash,
					Category: "godownloader",
					Tags:     []string{"job_deadbeef"}, // Stale job ID that does not exist in local DB
				}, nil
			}
			return nil, nil
		},
		adoptTorrentFunc: func(hash, jobID string) error {
			atomic.AddInt32(&adoptCalled, 1)
			return nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "", errors.New("409 conflict")
		},
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			atomic.AddInt32(&removeCalled, 1)
			return nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "orphan.iso", TotalSize: 3000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "orphan.iso", Size: 3000, Selected: true}}, nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:"+targetHash)
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusAwaitingSelection {
		t.Fatalf("expected StatusAwaitingSelection, got %s (err=%s)", jobObj.Status, jobObj.Error)
	}

	if atomic.LoadInt32(&adoptCalled) != 1 {
		t.Fatalf("expected AdoptTorrent to be called 1 time, got %d", adoptCalled)
	}
	if atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Fatalf("expected AddMagnet to NOT be called during adoption, called %d", addMagnetCalled)
	}
	if atomic.LoadInt32(&removeCalled) != 0 {
		t.Fatalf("expected DeleteTorrents to NOT be called during adoption, called %d", removeCalled)
	}
}

// 5. Another existing local GoDownloader job owns hash: returns TORRENT_ALREADY_MANAGED and does not mutate
func TestTorrentReconciliation_AnotherLocalJobOwnsHash_ReturnsConflict(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	var adoptCalled int32

	targetHash := "4444444444444444444444444444444444444444"

	// Pre-create active job 1 in local repo
	existingJob := &Job{
		ID:        "job_active1",
		Status:    StatusDownloading,
		Type:      TypeTorrent,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), existingJob)

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if hash == targetHash {
				return &TorrentOwnership{
					Hash:     targetHash,
					Category: "godownloader",
					Tags:     []string{"job_active1"},
				}, nil
			}
			return nil, nil
		},
		adoptTorrentFunc: func(hash, jobID string) error {
			atomic.AddInt32(&adoptCalled, 1)
			return nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "", nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:"+targetHash)
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", jobObj.Status)
	}
	if !strings.Contains(jobObj.Error, "already managed by GoDownloader job job_active1") {
		t.Fatalf("expected error mentioning job_active1, got: %s", jobObj.Error)
	}
	if atomic.LoadInt32(&adoptCalled) != 0 || atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Fatal("torrent in qBittorrent must not be mutated when owned by another local job")
	}
}

// 6. Externally-owned qBittorrent torrent: returns TORRENT_ALREADY_EXISTS_EXTERNALLY and does not mutate
func TestTorrentReconciliation_ExternallyOwnedTorrent_ReturnsConflict(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	var adoptCalled int32
	var stopCalled int32

	targetHash := "5555555555555555555555555555555555555555"

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if hash == targetHash {
				return &TorrentOwnership{
					Hash:     targetHash,
					Category: "movies", // External category!
					Tags:     []string{"manual_upload"},
				}, nil
			}
			return nil, nil
		},
		adoptTorrentFunc: func(hash, jobID string) error {
			atomic.AddInt32(&adoptCalled, 1)
			return nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "", nil
		},
		stopDownloadFunc: func(hash string) error {
			atomic.AddInt32(&stopCalled, 1)
			return nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:"+targetHash)
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", jobObj.Status)
	}
	if !strings.Contains(jobObj.Error, "outside GoDownloader") {
		t.Fatalf("expected error mentioning outside GoDownloader, got: %s", jobObj.Error)
	}
	if atomic.LoadInt32(&adoptCalled) != 0 || atomic.LoadInt32(&addMagnetCalled) != 0 || atomic.LoadInt32(&stopCalled) != 0 {
		t.Fatal("external torrent in qBittorrent must not be mutated in any way")
	}
}

// 7. Add races and returns 409: one ownership re-query reconciles same-job torrent as success
func TestTorrentReconciliation_AddRace409_RequeriesAndReconciles(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var getOwnershipCallCount int32
	var addMagnetCallCount int32

	targetHash := "6666666666666666666666666666666666666666"

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			count := atomic.AddInt32(&getOwnershipCallCount, 1)
			if count == 1 {
				// Initial check: not found yet
				return nil, nil
			}
			// Second check after 409: found under godownloader
			return &TorrentOwnership{
				Hash:     targetHash,
				Category: "godownloader",
				Tags:     []string{"job_deadbeef"},
			}, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCallCount, 1)
			return "", errors.New("failed to add magnet, status: 409 (Torrent already exists)")
		},
		adoptTorrentFunc: func(hash, jobID string) error {
			return nil
		},
		getTorrentInfoFunc: func(hash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "raced.iso", TotalSize: 5000}, nil
		},
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "raced.iso", Size: 5000, Selected: true}}, nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.Create(context.Background(), "magnet:?xt=urn:btih:"+targetHash)
	if err != nil {
		t.Fatalf("mgr.Create failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusAwaitingSelection {
		t.Fatalf("expected StatusAwaitingSelection after 409 reconciliation, got %s (err=%s)", jobObj.Status, jobObj.Error)
	}

	if atomic.LoadInt32(&addMagnetCallCount) != 1 {
		t.Fatalf("expected AddMagnet to be called exactly once before 409, got %d", addMagnetCallCount)
	}
	if atomic.LoadInt32(&getOwnershipCallCount) != 2 {
		t.Fatalf("expected 2 GetTorrentOwnership calls (initial + post-409), got %d", getOwnershipCallCount)
	}
}

// 8. Uploaded .torrent file with same hash uses the same reconciliation rules
func TestTorrentReconciliation_UploadedTorrentFile_SameRules(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addTorrentFileCalled int32
	var adoptCalled int32

	data, hash, err := makeTestTorrentV1("uploaded.iso", 8000)
	if err != nil {
		t.Fatalf("makeTestTorrentV1 failed: %v", err)
	}

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test_upload.torrent")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(infoHash string) (*TorrentOwnership, error) {
			if infoHash == hash {
				return &TorrentOwnership{
					Hash:     hash,
					Category: "godownloader",
					Tags:     []string{"job_old123"},
				}, nil
			}
			return nil, nil
		},
		adoptTorrentFunc: func(infoHash, jobID string) error {
			atomic.AddInt32(&adoptCalled, 1)
			return nil
		},
		addTorrentFileFunc: func(path string) (string, error) {
			atomic.AddInt32(&addTorrentFileCalled, 1)
			return hash, nil
		},
		getTorrentInfoFunc: func(infoHash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "uploaded.iso", TotalSize: 8000}, nil
		},
		getFilesFunc: func(infoHash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "uploaded.iso", Size: 8000, Selected: true}}, nil
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	bus := newFakeEventBus()
	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	j, err := mgr.CreateTorrentFromFile(context.Background(), filePath)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		jobObj, _ := mgr.Get(context.Background(), j.ID)
		if jobObj != nil && jobObj.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	jobObj, _ := mgr.Get(context.Background(), j.ID)
	if jobObj.Status != StatusAwaitingSelection {
		t.Fatalf("expected StatusAwaitingSelection, got %s (err=%s)", jobObj.Status, jobObj.Error)
	}

	// Should have adopted instead of re-adding
	if atomic.LoadInt32(&adoptCalled) != 1 {
		t.Fatalf("expected AdoptTorrent to be called 1 time, got %d", adoptCalled)
	}
	if atomic.LoadInt32(&addTorrentFileCalled) != 0 {
		t.Fatalf("expected AddTorrentFile to NOT be called when existing in qBittorrent, got %d", addTorrentFileCalled)
	}
}
