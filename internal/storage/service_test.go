package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"downloader/internal/storage"
)

type fakeFreeSpaceProvider struct {
	freeBytes map[string]int64
	err       error
}

func (f *fakeFreeSpaceProvider) FreeBytes(path string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	cleaned := filepath.Clean(path)
	if val, ok := f.freeBytes[cleaned]; ok {
		return val, nil
	}
	// Fallback for parent folders
	for p, val := range f.freeBytes {
		if filepath.HasPrefix(cleaned, p) {
			return val, nil
		}
	}
	return 10 * 1024 * 1024 * 1024, nil // 10 GiB default
}

func setupTestDB(t *testing.T) (*sql.DB, storage.ICategoryRepository) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	createTable := `
	CREATE TABLE categories (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE,
		directory TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE UNIQUE INDEX idx_categories_name ON categories(name COLLATE NOCASE);
	`
	if _, err := db.Exec(createTable); err != nil {
		t.Fatalf("failed to create categories table: %v", err)
	}

	repo := storage.NewSQLiteCategoryRepository(db)
	return db, repo
}

func TestCategoryRepository_CRUD(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	// 1. Create
	cat1 := &storage.Category{Name: "Video", Directory: "Video"}
	if err := repo.Create(ctx, cat1); err != nil {
		t.Fatalf("failed to create category: %v", err)
	}
	if cat1.ID == "" {
		t.Errorf("expected generated ID, got empty")
	}

	// 2. Duplicate case-insensitive name rejection
	cat2 := &storage.Category{Name: "video", Directory: "Movies"}
	if err := repo.Create(ctx, cat2); err == nil {
		t.Errorf("expected error creating duplicate category name, got nil")
	}

	// 3. GetByID
	fetched, err := repo.GetByID(ctx, cat1.ID)
	if err != nil || fetched == nil {
		t.Fatalf("failed to get category by ID: %v", err)
	}
	if fetched.Name != "Video" || fetched.Directory != "Video" {
		t.Errorf("unexpected category data: %+v", fetched)
	}

	// 4. GetByName
	fetchedByName, err := repo.GetByName(ctx, "VIDEO")
	if err != nil || fetchedByName == nil {
		t.Fatalf("failed to get category by name case-insensitive: %v", err)
	}
	if fetchedByName.ID != cat1.ID {
		t.Errorf("ID mismatch by name lookup")
	}

	// 5. Update
	cat1.Name = "Video & Movies"
	cat1.Directory = "Media/Video"
	if err := repo.Update(ctx, cat1); err != nil {
		t.Fatalf("failed to update category: %v", err)
	}

	updated, _ := repo.GetByID(ctx, cat1.ID)
	if updated.Name != "Video & Movies" || updated.Directory != "Media/Video" {
		t.Errorf("update failed to persist new fields: %+v", updated)
	}

	// 6. List
	cat3 := &storage.Category{Name: "Archives", Directory: "Archives"}
	repo.Create(ctx, cat3)

	list, err := repo.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 categories, got %d (err: %v)", len(list), err)
	}
	if list[0].Name != "Archives" || list[1].Name != "Video & Movies" {
		t.Errorf("expected alphabetical order: %+v", list)
	}

	// 7. Delete
	if err := repo.Delete(ctx, cat1.ID); err != nil {
		t.Fatalf("failed to delete category: %v", err)
	}
	deleted, _ := repo.GetByID(ctx, cat1.ID)
	if deleted != nil {
		t.Errorf("expected deleted category to be nil")
	}
}

