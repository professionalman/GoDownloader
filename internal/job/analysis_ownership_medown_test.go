package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// waitForCondition polls cond until it holds or the deadline elapses. It synchronizes on observable
// state rather than on a fixed sleep.
func waitForCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func analysisWaiterCount(g *ResourceGovernor) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.analysisWaiters)
}

func countEvents(bus *fakeEventBus, eventType, jobID string) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	n := 0
	for _, e := range bus.events {
		if e.Type == eventType && e.Job.ID == jobID {
			n++
		}
	}
	return n
}

// controlledAnalyzer simulates a yt-dlp probe whose subprocess outlives the cancellation signal
// until the test allows it to terminate.
type controlledAnalyzer struct {
	dummyEngine
	entered     chan struct{}
	allowExit   chan struct{}
	onEnter     func()
	result      error
	enteredOnce sync.Once
	mu          sync.Mutex
	probes      int
}

func newControlledAnalyzer() *controlledAnalyzer {
	return &controlledAnalyzer{
		dummyEngine: dummyEngine{available: true},
		entered:     make(chan struct{}),
		allowExit:   make(chan struct{}),
	}
}

func (a *controlledAnalyzer) Analyze(ctx context.Context, url string) (*MediaInfo, error) {
	a.mu.Lock()
	a.probes++
	a.mu.Unlock()
	if a.onEnter != nil {
		a.onEnter()
	}
	a.enteredOnce.Do(func() { close(a.entered) })
	<-a.allowExit
	if a.result != nil {
		return nil, a.result
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &MediaInfo{Title: "Probed", Formats: []MediaFormat{{FormatID: "18"}}}, nil
}

func (a *controlledAnalyzer) probeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.probes
}

func newAnalyzingJob(id string) *Job {
	return &Job{
		ID:        id,
		Source:    "https://example.com/watch?v=" + id,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		Status:    StatusAnalyzing,
		Name:      "Analyzing...",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// Cancel must not free the analysis permit while the probe is still running, must not admit the
// next FIFO waiter early, and must keep blocking a duplicate worker for the same JobID.
func TestMediaAnalysis_CancelDoesNotReleasePermitWhileProbeAlive(t *testing.T) {
	repo := newFakeJobRepository()
	analyzer := newControlledAnalyzer()
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": analyzer}}
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1})
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	mgr.SetResourceGovernor(gov)
	ctx := context.Background()

	j := newAnalyzingJob("own-1")
	_ = repo.Create(ctx, j)

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		mgr.analyzeMedia(ctx, j.ID, j.Source)
	}()
	<-analyzer.entered

	// A second analysis queues behind the in-flight probe.
	nextAdmitted := make(chan *ResourceLease, 1)
	go func() {
		lease, err := gov.WaitAcquireAnalysis(ctx, "own-next")
		if err != nil {
			nextAdmitted <- nil
			return
		}
		nextAdmitted <- lease
	}()
	waitForCondition(t, "next analysis waiter to park", func() bool { return analysisWaiterCount(gov) == 1 })

	if _, err := mgr.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	// The probe has not terminated: the permit must still be held by the worker.
	if lease := gov.GetLease(j.ID); lease == nil || !lease.MediaAnalysis {
		t.Fatalf("expected analysis lease to remain held while probe is alive, got %+v", lease)
	}
	if got := gov.Snapshot().ActiveAnalysis; got != 1 {
		t.Fatalf("expected 1 active analysis permit while probe is alive, got %d", got)
	}
	select {
	case <-nextAdmitted:
		t.Fatal("next waiter was admitted before the cancelled probe terminated")
	default:
	}
	if !mgr.isAnalysisWorkerActive(j.ID) {
		t.Fatal("expected cancelled worker to remain registered until it exits")
	}
	if _, ok := mgr.registerAnalysisWorker(j.ID, func() {}); ok {
		t.Fatal("duplicate analysis worker registered while previous worker still alive")
	}

	// Probe terminates; the owning worker releases its permit and the next waiter is admitted.
	close(analyzer.allowExit)
	<-workerDone

	select {
	case lease := <-nextAdmitted:
		if lease == nil {
			t.Fatal("next waiter failed to acquire after release")
		}
		gov.ReleaseLease(lease)
	case <-time.After(5 * time.Second):
		t.Fatal("next waiter not admitted after probe terminated")
	}

	if gov.GetLease(j.ID) != nil {
		t.Fatal("expected cancelled worker to release its analysis lease on exit")
	}
	if mgr.isAnalysisWorkerActive(j.ID) {
		t.Fatal("expected worker to unregister on exit")
	}
	persisted, _ := repo.GetByID(ctx, j.ID)
	if persisted.Status != StatusCancelled {
		t.Fatalf("expected StatusCancelled to be preserved, got %s", persisted.Status)
	}
	if countEvents(bus, EventJobFailed, j.ID) != 0 {
		t.Fatal("cancelled analysis must not publish job.failed")
	}
}

