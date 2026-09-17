package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
)

func setupExecutionTestDB(t *testing.T) (*database.DB, *database.SQLiteJobRepository, *database.SQLiteExecutionRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_execution.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	return db, jobRepo, execRepo
}

func createTestJob(t *testing.T, jobRepo *database.SQLiteJobRepository, id string) *job.Job {
	t.Helper()
	now := time.Now()
	j := &job.Job{
		ID:        id,
		Source:    "https://example.com/file.bin",
		Name:      "file.bin",
		Status:    job.StatusDownloading,
		Type:      job.TypeDownload,
		Engine:    "native_http",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(context.Background(), j); err != nil {
		t.Fatalf("create test job %s: %v", id, err)
	}
	return j
}

func TestExecutionRepository_ExecutionsLifecycle(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	createTestJob(t, jobRepo, "job-exec-1")

	// 1. Create first execution attempt
	now := time.Now()
	exec1 := &job.JobExecution{
		ID:                  "exec-1-1",
		JobID:               "job-exec-1",
		AttemptNumber:       1,
		EngineFamily:        job.EngineFamilyNativeHTTP,
		RuntimeMode:         job.RuntimeModeManaged,
		EngineCorrelationID: "corr-1",
		Status:              job.ExecutionStatusActive,
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if err := execRepo.CreateExecution(ctx, exec1); err != nil {
		t.Fatalf("create exec1: %v", err)
	}

	// 2. Query by ID
	got1, err := execRepo.GetExecutionByID(ctx, "exec-1-1")
	if err != nil {
		t.Fatalf("get exec1: %v", err)
	}
	if got1 == nil || got1.JobID != "job-exec-1" || got1.AttemptNumber != 1 {
		t.Fatalf("unexpected got1: %+v", got1)
	}
	if got1.EngineFamily != job.EngineFamilyNativeHTTP || got1.RuntimeMode != job.RuntimeModeManaged {
		t.Fatalf("unexpected engine family or runtime mode: %+v", got1)
	}

	// 3. Update execution to failed
	got1.Status = job.ExecutionStatusFailed
	got1.FailureClassification = "transient_network"
	got1.FailureDetail = "connection reset by peer"
	completedAt := time.Now()
	got1.CompletedAt = &completedAt
	if err := execRepo.UpdateExecution(ctx, got1); err != nil {
		t.Fatalf("update exec1: %v", err)
	}

	// 4. Create attempt 2
	exec2 := &job.JobExecution{
		ID:                  "exec-1-2",
		JobID:               "job-exec-1",
		AttemptNumber:       2,
		EngineFamily:        job.EngineFamilyNativeHTTP,
		RuntimeMode:         job.RuntimeModeManaged,
		EngineCorrelationID: "corr-2",
		Status:              job.ExecutionStatusActive,
		StartedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	if err := execRepo.CreateExecution(ctx, exec2); err != nil {
		t.Fatalf("create exec2: %v", err)
	}

	// 5. GetLatestExecution returns attempt 2
	latest, err := execRepo.GetLatestExecution(ctx, "job-exec-1")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil || latest.ID != "exec-1-2" || latest.AttemptNumber != 2 {
		t.Fatalf("expected exec2 as latest, got: %+v", latest)
	}

	// 6. ListExecutions returns both in attempt order
	all, err := execRepo.ListExecutions(ctx, "job-exec-1")
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(all))
	}
	if all[0].AttemptNumber != 1 || all[1].AttemptNumber != 2 {
		t.Fatalf("executions not in attempt order: %+v", all)
	}
}

func TestExecutionRepository_HTTPCheckpointAndSegments(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	createTestJob(t, jobRepo, "job-http-1")

	now := time.Now()
	cp := &job.HTTPCheckpoint{
		JobID:             "job-http-1",
		ExecutionID:       "exec-http-1",
		CheckpointVersion: 1,
		EffectiveURL:      "https://example.com/data.tar.gz",
		ETag:              `"3f82-598e3b"`,
		LastModified:      "Wed, 21 Oct 2026 07:28:00 GMT",
		ContentLength:     1048576,
		RangeSupported:    true,
		StagingPath:       "/downloads/.staging/job-http-1.part",
		VerifiedBytes:     524288,
		UpdatedAt:         now,
	}

	segments := []job.HTTPCheckpointSegment{
		{
			JobID:         "job-http-1",
			SegmentIndex:  0,
			StartOffset:   0,
			CurrentOffset: 524288,
			EndOffset:     524287,
			VerifiedBytes: 524288,
			Completed:     true,
			UpdatedAt:     now,
		},
		{
			JobID:         "job-http-1",
			SegmentIndex:  1,
			StartOffset:   524288,
			CurrentOffset: 524288,
			EndOffset:     1048575,
			VerifiedBytes: 0,
			Completed:     false,
			UpdatedAt:     now,
		},
	}

	// 1. Save checkpoint with 2 segments
	if err := execRepo.SaveHTTPCheckpoint(ctx, cp, segments); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	// 2. Read back
	gotCP, gotSegs, err := execRepo.GetHTTPCheckpoint(ctx, "job-http-1")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if gotCP == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if gotCP.ETag != `"3f82-598e3b"` || gotCP.ContentLength != 1048576 || !gotCP.RangeSupported {
		t.Fatalf("checkpoint fields mismatch: %+v", gotCP)
	}
	if len(gotSegs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(gotSegs))
	}
	if gotSegs[0].Completed != true || gotSegs[1].Completed != false {
		t.Fatalf("unexpected segment completion: %+v", gotSegs)
	}

	// 3. Update segment 1 progress
	if err := execRepo.UpdateHTTPSegmentProgress(ctx, "job-http-1", 1, 786432, 262144, false); err != nil {
		t.Fatalf("update segment progress: %v", err)
	}

	_, updatedSegs, err := execRepo.GetHTTPCheckpoint(ctx, "job-http-1")
	if err != nil {
		t.Fatalf("get checkpoint after segment update: %v", err)
	}
	if updatedSegs[1].CurrentOffset != 786432 || updatedSegs[1].VerifiedBytes != 262144 {
		t.Fatalf("unexpected updated segment 1: %+v", updatedSegs[1])
	}
}

