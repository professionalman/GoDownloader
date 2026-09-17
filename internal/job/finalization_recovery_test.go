package job_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
	"downloader/internal/storage"

	"github.com/google/uuid"
)

func setupFinalizationTest(t *testing.T) (*job.Manager, *database.SQLiteJobRepository, *database.SQLiteExecutionRepository, *storage.StorageService, string, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_finalization.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	storageSvc := storage.NewStorageService(nil, nil, nil, tempDir, tempDir)

	downloadDir := filepath.Join(tempDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		t.Fatalf("failed to create downloadDir: %v", err)
	}

	bus := &fakeBus{}
	engReg := &fakeRegistry{eng: &fakeEngine{}}
	mgr := job.NewManager(jobRepo, engReg, bus, downloadDir, nil, tempDir)
	mgr.SetStorageService(storageSvc)
	mgr.SetExecutionRepository(execRepo)

	return mgr, jobRepo, execRepo, storageSvc, downloadDir, tempDir
}

func setupWorkDirWithFile(t *testing.T, baseDir, jobID, filename string, content []byte) (string, string) {
	t.Helper()
	workDir := filepath.Join(baseDir, "workdirs", jobID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create workDir: %v", err)
	}
	// Write workdir safety marker
	markerPath := filepath.Join(workDir, storage.WorkDirMarkerFilename)
	if err := os.WriteFile(markerPath, []byte(jobID+"\n"), 0644); err != nil {
		t.Fatalf("failed to write workdir marker: %v", err)
	}

	srcFile := filepath.Join(workDir, filename)
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatalf("failed to write staging file: %v", err)
	}
	return workDir, srcFile
}

func TestFinalization_HappyPath(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "happy-media-1"
	payload := []byte("happy-path-test-content-1234567890")
	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "test_video.mp4", payload)

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "test_video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	status := &job.EngineStatus{
		Status:     job.StatusCompleted,
		OutputPath: srcFile,
		FileName:   "test_video.mp4",
	}

	mgr.UpdateJobFromEngine(ctx, j, status, true)

	// Check job in DB
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get saved job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s (error: %s)", savedJob.Status, savedJob.Error)
	}
	if savedJob.Progress != 100 {
		t.Errorf("expected Progress 100, got %f", savedJob.Progress)
	}
	if savedJob.TotalBytes != int64(len(payload)) {
		t.Errorf("expected TotalBytes %d, got %d", len(payload), savedJob.TotalBytes)
	}
	if savedJob.CompletedBytes != int64(len(payload)) {
		t.Errorf("expected CompletedBytes %d, got %d", len(payload), savedJob.CompletedBytes)
	}

	// Check destination file
	if _, err := os.Stat(savedJob.FinalPath); err != nil {
		t.Errorf("destination file missing at %s: %v", savedJob.FinalPath, err)
	}

	// Check workDir cleaned up
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be cleaned up, but it still exists at %s", workDir)
	}

	// Check finalization journal
	rec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get finalization record: %v", err)
	}
	if rec == nil {
		t.Fatalf("expected finalization record, got nil")
	}
	if rec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", rec.Phase)
	}
	if rec.CompletedAt == nil {
		t.Error("expected CompletedAt to be non-nil")
	}
	if rec.Error != "" {
		t.Errorf("expected empty error, got %s", rec.Error)
	}
}

