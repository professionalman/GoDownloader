package ytdlp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// buildFakeYtDlp compiles a cross-platform fake yt-dlp helper binary for testing.
func buildFakeYtDlp(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()

	srcFile := filepath.Join(tempDir, "fake_ytdlp_main.go")
	binName := "fake_ytdlp"
	if runtime.GOOS == "windows" {
		binName = "fake_ytdlp.exe"
	}
	binPath := filepath.Join(tempDir, binName)

	srcCode := `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if recordPath := os.Getenv("FAKE_YTDLP_RECORD_ARGS"); recordPath != "" {
		_ = os.WriteFile(recordPath, []byte(strings.Join(os.Args, "\n")), 0600)
	}

	if os.Getenv("FAKE_YTDLP_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "ERROR: simulated yt-dlp failure")
		os.Exit(1)
	}

	var cookiePath string
	for i, arg := range os.Args {
		if arg == "--cookies" && i+1 < len(os.Args) {
			cookiePath = os.Args[i+1]
			break
		}
	}
	if cookiePath != "" {
		if _, err := os.Stat(cookiePath); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: cookie file %s missing: %v\n", cookiePath, err)
			os.Exit(2)
		}
		if statusPath := os.Getenv("FAKE_YTDLP_COOKIE_CHECK"); statusPath != "" {
			_ = os.WriteFile(statusPath, []byte("COOKIE_EXISTS"), 0600)
		}
	}

	for _, arg := range os.Args {
		if arg == "--dump-json" {
			jsonOutput := ` + "`" + `{"id":"test_vid","title":"Test Video Title","formats":[{"format_id":"18","ext":"mp4","width":640,"height":360,"vcodec":"avc1","acodec":"mp4a","filesize":1000000}]}` + "`" + `
			fmt.Println(jsonOutput)
			os.Exit(0)
		}
	}

	if holdDir := os.Getenv("FAKE_YTDLP_HOLD_DIR"); holdDir != "" {
		readyFile := filepath.Join(holdDir, "ready.txt")
		releaseFile := filepath.Join(holdDir, "release.txt")
		_ = os.WriteFile(readyFile, []byte("READY"), 0600)

		for i := 0; i < 200; i++ {
			if _, err := os.Stat(releaseFile); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	fmt.Println("__GODOWNLOADER_PROGRESS__:50.0%|500000|1000000|1000000|500000|1")
	fmt.Println("__GODOWNLOADER_PROGRESS__:100.0%|1000000|1000000|1000000|1000000|0")
	var outPath string
	for i, arg := range os.Args {
		if arg == "-o" && i+1 < len(os.Args) {
			template := os.Args[i+1]
			outPath = strings.ReplaceAll(template, "%(title)s.%(ext)s", "video.mp4")
			break
		}
	}
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "video.mp4")
	}
	_ = os.WriteFile(outPath, []byte("dummy video content"), 0600)
	fmt.Printf("[Merger] Merging formats into %q\n", outPath)
	os.Exit(0)
}
`
	if err := os.WriteFile(srcFile, []byte(srcCode), 0600); err != nil {
		t.Fatalf("failed to write fake yt-dlp source: %v", err)
	}

	buildCmd := exec.Command("go", "build", "-o", binPath, srcFile)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build fake yt-dlp binary: %v, output: %s", err, string(out))
	}

	return binPath
}

// 21. Real Child Process: Analyze in browser mode passes --cookies-from-browser
func TestEngine_Analyze_BrowserAuth_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	mockAuth := &mockAuthProvider{
		args: []string{"--cookies-from-browser", "chrome"},
	}

	eng := NewEngine(binPath, t.TempDir())
	eng.SetAuthProvider(mockAuth)

	info, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err != nil {
		t.Fatalf("unexpected Analyze error: %v", err)
	}
	if info == nil || info.Title != "Test Video Title" {
		t.Fatalf("unexpected info returned: %+v", info)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	args := strings.Split(string(data), "\n")
	if !slices.Contains(args, "--cookies-from-browser") || !slices.Contains(args, "chrome") {
		t.Fatalf("child process did not receive expected browser auth args: %v", args)
	}
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to be called after analyze")
	}
}

// 22, 23, 24. Real Child Process: Analyze in cookie-file mode verifies temp file presence during run and cleanup after
func TestEngine_Analyze_CookieAuth_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "args.txt")
	cookieCheckFile := filepath.Join(tempDir, "cookie_checked.txt")
	cookieFile := filepath.Join(tempDir, "test_cookie.txt")

	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)
	t.Setenv("FAKE_YTDLP_COOKIE_CHECK", cookieCheckFile)

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	info, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err != nil {
		t.Fatalf("unexpected Analyze error: %v", err)
	}
	if info == nil {
		t.Fatalf("expected non-nil media info")
	}

	// 1. Verify child process saw the cookie file
	checkContent, err := os.ReadFile(cookieCheckFile)
	if err != nil || string(checkContent) != "COOKIE_EXISTS" {
		t.Fatalf("child process failed to find cookie file during execution: %v", err)
	}

	// 2. Verify temp cookie file is removed after Analyze exits
	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp cookie file to be deleted after Analyze, but it still exists")
	}
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to run")
	}
}

// 25. Real Child Process: Analyze failure still cleans up temp cookie
func TestEngine_Analyze_CookieAuth_FailureCleanup_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	cookieFile := filepath.Join(tempDir, "fail_cookie.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_FAIL", "1")

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	_, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err == nil {
		t.Fatalf("expected Analyze error when child exits with failure")
	}

	// Assert temp cookie file is cleaned up despite failure
	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp cookie file to be deleted on failure, but it still exists")
	}
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to run on failure")
	}
}

