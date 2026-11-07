package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestGovernor_AnalysisCapacityEnforcement(t *testing.T) {
	cfg := DefaultResourceGovernorConfig() // Analysis limit = 2
	gov := NewResourceGovernor(cfg)

	// Acquire 2 analysis slots
	ctx := context.Background()
	lease1, err := gov.WaitAcquireAnalysis(ctx, "analysis-1")
	if err != nil {
		t.Fatalf("acquire analysis-1 failed: %v", err)
	}
	if lease1 == nil || !lease1.MediaAnalysis {
		t.Fatalf("unexpected lease1: %+v", lease1)
	}

	lease2, err := gov.WaitAcquireAnalysis(ctx, "analysis-2")
	if err != nil {
		t.Fatalf("acquire analysis-2 failed: %v", err)
	}
	if lease2 == nil || !lease2.MediaAnalysis {
		t.Fatalf("unexpected lease2: %+v", lease2)
	}

	snap := gov.Snapshot()
	if snap.ActiveAnalysis != 2 {
		t.Errorf("expected 2 active analyses, got %d", snap.ActiveAnalysis)
	}
	// Analysis slots MUST NOT consume transfer slots
	if snap.ActiveTransfers != 0 {
		t.Errorf("expected 0 active transfers, got %d", snap.ActiveTransfers)
	}

	// 3rd analysis slot must block until released
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		lease3, err := gov.WaitAcquireAnalysis(ctx, "analysis-3")
		if err != nil {
			t.Errorf("acquire analysis-3 failed: %v", err)
			return
		}
		if lease3 == nil {
			t.Errorf("expected non-nil lease3")
		}
	}()

	select {
	case <-waiterDone:
		t.Fatal("analysis-3 acquired immediately without available capacity")
	case <-time.After(50 * time.Millisecond):
		// Expected to still be blocked
	}

	// Release analysis-1; analysis-3 should unblock
	gov.Release("analysis-1")

	select {
	case <-waiterDone:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("analysis-3 was not woken after analysis-1 release")
	}

	snap = gov.Snapshot()
	if snap.ActiveAnalysis != 2 {
		t.Errorf("expected 2 active analyses after handover, got %d", snap.ActiveAnalysis)
	}
	gov.Release("analysis-2")
	gov.Release("analysis-3")
	if gov.Snapshot().ActiveAnalysis != 0 {
		t.Errorf("expected 0 active analyses after all released")
	}
}

func TestGovernor_IndependentYtdlpAndAnalysisCapacity(t *testing.T) {
	cfg := DefaultResourceGovernorConfig() // ytdlp = 2, analysis = 2, global = 3
	gov := NewResourceGovernor(cfg)

	// Acquire 2 ytdlp download leases
	_, err := gov.TryAcquire("dl-1", ResourceRequirements{Engine: "ytdlp"})
	if err != nil {
		t.Fatalf("dl-1 acquire failed: %v", err)
	}
	_, err = gov.TryAcquire("dl-2", ResourceRequirements{Engine: "ytdlp"})
	if err != nil {
		t.Fatalf("dl-2 acquire failed: %v", err)
	}

	snap := gov.Snapshot()
	if snap.ActivePerEngine["ytdlp"] != 2 {
		t.Fatalf("expected 2 active ytdlp downloads, got %d", snap.ActivePerEngine["ytdlp"])
	}

	// Active ytdlp downloads do NOT block media analysis slots
	ctx := context.Background()
	leaseA1, err := gov.WaitAcquireAnalysis(ctx, "an-1")
	if err != nil {
		t.Fatalf("an-1 acquire failed: %v", err)
	}
	if leaseA1 == nil {
		t.Fatal("expected non-nil lease for an-1")
	}

	leaseA2, err := gov.WaitAcquireAnalysis(ctx, "an-2")
	if err != nil {
		t.Fatalf("an-2 acquire failed: %v", err)
	}
	if leaseA2 == nil {
		t.Fatal("expected non-nil lease for an-2")
	}

	snap = gov.Snapshot()
	if snap.ActiveAnalysis != 2 {
		t.Errorf("expected 2 active analyses, got %d", snap.ActiveAnalysis)
	}
	if snap.ActiveTransfers != 2 {
		t.Errorf("expected 2 active transfers, got %d", snap.ActiveTransfers)
	}

	gov.Release("dl-1")
	gov.Release("dl-2")
	gov.Release("an-1")
	gov.Release("an-2")
}

