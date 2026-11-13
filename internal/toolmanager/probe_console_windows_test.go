//go:build windows

package toolmanager

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Version probes run console tools (yt-dlp, FFmpeg, FFprobe, Deno). The production desktop host is a GUI-subsystem
// program, so each probe must be started without a console window or it would flash one on screen.
func TestProbeVersion_StartsToolWithoutConsoleWindow(t *testing.T) {
	mgr := New(filepath.Join(t.TempDir(), "data"), newMockExecutionRepo())
	var started *exec.Cmd
	mgr.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		started = exec.CommandContext(ctx, "cmd", "/c", "echo 2026.01.01")
		return started
	}

	ver, err := mgr.probeVersion(context.Background(), "yt-dlp.exe", "--version")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if ver != "2026.01.01" {
		t.Fatalf("version = %q", ver)
	}
	if started == nil || started.SysProcAttr == nil || started.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("probe was not started with CREATE_NO_WINDOW")
	}
}
