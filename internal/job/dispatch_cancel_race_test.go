package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// H-1: dispatch (engine start + QUEUED -> DOWNLOADING) and Cancel race on the same job. The persisted job status
// is the linearization point: both sides write it conditionally on the state they observed, so the first committed
// transition wins, and the transfer lease is released exactly once and only after any started engine is stopped.
// Every interleaving below is forced at a fixed hook point, so the tests are deterministic (no sleeps or timing).

type raceEnv struct {
	m     *Manager
	repo  *fakeJobRepository
	queue *fakeQueueRepo
	gov   *ResourceGovernor
	eng   *fakeEngine
	bus   *fakeEventBus

	mu        sync.Mutex
	cancelled []string // engine IDs passed to eng.Cancel
	paused    []string // engine IDs passed to eng.Pause
}

func newRaceEnv(t *testing.T) *raceEnv {
	t.Helper()
	m, eng, bus, cleanup, _ := setupManagerTest(t)
	t.Cleanup(cleanup)
	env := &raceEnv{m: m, repo: m.repo.(*fakeJobRepository), eng: eng, bus: bus}
	env.queue = &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	m.SetQueueRepository(env.queue)
	env.gov = NewResourceGovernor(DefaultResourceGovernorConfig())
	m.SetResourceGovernor(env.gov)
	eng.cancelFunc = func(ctx context.Context, j *Job) error {
		env.mu.Lock()
		env.cancelled = append(env.cancelled, j.EngineID)
		env.mu.Unlock()
		return nil
	}
	eng.pauseFunc = func(ctx context.Context, j *Job) error {
		env.mu.Lock()
		env.paused = append(env.paused, j.EngineID)
		env.mu.Unlock()
		return nil
	}
	return env
}

// queued creates a QUEUED aria2 job with its queue row and the transfer lease the scheduler acquires before dispatch.
func (e *raceEnv) queued(t *testing.T, id string, status JobStatus, action QueueAction) *QueuedJob {
	t.Helper()
	now := time.Now()
	j := &Job{ID: id, Source: "https://example.com/" + id, Name: id, Status: status, Type: TypeDownload,
		Engine: "aria2", Priority: JobPriorityNormal, CreatedAt: now, UpdatedAt: now}
	if action == QueueActionResume {
		j.EngineID = "gid-existing-" + id
	}
	if err := e.repo.Create(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if err := e.queue.Enqueue(context.Background(), &QueueEntry{JobID: id, Position: 1, Action: action}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.gov.TryAcquire(id, ResourceRequirements{Engine: "aria2"}); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	return &QueuedJob{JobID: id, Action: action}
}

func (e *raceEnv) status(t *testing.T, id string) JobStatus {
	t.Helper()
	j, _ := e.repo.GetByID(context.Background(), id)
	if j == nil {
		return ""
	}
	return j.Status
}

func (e *raceEnv) leaseHeld(id string) bool { return e.gov.GetLease(id) != nil }

func (e *raceEnv) active(id string) bool {
	e.m.mu.RLock()
	defer e.m.mu.RUnlock()
	_, ok := e.m.activeJobs[id]
	return ok
}

func (e *raceEnv) engineCancels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.cancelled...)
}

func (e *raceEnv) lastJobEvent(id string) string {
	e.bus.mu.Lock()
	defer e.bus.mu.Unlock()
	for i := len(e.bus.events) - 1; i >= 0; i-- {
		if e.bus.events[i].Job.ID == id {
			return e.bus.events[i].Type
		}
	}
	return ""
}

func assertHandled(t *testing.T, err error) {
	t.Helper()
	var handled *DispatchHandledError
	if !errors.As(err, &handled) {
		t.Fatalf("expected DispatchHandledError, got %v", err)
	}
}