// 26. Real Child Process: Download in browser mode receives browser args
func TestEngine_Download_BrowserAuth_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "args_dl.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	mockAuth := &mockAuthProvider{
		args: []string{"--cookies-from-browser", "edge:Profile1"},
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "dl_browser_job",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	workDir := filepath.Join(tempDir, "work")
	_ = os.MkdirAll(workDir, 0700)

	_, err := eng.Start(context.Background(), j, workDir)
	if err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	// Wait for completion
	for i := 0; i < 300; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args: %v", err)
	}
	args := strings.Split(string(data), "\n")
	if !slices.Contains(args, "--cookies-from-browser") || !slices.Contains(args, "edge:Profile1") {
		t.Fatalf("child process missing browser auth args: %v", args)
	}
}

// 27, 28, 29. Real Child Process: Download in cookie mode keeps temp cookie while running, deletes on completion
func TestEngine_Download_CookieAuth_Lifecycle_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	holdDir := filepath.Join(tempDir, "hold")
	_ = os.MkdirAll(holdDir, 0700)

	cookieFile := filepath.Join(tempDir, "dl_cookie.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_HOLD_DIR", holdDir)

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "dl_lifecycle_job",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	workDir := filepath.Join(tempDir, "work")
	_ = os.MkdirAll(workDir, 0700)

	_, err := eng.Start(context.Background(), j, workDir)
	if err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	// 1. Wait for child to start and signal ready
	readyFile := filepath.Join(holdDir, "ready.txt")
	for i := 0; i < 300; i++ {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("child process failed to signal ready: %v", err)
	}

	// 2. Assert cookie file EXISTS while child is running
	if _, err := os.Stat(cookieFile); err != nil {
		t.Fatalf("temp cookie file must exist while child process is executing: %v", err)
	}

	// 3. Release the child to let it finish
	releaseFile := filepath.Join(holdDir, "release.txt")
	if err := os.WriteFile(releaseFile, []byte("RELEASE"), 0600); err != nil {
		t.Fatal(err)
	}

	// 4. Wait for download completion
	for i := 0; i < 300; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 5. Assert cookie file is now DELETED
	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp cookie file to be deleted after download completion, but it still exists")
	}
	if !mockAuth.cleanupRan.Load() {
		t.Fatalf("expected cleanup to be called after download completed")
	}
}

// 30. Real Child Process: Download failure cleans up temp cookie
func TestEngine_Download_CookieAuth_FailureCleanup_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	cookieFile := filepath.Join(tempDir, "dl_fail_cookie.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_FAIL", "1")

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "dl_fail_job",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	workDir := filepath.Join(tempDir, "work")
	_ = os.MkdirAll(workDir, 0700)

	_, err := eng.Start(context.Background(), j, workDir)
	if err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	// Wait for cleanup
	for i := 0; i < 300; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp cookie file to be deleted on download failure, but it still exists")
	}
}

// 31. Real Child Process: Download cancellation cleans up temp cookie
func TestEngine_Download_CookieAuth_CancellationCleanup_RealChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	holdDir := filepath.Join(tempDir, "hold_cancel")
	_ = os.MkdirAll(holdDir, 0700)

	cookieFile := filepath.Join(tempDir, "dl_cancel_cookie.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_YTDLP_HOLD_DIR", holdDir)

	mockAuth := &mockAuthProvider{
		args:     []string{"--cookies", cookieFile},
		tempFile: cookieFile,
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	j := &job.Job{
		ID:     "dl_cancel_job",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}

	workDir := filepath.Join(tempDir, "work")
	_ = os.MkdirAll(workDir, 0700)

	_, err := eng.Start(context.Background(), j, workDir)
	if err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	// Wait for child to be running
	readyFile := filepath.Join(holdDir, "ready.txt")
	for i := 0; i < 300; i++ {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Cancel the active download
	if err := eng.Cancel(context.Background(), j); err != nil {
		t.Fatalf("failed to cancel job: %v", err)
	}

	// Wait for cleanup
	for i := 0; i < 300; i++ {
		if mockAuth.cleanupRan.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp cookie file to be deleted on cancellation, but it still exists")
	}
}

// 32. Real Child Process: Preparation failure does NOT start child process
func TestEngine_AuthPreparationFailure_DoesNotStartChild(t *testing.T) {
	binPath := buildFakeYtDlp(t)
	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "args_should_not_exist.txt")
	t.Setenv("FAKE_YTDLP_RECORD_ARGS", argsFile)

	mockAuth := &mockAuthProvider{
		prepErr: errors.New("auth decryption failed"),
	}

	eng := NewEngine(binPath, tempDir)
	eng.SetAuthProvider(mockAuth)

	// 1. Analyze
	_, err := eng.Analyze(context.Background(), "https://example.com/video")
	if err == nil || !strings.Contains(err.Error(), "auth decryption failed") {
		t.Fatalf("expected auth preparation error in Analyze, got %v", err)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatalf("child process was executed despite Analyze auth preparation failure")
	}

	// 2. Start
	j := &job.Job{
		ID:     "test_job_prep_fail",
		Source: "https://example.com/video",
		Type:   job.TypeMedia,
	}
	_, err = eng.Start(context.Background(), j, tempDir)
	if err == nil || !strings.Contains(err.Error(), "auth decryption failed") {
		t.Fatalf("expected auth preparation error in Start, got %v", err)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatalf("child process was executed despite Start auth preparation failure")
	}
}