func TestRecovery_CrashAtPrepared_SourceIntact(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-prepared-1"
	payload := []byte("prepared-recovery-payload-bytes")
	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", payload)
	destPath := filepath.Join(downloadDir, "video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyOverwrite),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(payload)),
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save finalization record: %v", err)
	}

	// Verify destination does not exist before recovery
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("expected destination file not to exist yet")
	}

	// Run recovery
	mgr.Recover(ctx)

	// Verify destination file now exists and is valid
	destInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("destination file not found after recovery: %v", err)
	}
	if destInfo.Size() != int64(len(payload)) {
		t.Errorf("expected size %d, got %d", len(payload), destInfo.Size())
	}

	// Verify job in DB updated to StatusCompleted
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s (error: %s)", savedJob.Status, savedJob.Error)
	}

	// Verify workDir removed
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be cleaned up, but it still exists")
	}

	// Verify record Complete
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_CrashAtInProgress_DestinationValid(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-inprogress-valid-1"
	payload := []byte("completed-file-content-already-at-dest")
	destPath := filepath.Join(downloadDir, "valid_video.mp4")
	if err := os.WriteFile(destPath, payload, 0644); err != nil {
		t.Fatalf("failed to write destination file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	_ = os.MkdirAll(workDir, 0755)
	_ = os.WriteFile(filepath.Join(workDir, storage.WorkDirMarkerFilename), []byte(jobID+"\n"), 0644)
	srcFile := filepath.Join(workDir, "valid_video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/valid_video",
		Name:           "valid_video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(payload)),
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save finalization record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Verify destination file remains intact
	destInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("destination file missing: %v", err)
	}
	if destInfo.Size() != int64(len(payload)) {
		t.Errorf("expected destination size %d, got %d", len(payload), destInfo.Size())
	}

	// Verify job in DB updated to StatusCompleted
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", savedJob.Status)
	}
	if savedJob.FinalPath != destPath {
		t.Errorf("expected FinalPath %s, got %s", destPath, savedJob.FinalPath)
	}

	// Verify workDir removed
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be cleaned up, but it still exists")
	}

	// Verify record Complete
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_CrashAtInProgress_DestinationInvalid_SourceIntact(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-inprogress-resume-1"
	validPayload := []byte("valid-full-payload-from-staging-3000-bytes")
	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", validPayload)

	// Destination has partial/corrupted content (e.g. 3 bytes)
	destPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(destPath, []byte("bad"), 0644); err != nil {
		t.Fatalf("failed to write partial dest file: %v", err)
	}

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyOverwrite),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(validPayload)),
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save finalization record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Verify destination file now has the valid payload
	destInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("destination file missing: %v", err)
	}
	if destInfo.Size() != int64(len(validPayload)) {
		t.Errorf("expected size %d, got %d", len(validPayload), destInfo.Size())
	}

	// Verify job is StatusCompleted
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", savedJob.Status)
	}

	// Verify record Complete
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_CrashAtInProgress_DestinationInvalid_SourceMissing_PreservesDestination(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-inprogress-ambig-1"
	// Destination has an unknown user file of 43 bytes
	userPayload := []byte("important-user-existing-file-do-not-delete!")
	destPath := filepath.Join(downloadDir, "ambiguous_video.mp4")
	if err := os.WriteFile(destPath, userPayload, 0644); err != nil {
		t.Fatalf("failed to write dest file: %v", err)
	}

	// Staging file does NOT exist
	workDir := filepath.Join(tempDir, "workdirs", jobID)
	srcFile := filepath.Join(workDir, "ambiguous_video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/ambiguous_video",
		Name:           "ambiguous_video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    1000000, // Expected 1MB, but file is small
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save finalization record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// INVARIANT: Destination file MUST NOT BE DELETED OR OVERWRITTEN!
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("destination file was deleted or cannot be read: %v", err)
	}
	if string(content) != string(userPayload) {
		t.Errorf("destination file content was modified! Expected %q, got %q", string(userPayload), string(content))
	}

	// Verify job is StatusFailed
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Errorf("expected StatusFailed, got %s", savedJob.Status)
	}

	// Verify record is Failed
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseFailed {
		t.Errorf("expected phase Failed, got %s", updatedRec.Phase)
	}
}

