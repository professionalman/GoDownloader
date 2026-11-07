package job

import (
	"context"
	"testing"

	"downloader/internal/networkpolicy"
)

type dummyEngine struct {
	available bool
}

func (d *dummyEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{}
}
func (d *dummyEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	return "gid-1", nil
}
func (d *dummyEngine) Pause(ctx context.Context, j *Job) error  { return nil }
func (d *dummyEngine) Resume(ctx context.Context, j *Job) error { return nil }
func (d *dummyEngine) Cancel(ctx context.Context, j *Job) error { return nil }
func (d *dummyEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	return &EngineStatus{Status: StatusDownloading}, nil
}
func (d *dummyEngine) Available() bool {
	return d.available
}

type dummyRegistry struct {
	engines  map[string]IEngine
	detectFn func(source string) string
}

func (r *dummyRegistry) Register(name string, e IEngine) {
	r.engines[name] = e
}
func (r *dummyRegistry) Get(name string) (IEngine, bool) {
	e, ok := r.engines[name]
	return e, ok
}
func (r *dummyRegistry) Detect(source string) string {
	if r.detectFn != nil {
		return r.detectFn(source)
	}
	return "aria2"
}

func TestRouting_MediaHostClassificationAndNoFallback(t *testing.T) {
	repo := newFakeJobRepository()
	reg := &dummyRegistry{
		engines: make(map[string]IEngine),
		detectFn: func(source string) string {
			// standard media classification
			return "ytdlp"
		},
	}
	mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil)
	ctx := context.Background()

	// 1. yt-dlp is NOT registered -> must return ErrYtDlpUnavailable, NEVER fall back to aria2
	_, err := mgr.Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil {
		t.Fatal("expected Create to fail when ytdlp unavailable, got nil")
	}
	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrYtDlpUnavailable {
		t.Fatalf("expected ErrYtDlpUnavailable, got: %v", err)
	}

	// Verify no jobs were persisted
	jobs, _ := repo.List(ctx)
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs persisted on refusal, got %d", len(jobs))
	}

	// 2. yt-dlp registered but Available() == false -> still returns ErrYtDlpUnavailable
	reg.Register("ytdlp", &dummyEngine{available: false})
	_, err = mgr.Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil {
		t.Fatal("expected Create to fail when ytdlp Available() is false, got nil")
	}
	appErr, ok = err.(*AppError)
	if !ok || appErr.Code != ErrYtDlpUnavailable {
		t.Fatalf("expected ErrYtDlpUnavailable when engine reports unavailable, got: %v", err)
	}

	// 3. yt-dlp registered and Available() == true -> creates media job in StatusAnalyzing
	reg.Register("ytdlp", &dummyEngine{available: true})
	j, err := mgr.Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("expected Create to succeed when ytdlp available, got: %v", err)
	}
	if j.Type != TypeMedia {
		t.Errorf("expected TypeMedia, got %s", j.Type)
	}
	if j.Engine != "ytdlp" {
		t.Errorf("expected Engine ytdlp, got %s", j.Engine)
	}
	if j.Status != StatusAnalyzing {
		t.Errorf("expected StatusAnalyzing, got %s", j.Status)
	}
}

func TestRouting_DirectDownloadURLRoutesToAria2(t *testing.T) {
	repo := newFakeJobRepository()
	reg := &dummyRegistry{
		engines: make(map[string]IEngine),
		detectFn: func(source string) string {
			return "aria2"
		},
	}
	reg.Register("aria2", &dummyEngine{available: true})
	mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil)
	ctx := context.Background()

	j, err := mgr.Create(ctx, "https://example.com/archive.zip")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if j.Type != TypeDownload {
		t.Errorf("expected TypeDownload, got %s", j.Type)
	}
	if j.Engine != "aria2" {
		t.Errorf("expected Engine aria2, got %s", j.Engine)
	}
}
