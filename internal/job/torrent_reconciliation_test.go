package job

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// Helper to create a bencoded v1 torrent file buffer
func makeTestTorrentV1(name string, length int64) ([]byte, TorrentHashIdentity, error) {
	infoMap := map[string]interface{}{
		"name":         name,
		"piece length": int64(262144),
		"pieces":       string(make([]byte, 20)),
		"length":       length,
	}
	infoBytes, err := bencode.Marshal(infoMap)
	if err != nil {
		return nil, TorrentHashIdentity{}, err
	}
	h1 := sha1.Sum(infoBytes)
	v1Hex := strings.ToLower(hex.EncodeToString(h1[:]))

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, TorrentHashIdentity{}, err
	}
	ident := TorrentHashIdentity{
		V1Hash:        v1Hex,
		QBitTorrentID: v1Hex,
	}
	return torrentBytes, ident, nil
}

// Helper to create a bencoded pure v2 torrent file buffer
func makeTestTorrentV2(name string, length int64) ([]byte, TorrentHashIdentity, error) {
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
		return nil, TorrentHashIdentity{}, err
	}
	h2 := sha256.Sum256(infoBytes)
	v2Hex := strings.ToLower(hex.EncodeToString(h2[:]))
	qbitID := v2Hex[:40]

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, TorrentHashIdentity{}, err
	}
	ident := TorrentHashIdentity{
		V2Hash:        v2Hex,
		QBitTorrentID: qbitID,
	}
	return torrentBytes, ident, nil
}

// Helper to create a bencoded hybrid (v1 + v2) torrent file buffer
func makeTestTorrentHybrid(name string, length int64) ([]byte, TorrentHashIdentity, error) {
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
		return nil, TorrentHashIdentity{}, err
	}
	h1 := sha1.Sum(infoBytes)
	v1Hex := strings.ToLower(hex.EncodeToString(h1[:]))
	h2 := sha256.Sum256(infoBytes)
	v2Hex := strings.ToLower(hex.EncodeToString(h2[:]))
	qbitID := v2Hex[:40]

	torrentMap := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info":     bencode.Bytes(infoBytes),
	}
	torrentBytes, err := bencode.Marshal(torrentMap)
	if err != nil {
		return nil, TorrentHashIdentity{}, err
	}
	ident := TorrentHashIdentity{
		V1Hash:        v1Hex,
		V2Hash:        v2Hex,
		QBitTorrentID: qbitID,
	}
	return torrentBytes, ident, nil
}

// 1. Test .torrent hash extraction identity semantics for v1, v2, and hybrid
func TestExtractTorrentIdentity_Fixtures(t *testing.T) {
	// v1 only
	v1Bytes, expectedV1, err := makeTestTorrentV1("ubuntu-22.04.iso", 1024*1024*1024)
	if err != nil {
		t.Fatalf("failed to make v1 torrent: %v", err)
	}
	identV1, err := ExtractTorrentIdentity(v1Bytes)
	if err != nil {
		t.Fatalf("ExtractTorrentIdentity(v1) failed: %v", err)
	}
	if identV1.V1Hash != expectedV1.V1Hash {
		t.Errorf("v1 V1Hash mismatch: got %s, want %s", identV1.V1Hash, expectedV1.V1Hash)
	}
	if identV1.V2Hash != "" {
		t.Errorf("v1 V2Hash expected empty, got %s", identV1.V2Hash)
	}
	if identV1.QBitTorrentID != expectedV1.QBitTorrentID || len(identV1.QBitTorrentID) != 40 {
		t.Errorf("v1 QBitTorrentID mismatch: got %s (len=%d), want %s (40 hex)", identV1.QBitTorrentID, len(identV1.QBitTorrentID), expectedV1.QBitTorrentID)
	}

	// v2 only
	v2Bytes, expectedV2, err := makeTestTorrentV2("v2-archive.iso", 2048*1024*1024)
	if err != nil {
		t.Fatalf("failed to make v2 torrent: %v", err)
	}
	identV2, err := ExtractTorrentIdentity(v2Bytes)
	if err != nil {
		t.Fatalf("ExtractTorrentIdentity(v2) failed: %v", err)
	}
	if identV2.V2Hash != expectedV2.V2Hash || len(identV2.V2Hash) != 64 {
		t.Errorf("v2 V2Hash mismatch: got %s (len=%d), want %s (64 hex)", identV2.V2Hash, len(identV2.V2Hash), expectedV2.V2Hash)
	}
	if identV2.V1Hash != "" {
		t.Errorf("v2 V1Hash expected empty, got %s", identV2.V1Hash)
	}
	if identV2.QBitTorrentID != expectedV2.QBitTorrentID || len(identV2.QBitTorrentID) != 40 {
		t.Errorf("v2 QBitTorrentID mismatch: got %s (len=%d), want %s (40 hex)", identV2.QBitTorrentID, len(identV2.QBitTorrentID), expectedV2.QBitTorrentID)
	}

	// hybrid (v1 + v2)
	hybridBytes, expectedHybrid, err := makeTestTorrentHybrid("hybrid.iso", 512*1024*1024)
	if err != nil {
		t.Fatalf("failed to make hybrid torrent: %v", err)
	}
	identHybrid, err := ExtractTorrentIdentity(hybridBytes)
	if err != nil {
		t.Fatalf("ExtractTorrentIdentity(hybrid) failed: %v", err)
	}
	if identHybrid.V1Hash != expectedHybrid.V1Hash || len(identHybrid.V1Hash) != 40 {
		t.Errorf("hybrid V1Hash mismatch: got %s, want %s", identHybrid.V1Hash, expectedHybrid.V1Hash)
	}
	if identHybrid.V2Hash != expectedHybrid.V2Hash || len(identHybrid.V2Hash) != 64 {
		t.Errorf("hybrid V2Hash mismatch: got %s, want %s", identHybrid.V2Hash, expectedHybrid.V2Hash)
	}
	if identHybrid.QBitTorrentID != expectedHybrid.QBitTorrentID || len(identHybrid.QBitTorrentID) != 40 {
		t.Errorf("hybrid QBitTorrentID mismatch: got %s (len=%d), want %s (40 hex)", identHybrid.QBitTorrentID, len(identHybrid.QBitTorrentID), expectedHybrid.QBitTorrentID)
	}
}