func TestExecutionRepository_FinalizationRecords(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	createTestJob(t, jobRepo, "job-fin-1")

	now := time.Now()
	rec := &job.FinalizationRecord{
		ID:              "fin-1",
		JobID:           "job-fin-1",
		ExecutionID:     "exec-fin-1",
		StagingPath:     "/downloads/.staging/job-fin-1.part",
		DestinationPath: "/downloads/complete/file.bin",
		ExpectedSize:    1048576,
		ExpectedDigest:  "sha256:abcd",
		ConflictPolicy:  "rename",
		Phase:           job.FinalizationPhasePrepared,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// 1. Save finalization record
	if err := execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
		t.Fatalf("save finalization: %v", err)
	}

	// 2. Query by Job ID
	got, err := execRepo.GetFinalizationByJobID(ctx, "job-fin-1")
	if err != nil {
		t.Fatalf("get by job id: %v", err)
	}
	if got == nil || got.ID != "fin-1" || got.Phase != job.FinalizationPhasePrepared {
		t.Fatalf("unexpected record: %+v", got)
	}

	// 3. Check pending finalizations
	pending, err := execRepo.GetPendingFinalizations(ctx)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "fin-1" {
		t.Fatalf("expected fin-1 in pending, got %+v", pending)
	}

	// 4. Update phase to Complete
	if err := execRepo.UpdateFinalizationPhase(ctx, "fin-1", job.FinalizationPhaseComplete, ""); err != nil {
		t.Fatalf("update phase to complete: %v", err)
	}

	// 5. Pending list should now be empty
	pendingAfter, err := execRepo.GetPendingFinalizations(ctx)
	if err != nil {
		t.Fatalf("get pending after complete: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Fatalf("expected 0 pending finalizations, got %d", len(pendingAfter))
	}

	completedRec, err := execRepo.GetFinalizationByJobID(ctx, "job-fin-1")
	if err != nil || completedRec.CompletedAt == nil {
		t.Fatalf("expected completedAt set, got %+v (err=%v)", completedRec, err)
	}
}

func TestExecutionRepository_ToolRecords(t *testing.T) {
	ctx := context.Background()
	db, _, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	now := time.Now()
	toolV1 := &job.ToolRecord{
		Name:               "yt-dlp",
		Version:            "2026.08.01",
		PlatformArch:       "windows/amd64",
		OwnershipMode:      "managed",
		ExecutablePath:     "C:\\tools\\yt-dlp-2026.08.01.exe",
		SHA256:             "hash1",
		VerificationStatus: "verified",
		Status:             "last_known_good",
		CreatedAt:          now.Add(-24 * time.Hour),
		UpdatedAt:          now.Add(-24 * time.Hour),
	}
	toolV2 := &job.ToolRecord{
		Name:               "yt-dlp",
		Version:            "2026.09.01",
		PlatformArch:       "windows/amd64",
		OwnershipMode:      "managed",
		ExecutablePath:     "C:\\tools\\yt-dlp-2026.09.01.exe",
		SHA256:             "hash2",
		VerificationStatus: "verified",
		Status:             "active",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := execRepo.SaveToolRecord(ctx, toolV1); err != nil {
		t.Fatalf("save tool v1: %v", err)
	}
	if err := execRepo.SaveToolRecord(ctx, toolV2); err != nil {
		t.Fatalf("save tool v2: %v", err)
	}

	// 1. GetActiveTool returns v2
	active, err := execRepo.GetActiveTool(ctx, "yt-dlp")
	if err != nil {
		t.Fatalf("get active tool: %v", err)
	}
	if active == nil || active.Version != "2026.09.01" {
		t.Fatalf("expected active version 2026.09.01, got %+v", active)
	}

	// 2. ListToolRecords returns both
	all, err := execRepo.ListToolRecords(ctx, "yt-dlp")
	if err != nil {
		t.Fatalf("list tool records: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tool records, got %d", len(all))
	}
}

func TestExecutionRepository_EventCursor(t *testing.T) {
	ctx := context.Background()
	db, _, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	// Initial cursor for unrecorded scope is 0
	cur, err := execRepo.GetEventCursor(ctx, "global")
	if err != nil {
		t.Fatalf("get initial cursor: %v", err)
	}
	if cur != 0 {
		t.Fatalf("expected initial cursor 0, got %d", cur)
	}

	// Advance cursor monotonically
	for expected := int64(1); expected <= 5; expected++ {
		next, err := execRepo.AdvanceEventCursor(ctx, "global")
		if err != nil {
			t.Fatalf("advance cursor: %v", err)
		}
		if next != expected {
			t.Fatalf("expected cursor %d, got %d", expected, next)
		}
	}

	got, err := execRepo.GetEventCursor(ctx, "global")
	if err != nil || got != 5 {
		t.Fatalf("expected cursor 5, got %d (err=%v)", got, err)
	}

	// Separate scope has independent sequence
	scopeB, err := execRepo.AdvanceEventCursor(ctx, "scope_b")
	if err != nil || scopeB != 1 {
		t.Fatalf("expected scope_b cursor 1, got %d (err=%v)", scopeB, err)
	}
}

func TestExecutionRepository_CascadeDeleteCleansFnd1Children(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	j := createTestJob(t, jobRepo, "job-cascade-1")

	// Add execution
	exec := &job.JobExecution{
		ID:            "exec-casc-1",
		JobID:         j.ID,
		AttemptNumber: 1,
		EngineFamily:  job.EngineFamilyNativeHTTP,
		Status:        job.ExecutionStatusActive,
		StartedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := execRepo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("create exec: %v", err)
	}

	// Add checkpoint + segments
	cp := &job.HTTPCheckpoint{
		JobID:             j.ID,
		ExecutionID:       exec.ID,
		CheckpointVersion: 1,
		EffectiveURL:      "https://example.com/test",
		UpdatedAt:         time.Now(),
	}
	segs := []job.HTTPCheckpointSegment{
		{
			JobID:        j.ID,
			SegmentIndex: 0,
			StartOffset:  0,
			EndOffset:    100,
			UpdatedAt:    time.Now(),
		},
	}
	if err := execRepo.SaveHTTPCheckpoint(ctx, cp, segs); err != nil {
		t.Fatalf("save cp: %v", err)
	}

	// Add finalization record
	fin := &job.FinalizationRecord{
		ID:              "fin-casc-1",
		JobID:           j.ID,
		ExecutionID:     exec.ID,
		StagingPath:     "/staging/path",
		DestinationPath: "/dest/path",
		Phase:           job.FinalizationPhasePrepared,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := execRepo.SaveFinalizationRecord(ctx, fin); err != nil {
		t.Fatalf("save fin: %v", err)
	}

	// Delete job with DeleteJobCascade
	if err := jobRepo.DeleteJobCascade(ctx, j.ID); err != nil {
		t.Fatalf("DeleteJobCascade failed: %v", err)
	}

	// Assert job is gone
	gotJob, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil || gotJob != nil {
		t.Fatalf("expected job deleted, got %+v (err=%v)", gotJob, err)
	}

	// Assert execution is gone
	gotExec, err := execRepo.GetExecutionByID(ctx, exec.ID)
	if err != nil || gotExec != nil {
		t.Fatalf("expected execution deleted, got %+v (err=%v)", gotExec, err)
	}

	// Assert checkpoint and segments are gone
	gotCP, gotSegs, err := execRepo.GetHTTPCheckpoint(ctx, j.ID)
	if err != nil || gotCP != nil || len(gotSegs) != 0 {
		t.Fatalf("expected checkpoint deleted, got cp=%+v, segs=%+v", gotCP, gotSegs)
	}

	// Assert finalization record is gone
	gotFin, err := execRepo.GetFinalizationByJobID(ctx, j.ID)
	if err != nil || gotFin != nil {
		t.Fatalf("expected finalization deleted, got %+v", gotFin)
	}
}

func TestExecutionRepository_HTTPCheckpointMultiAttemptRebindAndReset(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, execRepo := setupExecutionTestDB(t)
	defer db.Close()

	j := createTestJob(t, jobRepo, "job-multi-attempt")

	// 1. Attempt 1 starts, creates checkpoint and 2 segments
	exec1 := &job.JobExecution{
		ID:            "exec-att-1",
		JobID:         j.ID,
		AttemptNumber: 1,
		EngineFamily:  job.EngineFamilyNativeHTTP,
		RuntimeMode:   job.RuntimeModeManaged,
		Status:        job.ExecutionStatusActive,
		StartedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := execRepo.CreateExecution(ctx, exec1); err != nil {
		t.Fatalf("create exec1: %v", err)
	}

	cp1 := &job.HTTPCheckpoint{
		JobID:             j.ID,
		ExecutionID:       exec1.ID,
		CheckpointVersion: 1,
		EffectiveURL:      "https://example.com/file.iso",
		ETag:              `"etag-v1"`,
		LastModified:      "Mon, 01 Jan 2026 00:00:00 GMT",
		ContentLength:     1000,
		RangeSupported:    true,
		StagingPath:       "/staging/file.iso.part",
		VerifiedBytes:     500,
		UpdatedAt:         time.Now(),
	}
	segs1 := []job.HTTPCheckpointSegment{
		{JobID: j.ID, SegmentIndex: 0, StartOffset: 0, CurrentOffset: 500, EndOffset: 499, VerifiedBytes: 500, Completed: true, UpdatedAt: time.Now()},
		{JobID: j.ID, SegmentIndex: 1, StartOffset: 500, CurrentOffset: 500, EndOffset: 999, VerifiedBytes: 0, Completed: false, UpdatedAt: time.Now()},
	}
	if err := execRepo.SaveHTTPCheckpoint(ctx, cp1, segs1); err != nil {
		t.Fatalf("save cp1: %v", err)
	}

	// 2. Attempt 1 fails
	exec1.Status = job.ExecutionStatusFailed
	exec1.FailureClassification = "transient_network"
	if err := execRepo.UpdateExecution(ctx, exec1); err != nil {
		t.Fatalf("update exec1: %v", err)
	}

	// 3. Attempt 2 starts (retry)
	exec2 := &job.JobExecution{
		ID:            "exec-att-2",
		JobID:         j.ID,
		AttemptNumber: 2,
		EngineFamily:  job.EngineFamilyNativeHTTP,
		RuntimeMode:   job.RuntimeModeManaged,
		Status:        job.ExecutionStatusActive,
		StartedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := execRepo.CreateExecution(ctx, exec2); err != nil {
		t.Fatalf("create exec2: %v", err)
	}

	// Attempt 2 reads existing checkpoint and rebinds to exec-2
	existingCP, existingSegs, err := execRepo.GetHTTPCheckpoint(ctx, j.ID)
	if err != nil || existingCP == nil {
		t.Fatalf("get existing cp: %v", err)
	}
	if existingCP.ExecutionID != exec1.ID || len(existingSegs) != 2 {
		t.Fatalf("unexpected existing cp state: %+v, segs=%d", existingCP, len(existingSegs))
	}

	// Rebind to exec-2 and update progress
	existingCP.ExecutionID = exec2.ID
	existingCP.VerifiedBytes = 750
	if err := execRepo.SaveHTTPCheckpoint(ctx, existingCP, nil); err != nil {
		t.Fatalf("rebind cp to exec2: %v", err)
	}
	if err := execRepo.UpdateHTTPSegmentProgress(ctx, j.ID, 1, 750, 250, false); err != nil {
		t.Fatalf("update segment progress: %v", err)
	}

	// Verify attempt 2 binding and progress
	reboundCP, reboundSegs, err := execRepo.GetHTTPCheckpoint(ctx, j.ID)
	if err != nil || reboundCP.ExecutionID != exec2.ID || reboundSegs[1].CurrentOffset != 750 {
		t.Fatalf("rebound cp mismatch: cp=%+v, seg1=%+v", reboundCP, reboundSegs)
	}

	// 4. Scenario: Server mutates resource (new ETag "etag-v2"). Attempt 3 replaces checkpoint completely.
	exec3 := &job.JobExecution{
		ID:            "exec-att-3",
		JobID:         j.ID,
		AttemptNumber: 3,
		EngineFamily:  job.EngineFamilyNativeHTTP,
		RuntimeMode:   job.RuntimeModeManaged,
		Status:        job.ExecutionStatusActive,
		StartedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := execRepo.CreateExecution(ctx, exec3); err != nil {
		t.Fatalf("create exec3: %v", err)
	}

	cp3 := &job.HTTPCheckpoint{
		JobID:             j.ID,
		ExecutionID:       exec3.ID,
		CheckpointVersion: 1,
		EffectiveURL:      "https://example.com/file.iso",
		ETag:              `"etag-v2"`,
		LastModified:      "Tue, 02 Feb 2026 00:00:00 GMT",
		ContentLength:     2000,
		RangeSupported:    true,
		StagingPath:       "/staging/file.iso.part",
		VerifiedBytes:     0,
		UpdatedAt:         time.Now(),
	}
	segs3 := []job.HTTPCheckpointSegment{
		{JobID: j.ID, SegmentIndex: 0, StartOffset: 0, CurrentOffset: 0, EndOffset: 1999, VerifiedBytes: 0, Completed: false, UpdatedAt: time.Now()},
	}
	if err := execRepo.SaveHTTPCheckpoint(ctx, cp3, segs3); err != nil {
		t.Fatalf("save cp3 (reset): %v", err)
	}

	// 5. Verify stale attempt 1/2 segments are completely gone and replaced by single new segment
	finalCP, finalSegs, err := execRepo.GetHTTPCheckpoint(ctx, j.ID)
	if err != nil {
		t.Fatalf("get final cp: %v", err)
	}
	if finalCP.ExecutionID != exec3.ID || finalCP.ETag != `"etag-v2"` || finalCP.ContentLength != 2000 {
		t.Fatalf("unexpected final cp: %+v", finalCP)
	}
	if len(finalSegs) != 1 || finalSegs[0].EndOffset != 1999 {
		t.Fatalf("expected exactly 1 replaced segment, got %d: %+v", len(finalSegs), finalSegs)
	}
}