func TestStorageService_ResolveDestination(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	tmpDir := t.TempDir()
	defaultDownloads := filepath.Join(tmpDir, "Downloads")
	dataDir := filepath.Join(tmpDir, "data")

	svc := storage.NewStorageService(repo, nil, nil, defaultDownloads, dataDir)

	cat := &storage.Category{Name: "Video", Directory: "Video"}
	repo.Create(ctx, cat)

	absCat := &storage.Category{Name: "Torrents", Directory: filepath.Join(tmpDir, "Torrents")}
	repo.Create(ctx, absCat)

	// Case 1: Default resolution
	res, err := svc.ResolveDestination(ctx, "", "", "", "job_1", false)
	if err != nil {
		t.Fatalf("unexpected error resolving default: %v", err)
	}
	if res.DestinationDir != defaultDownloads {
		t.Errorf("expected %s, got %s", defaultDownloads, res.DestinationDir)
	}
	if res.ConflictPolicy != storage.ConflictPolicyRename {
		t.Errorf("expected default conflict policy rename, got %s", res.ConflictPolicy)
	}

	// Case 2: Relative Category resolution
	resCat, err := svc.ResolveDestination(ctx, cat.ID, "", "", "job_2", false)
	if err != nil {
		t.Fatalf("unexpected error resolving relative category: %v", err)
	}
	expectedCatPath := filepath.Join(defaultDownloads, "Video")
	if resCat.DestinationDir != expectedCatPath {
		t.Errorf("expected %s, got %s", expectedCatPath, resCat.DestinationDir)
	}
	if resCat.CategoryID != cat.ID {
		t.Errorf("expected category ID %s, got %s", cat.ID, resCat.CategoryID)
	}

	// Case 3: Absolute Category resolution
	resAbsCat, err := svc.ResolveDestination(ctx, absCat.ID, "", "", "job_3", false)
	if err != nil {
		t.Fatalf("unexpected error resolving absolute category: %v", err)
	}
	if resAbsCat.DestinationDir != absCat.Directory {
		t.Errorf("expected %s, got %s", absCat.Directory, resAbsCat.DestinationDir)
	}

	// Case 4: Explicit custom destination
	customPath := filepath.Join(tmpDir, "CustomDest")
	resCustom, err := svc.ResolveDestination(ctx, "", customPath, storage.ConflictPolicyOverwrite, "job_4", true)
	if err != nil {
		t.Fatalf("unexpected error resolving custom destination: %v", err)
	}
	if resCustom.DestinationDir != customPath {
		t.Errorf("expected %s, got %s", customPath, resCustom.DestinationDir)
	}
	if resCustom.ConflictPolicy != storage.ConflictPolicyOverwrite {
		t.Errorf("expected overwrite policy, got %s", resCustom.ConflictPolicy)
	}
	expectedWorkDir := filepath.Join(dataDir, "tmp", "job_4")
	if resCustom.WorkDir != expectedWorkDir {
		t.Errorf("expected workdir %s, got %s", expectedWorkDir, resCustom.WorkDir)
	}

	// Case 5: Both categoryId AND custom destination provided -> ErrInvalidStorageSelection
	_, errBoth := svc.ResolveDestination(ctx, cat.ID, customPath, "", "job_5", false)
	if !errors.Is(errBoth, storage.ErrInvalidStorageSelection) {
		t.Errorf("expected ErrInvalidStorageSelection, got %v", errBoth)
	}

	// Case 6: Relative traversal rejection
	_, errTraverse := svc.ResolveDestination(ctx, "", "../EscapedDir", "", "job_6", false)
	if !errors.Is(errTraverse, storage.ErrInvalidDestination) {
		t.Errorf("expected ErrInvalidDestination for traversal, got %v", errTraverse)
	}
}

func TestStorageService_Preflight(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "dest")
	workDir := filepath.Join(tmpDir, "work")

	fakeFree := &fakeFreeSpaceProvider{
		freeBytes: map[string]int64{
			filepath.Clean(destDir): 2 * 1024 * 1024 * 1024, // 2 GiB free
			filepath.Clean(workDir): 500 * 1024 * 1024,      // 500 MiB free
		},
	}

	svc := storage.NewStorageService(repo, nil, fakeFree, destDir, tmpDir)

	// 1. Fits requirement (dest has 2 GiB free, required = 500 MB download + 1 GiB reserve = 1.5 GiB)
	if err := svc.Preflight(ctx, destDir, "", 500*1024*1024, 0); err != nil {
		t.Errorf("expected preflight success for destDir, got %v", err)
	}

	// 2. Fails requirement for dest (dest has 2 GiB free, required = 2 GB download + 1 GiB reserve = 3 GiB)
	errDest := svc.Preflight(ctx, destDir, "", 2*1024*1024*1024, 0)
	if !errors.Is(errDest, storage.ErrInsufficientDiskSpace) {
		t.Errorf("expected ErrInsufficientDiskSpace for destDir, got %v", errDest)
	}

	// 3. Fails requirement for workdir (workdir has 500 MiB free, required = 1 GiB reserve)
	errWork := svc.Preflight(ctx, destDir, workDir, 0, 0)
	if !errors.Is(errWork, storage.ErrInsufficientDiskSpace) {
		t.Errorf("expected ErrInsufficientDiskSpace for workDir, got %v", errWork)
	}
}

