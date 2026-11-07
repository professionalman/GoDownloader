package job

import (
	"context"
	"errors"
	"testing"
)

// maintenanceEngine reports a managed yt-dlp activation awaiting restart.
type maintenanceEngine struct {
	dummyEngine
	pending bool
}

func (e *maintenanceEngine) MaintenancePending() bool { return e.pending }

func TestRestartRequired_NewMediaRefusedWithTypedError(t *testing.T) {
	repo := newFakeJobRepository()
	eng := &maintenanceEngine{dummyEngine: dummyEngine{available: true}, pending: true}
	reg := &dummyRegistry{
		engines:  map[string]IEngine{"ytdlp": eng},
		detectFn: func(string) string { return "ytdlp" },
	}
	mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil, t.TempDir())
	ctx := context.Background()

	_, err := mgr.Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrYtDlpRestartRequired {
		t.Fatalf("expected %s, got %v", ErrYtDlpRestartRequired, err)
	}
	if jobs, _ := repo.List(ctx); len(jobs) != 0 {
		t.Fatalf("refused submission persisted %d job(s)", len(jobs))
	}

	_ = repo.Create(ctx, &Job{ID: "aw", Type: TypeMedia, Engine: "ytdlp", Status: StatusAwaitingSelection,
		MediaInfo: &MediaInfo{Formats: []MediaFormat{{FormatID: "18"}}}})
	if _, err := mgr.SelectFormat(ctx, "aw", "18"); !errors.As(err, &appErr) || appErr.Code != ErrYtDlpRestartRequired {
		t.Fatalf("SelectFormat: expected %s, got %v", ErrYtDlpRestartRequired, err)
	}
	if j, _ := repo.GetByID(ctx, "aw"); j.Status != StatusAwaitingSelection {
		t.Fatalf("refused selection changed status to %s", j.Status)
	}

	// No pending maintenance: Checkpoint 1 behaviour is unchanged.
	eng.pending = false
	if j, err := mgr.Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ"); err != nil || j.Status != StatusAnalyzing {
		t.Fatalf("expected normal media creation, got %+v %v", j, err)
	}
}
