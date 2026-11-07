//go:build windows

package toolmanager

// syncDir is a no-op on Windows: directory handles cannot be flushed through os.File, and NTFS journals the
// rename metadata. File contents are flushed with File.Sync before the rename. This is the practical
// Windows-supported extent; it is not a cross-filesystem power-loss atomicity guarantee.
func syncDir(string) {}
