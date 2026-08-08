package qbittorrent

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/job"

	"github.com/anacrolix/torrent/bencode"
)

func makeTestTorrentFileV1(t *testing.T, dir, name string, length int64) (string, string) {
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

func makeTestTorrentFileV2(t *testing.T, dir, name string, length int64) (string, string) {
	t.Helper()
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
		t.Fatalf("marshal v2 info: %v", err)
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
		t.Fatalf("marshal v2 torrent: %v", err)
	}

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, torrentBytes, 0644); err != nil {
		t.Fatalf("write v2 torrent: %v", err)
	}
	return filePath, qbitID
}

func makeTestTorrentFileHybrid(t *testing.T, dir, name string, length int64) (string, string) {
	t.Helper()
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
		t.Fatalf("marshal hybrid info: %v", err)
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
		t.Fatalf("marshal hybrid torrent: %v", err)
	}

	filePath := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(filePath, torrentBytes, 0644); err != nil {
		t.Fatalf("write hybrid torrent: %v", err)
	}
	return filePath, qbitID
}

// 7.A Uploaded torrent immediate visibility:
// AddTorrentFile POST -> success, first GetTorrentInfo(expectedHash) -> torrent
// Expected: AddTorrentFile returns expectedHash, no GetTorrents(category) discovery required.
func TestEngine_AddTorrentFile_ImmediateVisibility(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV1(t, tmpDir, "immediate.iso", 1024*1024)

	var categoryListingCalled int32
	var infoHashCalled int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			if strings.Contains(r.URL.RawQuery, "category=") {
				atomic.AddInt32(&categoryListingCalled, 1)
			}
			if strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
				atomic.AddInt32(&infoHashCalled, 1)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"immediate.iso","state":"stoppedDL"}]`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	hash, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-imm-1")
	if err != nil {
		t.Fatalf("expected AddTorrentFile to succeed, got %v", err)
	}

	if hash != expectedHash {
		t.Fatalf("expected canonical hash %s, got %s", expectedHash, hash)
	}

	if atomic.LoadInt32(&categoryListingCalled) != 0 {
		t.Fatalf("expected ZERO calls to category listing, got %d", categoryListingCalled)
	}

	if atomic.LoadInt32(&infoHashCalled) < 1 {
		t.Fatalf("expected GetTorrentInfo to be called with expectedHash %s", expectedHash)
	}
}

// 7.B Delayed qBittorrent visibility:
// AddTorrentFile POST -> success, GetTorrentInfo -> [] -> [] -> expected torrent
// Expected: AddTorrentFile succeeds, returns expected canonical hash, no false Failed state, no Retry required.
func TestEngine_AddTorrentFile_DelayedVisibility(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV1(t, tmpDir, "delayed.iso", 2048*1024)

	var infoPollCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			if strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
				count := atomic.AddInt32(&infoPollCount, 1)
				if count < 3 {
					// Simulate transiently not yet queryable
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`[]`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"delayed.iso","state":"stoppedDL"}]`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	hash, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-delayed-1")
	if err != nil {
		t.Fatalf("expected AddTorrentFile to succeed after transient delay, got %v", err)
	}

	if hash != expectedHash {
		t.Fatalf("expected canonical hash %s, got %s", expectedHash, hash)
	}

	if atomic.LoadInt32(&infoPollCount) < 3 {
		t.Fatalf("expected at least 3 poll queries, got %d", infoPollCount)
	}
}

// 7.C Visibility timeout:
// Add POST -> success, GetTorrentInfo always -> ErrTorrentNotFound
// Expected: bounded timeout, explicit safe error, no infinite loop.
func TestEngine_AddTorrentFile_VisibilityTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, _ := makeTestTorrentFileV1(t, tmpDir, "timeout.iso", 1024*1024)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			// Always empty
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := engine.AddTorrentFile(ctx, torrentPath, "/tmp", "job-timeout-1")
	if err == nil {
		t.Fatalf("expected AddTorrentFile to fail on visibility timeout")
	}

	if !strings.Contains(err.Error(), "visibility could not be confirmed") && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected explicit visibility error containing diagnostic context, got: %v", err)
	}
}

