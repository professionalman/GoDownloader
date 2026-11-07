package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"downloader/internal/job"
)

type selectCancelRegistry struct{}

func (selectCancelRegistry) Get(name string) (job.IEngine, bool) { return nil, false }
func (selectCancelRegistry) Detect(source string) string         { return "ytdlp" }

type selectCancelBus struct{}

func (selectCancelBus) Publish(e job.Event)             { _ = e }
func (selectCancelBus) Subscribe() <-chan job.Event     { return nil }
func (selectCancelBus) Unsubscribe(ch <-chan job.Event) { _ = ch }

func createAwaitingSelectionJob(t *testing.T, repo *SQLiteJobRepository, id string) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	if err := repo.Create(context.Background(), &job.Job{
		ID:       id,
		Source:   "https://youtube.com/watch?v=" + id,
		Name:     "Video " + id,
		Status:   job.StatusAwaitingSelection,
		Type:     job.TypeMedia,
		Engine:   "ytdlp",
		Priority: job.JobPriorityNormal,
		MediaInfo: &job.MediaInfo{
			Title:   "Video " + id,
			Formats: []job.MediaFormat{{FormatID: "18"}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
}

func runnableContains(t *testing.T, queue *SQLiteQueueRepository, id string) bool {
	t.Helper()
	runnable, err := queue.ListRunnable(context.Background())
	if err != nil {
		t.Fatalf("ListRunnable failed: %v", err)
	}
	for _, qj := range runnable {
		if qj.JobID == id {
			return true
		}
	}
	return false
}

// Real SQLite transactions: selection and cancellation race on the same row. Exactly one compare-and-set wins,
// and job status, media selection, and queue membership never diverge.
func TestRepository_SelectMediaFormatVersusCancel_Atomic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	queue := NewSQLiteQueueRepository(db)
	ctx := context.Background()

	selectWins, cancelWins := 0, 0
	for round := 0; round < 100; round++ {
		id := fmt.Sprintf("sc-%d", round)
		createAwaitingSelectionJob(t, repo, id)

		info := &job.MediaInfo{Title: "Video", Formats: []job.MediaFormat{{FormatID: "18"}}, SelectedFmt: "18"}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var selWon, canWon bool
		var selErr, canErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			now := time.Now()
			selWon, selErr = repo.SelectMediaFormat(ctx, id, info, &job.QueueEntry{Action: job.QueueActionStart, EnqueuedAt: now, UpdatedAt: now})
		}()
		go func() {
			defer wg.Done()
			<-start
			canWon, canErr = repo.CancelAwaitingSelection(ctx, id)
		}()
		close(start)
		wg.Wait()

		if selErr != nil || canErr != nil {
			t.Fatalf("round %d: unexpected errors select=%v cancel=%v", round, selErr, canErr)
		}
		if selWon == canWon {
			t.Fatalf("round %d: expected exactly one winner, select=%v cancel=%v", round, selWon, canWon)
		}

		persisted, _ := repo.GetByID(ctx, id)
		entry, _ := queue.Get(ctx, id)
		if selWon {
			selectWins++
			if persisted.Status != job.StatusQueued || persisted.MediaInfo == nil || persisted.MediaInfo.SelectedFmt != "18" || entry == nil {
				t.Fatalf("round %d: selection won but state diverged: status=%s entry=%v", round, persisted.Status, entry != nil)
			}
		} else {
			cancelWins++
			if persisted.Status != job.StatusCancelled || entry != nil || runnableContains(t, queue, id) {
				t.Fatalf("round %d: cancel won but state diverged: status=%s entry=%v", round, persisted.Status, entry != nil)
			}
			if persisted.MediaInfo != nil && persisted.MediaInfo.SelectedFmt != "" {
				t.Fatalf("round %d: selection recorded on cancelled job", round)
			}
		}
	}
	t.Logf("select won %d rounds, cancel won %d rounds", selectWins, cancelWins)
}

// Real SQLite, full Manager: concurrent SelectFormat and Cancel always end CANCELLED with no queue entry and
// nothing runnable, regardless of which committed first.
func TestManager_SelectFormatVersusCancel_SQLite(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	queue := NewSQLiteQueueRepository(db)
	ctx := context.Background()

	mgr := job.NewManager(repo, selectCancelRegistry{}, selectCancelBus{}, t.TempDir(), nil, t.TempDir())
	mgr.SetQueueRepository(queue)
	// Unstarted scheduler: production wiring without dispatch, so a runnable entry would be an orphan.
	mgr.SetScheduler(job.NewScheduler(repo, queue, nil, func(ctx context.Context, qj *job.QueuedJob) error {
		return errors.New("dispatch must not run")
	}))

	for round := 0; round < 50; round++ {
		id := fmt.Sprintf("mgr-sc-%d", round)
		createAwaitingSelectionJob(t, repo, id)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var cancelErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = mgr.Cancel(ctx, id)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = mgr.SelectFormat(ctx, id, "18")
		}()
		close(start)
		wg.Wait()

		if cancelErr != nil {
			t.Fatalf("round %d: Cancel failed: %v", round, cancelErr)
		}
		persisted, _ := repo.GetByID(ctx, id)
		entry, _ := queue.Get(ctx, id)
		if persisted.Status != job.StatusCancelled || entry != nil || runnableContains(t, queue, id) {
			t.Fatalf("round %d: expected CANCELLED with no queue entry, got status=%s entry=%v", round, persisted.Status, entry != nil)
		}
	}
}