func TestStorageService_FinalizeFile_ConflictPolicies(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	destDir := filepath.Join(tmpDir, "dest")
	os.MkdirAll(srcDir, 0755)
	os.MkdirAll(destDir, 0755)

	svc := storage.NewStorageService(repo, nil, nil, destDir, tmpDir)

	// Pre-create existing file in destination
	existingFile := filepath.Join(destDir, "video.mp4")
	os.WriteFile(existingFile, []byte("existing content"), 0644)

	// 1. ConflictPolicyRename
	srcRename := filepath.Join(srcDir, "video.mp4")
	os.WriteFile(srcRename, []byte("new rename content"), 0644)

	finalRename, err := svc.FinalizeFile(ctx, srcRename, destDir, storage.ConflictPolicyRename)
	if err != nil {
		t.Fatalf("unexpected error on finalize rename: %v", err)
	}
	expectedRename := filepath.Join(destDir, "video (1).mp4")
	if finalRename != expectedRename {
		t.Errorf("expected final path %s, got %s", expectedRename, finalRename)
	}
	if data, _ := os.ReadFile(finalRename); string(data) != "new rename content" {
		t.Errorf("new file content mismatch")
	}
	if data, _ := os.ReadFile(existingFile); string(data) != "existing content" {
		t.Errorf("original file was mutated during rename policy")
	}

	// 2. ConflictPolicyOverwrite
	srcOverwrite := filepath.Join(srcDir, "video.mp4")
	os.WriteFile(srcOverwrite, []byte("overwritten content"), 0644)

	finalOverwrite, err := svc.FinalizeFile(ctx, srcOverwrite, destDir, storage.ConflictPolicyOverwrite)
	if err != nil {
		t.Fatalf("unexpected error on finalize overwrite: %v", err)
	}
	if finalOverwrite != existingFile {
		t.Errorf("expected overwrite at %s, got %s", existingFile, finalOverwrite)
	}
	if data, _ := os.ReadFile(existingFile); string(data) != "overwritten content" {
		t.Errorf("file was not overwritten with new content")
	}

	// 3. ConflictPolicyFail
	srcFail := filepath.Join(srcDir, "video.mp4")
	os.WriteFile(srcFail, []byte("fail policy content"), 0644)

	_, errFail := svc.FinalizeFile(ctx, srcFail, destDir, storage.ConflictPolicyFail)
	if !errors.Is(errFail, storage.ErrFileConflict) {
		t.Errorf("expected ErrFileConflict, got %v", errFail)
	}
}

func TestStorageService_WorkDirSafetyMarker(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	tmpDir := t.TempDir()
	svc := storage.NewStorageService(repo, nil, nil, tmpDir, tmpDir)

	jobID := "job_12345"
	workDir := filepath.Join(tmpDir, "workdirs", jobID)

	// 1. Cleanup before marker created -> refused
	os.MkdirAll(workDir, 0755)
	errNoMarker := svc.CleanupWorkDir(ctx, jobID, workDir)
	if errNoMarker == nil {
		t.Errorf("expected error cleaning workdir without marker")
	}

	// 2. Prepare workdir (creates marker)
	if err := svc.PrepareWorkDir(ctx, jobID, workDir); err != nil {
		t.Fatalf("failed to prepare workdir: %v", err)
	}

	// 3. Cleanup with wrong job ID -> refused
	errWrongJob := svc.CleanupWorkDir(ctx, "job_other", workDir)
	if errWrongJob == nil {
		t.Errorf("expected error cleaning workdir with wrong job ID")
	}

	// 4. Cleanup with correct job ID -> succeeds
	if err := svc.CleanupWorkDir(ctx, jobID, workDir); err != nil {
		t.Fatalf("expected successful cleanup with valid marker, got %v", err)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be removed")
	}
}