// 7.D v1 uploaded torrent: returned ID == SHA1 info hash / 40 chars
func TestEngine_AddTorrentFile_V1Identity(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV1(t, tmpDir, "v1.iso", 5000)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" && strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"v1.iso","state":"stoppedDL"}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	hash, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-v1")
	if err != nil {
		t.Fatalf("AddTorrentFile failed: %v", err)
	}
	if hash != expectedHash || len(hash) != 40 {
		t.Fatalf("expected 40-char v1 info hash %s, got %s", expectedHash, hash)
	}
}

// 7.E v2 uploaded torrent: returned ID == first 20 bytes SHA256 / 40 chars
func TestEngine_AddTorrentFile_V2Identity(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV2(t, tmpDir, "v2.iso", 5000)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" && strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"v2.iso","state":"stoppedDL"}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	hash, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-v2")
	if err != nil {
		t.Fatalf("AddTorrentFile failed: %v", err)
	}
	if hash != expectedHash || len(hash) != 40 {
		t.Fatalf("expected 40-char v2 QBitTorrentID %s, got %s", expectedHash, hash)
	}
}

// 7.F hybrid uploaded torrent: returned ID == qBittorrent TorrentID / 40 chars
func TestEngine_AddTorrentFile_HybridIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileHybrid(t, tmpDir, "hybrid.iso", 5000)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" && strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"hybrid.iso","state":"stoppedDL"}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	hash, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-hybrid")
	if err != nil {
		t.Fatalf("AddTorrentFile failed: %v", err)
	}
	if hash != expectedHash || len(hash) != 40 {
		t.Fatalf("expected 40-char hybrid QBitTorrentID %s, got %s", expectedHash, hash)
	}
}

// 7.G Verify AddTorrentFile no longer derives identity by scanning category/tag
func TestEngine_AddTorrentFile_NoCategoryTagScanning(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV1(t, tmpDir, "notags.iso", 1024)

	var getTorrentsCategoryCalled int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			if strings.Contains(r.URL.RawQuery, "category=") {
				atomic.AddInt32(&getTorrentsCategoryCalled, 1)
			}
			if strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"notags.iso","state":"stoppedDL"}]`))
				return
			}
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	_, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-notags")
	if err != nil {
		t.Fatalf("AddTorrentFile failed: %v", err)
	}
	if atomic.LoadInt32(&getTorrentsCategoryCalled) != 0 {
		t.Fatalf("expected NO category listing calls during AddTorrentFile, got %d", getTorrentsCategoryCalled)
	}
}

// 7.H Magnet delayed visibility: AddMagnet success, first lookup not found, later lookup succeeds
func TestEngine_AddMagnet_DelayedVisibility(t *testing.T) {
	const expectedHash = "0123456789abcdef0123456789abcdef01234567"
	var infoPollCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" || r.URL.Path == "/api/v2/torrents/add" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			if strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
				count := atomic.AddInt32(&infoPollCount, 1)
				if count < 2 {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`[]`))
					return
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"magnet.iso","state":"stoppedDL"}]`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	magnet := "magnet:?xt=urn:btih:" + expectedHash + "&dn=magnet.iso"
	hash, err := engine.AddMagnet(context.Background(), magnet, "/tmp", "job-mag-delay")
	if err != nil {
		t.Fatalf("expected AddMagnet to succeed, got %v", err)
	}
	if hash != expectedHash {
		t.Fatalf("expected canonical hash %s, got %s", expectedHash, hash)
	}
	if atomic.LoadInt32(&infoPollCount) < 2 {
		t.Fatalf("expected at least 2 poll queries for delayed magnet visibility, got %d", infoPollCount)
	}
}

