package job

import (
	"context"
	"errors"
	"testing"
)

// Cancellation vs. dispatch-persistence reconciliation. When a dispatch started an engine but its DOWNLOADING write
// failed, the scheduler keeps a dirty reservation (and the lease) and reconciles every round. A cancellation that
// commits meanwhile cannot know the engine ID. It must stay authoritative: the reconciler stops the engine and only
// then releases the lease; it never writes a live state over CANCELLED or re-activates the job.

// dirtyDispatch dispatches through the real scheduler path with the DOWNLOADING write failing once and the
// immediate reconciliation unable to query the engine, leaving a dirty external-execution reservation.
func dirtyDispatch(t *testing.T, env *raceEnv, id string) (*Scheduler, *int) {
	t.Helper()
	qj := env.queued(t, id, StatusQueued, QueueActionStart)
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { return "gid-" + j.ID, nil }
	failNext := true
	env.repo.beforeUpdateIfStatus = func(j *Job, expected JobStatus) {
		if j.ID == id && j.Status == StatusDownloading && expected == StatusQueued && failNext {
			failNext = false
			env.repo.updateErr = errors.New("simulated DOWNLOADING persistence failure")
			return
		}
		env.repo.updateErr = nil
	}
	statusCalls := 0
	env.eng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		statusCalls++
		if statusCalls == 1 {
			return nil, errors.New("engine status unavailable") // keeps the reservation dirty
		}
		return &EngineStatus{Status: StatusDownloading, Progress: 5}, nil
	}
	sched := NewScheduler(env.repo, env.queue, func(context.Context) int { return 3 }, env.m.dispatchQueuedJob)
	env.m.SetScheduler(sched)
	sched.ctx = context.Background()
	if !sched.reserveInFlight(id, QueueActionStart) {
		t.Fatal("reserve")
	}
	var pErr *DispatchPersistenceError
	if err := sched.dispatchSingle(qj); !errors.As(err, &pErr) {
		t.Fatalf("expected a dispatch persistence error, got %v", err)
	}
	env.repo.updateErr = nil
	if !sched.isInFlight(id) || !env.leaseHeld(id) || env.status(t, id) != StatusQueued {
		t.Fatalf("precondition: inFlight=%v lease=%v status=%s", sched.isInFlight(id), env.leaseHeld(id), env.status(t, id))
	}
	return sched, &statusCalls
}

func TestReconcileRace_CancelDuringDirtyReservation_EngineStoppedBeforeLeaseRelease(t *testing.T) {
	env := newRaceEnv(t)
	sched, _ := dirtyDispatch(t, env, "rec-1")

	if _, err := env.m.Cancel(context.Background(), "rec-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if env.status(t, "rec-1") != StatusCancelled {
		t.Fatalf("status = %s", env.status(t, "rec-1"))
	}
	if !env.leaseHeld("rec-1") {
		t.Fatal("cancel released the lease while the unpersisted engine execution is still running")
	}

	sched.reconcileAll(context.Background())

	if env.status(t, "rec-1") != StatusCancelled {
		t.Fatalf("reconciliation overwrote CANCELLED with %s", env.status(t, "rec-1"))
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-rec-1" {
		t.Fatalf("reconciler did not stop the started engine: %v", c)
	}
	if env.leaseHeld("rec-1") || sched.isInFlight("rec-1") || env.active("rec-1") {
		t.Fatalf("lease=%v inFlight=%v active=%v after reconciliation", env.leaseHeld("rec-1"), sched.isInFlight("rec-1"), env.active("rec-1"))
	}
	if ev := env.lastJobEvent("rec-1"); ev != EventJobCancelled {
		t.Fatalf("last event = %s", ev)
	}
}

