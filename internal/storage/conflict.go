package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	finalizationMu sync.Mutex
	renameFunc     = os.Rename
)

const WorkDirMarkerFilename = ".godownloader-workdir"

// WriteWorkDirMarker creates or validates the .godownloader-workdir marker inside workDir safely.
func WriteWorkDirMarker(workDir, jobID string) error {
	if workDir == "" {
		return fmt.Errorf("%w: work directory path is empty", ErrStorageError)
	}

	fi, err := os.Stat(workDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return fmt.Errorf("%w: create workdir %s: %v", ErrStorageError, workDir, err)
		}
	} else if err != nil {
		return fmt.Errorf("%w: stat workdir %s: %v", ErrStorageError, workDir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%w: workdir path %s is not a directory", ErrStorageError, workDir)
	} else {
		// WorkDir exists. Check marker or directory contents.
		markerErr := ValidateWorkDirMarker(workDir, jobID)
		if markerErr == nil {
			// Marker matches jobID, okay!
		} else {
			// Marker missing or mismatched. Check if directory contains non-marker files.
			entries, readErr := os.ReadDir(workDir)
			if readErr != nil {
				return fmt.Errorf("%w: read workdir %s: %v", ErrStorageError, workDir, readErr)
			}
			nonMarkerCount := 0
			for _, entry := range entries {
				if entry.Name() != WorkDirMarkerFilename {
					nonMarkerCount++
				}
			}
			if nonMarkerCount > 0 {
				return fmt.Errorf("%w: workdir %s already exists with existing unowned files: %v", ErrStorageError, workDir, markerErr)
			}
		}
	}

	markerPath := filepath.Join(workDir, WorkDirMarkerFilename)
	return os.WriteFile(markerPath, []byte(jobID+"\n"), 0644)
}

// ValidateWorkDirMarker checks that workDir exists and contains a valid .godownloader-workdir marker matching jobID.
func ValidateWorkDirMarker(workDir, jobID string) error {
	if workDir == "" {
		return fmt.Errorf("work directory path is empty")
	}

	markerPath := filepath.Join(workDir, WorkDirMarkerFilename)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return fmt.Errorf("workdir safety marker missing or unreadable at %s: %w", markerPath, err)
	}

	content := strings.TrimSpace(string(data))
	if content != jobID {
		return fmt.Errorf("workdir safety marker job ID mismatch (expected %s, got %s)", jobID, content)
	}

	return nil
}

// SplitFilenameExt splits a filename into base name and extension, supporting multi-part extensions like .tar.gz.
func SplitFilenameExt(filename string) (string, string) {
	lower := strings.ToLower(filename)
	knownMultiExts := []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"}
	for _, ext := range knownMultiExts {
		if strings.HasSuffix(lower, ext) {
			base := filename[:len(filename)-len(ext)]
			return base, filename[len(filename)-len(ext):]
		}
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		return filename, ""
	}
	base := filename[:len(filename)-len(ext)]
	return base, ext
}

// GenerateUniqueFilename generates a non-colliding filename in dstDir if filename already exists.
// Example: video.mp4 -> video (1).mp4 -> video (2).mp4
func GenerateUniqueFilename(dstDir, filename string) string {
	target := filepath.Join(dstDir, filename)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return filename
	}

	base, ext := SplitFilenameExt(filename)
	counter := 1
	for {
		candidate := fmt.Sprintf("%s (%d)%s", base, counter, ext)
		candPath := filepath.Join(dstDir, candidate)
		if _, err := os.Stat(candPath); os.IsNotExist(err) {
			return candidate
		}
		counter++
	}
}

// MoveOrCopyFile moves src to dst, replacing dst safely if it exists (for ConflictPolicyOverwrite),
// with fallback to atomic copy+move for cross-device/filesystem moves or OS rename limitations (e.g. Windows).
func MoveOrCopyFile(src, dst string) error {
	if src == dst {
		return nil
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("mkdir dst dir: %w", err)
	}

	// Ensure dst is not a directory if it exists
	if fi, err := os.Stat(dst); err == nil {
		if fi.IsDir() {
			return fmt.Errorf("%w: target destination %s is a directory", ErrFileConflict, dst)
		}
	}

	// 1. Try fast atomic rename first if dst does NOT exist
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := renameFunc(src, dst); err == nil {
			return nil
		}
	}

	// 2. Safe copy-then-replace with rollback protection
	randBuf := make([]byte, 4)
	rand.Read(randBuf)
	tempPath := dst + ".tmp-" + hex.EncodeToString(randBuf)

	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src file %s: %w", src, err)
	}

	tf, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		sf.Close()
		return fmt.Errorf("create temp file %s: %w", tempPath, err)
	}

	_, copyErr := io.Copy(tf, sf)
	syncErr := tf.Sync()
	closeErr := tf.Close()
	sf.Close()

	if copyErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(tempPath)
		if copyErr != nil {
			return fmt.Errorf("copy file bytes: %w", copyErr)
		}
		if syncErr != nil {
			return fmt.Errorf("sync temp file: %w", syncErr)
		}
		return fmt.Errorf("close temp file: %w", closeErr)
	}

	// 3. Handle replacement if dst exists (using destination-side backup)
	var backupPath string
	hasBackup := false
	if _, err := os.Stat(dst); err == nil {
		backupPath = dst + ".bak-" + hex.EncodeToString(randBuf)
		if err := renameFunc(dst, backupPath); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("backup existing target file %s: %w", dst, err)
		}
		hasBackup = true
	}

	// 4. Rename temp file to final destination
	if err := renameFunc(tempPath, dst); err != nil {
		// Replacement failed! Rollback backup if present
		os.Remove(tempPath)
		if hasBackup {
			_ = renameFunc(backupPath, dst)
		}
		return fmt.Errorf("rename temp file to dst: %w", err)
	}

	// 5. Success! Clean up backup and remove source file
	if hasBackup {
		os.Remove(backupPath)
	}
	os.Remove(src)
	return nil
}