func TestGovernor_FFmpegPreAdmissionReservation(t *testing.T) {
	cfg := DefaultResourceGovernorConfig() // FFmpeg limit = 1
	gov := NewResourceGovernor(cfg)

	// Job 1 acquires with FFmpeg processing
	lease1, err := gov.TryAcquire("media-ff-1", ResourceRequirements{
		Engine:           "ytdlp",
		FFmpegProcessing: true,
	})
	if err != nil {
		t.Fatalf("media-ff-1 acquire failed: %v", err)
	}
	if lease1 == nil || !lease1.FFmpegProcessing {
		t.Fatalf("unexpected lease1: %+v", lease1)
	}

	snap := gov.Snapshot()
	if snap.ActiveFFmpegProcessing != 1 {
		t.Errorf("expected 1 active ffmpeg, got %d", snap.ActiveFFmpegProcessing)
	}

	// Job 2 requests FFmpeg processing, should fail because limit is 1
	_, err = gov.TryAcquire("media-ff-2", ResourceRequirements{
		Engine:           "ytdlp",
		FFmpegProcessing: true,
	})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded for media-ff-2, got: %v", err)
	}

	// But a job WITHOUT FFmpeg processing CAN acquire (ytdlp has capacity 2)
	lease3, err := gov.TryAcquire("media-no-ff", ResourceRequirements{
		Engine:           "ytdlp",
		FFmpegProcessing: false,
	})
	if err != nil {
		t.Fatalf("media-no-ff acquire failed: %v", err)
	}
	if lease3 == nil {
		t.Fatal("expected non-nil lease3")
	}

	// Release job 1
	gov.Release("media-ff-1")
	snap = gov.Snapshot()
	if snap.ActiveFFmpegProcessing != 0 {
		t.Errorf("expected 0 active ffmpeg after release, got %d", snap.ActiveFFmpegProcessing)
	}

	// Now Job 2 can acquire
	lease2, err := gov.TryAcquire("media-ff-2", ResourceRequirements{
		Engine:           "ytdlp",
		FFmpegProcessing: true,
	})
	if err != nil {
		t.Fatalf("media-ff-2 acquire failed after release: %v", err)
	}
	if lease2 == nil {
		t.Fatal("expected non-nil lease2")
	}

	gov.Release("media-no-ff")
	gov.Release("media-ff-2")
}

func TestGovernor_AtomicAllOrNothingAllocation(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 5,
		EngineLimits: map[string]int{
			"ytdlp": 5,
		},
		FFmpegProcessingLimit: 1,
	}
	gov := NewResourceGovernor(cfg)

	// Consume the single FFmpeg permit
	_, err := gov.TryAcquire("holder", ResourceRequirements{Engine: "ytdlp", FFmpegProcessing: true})
	if err != nil {
		t.Fatalf("holder acquire failed: %v", err)
	}

	// Attempt compound acquire: engine capacity is plenty, but FFmpeg is exhausted
	_, err = gov.TryAcquire("candidate", ResourceRequirements{Engine: "ytdlp", FFmpegProcessing: true})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded, got: %v", err)
	}

	// Verify that candidate acquired ZERO permits (no partial leak of ytdlp or global transfer slots)
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected exactly 1 active transfer (from holder), got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["ytdlp"] != 1 {
		t.Errorf("expected exactly 1 active ytdlp, got %d", snap.ActivePerEngine["ytdlp"])
	}
	if snap.ActiveFFmpegProcessing != 1 {
		t.Errorf("expected exactly 1 active ffmpeg, got %d", snap.ActiveFFmpegProcessing)
	}

	gov.Release("holder")
}

func TestGovernor_SameJobIDIdempotencyVsConflict(t *testing.T) {
	cfg := DefaultResourceGovernorConfig()
	gov := NewResourceGovernor(cfg)

	req := ResourceRequirements{Engine: "aria2"}
	lease1, err := gov.TryAcquire("job-idem", req)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Re-acquire with exact same requirements returns same lease
	lease2, err := gov.TryAcquire("job-idem", req)
	if err != nil {
		t.Fatalf("second identical acquire failed: %v", err)
	}
	if lease1 != lease2 {
		t.Errorf("expected exact same lease pointer, got %p vs %p", lease1, lease2)
	}

	// Re-acquire with DIFFERENT requirements returns ErrLeaseConflict
	diffReq := ResourceRequirements{Engine: "ytdlp"}
	_, err = gov.TryAcquire("job-idem", diffReq)
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("expected ErrLeaseConflict, got: %v", err)
	}

	// Active transfers counter remains 1
	if gov.Snapshot().ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer, got %d", gov.Snapshot().ActiveTransfers)
	}

	gov.Release("job-idem")
}