// Case 1 (audit interleaving b): Cancel commits while the engine is starting. The dispatch must not resurrect the
// job as DOWNLOADING, must stop the engine it started, and the lease must stay held until that engine is stopped.
func TestH1_CancelDuringEngineStart_DispatchLosesAndStopsEngine(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-b", StatusQueued, QueueActionStart)

	var leaseHeldAfterCancel, cancelErr = false, error(nil)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		_, cancelErr = env.m.Cancel(ctx, j.ID) // commits CANCELLED while the engine is still starting
		leaseHeldAfterCancel = env.leaseHeld(j.ID)
		return "gid-race-b", nil
	}

	err := env.m.dispatchQueuedJob(context.Background(), qj)
	if cancelErr != nil {
		t.Fatalf("cancel: %v", cancelErr)
	}
	assertHandled(t, err)
	if !leaseHeldAfterCancel {
		t.Fatal("cancel released the transfer lease while the dispatch's engine was still starting")
	}
	if got := env.status(t, qj.JobID); got != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED (dispatch must not overwrite a committed cancel)", got)
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-race-b" {
		t.Fatalf("started engine not cancelled exactly once with its new engine ID: %v", c)
	}
	if env.leaseHeld(qj.JobID) {
		t.Fatal("lease not released after the started engine was stopped")
	}
	if env.active(qj.JobID) {
		t.Fatal("cancelled job left in the active set")
	}
	if ev := env.lastJobEvent(qj.JobID); ev != EventJobCancelled {
		t.Fatalf("last event = %s, want %s", ev, EventJobCancelled)
	}
}

// Case 2 (audit interleaving c): Cancel observes QUEUED (no engine ID), then the dispatch commits DOWNLOADING with
// a running engine before Cancel writes. Cancel must not blindly overwrite: it re-evaluates and cancels the engine.
func TestH1_DispatchCommitsFirst_CancelReevaluatesAndCancelsEngine(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-c", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { return "gid-race-c", nil }

	// The dispatch runs on its own goroutine (as on the scheduler). Cancel's conditional write is held until the
	// dispatch's DOWNLOADING commit is visible: the queue-entry delete follows that commit and precedes the
	// dispatch's state-locked tail, which then waits for Cancel's commit section as it would in production.
	committed := make(chan struct{})
	env.queue.onDelete = func(id string) {
		if id == qj.JobID {
			env.queue.onDelete = nil
			close(committed)
		}
	}
	dispatchDone := make(chan error, 1)
	var once sync.Once
	env.repo.beforeUpdateIfStatus = func(j *Job, expected JobStatus) {
		if j.ID == qj.JobID && j.Status == StatusCancelled && expected == StatusQueued {
			once.Do(func() {
				go func() { dispatchDone <- env.m.dispatchQueuedJob(context.Background(), qj) }()
				<-committed
			})
		}
	}

	got, err := env.m.Cancel(context.Background(), qj.JobID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var dispatchErr error
	select {
	case dispatchErr = <-dispatchDone:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not finish")
	}
	if dispatchErr != nil {
		t.Fatalf("dispatch: %v", dispatchErr)
	}
	if got.Status != StatusCancelled || env.status(t, qj.JobID) != StatusCancelled {
		t.Fatalf("status = %s/%s, want CANCELLED", got.Status, env.status(t, qj.JobID))
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-race-c" {
		t.Fatalf("running engine not cancelled (orphaned download): %v", c)
	}
	if env.leaseHeld(qj.JobID) || env.active(qj.JobID) {
		t.Fatalf("lease held=%v active=%v after cancel", env.leaseHeld(qj.JobID), env.active(qj.JobID))
	}
}

// Case 3: Cancel committed before the dispatch read the job. No engine is started.
func TestH1_CancelBeforeDispatchRead_NoEngineStart(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-a", StatusQueued, QueueActionStart)
	started := false
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { started = true; return "gid", nil }

	if _, err := env.m.Cancel(context.Background(), qj.JobID); err != nil {
		t.Fatal(err)
	}
	assertHandled(t, env.m.dispatchQueuedJob(context.Background(), qj))
	if started {
		t.Fatal("engine started for an already-cancelled job")
	}
	if env.status(t, qj.JobID) != StatusCancelled || env.leaseHeld(qj.JobID) {
		t.Fatalf("status=%s lease=%v", env.status(t, qj.JobID), env.leaseHeld(qj.JobID))
	}
}

// Case 4: engine start fails while a cancel commits. The scheduler's failure persistence must not turn the
// CANCELLED job into FAILED, and the lease is released.
func TestH1_StartFailureAfterCancel_SchedulerDoesNotOverwrite(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-fail", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		if _, err := env.m.Cancel(ctx, j.ID); err != nil {
			t.Errorf("cancel: %v", err)
		}
		return "", errors.New("aria2 unreachable")
	}
	sched := NewScheduler(env.repo, env.queue, func(context.Context) int { return 3 }, env.m.dispatchQueuedJob)
	sched.SetResourceGovernor(env.gov)
	sched.ctx = context.Background()

	if err := sched.dispatchSingle(qj); err != nil {
		t.Fatalf("dispatchSingle: %v", err)
	}
	if got := env.status(t, qj.JobID); got != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", got)
	}
	if env.leaseHeld(qj.JobID) {
		t.Fatal("lease leaked")
	}
	if ev := env.lastJobEvent(qj.JobID); ev == EventJobFailed {
		t.Fatal("FAILED published for a cancelled job")
	}
}