func TestRecovery_CrashAtValidated(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-validated-1"
	payload := []byte("destination-already-validated-content")
	destPath := filepath.Join(downloadDir, "validated_video.mp4")
	if err := os.WriteFile(destPath, payload, 0644); err != nil {
		t.Fatalf("failed to write dest file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	_ = os.MkdirAll(workDir, 0755)
	_ = os.WriteFile(filepath.Join(workDir, storage.WorkDirMarkerFilename), []byte(jobID+"\n"), 0644)
	srcFile := filepath.Join(workDir, "validated_video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/validated_video",
		Name:           "validated_video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(payload)),
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseValidated,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Check job is StatusCompleted
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", savedJob.Status)
	}

	// Check workDir cleaned up
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be cleaned up")
	}

	// Check record is Complete
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_CrashAtDBCommitted_CleanupPending(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-cleanup-pending-1"
	payload := []byte("db-already-committed-content")
	destPath := filepath.Join(downloadDir, "committed_video.mp4")
	if err := os.WriteFile(destPath, payload, 0644); err != nil {
		t.Fatalf("failed to write dest file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	_ = os.MkdirAll(workDir, 0755)
	_ = os.WriteFile(filepath.Join(workDir, storage.WorkDirMarkerFilename), []byte(jobID+"\n"), 0644)
	srcFile := filepath.Join(workDir, "committed_video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/committed_video",
		Name:           "committed_video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusCompleted,
		FinalPath:      destPath,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(payload)),
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseDBCommitted,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Check workDir cleaned up
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be cleaned up, but it still exists")
	}

	// Check record is Complete
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Errorf("expected phase Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_IdempotentRepetition(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-idempotent-1"
	payload := []byte("idempotent-recovery-payload")
	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", payload)
	destPath := filepath.Join(downloadDir, "video.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyOverwrite),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(payload)),
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// First recovery run
	mgr.Recover(ctx)

	savedJob1, _ := jobRepo.GetByID(ctx, jobID)
	if savedJob1.Status != job.StatusCompleted {
		t.Fatalf("first recovery failed: expected StatusCompleted, got %s", savedJob1.Status)
	}

	// Second recovery run immediately after
	mgr.Recover(ctx)

	savedJob2, _ := jobRepo.GetByID(ctx, jobID)
	if savedJob2.Status != job.StatusCompleted {
		t.Fatalf("second recovery failed: expected StatusCompleted, got %s", savedJob2.Status)
	}

	// Third recovery run
	mgr.Recover(ctx)

	destInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("destination missing after multiple recovery runs: %v", err)
	}
	if destInfo.Size() != int64(len(payload)) {
		t.Errorf("size mismatch: expected %d, got %d", len(payload), destInfo.Size())
	}
}

func TestRecovery_SubtitleSidecarPromotion(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-subtitles-1"
	videoPayload := []byte("video-data-bytes-for-subtitle-test")
	subPayload := []byte("1\n00:00:01,000 --> 00:00:04,000\nHello World\n")

	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "media.mp4", videoPayload)
	// Write subtitle sidecar in workdir matching primary base name
	subFile := filepath.Join(workDir, "media.en.srt")
	if err := os.WriteFile(subFile, subPayload, 0644); err != nil {
		t.Fatalf("failed to write subtitle file: %v", err)
	}

	destPath := filepath.Join(downloadDir, "media.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/media",
		Name:           "media.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyOverwrite),
		MediaInfo: &job.MediaInfo{
			SubtitleOptions: &job.SubtitleOptions{
				Languages: []string{"en"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    int64(len(videoPayload)),
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Check video was promoted
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("promoted video missing: %v", err)
	}

	// Check subtitle sidecar was promoted to destination directory
	promotedSub := filepath.Join(downloadDir, "media.en.srt")
	subData, err := os.ReadFile(promotedSub)
	if err != nil {
		t.Errorf("promoted subtitle sidecar missing: %v", err)
	} else if string(subData) != string(subPayload) {
		t.Errorf("promoted subtitle content mismatch: got %q, expected %q", string(subData), string(subPayload))
	}

	// Check workDir cleaned up
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected workDir to be removed")
	}

	// Check job completed
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", savedJob.Status)
	}
}

func TestRecovery_MissingStagingInPrepared(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-missing-prep-1"
	workDir := filepath.Join(tempDir, "workdirs", jobID)
	srcFile := filepath.Join(workDir, "missing.mp4")
	destPath := filepath.Join(downloadDir, "missing.mp4")

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/missing",
		Name:           "missing.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyOverwrite),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: destPath,
		ExpectedSize:    1024,
		ConflictPolicy:  string(storage.ConflictPolicyOverwrite),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Verify record failed
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseFailed {
		t.Errorf("expected phase Failed, got %s", updatedRec.Phase)
	}

	// Verify job failed
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Errorf("expected StatusFailed, got %s", savedJob.Status)
	}
}

func TestRecovery_SameSizeUnrelatedDestination_PreparedPhase(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-samesize-prep-1"
	userContent := []byte("user-file-content-32-bytes-long!")
	downContent := []byte("real-down-content-32-bytes-long!")
	expectedSize := int64(len(downContent))

	// Pre-existing user file in downloadDir
	userDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(userDstPath, userContent, 0644); err != nil {
		t.Fatalf("failed to write pre-existing user file: %v", err)
	}

	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", downContent)

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: userDstPath,
		ExpectedSize:    expectedSize,
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// 1. Verify user's file is untouched
	uData, err := os.ReadFile(userDstPath)
	if err != nil {
		t.Fatalf("failed to read user destination file: %v", err)
	}
	if string(uData) != string(userContent) {
		t.Fatalf("user destination file was corrupted or overwritten! got %q, expected %q", string(uData), string(userContent))
	}

	// 2. Verify download artifact was promoted under rename to video (1).mp4
	expectedFinalPath := filepath.Join(downloadDir, "video (1).mp4")
	dData, err := os.ReadFile(expectedFinalPath)
	if err != nil {
		t.Fatalf("expected renamed artifact at %s: %v", expectedFinalPath, err)
	}
	if string(dData) != string(downContent) {
		t.Fatalf("renamed artifact content mismatch: got %q, expected %q", string(dData), string(downContent))
	}

	// 3. Verify job completed pointing to renamed artifact
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %s (err: %s)", savedJob.Status, savedJob.Error)
	}
	if savedJob.FinalPath != expectedFinalPath {
		t.Fatalf("expected FinalPath %s, got %s", expectedFinalPath, savedJob.FinalPath)
	}

	// 4. Verify record completed
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Fatalf("expected record Complete, got %s", updatedRec.Phase)
	}
}

func TestRecovery_SameSizeUnrelatedDestination_InProgressPhase(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-samesize-inp-1"
	userContent := []byte("user-file-content-32-bytes-long!")
	downContent := []byte("real-down-content-32-bytes-long!")
	expectedSize := int64(len(downContent))

	// Pre-existing user file in downloadDir
	userDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(userDstPath, userContent, 0644); err != nil {
		t.Fatalf("failed to write pre-existing user file: %v", err)
	}

	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", downContent)

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: userDstPath,
		ExpectedSize:    expectedSize,
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// 1. Verify user's file is untouched
	uData, err := os.ReadFile(userDstPath)
	if err != nil {
		t.Fatalf("failed to read user destination file: %v", err)
	}
	if string(uData) != string(userContent) {
		t.Fatalf("user destination file was corrupted or overwritten! got %q, expected %q", string(uData), string(userContent))
	}

	// 2. Verify download artifact was promoted under rename to video (1).mp4
	expectedFinalPath := filepath.Join(downloadDir, "video (1).mp4")
	dData, err := os.ReadFile(expectedFinalPath)
	if err != nil {
		t.Fatalf("expected renamed artifact at %s: %v", expectedFinalPath, err)
	}
	if string(dData) != string(downContent) {
		t.Fatalf("renamed artifact content mismatch: got %q, expected %q", string(dData), string(downContent))
	}

	// 3. Verify job completed pointing to renamed artifact
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %s", savedJob.Status)
	}
	if savedJob.FinalPath != expectedFinalPath {
		t.Fatalf("expected FinalPath %s, got %s", expectedFinalPath, savedJob.FinalPath)
	}
}