// 2. Test v2 magnet and hybrid magnet handling
func TestExtractMagnetIdentity_Variants(t *testing.T) {
	// Standard v1 hex
	v1Hex := "1111111111111111111111111111111111111111"
	m1 := "magnet:?xt=urn:btih:" + v1Hex + "&dn=test.iso"
	id1, err := ExtractMagnetIdentity(m1)
	if err != nil {
		t.Fatalf("ExtractMagnetIdentity(v1 hex) failed: %v", err)
	}
	if id1.V1Hash != v1Hex || id1.QBitTorrentID != v1Hex {
		t.Errorf("v1 hex identity mismatch: got %+v", id1)
	}

	// BEP 52 v2 multihash (xt=urn:btmh:1220<64-hex>)
	v2Hex := "2222222222222222222222222222222222222222222222222222222222222222"
	m2 := "magnet:?xt=urn:btmh:1220" + v2Hex + "&dn=test-v2.iso"
	id2, err := ExtractMagnetIdentity(m2)
	if err != nil {
		t.Fatalf("ExtractMagnetIdentity(v2 multihash) failed: %v", err)
	}
	if id2.V2Hash != v2Hex || id2.QBitTorrentID != v2Hex[:40] {
		t.Errorf("v2 multihash identity mismatch: got %+v, want QBitTorrentID %s", id2, v2Hex[:40])
	}

	// Hybrid magnet (both btih and btmh)
	m3 := "magnet:?xt=urn:btih:" + v1Hex + "&xt=urn:btmh:1220" + v2Hex + "&dn=hybrid.iso"
	id3, err := ExtractMagnetIdentity(m3)
	if err != nil {
		t.Fatalf("ExtractMagnetIdentity(hybrid) failed: %v", err)
	}
	if id3.V1Hash != v1Hex || id3.V2Hash != v2Hex || id3.QBitTorrentID != v2Hex[:40] {
		t.Errorf("hybrid identity mismatch: got %+v, want QBitTorrentID %s", id3, v2Hex[:40])
	}
}

// 3. Magnet hash absent in qBittorrent: AddMagnet called normally
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

// 4. Same-job existing torrent: AddMagnet NOT called, existing torrent reused
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
					Tags:     []string{currentJobID},
				}, nil
			}
			return nil, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCalled, 1)
			return "", &EngineAPIError{Operation: "AddMagnet", StatusCode: http.StatusConflict, Detail: "Torrent already present"}
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

	if atomic.LoadInt32(&addMagnetCalled) != 0 {
		t.Fatalf("expected AddMagnet to NOT be called for same-job existing torrent, called %d times", addMagnetCalled)
	}
	if atomic.LoadInt32(&stopDownloadCalled) < 1 {
		t.Fatalf("expected StopDownload to be called, got %d", stopDownloadCalled)
	}
}

