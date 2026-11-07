package job

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMediaState_AnalysisCompletionToAwaitingSelection(t *testing.T) {
	repo := newFakeJobRepository()
	ctx := context.Background()

	// Initial job in analyzing state
	j := &Job{
		ID:        "media-1",
		Source:    "https://example.com/watch?v=123",
		Type:      TypeMedia,
		Status:    StatusAnalyzing,
		Name:      "Analyzing...",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = repo.Create(ctx, j)

	// CompleteMediaAnalysis commits atomic update
	info := &MediaInfo{
		Title: "Real Video Title",
		Formats: []MediaFormat{
			{FormatID: "18", VCodec: "h264", ACodec: "aac"},
		},
	}
	won, err := repo.CompleteMediaAnalysis(ctx, "media-1", info.Title, info)
	if err != nil {
		t.Fatalf("CompleteMediaAnalysis failed: %v", err)
	}
	if !won {
		t.Fatalf("expected won == true")
	}

	updated, err := repo.GetByID(ctx, "media-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if updated.Status != StatusAwaitingSelection {
		t.Errorf("expected StatusAwaitingSelection, got %s", updated.Status)
	}
	if updated.Name != "Real Video Title" {
		t.Errorf("expected name updated to Real Video Title, got %s", updated.Name)
	}
	if updated.MediaInfo == nil || len(updated.MediaInfo.Formats) != 1 {
		t.Errorf("expected MediaInfo to be persisted with 1 format")
	}
}

func TestMediaState_StaleAnalysisCannotOverwriteCancellation(t *testing.T) {
	repo := newFakeJobRepository()
	ctx := context.Background()

	// Job starts in analyzing
	j := &Job{
		ID:        "media-cancel-race",
		Source:    "https://example.com/watch?v=123",
		Type:      TypeMedia,
		Status:    StatusAnalyzing,
		Name:      "Analyzing...",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = repo.Create(ctx, j)

	// User cancels while analysis in background
	j.Status = StatusCancelled
	j.UpdatedAt = time.Now()
	_ = repo.Update(ctx, j)

	// Stale analysis worker attempts to complete
	info := &MediaInfo{Title: "Stale Video Title"}
	won, err := repo.CompleteMediaAnalysis(ctx, "media-cancel-race", info.Title, info)
	if err != nil {
		t.Fatalf("CompleteMediaAnalysis returned unexpected error: %v", err)
	}
	if won {
		t.Fatal("expected won == false when job was cancelled concurrently")
	}

	// Status must remain StatusCancelled
	persisted, _ := repo.GetByID(ctx, "media-cancel-race")
	if persisted.Status != StatusCancelled {
		t.Errorf("expected StatusCancelled to be preserved, got %s", persisted.Status)
	}
	if persisted.MediaInfo != nil {
		t.Errorf("expected MediaInfo to remain nil, got %+v", persisted.MediaInfo)
	}
}

func TestMediaState_SelectFormatValidationAndConcurrency(t *testing.T) {
	repo := newFakeJobRepository()
	reg := &dummyRegistry{engines: make(map[string]IEngine)}
	reg.Register("ytdlp", &dummyEngine{available: true})
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	ctx := context.Background()

	// 1. Cannot select format for job in StatusAnalyzing
	jAnalyzing := &Job{
		ID:     "j-an",
		Type:   TypeMedia,
		Status: StatusAnalyzing,
	}
	_ = repo.Create(ctx, jAnalyzing)

	_, err := mgr.SelectFormat(ctx, "j-an", "18")
	if err == nil {
		t.Fatal("expected SelectFormat to fail on StatusAnalyzing, got nil")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrInvalidJobState {
		t.Fatalf("expected ErrInvalidJobState, got: %v", err)
	}

	// 2. Cannot select format if MediaInfo is nil
	jNoInfo := &Job{
		ID:        "j-no-info",
		Type:      TypeMedia,
		Status:    StatusAwaitingSelection,
		MediaInfo: nil,
	}
	_ = repo.Create(ctx, jNoInfo)

	_, err = mgr.SelectFormat(ctx, "j-no-info", "18")
	if err == nil {
		t.Fatal("expected SelectFormat to fail on nil MediaInfo, got nil")
	}

	// 3. Cannot select invalid formatID
	jValid := &Job{
		ID:     "j-valid",
		Type:   TypeMedia,
		Status: StatusAwaitingSelection,
		MediaInfo: &MediaInfo{
			Formats: []MediaFormat{
				{FormatID: "18"},
			},
		},
	}
	_ = repo.Create(ctx, jValid)

	_, err = mgr.SelectFormat(ctx, "j-valid", "999") // invalid format ID
	if err == nil {
		t.Fatal("expected SelectFormat to fail on invalid format ID, got nil")
	}

	// 4. Concurrency guard: simulated racing calls on same job
	mgr.mu.Lock()
	mgr.inFlightSelections["j-valid"] = true
	mgr.mu.Unlock()

	// Call while inFlightSelections flag is held
	_, errRacing := mgr.SelectFormat(ctx, "j-valid", "18")
	if errRacing == nil {
		t.Fatal("expected SelectFormat to fail when selection already in-flight")
	}
	appErr, ok = errRacing.(*AppError)
	if !ok || appErr.Code != ErrInvalidJobState {
		t.Fatalf("expected ErrInvalidJobState for concurrent selection, got: %v", errRacing)
	}

	// Release in-flight lock
	mgr.mu.Lock()
	delete(mgr.inFlightSelections, "j-valid")
	mgr.mu.Unlock()

	// 5. Successful selection transitions to StatusQueued (or downloading if test fallback)
	res, err := mgr.SelectFormat(ctx, "j-valid", "18")
	if err != nil {
		t.Fatalf("expected successful SelectFormat, got: %v", err)
	}
	if res.MediaInfo.SelectedFmt != "18" {
		t.Errorf("expected selected format 18, got %s", res.MediaInfo.SelectedFmt)
	}
}

func TestMediaState_RetryPolicy(t *testing.T) {
	repo := newFakeJobRepository()
	reg := &dummyRegistry{engines: make(map[string]IEngine)}
	reg.Register("ytdlp", &dummyEngine{available: true})
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	ctx := context.Background()

	// 1. Actively analyzing job cannot be retried
	jAnalyzing := &Job{
		ID:     "j-retry-an",
		Type:   TypeMedia,
		Status: StatusAnalyzing,
	}
	_ = repo.Create(ctx, jAnalyzing)

	_, err := mgr.Retry(ctx, "j-retry-an")
	if err == nil {
		t.Fatal("expected Retry on analyzing job to fail")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrInvalidJobState {
		t.Fatalf("expected ErrInvalidJobState on retrying analyzing job, got: %v", err)
	}

	// 2. Awaiting selection job cannot be retried
	jAwaiting := &Job{
		ID:     "j-retry-aw",
		Type:   TypeMedia,
		Status: StatusAwaitingSelection,
	}
	_ = repo.Create(ctx, jAwaiting)

	_, err = mgr.Retry(ctx, "j-retry-aw")
	if err == nil {
		t.Fatal("expected Retry on awaiting_selection job to fail")
	}
	appErr, ok = err.(*AppError)
	if !ok || appErr.Code != ErrInvalidJobState {
		t.Fatalf("expected ErrInvalidJobState on retrying awaiting_selection job, got: %v", err)
	}

	// 3. Failed media job can be retried safely
	jFailed := &Job{
		ID:        "j-retry-fail",
		Type:      TypeMedia,
		Status:    StatusFailed,
		Source:    "https://example.com/watch?v=123",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = repo.Create(ctx, jFailed)

	retried, err := mgr.Retry(ctx, "j-retry-fail")
	if err != nil {
		t.Fatalf("expected Retry on failed media job to succeed, got: %v", err)
	}
	if retried.Status != StatusAnalyzing {
		t.Errorf("expected StatusAnalyzing after retry, got %s", retried.Status)
	}
}

func TestMediaRecovery_Matrix(t *testing.T) {
	ctx := context.Background()

	// Case 1: Analyzing, no MediaInfo -> resumes analysis through bounded admission
	t.Run("Analyzing_NoMediaInfo_ResumesAnalysis", func(t *testing.T) {
		repo := newFakeJobRepository()
		mgr := NewManager(repo, &dummyRegistry{}, &fakeEventBus{}, t.TempDir(), nil)
		j := &Job{
			ID:        "rec-1",
			Type:      TypeMedia,
			Status:    StatusAnalyzing,
			Source:    "https://example.com/watch?v=1",
			MediaInfo: nil,
		}
		_ = repo.Create(ctx, j)

		summary := &RecoverySummary{}
		mgr.recoverJob(ctx, j, summary)

		if summary.RestartedMetadataAcquisitions != 1 {
			t.Errorf("expected 1 restarted metadata acquisition, got %d", summary.RestartedMetadataAcquisitions)
		}
		mgr.mu.RLock()
		pendingLen := len(mgr.pendingMediaAnalysisResumptions)
		mgr.mu.RUnlock()
		if pendingLen != 1 {
			t.Errorf("expected 1 pending media analysis resumption, got %d", pendingLen)
		}
	})

	// Case 2: Analyzing, MediaInfo populated -> normalizes to StatusAwaitingSelection
	t.Run("Analyzing_MediaInfoPopulated_NormalizesToAwaitingSelection", func(t *testing.T) {
		repo := newFakeJobRepository()
		mgr := NewManager(repo, &dummyRegistry{}, &fakeEventBus{}, t.TempDir(), nil)
		j := &Job{
			ID:     "rec-2",
			Type:   TypeMedia,
			Status: StatusAnalyzing,
			MediaInfo: &MediaInfo{
				Formats: []MediaFormat{{FormatID: "18"}},
			},
		}
		_ = repo.Create(ctx, j)

		summary := &RecoverySummary{}
		mgr.recoverJob(ctx, j, summary)

		persisted, _ := repo.GetByID(ctx, "rec-2")
		if persisted.Status != StatusAwaitingSelection {
			t.Errorf("expected normalized StatusAwaitingSelection, got %s", persisted.Status)
		}
	})

	// Case 3: AwaitingSelection, MediaInfo populated -> preserves state
	t.Run("AwaitingSelection_ValidMetadata_PreservesState", func(t *testing.T) {
		repo := newFakeJobRepository()
		mgr := NewManager(repo, &dummyRegistry{}, &fakeEventBus{}, t.TempDir(), nil)
		j := &Job{
			ID:     "rec-3",
			Type:   TypeMedia,
			Status: StatusAwaitingSelection,
			MediaInfo: &MediaInfo{
				Formats: []MediaFormat{{FormatID: "137"}},
			},
		}
		_ = repo.Create(ctx, j)

		summary := &RecoverySummary{}
		mgr.recoverJob(ctx, j, summary)

		persisted, _ := repo.GetByID(ctx, "rec-3")
		if persisted.Status != StatusAwaitingSelection {
			t.Errorf("expected preserved StatusAwaitingSelection, got %s", persisted.Status)
		}
	})

	// Case 4: AwaitingSelection, MediaInfo missing -> fails truthfully
	t.Run("AwaitingSelection_MissingMetadata_FailsTruthfully", func(t *testing.T) {
		repo := newFakeJobRepository()
		mgr := NewManager(repo, &dummyRegistry{}, &fakeEventBus{}, t.TempDir(), nil)
		j := &Job{
			ID:        "rec-4",
			Type:      TypeMedia,
			Status:    StatusAwaitingSelection,
			MediaInfo: nil,
		}
		_ = repo.Create(ctx, j)

		summary := &RecoverySummary{}
		mgr.recoverJob(ctx, j, summary)

		persisted, _ := repo.GetByID(ctx, "rec-4")
		if persisted.Status != StatusFailed {
			t.Errorf("expected StatusFailed, got %s", persisted.Status)
		}
		if persisted.Error != "Media metadata was lost during restart. Retry the job." {
			t.Errorf("unexpected error message: %s", persisted.Error)
		}
		if len(summary.Issues) != 1 || summary.Issues[0].Kind != RecoveryIssueExternalStateUnrecoverable {
			t.Errorf("expected RecoveryIssueExternalStateUnrecoverable issue, got %+v", summary.Issues)
		}
	})

	// Case 5: Downloading or Processing -> marks failed with DSK-4C semantics
	t.Run("DownloadingOrProcessing_FailsWithInterruptedError", func(t *testing.T) {
		repo := newFakeJobRepository()
		mgr := NewManager(repo, &dummyRegistry{}, &fakeEventBus{}, t.TempDir(), nil)
		jDl := &Job{
			ID:     "rec-5-dl",
			Type:   TypeMedia,
			Status: StatusDownloading,
		}
		_ = repo.Create(ctx, jDl)

		summary := &RecoverySummary{}
		mgr.recoverJob(ctx, jDl, summary)

		persisted, _ := repo.GetByID(ctx, "rec-5-dl")
		if persisted.Status != StatusFailed {
			t.Errorf("expected StatusFailed for interrupted download, got %s", persisted.Status)
		}
		if persisted.Error != "Media download was interrupted by application restart. Retry the job." {
			t.Errorf("unexpected error message: %s", persisted.Error)
		}
		if summary.InterruptedMediaJobs != 1 {
			t.Errorf("expected 1 InterruptedMediaJobs, got %d", summary.InterruptedMediaJobs)
		}
	})
}

func TestGovernor_ReleaseLease_PointerIdentity(t *testing.T) {
	gov := NewResourceGovernor(ResourceGovernorConfig{
		MediaAnalysisLimit: 2,
	})
	ctx := context.Background()

	lease1, err := gov.WaitAcquireAnalysis(ctx, "job-ptr-1")
	if err != nil {
		t.Fatalf("WaitAcquireAnalysis failed: %v", err)
	}

	// Mismatched pointer for the same JobID must not release the lease
	staleLease := &ResourceLease{
		JobID:         "job-ptr-1",
		MediaAnalysis: true,
	}
	if gov.ReleaseLease(staleLease) {
		t.Fatal("expected ReleaseLease with mismatched pointer to return false")
	}

	// Active lease must still be held
	current := gov.GetLease("job-ptr-1")
	if current == nil || current.JobID != lease1.JobID {
		t.Fatalf("expected active lease for job-ptr-1, got %+v", current)
	}
	if gov.Snapshot().ActiveAnalysis != 1 {
		t.Fatalf("expected 1 active analysis permit, got %d", gov.Snapshot().ActiveAnalysis)
	}

	// Release with matching pointer succeeds
	if !gov.ReleaseLease(lease1) {
		t.Fatal("expected ReleaseLease with matching pointer to return true")
	}
	if gov.Snapshot().ActiveAnalysis != 0 {
		t.Fatalf("expected 0 active analysis permits after release, got %d", gov.Snapshot().ActiveAnalysis)
	}
	if gov.GetLease("job-ptr-1") != nil {
		t.Fatal("expected nil lease after release")
	}
}

type spyMediaAnalyzer struct {
	dummyEngine
	probed atomic.Bool
}

func (s *spyMediaAnalyzer) Analyze(ctx context.Context, url string) (*MediaInfo, error) {
	s.probed.Store(true)
	return &MediaInfo{Title: "Probed"}, nil
}

func TestMediaAnalysis_CancelledWhileQueued_DoesNotExecute(t *testing.T) {
	repo := newFakeJobRepository()
	spy := &spyMediaAnalyzer{dummyEngine: dummyEngine{available: true}}
	reg := &dummyRegistry{
		engines: map[string]IEngine{
			"ytdlp": spy,
		},
	}
	gov := NewResourceGovernor(ResourceGovernorConfig{
		MediaAnalysisLimit: 1,
	})

	mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil)
	mgr.SetResourceGovernor(gov)
	ctx := context.Background()

	// 1. Occupy the single analysis slot with a blocker lease
	blockerLease, err := gov.WaitAcquireAnalysis(ctx, "blocker-job")
	if err != nil {
		t.Fatalf("failed to acquire blocker lease: %v", err)
	}

	// 2. Create the target media job in StatusAnalyzing
	targetJob := &Job{
		ID:        "target-job",
		Source:    "https://example.com/video",
		Type:      TypeMedia,
		Status:    StatusAnalyzing,
		Name:      "Analyzing...",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = repo.Create(ctx, targetJob)

	// 3. Launch analyzeMedia in background - it will queue waiting for the analysis slot
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		mgr.analyzeMedia(ctx, "target-job", targetJob.Source)
	}()

	// Wait until the worker is parked in the FIFO admission queue
	waitForCondition(t, "worker to park for analysis admission", func() bool { return analysisWaiterCount(gov) == 1 })

	// 4. Concurrently cancel targetJob in DB while it is waiting in queue
	targetJob.Status = StatusCancelled
	targetJob.UpdatedAt = time.Now()
	_ = repo.Update(ctx, targetJob)

	// 5. Release the blocker lease so waiter is admitted
	gov.ReleaseLease(blockerLease)

	// Wait for worker goroutine to complete
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for analyzeMedia to exit")
	}

	// 6. Invariant: probe was NEVER executed because worker checked DB immediately after admission
	if spy.probed.Load() {
		t.Fatal("expected probe NOT to execute for job cancelled while waiting in queue")
	}

	// 7. Invariant: worker released its permit; active analysis slots returned to 0
	if gov.Snapshot().ActiveAnalysis != 0 {
		t.Fatalf("expected 0 active analysis permits, got %d", gov.Snapshot().ActiveAnalysis)
	}
}