// Case 5: dispatch-failure persistence (preflight/selection/torrent start failures) is conditional too.
func TestH1_PersistDispatchFailureDoesNotOverwriteCancel(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-pf", StatusQueued, QueueActionStart)
	stale, _ := env.repo.GetByID(context.Background(), qj.JobID) // dispatch's snapshot: QUEUED
	if _, err := env.m.Cancel(context.Background(), qj.JobID); err != nil {
		t.Fatal(err)
	}
	assertHandled(t, env.m.persistDispatchFailure(context.Background(), stale, qj, StatusFailed, errors.New("preflight failed")))
	if got := env.status(t, qj.JobID); got != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", got)
	}
	if ev := env.lastJobEvent(qj.JobID); ev != EventJobCancelled {
		t.Fatalf("last event = %s, want %s", ev, EventJobCancelled)
	}
}

// Case 6: resume dispatch (PAUSED job with an existing engine download) racing a cancel.
func TestH1_CancelDuringResume_DispatchLoses(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-resume", StatusPaused, QueueActionResume)
	env.eng.resumeFunc = func(ctx context.Context, j *Job) error {
		if _, err := env.m.Cancel(ctx, j.ID); err != nil {
			t.Errorf("cancel: %v", err)
		}
		return nil
	}
	assertHandled(t, env.m.dispatchQueuedJob(context.Background(), qj))
	if got := env.status(t, qj.JobID); got != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", got)
	}
	if env.leaseHeld(qj.JobID) || env.active(qj.JobID) {
		t.Fatal("lease or active entry leaked")
	}
	for _, id := range env.engineCancels() {
		if id != "gid-existing-race-resume" {
			t.Fatalf("unexpected engine cancel target %q", id)
		}
	}
	if len(env.engineCancels()) == 0 {
		t.Fatal("resumed engine download not cancelled")
	}
}

// Case 7: a non-cancel transition (pause) wins during start: the started engine is paused, not cancelled, and the
// PAUSED state stands.
func TestH1_PauseDuringStart_EnginePausedNotCancelled(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-pause", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		cur, _ := env.repo.GetByID(ctx, j.ID)
		cur.Status = StatusPaused
		_ = env.repo.Update(ctx, cur)
		return "gid-race-pause", nil
	}
	assertHandled(t, env.m.dispatchQueuedJob(context.Background(), qj))
	if got := env.status(t, qj.JobID); got != StatusPaused {
		t.Fatalf("status = %s, want PAUSED", got)
	}
	env.mu.Lock()
	paused, cancelled := append([]string(nil), env.paused...), append([]string(nil), env.cancelled...)
	env.mu.Unlock()
	if len(paused) != 1 || paused[0] != "gid-race-pause" || len(cancelled) != 0 {
		t.Fatalf("paused=%v cancelled=%v; want the started engine paused only", paused, cancelled)
	}
	if env.active(qj.JobID) {
		t.Fatal("job that lost the dispatch is in the active set")
	}
}