func TestReconcileRace_CancelDuringReconcileEngineQuery(t *testing.T) {
	env := newRaceEnv(t)
	sched, statusCalls := dirtyDispatch(t, env, "rec-2")
	env.eng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		*statusCalls++
		if _, err := env.m.Cancel(ctx, j.ID); err != nil { // commits between the reconciler's read and its write
			t.Errorf("cancel: %v", err)
		}
		return &EngineStatus{Status: StatusDownloading, Progress: 5}, nil
	}
	sched.reconcileAll(context.Background())
	if env.status(t, "rec-2") != StatusCancelled || env.active("rec-2") {
		t.Fatalf("reconciler resurrected the job: status=%s active=%v", env.status(t, "rec-2"), env.active("rec-2"))
	}
	if !env.leaseHeld("rec-2") {
		t.Fatal("lease released while the engine still runs")
	}
	sched.reconcileAll(context.Background()) // next round observes CANCELLED
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-rec-2" {
		t.Fatalf("engine not stopped: %v", c)
	}
	if env.leaseHeld("rec-2") || sched.isInFlight("rec-2") || env.active("rec-2") {
		t.Fatal("lease/reservation/active entry leaked")
	}
}

func TestReconcileRace_StateReconciliationSupersededByCancel(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "rec-3", StatusQueued, QueueActionStart)
	sched := NewScheduler(env.repo, env.queue, func(context.Context) int { return 3 }, env.m.dispatchQueuedJob)
	env.m.SetScheduler(sched)
	sched.ctx = context.Background()
	sched.reserveInFlight(qj.JobID, QueueActionStart)
	sched.markReservationDirtyState(qj.JobID, QueueActionStart, StatusFailed, "start failed")

	if _, err := env.m.Cancel(context.Background(), qj.JobID); err != nil {
		t.Fatal(err)
	}
	if !env.leaseHeld(qj.JobID) {
		t.Fatal("cancel released the lease the dirty reservation still owns")
	}
	sched.reconcileAll(context.Background())
	if env.status(t, qj.JobID) != StatusCancelled {
		t.Fatalf("state reconciliation overwrote CANCELLED with %s", env.status(t, qj.JobID))
	}
	if env.leaseHeld(qj.JobID) || sched.isInFlight(qj.JobID) {
		t.Fatal("lease or reservation leaked")
	}
	if env.lastJobEvent(qj.JobID) != EventJobCancelled {
		t.Fatal("a stale FAILED event followed the cancellation")
	}
}

// Without contention the reconciler still restores a running execution (no regression).
func TestReconcileRace_UncontendedReconciliationActivates(t *testing.T) {
	env := newRaceEnv(t)
	sched, _ := dirtyDispatch(t, env, "rec-4")
	sched.reconcileAll(context.Background())
	if env.status(t, "rec-4") != StatusDownloading || !env.active("rec-4") || !env.leaseHeld("rec-4") || sched.isInFlight("rec-4") {
		t.Fatalf("status=%s active=%v lease=%v inFlight=%v", env.status(t, "rec-4"), env.active("rec-4"), env.leaseHeld("rec-4"), sched.isInFlight("rec-4"))
	}
	if len(env.engineCancels()) != 0 {
		t.Fatal("engine cancelled without a cancellation")
	}
}

// The cancellation commits after the reconciler's conditional DOWNLOADING write but before it registers the job as
// active (at the queue-entry delete in between). The cancellation reaches the engine (the row now carries its ID),
// and the reconciler's registration is refused, so no cancelled job lingers in the active set.
func TestReconcileRace_CancelBetweenReconcileWriteAndActivation(t *testing.T) {
	env := newRaceEnv(t)
	sched, _ := dirtyDispatch(t, env, "rec-5")
	env.queue.onDelete = func(id string) {
		if id == "rec-5" && env.status(t, id) == StatusDownloading {
			env.queue.onDelete = nil
			if _, err := env.m.Cancel(context.Background(), id); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}
	}
	sched.reconcileAll(context.Background())
	if env.status(t, "rec-5") != StatusCancelled {
		t.Fatalf("status = %s", env.status(t, "rec-5"))
	}
	if env.active("rec-5") {
		t.Fatal("reconciler re-activated a job cancelled after its write")
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-rec-5" {
		t.Fatalf("engine not cancelled: %v", c)
	}
	if env.leaseHeld("rec-5") || sched.isInFlight("rec-5") {
		t.Fatal("lease or reservation leaked")
	}
}
