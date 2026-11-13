//go:build !windows

package process

import "os/exec"

// HideConsoleWindow is a no-op outside Windows: there is no console window to suppress.
func HideConsoleWindow(cmd *exec.Cmd) {}