// A probe failure that races with cancellation must not overwrite the persisted cancellation.
func TestMediaAnalysis_FailureCannotOverwriteConcurrentCancel(t *testing.T) {
	repo := newFakeJobRepository()
	analyzer := newControlledAnalyzer()
	analyzer.result = errors.New("probe exploded")
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": analyzer}}
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	mgr.SetResourceGovernor(NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1}))
	ctx := context.Background()

	j := newAnalyzingJob("own-fail-race")
	_ = repo.Create(ctx, j)
	// Durable cancellation lands while the probe is running, before the worker context is signalled.
	analyzer.onEnter = func() {
		cancelled := *j
		cancelled.Status = StatusCancelled
		_ = repo.Update(ctx, &cancelled)
	}
	close(analyzer.allowExit)

	mgr.analyzeMedia(ctx, j.ID, j.Source)

	persisted, _ := repo.GetByID(ctx, j.ID)
	if persisted.Status != StatusCancelled {
		t.Fatalf("expected StatusCancelled to win over stale failure, got %s", persisted.Status)
	}
	if countEvents(bus, EventJobFailed, j.ID) != 0 {
		t.Fatal("stale analysis failure must not publish job.failed")
	}
}

// A genuine probe failure on a still-analyzing job is persisted and published from repository state.
func TestMediaAnalysis_FailurePersistsConditionally(t *testing.T) {
	repo := newFakeJobRepository()
	analyzer := newControlledAnalyzer()
	analyzer.result = errors.New("unsupported URL")
	close(analyzer.allowExit)
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": analyzer}}
	bus := &fakeEventBus{}
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1})
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	mgr.SetResourceGovernor(gov)
	ctx := context.Background()

	j := newAnalyzingJob("own-fail")
	_ = repo.Create(ctx, j)
	mgr.analyzeMedia(ctx, j.ID, j.Source)

	persisted, _ := repo.GetByID(ctx, j.ID)
	if persisted.Status != StatusFailed {
		t.Fatalf("expected StatusFailed, got %s", persisted.Status)
	}
	if countEvents(bus, EventJobFailed, j.ID) != 1 {
		t.Fatal("expected exactly one job.failed event")
	}
	if gov.Snapshot().ActiveAnalysis != 0 {
		t.Fatal("expected analysis permit released after failure")
	}
}

// Governor shutdown while a worker waits for admission must leave the job ANALYZING for recovery,
// not mark it failed, and must never launch the probe.
func TestMediaAnalysis_GovernorShutdownWhileWaiting_LeavesJobAnalyzing(t *testing.T) {
	repo := newFakeJobRepository()
	analyzer := newControlledAnalyzer()
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": analyzer}}
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1})
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil)
	mgr.SetResourceGovernor(gov)
	ctx := context.Background()

	blocker, err := gov.WaitAcquireAnalysis(ctx, "shutdown-blocker")
	if err != nil {
		t.Fatalf("blocker acquire failed: %v", err)
	}
	defer gov.ReleaseLease(blocker)

	j := newAnalyzingJob("own-shutdown")
	_ = repo.Create(ctx, j)

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		mgr.analyzeMedia(ctx, j.ID, j.Source)
	}()
	waitForCondition(t, "worker to park for admission", func() bool { return analysisWaiterCount(gov) == 1 })

	gov.Stop()
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after governor shutdown")
	}

	persisted, _ := repo.GetByID(ctx, j.ID)
	if persisted.Status != StatusAnalyzing {
		t.Fatalf("expected job to remain ANALYZING for restart recovery, got %s", persisted.Status)
	}
	if countEvents(bus, EventJobFailed, j.ID) != 0 {
		t.Fatal("shutdown must not publish job.failed")
	}
	if analyzer.probeCount() != 0 {
		t.Fatal("probe must not run after governor shutdown")
	}
}

// Repeated cancellation racing admission must never leak permits or leave waiters behind.
func TestGovernor_AnalysisCancellationContention(t *testing.T) {
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 2})
	for round := 0; round < 200; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				if idx%2 == 0 {
					cancel()
				} else {
					defer cancel()
					go cancel()
				}
				lease, err := gov.WaitAcquireAnalysis(ctx, fmt.Sprintf("r%d-%d", round, idx))
				if err == nil {
					gov.ReleaseLease(lease)
				}
			}(i)
		}
		wg.Wait()
		snap := gov.Snapshot()
		if snap.ActiveAnalysis != 0 || snap.ActiveLeases != 0 || analysisWaiterCount(gov) != 0 {
			t.Fatalf("round %d leaked state: active=%d leases=%d waiters=%d",
				round, snap.ActiveAnalysis, snap.ActiveLeases, analysisWaiterCount(gov))
		}
	}
}

