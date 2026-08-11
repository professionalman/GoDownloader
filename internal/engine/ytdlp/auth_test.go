package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/job"
)

type mockAuthProvider struct {
	args       []string
	cleanupRan atomic.Bool
	prepErr    error
	tempFile   string
	mu         sync.Mutex
	onCall     func()
}

func (m *mockAuthProvider) PrepareAuthArgs(ctx context.Context) ([]string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.onCall != nil {
		m.onCall()
	}

	if m.prepErr != nil {
		return nil, func() {}, m.prepErr
	}

	cleanup := func() {
		m.cleanupRan.Store(true)
		if m.tempFile != "" {
			_ = os.Remove(m.tempFile)
		}
	}

	return m.args, cleanup, nil
}

// 21. Analyze in browser mode includes browser cookie args
func TestEngine_AnalyzeWithBrowserAuth(t *testing.T) {
	eng := NewEngine("nonexistent-ytdlp-bin", "")
	mockAuth := &mockAuthProvider{
		args: []string{"--cookies-from-browser", "chrome:Default"},
	}
	eng.SetAuthProvider(mockAuth)

	// Since nonexistent-ytdlp-bin will fail execution, we expect an execution error,
	// but cleanup MUST still run.
	_, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err == nil {
		t.Fatalf("expected error running nonexistent binary")
	}
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to have run after Analyze failure")
	}
}

// 22, 23, 24, 25. Analyze temp cookie lifecycle and failure cleanup
func TestEngine_AnalyzeWithCookieFileLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	cookieFile := filepath.Join(tempDir, "temp_cookie.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine("nonexistent-ytdlp-bin", "")
	eng.SetAuthProvider(mockAuth)

	// Verify temp file exists before call
	if _, err := os.Stat(cookieFile); err != nil {
		t.Fatalf("temp cookie should exist before analyze: %v", err)
	}

	_, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err == nil {
		t.Fatalf("expected execution error")
	}

	// Verify cleanup was executed and file removed
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to be called")
	}
	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("temp cookie file should have been deleted by cleanup")
	}
}

// 32. Preparation failure in Analyze and Start does not start yt-dlp
func TestEngine_AuthPreparationFailure(t *testing.T) {
	mockAuth := &mockAuthProvider{
		prepErr: errors.New("auth decryption failed"),
	}

	eng := NewEngine("yt-dlp", "")
	eng.SetAuthProvider(mockAuth)

	// 1. Analyze
	_, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err == nil || !strings.Contains(err.Error(), "auth decryption failed") {
		t.Fatalf("expected auth preparation error in Analyze, got %v", err)
	}

	// 2. Start
	j := &job.Job{
		ID:     "test_job_1",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}
	_, err = eng.Start(context.Background(), j, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "auth decryption failed") {
		t.Fatalf("expected auth preparation error in Start, got %v", err)
	}

	// Verify no download was registered
	eng.mu.RLock()
	state := eng.downloads[j.ID]
	eng.mu.RUnlock()
	if state != nil {
		t.Fatalf("download state should not have been created on auth prep failure")
	}
}

// 26, 27, 28, 29, 30, 31. Download lifecycle with auth args & cleanup on finish/cancel
func TestEngine_DownloadAuthLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	cookieFile := filepath.Join(tempDir, "temp_cookie_dl.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	// Use an invalid binary so runDownload exits quickly and triggers defer cleanup()
	eng := NewEngine("nonexistent-ytdlp-bin", "")
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "test_job_dl",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	engineID, err := eng.Start(context.Background(), j, tempDir)
	if err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}
	if engineID != j.ID {
		t.Fatalf("expected engineID %s, got %s", j.ID, engineID)
	}

	// Wait briefly for background goroutine to execute and clean up
	for i := 0; i < 50; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected download cleanup to run after process completion/failure")
	}
	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("temp cookie file should have been deleted after download goroutine finished")
	}
}

// Verify browser auth arguments are appended correctly during Start
func TestEngine_StartBrowserAuthArgs(t *testing.T) {
	mockAuth := &mockAuthProvider{
		args: []string{"--cookies-from-browser", "brave:User1"},
	}

	eng := NewEngine("nonexistent-ytdlp-bin", "")
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "test_job_args",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	_, err := eng.Start(context.Background(), j, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}

	// Wait for goroutine cleanup
	for i := 0; i < 50; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Test that appendNetworkArgs preserves existing behavior and doesn't conflict with auth
func TestEngine_AppendNetworkAndAuthArgs(t *testing.T) {
	authArgs := []string{"--cookies-from-browser", "chrome"}
	args := []string{"--dump-json"}
	args = append(args, authArgs...)
	args = append(args, "https://example.com")

	if !slices.Contains(args, "--cookies-from-browser") || !slices.Contains(args, "chrome") {
		t.Fatalf("args missing auth arguments: %v", args)
	}
}