// 5. Adoption fails closed when StopTorrents returns an error
func TestTorrentReconciliation_AdoptionFailClosed_StopTorrentsError(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var startDownloadCalled int32

	targetHash := "3333333333333333333333333333333333333333"

	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(hash string) (*TorrentOwnership, error) {
			if hash == targetHash {
				return &TorrentOwnership{
					Hash:     targetHash,
					Category: "godownloader",
					Tags:     []string{"job_deadbeef"},
				}, nil
			}
			return nil, nil
		},
		adoptTorrentFunc: func(hash, jobID string) error {
			return errors.New("simulated StopTorrents daemon communication error")
		},
		startDownloadFunc: func(hash string) error {
			atomic.AddInt32(&startDownloadCalled, 1)
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
		t.Fatalf("expected StatusFailed when adoption fails closed, got %s", jobObj.Status)
	}
	if !strings.Contains(jobObj.Error, "StopTorrents") {
		t.Fatalf("expected StopTorrents error in job.Error, got: %s", jobObj.Error)
	}
	if atomic.LoadInt32(&startDownloadCalled) != 0 {
		t.Fatalf("StartDownload must NEVER be called when adoption fails, called %d times", startDownloadCalled)
	}
}

// 6. Another active local job owns hash: returns TORRENT_ALREADY_MANAGED
func TestTorrentReconciliation_AnotherLocalJobOwnsHash_ReturnsConflict(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addMagnetCalled int32
	var adoptCalled int32

	targetHash := "4444444444444444444444444444444444444444"

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

// 7. Externally-owned qBittorrent torrent: returns TORRENT_ALREADY_EXISTS_EXTERNALLY without mutating
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

// 8. Add returns typed *qbittorrent.APIError with HTTP 409: re-queries once and reconciles as success
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
				return nil, nil // Initial check: not found yet
			}
			return &TorrentOwnership{
				Hash:     targetHash,
				Category: "godownloader",
				Tags:     []string{"job_deadbeef"},
			}, nil
		},
		addMagnetFunc: func(magnet string) (string, error) {
			atomic.AddInt32(&addMagnetCallCount, 1)
			return "", &EngineAPIError{
				Operation:  "AddMagnet",
				StatusCode: http.StatusConflict,
				Detail:     "secret_passkey=123456&tracker_token=abcdef", // Sensitive daemon response
			}
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

// 9. Uploaded .torrent v2 and hybrid file reconciliation using 40-character QBitTorrentID
func TestTorrentReconciliation_UploadedTorrentV2AndHybrid_SameRules(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	var addTorrentFileCalled int32
	var adoptCalled int32

	data, ident, err := makeTestTorrentV2("uploaded-v2.iso", 8000)
	if err != nil {
		t.Fatalf("makeTestTorrentV2 failed: %v", err)
	}

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test_v2_upload.torrent")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	var lookedUpHash string
	eng := &fakeTorrentEngine{
		fakeEngine: &fakeEngine{},
		getOwnershipFunc: func(infoHash string) (*TorrentOwnership, error) {
			lookedUpHash = infoHash
			if infoHash == ident.QBitTorrentID {
				return &TorrentOwnership{
					Hash:     ident.QBitTorrentID,
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
			return ident.QBitTorrentID, nil
		},
		getTorrentInfoFunc: func(infoHash string) (*TorrentInfo, error) {
			return &TorrentInfo{Name: "uploaded-v2.iso", TotalSize: 8000}, nil
		},
		getFilesFunc: func(infoHash string) ([]TorrentFile, error) {
			return []TorrentFile{{Index: 0, Path: "uploaded-v2.iso", Size: 8000, Selected: true}}, nil
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

	// Verify qBittorrent was looked up using 40-character QBitTorrentID
	if lookedUpHash != ident.QBitTorrentID || len(lookedUpHash) != 40 {
		t.Fatalf("qBittorrent lookup must use 40-character QBitTorrentID, got %q (len=%d)", lookedUpHash, len(lookedUpHash))
	}
	if jobObj.EngineID != ident.QBitTorrentID {
		t.Fatalf("Job.EngineID must be 40-character QBitTorrentID, got %s", jobObj.EngineID)
	}
	if atomic.LoadInt32(&adoptCalled) != 1 {
		t.Fatalf("expected AdoptTorrent to be called 1 time, got %d", adoptCalled)
	}
	if atomic.LoadInt32(&addTorrentFileCalled) != 0 {
		t.Fatalf("expected AddTorrentFile to NOT be called when existing in qBittorrent, got %d", addTorrentFileCalled)
	}
}
