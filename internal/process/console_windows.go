//go:build windows

package process

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideConsoleWindow makes a console program started by cmd run without a visible console window.
//
// The production desktop host is a GUI-subsystem executable and has no console of its own, so Windows would open a
// new console window for every console child it starts (yt-dlp, FFmpeg, FFprobe, Deno version probes). With
// CREATE_NO_WINDOW the child still receives a console, just without a window; console programs that the child starts
// in turn (for example FFmpeg started by yt-dlp) inherit that hidden console and open no window either.
//
// Existing creation flags are preserved.
func HideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