func TestGovernor_TryAcquireAnalysisHonorsFIFOWaiters(t *testing.T) {
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1})
	ctx := context.Background()

	holder, err := gov.WaitAcquireAnalysis(ctx, "fifo-holder")
	if err != nil {
		t.Fatalf("holder acquire failed: %v", err)
	}
	admitted := make(chan *ResourceLease, 1)
	go func() {
		lease, _ := gov.WaitAcquireAnalysis(ctx, "fifo-waiter")
		admitted <- lease
	}()
	waitForCondition(t, "waiter to park", func() bool { return analysisWaiterCount(gov) == 1 })

	// Releasing hands the slot to the queued waiter; a non-blocking acquire cannot jump the queue.
	gov.ReleaseLease(holder)
	waiterLease := <-admitted
	if waiterLease == nil || waiterLease.JobID != "fifo-waiter" {
		t.Fatalf("expected FIFO waiter to be admitted, got %+v", waiterLease)
	}
	if _, err := gov.TryAcquire("fifo-jumper", ResourceRequirements{MediaAnalysis: true}); err == nil {
		t.Fatal("expected TryAcquire to fail while capacity is held by the admitted waiter")
	}

	// With a waiter queued, TryAcquire must not bypass it even if a slot is momentarily free.
	gov.SetMediaAnalysisLimit(1)
	queued := make(chan *ResourceLease, 1)
	go func() {
		lease, _ := gov.WaitAcquireAnalysis(ctx, "fifo-queued")
		queued <- lease
	}()
	waitForCondition(t, "second waiter to park", func() bool { return analysisWaiterCount(gov) == 1 })
	gov.mu.Lock()
	ok, _ := gov.canAdmitLocked(ResourceRequirements{MediaAnalysis: true})
	gov.mu.Unlock()
	if ok {
		t.Fatal("canAdmit must report false for analysis while FIFO waiters are queued")
	}

	// Raising the limit wakes the queued waiter without requiring a release.
	gov.SetMediaAnalysisLimit(2)
	select {
	case lease := <-queued:
		if lease == nil {
			t.Fatal("queued waiter failed after limit increase")
		}
		gov.ReleaseLease(lease)
	case <-time.After(5 * time.Second):
		t.Fatal("raising the analysis limit did not wake the queued waiter")
	}
	gov.ReleaseLease(waiterLease)
}

func TestGovernor_ReleaseTransferSkipsAnalysisLeases(t *testing.T) {
	gov := NewResourceGovernor(ResourceGovernorConfig{MediaAnalysisLimit: 1})
	ctx := context.Background()

	analysis, err := gov.WaitAcquireAnalysis(ctx, "rt-analysis")
	if err != nil {
		t.Fatalf("analysis acquire failed: %v", err)
	}
	if gov.ReleaseTransfer("rt-analysis") {
		t.Fatal("ReleaseTransfer must not release an analysis lease")
	}
	if gov.Snapshot().ActiveAnalysis != 1 {
		t.Fatal("analysis permit must remain held")
	}

	if _, err := gov.TryAcquire("rt-transfer", ResourceRequirements{Engine: "ytdlp", FFmpegProcessing: true}); err != nil {
		t.Fatalf("transfer acquire failed: %v", err)
	}
	if !gov.ReleaseTransfer("rt-transfer") {
		t.Fatal("ReleaseTransfer must release a transfer lease")
	}
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 0 || snap.ActivePerEngine["ytdlp"] != 0 || snap.ActiveFFmpegProcessing != 0 {
		t.Fatalf("transfer dimensions not restored: %+v", snap)
	}
	if gov.ReleaseTransfer("rt-transfer") {
		t.Fatal("second ReleaseTransfer must be a no-op")
	}

	if !gov.ReleaseLease(analysis) {
		t.Fatal("owner ReleaseLease must release the analysis lease")
	}
	if gov.Snapshot().ActiveAnalysis != 0 {
		t.Fatal("analysis permit not restored")
	}
}

// Concurrent format selections for the same job dispatch at most once.
func TestMediaState_ConcurrentSelectFormatDispatchesOnce(t *testing.T) {
	for round := 0; round < 50; round++ {
		repo := newFakeJobRepository()
		reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": &dummyEngine{available: true}}}
		mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil)
		ctx := context.Background()

		id := fmt.Sprintf("sel-%d", round)
		_ = repo.Create(ctx, &Job{
			ID:        id,
			Type:      TypeMedia,
			Engine:    "ytdlp",
			Status:    StatusAwaitingSelection,
			MediaInfo: &MediaInfo{Formats: []MediaFormat{{FormatID: "18"}}},
		})

		const callers = 8
		var wg sync.WaitGroup
		var mu sync.Mutex
		successes := 0
		start := make(chan struct{})
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := mgr.SelectFormat(ctx, id, "18"); err == nil {
					mu.Lock()
					successes++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()

		if successes != 1 {
			t.Fatalf("round %d: expected exactly 1 successful selection, got %d", round, successes)
		}
		mgr.mu.RLock()
		guards := len(mgr.inFlightSelections)
		mgr.mu.RUnlock()
		if guards != 0 {
			t.Fatalf("round %d: selection guard leaked (%d entries)", round, guards)
		}
	}
}
