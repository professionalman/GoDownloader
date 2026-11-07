//go:build !windows

package toolmanager

import "os"

// syncDir flushes directory metadata so a completed rename survives power loss where the platform supports it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