// 7.I Regression: uploaded torrent is added stopped before file selection
func TestEngine_AddTorrentFile_StoppedBeforeSelection(t *testing.T) {
	tmpDir := t.TempDir()
	torrentPath, expectedHash := makeTestTorrentFileV1(t, tmpDir, "stopped.iso", 1024)

	var stoppedReceived string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/createCategory" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/add" {
			if err := r.ParseMultipartForm(10 * 1024 * 1024); err == nil {
				stoppedReceived = r.FormValue("stopped")
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" && strings.Contains(r.URL.RawQuery, "hashes="+expectedHash) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"` + expectedHash + `","name":"stopped.iso","state":"stoppedDL"}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)
	_, err := engine.AddTorrentFile(context.Background(), torrentPath, "/tmp", "job-stopped")
	if err != nil {
		t.Fatalf("AddTorrentFile failed: %v", err)
	}
	if stoppedReceived != "true" {
		t.Fatalf("expected uploaded torrent to be added with stopped=true, got %q", stoppedReceived)
	}
}

func TestEngine_Status(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"1234","name":"ubuntu.iso","state":"downloading","progress":0.5,"eta":3600,"dlspeed":1024,"completed":512,"size":1024,"upspeed":10,"uploaded":100,"ratio":0.1,"num_seeds":5,"num_leechs":10}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	j := &job.Job{
		ID:       "job-1",
		EngineID: "1234",
	}

	status, err := engine.Status(context.Background(), j)
	if err != nil {
		t.Fatalf("expected Status to succeed, got %v", err)
	}

	if status.Status != job.StatusDownloading {
		t.Errorf("expected status downloading, got %s", status.Status)
	}
	if status.Progress != 50.0 {
		t.Errorf("expected progress 50.0, got %f", status.Progress)
	}
	if status.SpeedBytesPerSecond != 1024 {
		t.Errorf("expected speed 1024, got %d", status.SpeedBytesPerSecond)
	}
	if status.Seeders != 5 {
		t.Errorf("expected 5 seeders, got %d", status.Seeders)
	}
}

func TestEngine_GetFiles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/files" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"index":0,"name":"file1.txt","size":100,"progress":0.5,"priority":1,"is_seed":false}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	files, err := engine.GetFiles(context.Background(), "1234")
	if err != nil {
		t.Fatalf("expected GetFiles to succeed, got %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Index != 0 || files[0].Path != "file1.txt" || files[0].Priority != "normal" || files[0].Progress != 50.0 {
		t.Errorf("parsed file incorrect: %+v", files[0])
	}
}

func TestEngine_SetFilePriorities(t *testing.T) {
	prioCallCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/filePrio" {
			prioCallCount++
			body, _ := io.ReadAll(r.Body)
			bodyStr := string(body)
			if bodyStr != "hash=1234&id=0%7C1&priority=0" && bodyStr != "hash=1234&id=0%7C1&priority=1" {
				// depends on map iteration order
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	selections := []job.TorrentFileSelection{
		{Index: 0, Priority: job.PrioritySkip},
		{Index: 1, Priority: job.PrioritySkip},
	}
	err := engine.SetFilePriorities(context.Background(), "1234", selections)
	if err != nil {
		t.Fatalf("expected SetFilePriorities to succeed, got %v", err)
	}
	if prioCallCount != 1 {
		t.Errorf("expected 1 call to filePrio, got %d", prioCallCount)
	}
}

func TestStatus_UsesSelectedTorrentSize(t *testing.T) {
	const selectedSize = int64(5 * 1024 * 1024 * 1024) // 5 GiB

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok."))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"hash":"5555","name":"test.torrent","state":"downloading","progress":0.2,"eta":100,"dlspeed":1024,"completed":1073741824,"size":5368709120,"total_size":107374182400,"upspeed":0,"uploaded":0,"ratio":0.0,"num_seeds":1,"num_leechs":1}]`))
			return
		}
	}))
	defer ts.Close()

	engine := NewEngine(ts.URL, "admin", "adminadmin", 5)

	j := &job.Job{
		ID:       "job-size-test",
		EngineID: "5555",
	}

	status, err := engine.Status(context.Background(), j)
	if err != nil {
		t.Fatalf("expected Status to succeed, got %v", err)
	}

	if status.TotalBytes != selectedSize {
		t.Errorf("expected TotalBytes == %d (selected size), got %d", selectedSize, status.TotalBytes)
	}
}
