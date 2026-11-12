package storage_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"downloader/internal/storage"
)

// D-1 safety: startup cleanup of stale work directories deletes only marked, inactive, direct child directories of
// the temp root. Unknown entries, active jobs, and links that point outside the temp root (symlinks, Windows
// junctions) are never followed or deleted.
func TestCleanupStaleWorkDirs_RejectsEscapesAndUnknownEntries(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	dataDir := t.TempDir()
	svc := storage.NewStorageService(repo, nil, nil, dataDir, dataDir)
	tmpRoot := filepath.Join(dataDir, "tmp")

	mustMarked := func(dir, jobID string) {
		t.Helper()
		if err := storage.WriteWorkDirMarker(dir, jobID); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload.part"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stale := filepath.Join(tmpRoot, "job-stale")
	mustMarked(stale, "job-stale")
	active := filepath.Join(tmpRoot, "job-active")
	mustMarked(active, "job-active")
	unmarked := filepath.Join(tmpRoot, "user-folder")
	if err := os.MkdirAll(unmarked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmarked, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	looseFile := filepath.Join(tmpRoot, "loose.txt")
	if err := os.WriteFile(looseFile, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Outside target that looks like a stale GoDownloader work directory.
	outside := filepath.Join(t.TempDir(), "outside-victim")
	mustMarked(outside, "job-victim")

	var links []string
	if link := filepath.Join(tmpRoot, "escape-symlink"); os.Symlink(outside, link) == nil {
		links = append(links, link)
	} else {
		t.Log("symlink creation not permitted here; symlink case skipped")
	}
	if runtime.GOOS == "windows" {
		junction := filepath.Join(tmpRoot, "escape-junction")
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err == nil {
			links = append(links, junction)
		} else {
			t.Logf("junction creation failed (%v: %s); junction case skipped", err, out)
		}
	}
	if len(links) == 0 {
		t.Skip("no link type could be created; escape cases not exercised")
	}

	if err := svc.CleanupStaleWorkDirs(ctx, map[string]bool{"job-active": true}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale marked work dir not removed (err=%v)", err)
	}
	for _, keep := range []string{
		filepath.Join(active, "payload.part"),
		filepath.Join(unmarked, "notes.txt"),
		looseFile,
		filepath.Join(outside, "payload.part"),
		filepath.Join(outside, storage.WorkDirMarkerFilename),
	} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("cleanup removed %s, which it does not own: %v", keep, err)
		}
	}
	for _, link := range links {
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("link %s itself was removed: %v", filepath.Base(link), err)
		}
	}
}