func TestFinalizeOverwrite_ExistingFile(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	tmpDir := t.TempDir()
	svc := storage.NewStorageService(repo, nil, nil, tmpDir, tmpDir)

	srcDir := filepath.Join(tmpDir, "src")
	destDir := filepath.Join(tmpDir, "dest")
	os.MkdirAll(srcDir, 0755)
	os.MkdirAll(destDir, 0755)

	targetFile := filepath.Join(destDir, "test.txt")
	os.WriteFile(targetFile, []byte("old content"), 0644)

	srcFile := filepath.Join(srcDir, "test.txt")
	os.WriteFile(srcFile, []byte("new overwritten content"), 0644)

	finalPath, err := svc.FinalizeFile(ctx, srcFile, destDir, storage.ConflictPolicyOverwrite)
	if err != nil {
		t.Fatalf("FinalizeFile failed: %v", err)
	}
	if finalPath != targetFile {
		t.Errorf("expected finalPath %s, got %s", targetFile, finalPath)
	}

	data, err := os.ReadFile(targetFile)
	if err != nil || string(data) != "new overwritten content" {
		t.Errorf("expected overwritten content, got %s (err=%v)", string(data), err)
	}
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Errorf("expected source file to be removed after overwrite")
	}
}

func TestFinalizeOverwrite_CrossDeviceFallback(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	targetFile := filepath.Join(destDir, "cross_dev.txt")
	os.WriteFile(targetFile, []byte("original content"), 0644)

	srcFile := filepath.Join(srcDir, "cross_dev.txt")
	os.WriteFile(srcFile, []byte("cross device content"), 0644)

	if err := storage.MoveOrCopyFile(srcFile, targetFile); err != nil {
		t.Fatalf("MoveOrCopyFile failed: %v", err)
	}

	data, err := os.ReadFile(targetFile)
	if err != nil || string(data) != "cross device content" {
		t.Errorf("expected updated content, got %s (err=%v)", string(data), err)
	}
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Errorf("expected source file to be removed after move")
	}
}

func TestCategoryDelete_ClearsJobReference(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE categories (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE,
		directory TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		category_id TEXT NOT NULL DEFAULT '',
		destination_dir TEXT NOT NULL DEFAULT ''
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	repo := storage.NewSQLiteCategoryRepository(db)
	ctx := context.Background()

	cat := &storage.Category{Name: "Video", Directory: "Video"}
	if err := repo.Create(ctx, cat); err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	destDir := "/downloads/Video"
	if _, err := db.Exec(`INSERT INTO jobs (id, category_id, destination_dir) VALUES ('job-1', ?, ?)`, cat.ID, destDir); err != nil {
		t.Fatalf("failed to insert job: %v", err)
	}

	// Delete category
	if err := repo.Delete(ctx, cat.ID); err != nil {
		t.Fatalf("failed to delete category: %v", err)
	}

	// Category must be deleted
	deleted, err := repo.GetByID(ctx, cat.ID)
	if err != nil || deleted != nil {
		t.Errorf("expected category to be deleted, got %v", deleted)
	}

	// Job category_id must be cleared, destination_dir remains unchanged
	var catID, jobDest string
	if err := db.QueryRow(`SELECT category_id, destination_dir FROM jobs WHERE id = 'job-1'`).Scan(&catID, &jobDest); err != nil {
		t.Fatalf("failed to query job: %v", err)
	}

	if catID != "" {
		t.Errorf("expected job category_id to be cleared, got %s", catID)
	}
	if jobDest != destDir {
		t.Errorf("expected job destination_dir %s to remain unchanged, got %s", destDir, jobDest)
	}
}

func TestCategoryDelete_PreservesDestinationSnapshot(t *testing.T) {
	// Re-verify DestinationDir preservation on job after category deletion
	TestCategoryDelete_ClearsJobReference(t)
}