// Case 8: the job is deleted while its engine starts: the dispatch cannot resurrect a row and cancels the engine.
func TestH1_DeleteDuringStart_EngineCancelled(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-del", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		_ = env.repo.DeleteJobCascade(ctx, j.ID)
		return "gid-race-del", nil
	}
	assertHandled(t, env.m.dispatchQueuedJob(context.Background(), qj))
	if j, _ := env.repo.GetByID(context.Background(), qj.JobID); j != nil {
		t.Fatalf("deleted job resurrected: %+v", j)
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-race-del" {
		t.Fatalf("engine for deleted job not cancelled: %v", c)
	}
}

// Case 9: Cancel's conditional write keeps losing to concurrent transitions: it gives up after a bounded number of
// re-evaluations with a retryable error instead of overwriting.
func TestH1_CancelReevaluationIsBounded(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-flap", StatusQueued, QueueActionStart)
	attempts := 0
	env.repo.beforeUpdateIfStatus = func(j *Job, expected JobStatus) {
		if j.ID != qj.JobID || j.Status != StatusCancelled {
			return
		}
		attempts++
		env.repo.mu.Lock()
		cur := env.repo.jobs[j.ID]
		if cur.Status == StatusQueued {
			cur.Status = StatusPaused
		} else {
			cur.Status = StatusQueued
		}
		env.repo.mu.Unlock()
	}
	_, err := env.m.Cancel(context.Background(), qj.JobID)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrInvalidJobState {
		t.Fatalf("expected retryable invalid-state error, got %v", err)
	}
	if attempts != maxCancelAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, maxCancelAttempts)
	}
	if got := env.status(t, qj.JobID); got == StatusCancelled {
		t.Fatal("cancel overwrote a concurrent transition")
	}
}

// Case 10: uncontended dispatch is unchanged: DOWNLOADING, active, lease held, no engine cancel.
func TestH1_UncontendedDispatchUnchanged(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "plain", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { return "gid-plain", nil }
	if err := env.m.dispatchQueuedJob(context.Background(), qj); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if env.status(t, qj.JobID) != StatusDownloading || !env.active(qj.JobID) || !env.leaseHeld(qj.JobID) {
		t.Fatalf("status=%s active=%v lease=%v", env.status(t, qj.JobID), env.active(qj.JobID), env.leaseHeld(qj.JobID))
	}
	if len(env.engineCancels()) != 0 {
		t.Fatal("engine cancelled on uncontended dispatch")
	}
	// A later ordinary cancel still releases everything.
	if _, err := env.m.Cancel(context.Background(), qj.JobID); err != nil {
		t.Fatal(err)
	}
	if env.leaseHeld(qj.JobID) || env.active(qj.JobID) || len(env.engineCancels()) != 1 {
		t.Fatal("ordinary cancel after dispatch did not release lease/active entry or cancel the engine")
	}
}

// Case 11: Cancel after the dispatch committed DOWNLOADING but before the dispatch returned (active entry and
// queue cleanup still pending): the lease release is handed to the dispatch, which runs it after adding the
// active entry, so nothing stays active or leased.
func TestH1_CancelAfterCommitBeforeDispatchReturns(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "race-late", StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { return "gid-race-late", nil }
	var cancelErr error
	env.queue.onDelete = func(id string) {
		if id == qj.JobID && env.status(t, id) == StatusDownloading {
			env.queue.onDelete = nil
			_, cancelErr = env.m.Cancel(context.Background(), id)
		}
	}
	if err := env.m.dispatchQueuedJob(context.Background(), qj); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if cancelErr != nil {
		t.Fatalf("cancel: %v", cancelErr)
	}
	if env.status(t, qj.JobID) != StatusCancelled {
		t.Fatalf("status = %s", env.status(t, qj.JobID))
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-race-late" {
		t.Fatalf("engine cancels: %v", c)
	}
	if env.active(qj.JobID) || env.leaseHeld(qj.JobID) {
		t.Fatalf("active=%v lease=%v after late cancel", env.active(qj.JobID), env.leaseHeld(qj.JobID))
	}
	if ev := env.lastJobEvent(qj.JobID); ev != EventJobCancelled {
		t.Fatalf("last event = %s, want %s (dispatch's DOWNLOADING update must not be last)", ev, EventJobCancelled)
	}
}