func TestGovernor_IsolatedReleasesAndDoubleReleaseSafety(t *testing.T) {
	cfg := DefaultResourceGovernorConfig()
	gov := NewResourceGovernor(cfg)

	// Acquire analysis for job-a, transfer for job-b
	ctx := context.Background()
	_, err := gov.WaitAcquireAnalysis(ctx, "job-a")
	if err != nil {
		t.Fatalf("acquire analysis failed: %v", err)
	}
	_, err = gov.TryAcquire("job-b", ResourceRequirements{Engine: "aria2"})
	if err != nil {
		t.Fatalf("acquire transfer failed: %v", err)
	}

	snap := gov.Snapshot()
	if snap.ActiveAnalysis != 1 || snap.ActiveTransfers != 1 {
		t.Fatalf("expected 1 analysis and 1 transfer, got %d and %d", snap.ActiveAnalysis, snap.ActiveTransfers)
	}

	// Releasing job-a (analysis) MUST NOT affect job-b (transfer)
	gov.Release("job-a")
	snap = gov.Snapshot()
	if snap.ActiveAnalysis != 0 {
		t.Errorf("expected 0 active analyses, got %d", snap.ActiveAnalysis)
	}
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer to remain, got %d", snap.ActiveTransfers)
	}

	// Double release of job-a must be safe and not cause negative counters
	gov.Release("job-a")
	gov.Release("job-a")
	snap = gov.Snapshot()
	if snap.ActiveAnalysis != 0 {
		t.Errorf("expected 0 active analyses after redundant releases, got %d", snap.ActiveAnalysis)
	}

	// Releasing non-existent job must be safe
	gov.Release("non-existent-job")

	// Release job-b
	gov.Release("job-b")
	gov.Release("job-b")
	snap = gov.Snapshot()
	if snap.ActiveTransfers != 0 {
		t.Errorf("expected 0 active transfers after redundant releases, got %d", snap.ActiveTransfers)
	}
}

func TestGovernor_FIFOAnalysisWaitingAndCancellation(t *testing.T) {
	cfg := ResourceGovernorConfig{
		MediaAnalysisLimit: 1, // limit to 1 so waiter queue is exercised
	}
	gov := NewResourceGovernor(cfg)
	ctx := context.Background()

	// Fill slot
	_, err := gov.WaitAcquireAnalysis(ctx, "w-1")
	if err != nil {
		t.Fatalf("w-1 acquire failed: %v", err)
	}

	// Enqueue waiter 2 with cancellable context
	ctx2, cancel2 := context.WithCancel(ctx)
	w2Done := make(chan error, 1)
	go func() {
		_, err := gov.WaitAcquireAnalysis(ctx2, "w-2")
		w2Done <- err
	}()

	// Enqueue waiter 3 with non-cancellable context
	w3Done := make(chan error, 1)
	go func() {
		_, err := gov.WaitAcquireAnalysis(ctx, "w-3")
		w3Done <- err
	}()

	// Give goroutines time to queue
	time.Sleep(50 * time.Millisecond)

	// Cancel waiter 2
	cancel2()

	select {
	case err := <-w2Done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled for w-2, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("w-2 was not cancelled in time")
	}

	// Now release w-1. Waiter 3 (next in line) must be granted the slot, skipping cancelled w-2
	gov.Release("w-1")

	select {
	case err := <-w3Done:
		if err != nil {
			t.Fatalf("w-3 failed to acquire: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("w-3 was not woken after w-1 release")
	}

	gov.Release("w-3")
}

func TestGovernor_StopWakesAllWaiters(t *testing.T) {
	cfg := ResourceGovernorConfig{
		MediaAnalysisLimit: 1,
	}
	gov := NewResourceGovernor(cfg)
	ctx := context.Background()

	_, err := gov.WaitAcquireAnalysis(ctx, "slot-holder")
	if err != nil {
		t.Fatalf("slot-holder failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			_, err := gov.WaitAcquireAnalysis(ctx, fmt.Sprintf("waiter-%d", idx))
			errs[idx] = err
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// Stop governor
	gov.Stop()

	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, ErrGovernorClosed) {
			t.Errorf("waiter %d expected ErrGovernorClosed, got: %v", i, err)
		}
	}

	// Subsequent operations on closed governor must fail
	_, err = gov.WaitAcquireAnalysis(ctx, "after-stop")
	if !errors.Is(err, ErrGovernorClosed) {
		t.Errorf("expected ErrGovernorClosed on post-stop acquire, got: %v", err)
	}
}

func TestGovernor_RecoveryDoesNotReconstructPhantomPermits(t *testing.T) {
	cfg := DefaultResourceGovernorConfig()
	gov := NewResourceGovernor(cfg)

	// Reconstruct a non-active job (e.g. Queued or AwaitingSelection)
	queuedJob := &Job{ID: "j-queued", Status: StatusQueued, Engine: "aria2"}
	gov.ReconstructJob(queuedJob)

	awaitingJob := &Job{ID: "j-awaiting", Status: StatusAwaitingSelection, Engine: "ytdlp"}
	gov.ReconstructJob(awaitingJob)

	completedJob := &Job{ID: "j-completed", Status: StatusCompleted, Engine: "ytdlp"}
	gov.ReconstructJob(completedJob)

	// Snapshot must show ZERO active permits
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 0 {
		t.Errorf("expected 0 active transfers, got %d", snap.ActiveTransfers)
	}
	if snap.ActiveAnalysis != 0 {
		t.Errorf("expected 0 active analyses, got %d", snap.ActiveAnalysis)
	}

	// Reconstruct an active downloading job
	downloadingJob := &Job{ID: "j-active", Status: StatusDownloading, Engine: "aria2"}
	gov.ReconstructJob(downloadingJob)

	snap = gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 1 {
		t.Errorf("expected 1 active aria2, got %d", snap.ActivePerEngine["aria2"])
	}

	gov.Release("j-active")
}
