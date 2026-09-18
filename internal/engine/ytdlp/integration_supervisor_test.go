package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/job"
	"downloader/internal/process"
)

// TestYtdlpEngine_ProcessSupervisorIntegration proves that long-running download commands
// flow through ProcessSupervisor and that job cancellation terminates the supervised process tree.
func TestYtdlpEngine_ProcessSupervisorIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake yt-dlp batch/cmd file on Windows that sleeps
	fakeYtdlp := filepath.Join(tmpDir, "fake_ytdlp.cmd")
	script := "@echo off\r\nping -n 30 127.0.0.1 > NUL\r\n"
	if err := os.WriteFile(fakeYtdlp, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake yt-dlp: %v", err)
	}

	supervisor := process.NewSupervisor()
	defer supervisor.Shutdown(context.Background())

	eng := NewEngine(fakeYtdlp, "")
	eng.SetProcessSupervisor(supervisor)

	j := &job.Job{
		ID:     "test-job-proc-sup",
		Source: "https://example.com/video",
		Engine: "ytdlp",
		Status: job.StatusDownloading,
	}

	downloadDir := filepath.Join(tmpDir, "downloads")
	_ = os.MkdirAll(downloadDir, 0755)

	_, err := eng.Start(context.Background(), j, downloadDir)
	if err != nil {
		t.Fatalf("eng.Start failed: %v", err)
	}

	// Wait up to 2 seconds for the process to be spawned and registered in supervisor
	deadline := time.Now().Add(2 * time.Second)
	var procInfo *process.ProcessInfo
	for time.Now().Before(deadline) {
		procInfo = supervisor.Get(j.ID)
		if procInfo != nil && procInfo.PID > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if procInfo == nil {
		t.Fatalf("process was not registered in ProcessSupervisor")
	}
	if procInfo.Tool != "yt-dlp" {
		t.Errorf("expected tool yt-dlp, got %s", procInfo.Tool)
	}
	if procInfo.Purpose != "download" {
		t.Errorf("expected purpose download, got %s", procInfo.Purpose)
	}
	if supervisor.ActiveCount() != 1 {
		t.Errorf("expected active count 1, got %d", supervisor.ActiveCount())
	}

	// Verify cancellation triggers process-tree termination
	if err := eng.Cancel(context.Background(), j); err != nil {
		t.Fatalf("eng.Cancel failed: %v", err)
	}

	// Wait for process to exit and unregister
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.ActiveCount() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if supervisor.ActiveCount() != 0 {
		t.Errorf("expected active count 0 after cancellation, got %d", supervisor.ActiveCount())
	}
}

// TestYtdlpEngine_ProcessSupervisor_Shutdown proves that calling Engine.Shutdown
// terminates active supervised processes.
func TestYtdlpEngine_ProcessSupervisor_Shutdown(t *testing.T) {
	tmpDir := t.TempDir()

	fakeYtdlp := filepath.Join(tmpDir, "fake_ytdlp_shutdown.cmd")
	script := "@echo off\r\nping -n 30 127.0.0.1 > NUL\r\n"
	if err := os.WriteFile(fakeYtdlp, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake yt-dlp: %v", err)
	}

	supervisor := process.NewSupervisor()
	defer supervisor.Shutdown(context.Background())

	eng := NewEngine(fakeYtdlp, "")
	eng.SetProcessSupervisor(supervisor)

	j := &job.Job{
		ID:     "test-job-shutdown",
		Source: "https://example.com/video2",
		Engine: "ytdlp",
		Status: job.StatusDownloading,
	}

	downloadDir := filepath.Join(tmpDir, "downloads")
	_ = os.MkdirAll(downloadDir, 0755)

	_, err := eng.Start(context.Background(), j, downloadDir)
	if err != nil {
		t.Fatalf("eng.Start failed: %v", err)
	}

	// Wait for process to appear in supervisor
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.ActiveCount() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if supervisor.ActiveCount() != 1 {
		t.Fatalf("expected 1 active process in supervisor before shutdown")
	}

	// Shutdown engine
	eng.Shutdown()

	// Wait for supervisor to be clean
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.ActiveCount() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if supervisor.ActiveCount() != 0 {
		t.Errorf("expected 0 active processes after Engine.Shutdown(), got %d", supervisor.ActiveCount())
	}
}