func TestRecovery_CollisionRename_CrashBeforeJournalUpdate(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-crash-before-jupdate-1"
	userContent := []byte("user-file-different-content")
	downContent := []byte("downloaded-file-content-here-50-bytes-length-now!!")
	expectedSize := int64(len(downContent))

	// Pre-existing user file at video.mp4
	userDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(userDstPath, userContent, 0644); err != nil {
		t.Fatalf("failed to write user file: %v", err)
	}

	// Staging was already moved to video (1).mp4 before crash
	renamedDstPath := filepath.Join(downloadDir, "video (1).mp4")
	if err := os.WriteFile(renamedDstPath, downContent, 0644); err != nil {
		t.Fatalf("failed to write renamed dst file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(workDir, storage.WorkDirMarkerFilename), []byte(jobID+"\n"), 0644)
	srcFile := filepath.Join(workDir, "video.mp4") // Does NOT exist on disk because it was already moved

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	sum := sha256.Sum256(downContent)
	expectedDigest := "sha256:" + hex.EncodeToString(sum[:])

	// Journal record points to video.mp4 (un-updated before crash) in in_progress
	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: userDstPath, // Still points to video.mp4
		ExpectedSize:    expectedSize,
		ExpectedDigest:  expectedDigest,
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// User file video.mp4 should remain untouched
	uData, err := os.ReadFile(userDstPath)
	if err != nil {
		t.Fatalf("failed to read user file: %v", err)
	}
	if string(uData) != string(userContent) {
		t.Fatalf("user file corrupted! got %q, expected %q", string(uData), string(userContent))
	}

	// Job should complete with FinalPath = video (1).mp4
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %s (err: %s)", savedJob.Status, savedJob.Error)
	}
	if savedJob.FinalPath != renamedDstPath {
		t.Fatalf("expected FinalPath %s, got %s", renamedDstPath, savedJob.FinalPath)
	}

	// Record should be Complete with DestinationPath updated to video (1).mp4
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseComplete {
		t.Fatalf("expected record Complete, got %s", updatedRec.Phase)
	}
	if updatedRec.DestinationPath != renamedDstPath {
		t.Fatalf("expected record DestinationPath %s, got %s", renamedDstPath, updatedRec.DestinationPath)
	}
}

func TestRecovery_PreparedPhase_MissingStaging_SameSizeDestinationRejected(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-prep-missing-staging-1"
	userContent := []byte("user-file-content-32-bytes-long!")
	expectedSize := int64(len(userContent))

	// Pre-existing file at destination with same size
	dstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(dstPath, userContent, 0644); err != nil {
		t.Fatalf("failed to write destination file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	srcFile := filepath.Join(workDir, "video.mp4") // Missing!

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: dstPath,
		ExpectedSize:    expectedSize,
		ExpectedDigest:  "", // NO digest provided, cannot prove ownership
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhasePrepared,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Must NOT adopt destination; must fail safely
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", savedJob.Status)
	}

	// Destination file must remain untouched
	dData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(dData) != string(userContent) {
		t.Fatalf("destination file was modified! got %q, expected %q", string(dData), string(userContent))
	}
}

type raceStorageService struct {
	storage.IStorageService
	onResolveFinalPath func(resolvedPath string)
}

func (r *raceStorageService) ResolveFinalPath(srcPath, destinationDir string, policy storage.FilenameConflictPolicy) (string, error) {
	p, err := r.IStorageService.ResolveFinalPath(srcPath, destinationDir, policy)
	if err == nil && r.onResolveFinalPath != nil {
		r.onResolveFinalPath(p)
	}
	return p, err
}

func TestFinalization_PostResolutionCollision_RejectsSilentlyUnjournaledPath(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, baseStorageSvc, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "post-resolution-collision-1"
	origDestContent := []byte("original-dest-content-at-video.mp4")
	concurrentContent := []byte("concurrent-user-file-at-video-(1).mp4-do-not-delete!")
	downContent := []byte("real-downloaded-video-payload-content-50-bytes-long!")

	// 1. Original destination file video.mp4 exists
	origDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(origDstPath, origDestContent, 0644); err != nil {
		t.Fatalf("failed to write original dest file: %v", err)
	}

	workDir, srcFile := setupWorkDirWithFile(t, tempDir, jobID, "video.mp4", downContent)

	// Wrap storage service to inject a concurrent file at video (1).mp4 immediately after ResolveFinalPath chooses it
	raceSvc := &raceStorageService{
		IStorageService: baseStorageSvc,
		onResolveFinalPath: func(resolvedPath string) {
			// Step 4: before file movement, create an unrelated file at video (1).mp4
			if err := os.WriteFile(resolvedPath, concurrentContent, 0644); err != nil {
				t.Fatalf("failed to inject concurrent file at %s: %v", resolvedPath, err)
			}
		},
	}
	mgr.SetStorageService(raceSvc)

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	status := &job.EngineStatus{
		Status:     job.StatusCompleted,
		OutputPath: srcFile,
		FileName:   "video.mp4",
	}

	// 5. Finalization continues via UpdateJobFromEngine
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	// Required outcome:
	// A. GoDownloader must NOT silently land at an unrecorded path such as video (2).mp4
	unjournaledPath := filepath.Join(downloadDir, "video (2).mp4")
	if _, err := os.Stat(unjournaledPath); !os.IsNotExist(err) {
		t.Fatalf("CRITICAL INVARIANT VIOLATION: artifact silently landed at unjournaled path %s!", unjournaledPath)
	}

	// B. GoDownloader must NOT overwrite/delete the newly-created user file at video (1).mp4
	resolvedPath := filepath.Join(downloadDir, "video (1).mp4")
	cData, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatalf("concurrent user file at %s was deleted: %v", resolvedPath, err)
	}
	if string(cData) != string(concurrentContent) {
		t.Fatalf("concurrent user file at %s was modified/overwritten! got %q, expected %q", resolvedPath, string(cData), string(concurrentContent))
	}

	// C. Staging/source must remain intact and recoverable
	sData, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("staging file in workdir was lost/deleted: %v", err)
	}
	if string(sData) != string(downContent) {
		t.Fatalf("staging file content mismatch: got %q, expected %q", string(sData), string(downContent))
	}

	// D. Job fails safely
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", savedJob.Status)
	}

	// E. Finalization record is Failed
	rec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if rec.Phase != job.FinalizationPhaseFailed {
		t.Fatalf("expected record Phase Failed, got %s", rec.Phase)
	}
}

func TestRecovery_UnrecordedCollisionCandidate_RejectedWithoutDigest(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-unrec-candidate-no-digest-1"
	userOriginalContent := []byte("original-video-content-at-destination")
	unrelatedCandidateContent := []byte("unrelated-user-file-with-same-size-50-bytes-length!")
	expectedSize := int64(len(unrelatedCandidateContent))

	// 1. Journal says original destination path video.mp4
	userDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(userDstPath, userOriginalContent, 0644); err != nil {
		t.Fatalf("failed to write original user file: %v", err)
	}

	// 2. Unrecorded collision candidate video (1).mp4 exists with EXACTLY ExpectedSize
	candidatePath := filepath.Join(downloadDir, "video (1).mp4")
	if err := os.WriteFile(candidatePath, unrelatedCandidateContent, 0644); err != nil {
		t.Fatalf("failed to write candidate file: %v", err)
	}

	// 3. Staging / source is MISSING
	workDir := filepath.Join(tempDir, "workdirs", jobID)
	srcFile := filepath.Join(workDir, "video.mp4") // Does not exist

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	// 4. ExpectedDigest is EMPTY
	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: userDstPath, // Points to video.mp4
		ExpectedSize:    expectedSize,
		ExpectedDigest:  "", // NO DIGEST
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Invariant: Recovery MUST NOT adopt video (1).mp4
	cData, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatalf("candidate file was lost: %v", err)
	}
	if string(cData) != string(unrelatedCandidateContent) {
		t.Fatalf("candidate file was modified! got %q, expected %q", string(cData), string(unrelatedCandidateContent))
	}

	// Job MUST NOT be completed
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Fatalf("expected StatusFailed, got %s (err: %s)", savedJob.Status, savedJob.Error)
	}

	// Record MUST be Failed
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseFailed {
		t.Fatalf("expected record Phase Failed, got %s", updatedRec.Phase)
	}
}

func TestRecovery_UnrecordedCollisionCandidate_DigestMismatchRejected(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, execRepo, _, downloadDir, tempDir := setupFinalizationTest(t)

	jobID := "recover-candidate-digest-mismatch-1"
	userOriginalContent := []byte("original-dest-content")
	candidateContent := []byte("candidate-file-50-bytes-long-with-wrong-sha256-hash!")
	expectedSize := int64(len(candidateContent))

	userDstPath := filepath.Join(downloadDir, "video.mp4")
	if err := os.WriteFile(userDstPath, userOriginalContent, 0644); err != nil {
		t.Fatalf("failed to write original user file: %v", err)
	}

	candidatePath := filepath.Join(downloadDir, "video (1).mp4")
	if err := os.WriteFile(candidatePath, candidateContent, 0644); err != nil {
		t.Fatalf("failed to write candidate file: %v", err)
	}

	workDir := filepath.Join(tempDir, "workdirs", jobID)
	srcFile := filepath.Join(workDir, "video.mp4") // Missing

	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/video",
		Name:           "video.mp4",
		Type:           job.TypeMedia,
		Status:         job.StatusDownloading,
		Engine:         "ytdlp",
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		ConflictPolicy: job.FilenameConflictPolicy(storage.ConflictPolicyRename),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	// Supply a validly formatted sha256 that does NOT match candidateContent
	mismatchDigest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rec := &job.FinalizationRecord{
		ID:              uuid.New().String(),
		JobID:           jobID,
		StagingPath:     srcFile,
		DestinationPath: userDstPath,
		ExpectedSize:    expectedSize,
		ExpectedDigest:  mismatchDigest,
		ConflictPolicy:  string(storage.ConflictPolicyRename),
		Phase:           job.FinalizationPhaseInProgress,
		CleanupPath:     workDir,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("failed to save record: %v", err)
	}

	// Run recovery
	mgr.Recover(ctx)

	// Candidate must NOT be adopted
	cData, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatalf("candidate file was lost: %v", err)
	}
	if string(cData) != string(candidateContent) {
		t.Fatalf("candidate file modified! got %q, expected %q", string(cData), string(candidateContent))
	}

	// Job fails safely
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if savedJob.Status != job.StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", savedJob.Status)
	}

	// Record fails
	updatedRec, err := execRepo.GetFinalizationByJobID(ctx, jobID)
	if err != nil {
		t.Fatalf("failed to get record: %v", err)
	}
	if updatedRec.Phase != job.FinalizationPhaseFailed {
		t.Fatalf("expected record Phase Failed, got %s", updatedRec.Phase)
	}
}
