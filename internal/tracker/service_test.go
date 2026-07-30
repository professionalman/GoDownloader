package tracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type memoryRepository struct {
	mu        sync.Mutex
	source    *networkpolicy.TrackerSource
	entries   []string
	listErr   error
	getErr    error
	updateErr error
}

func (m *memoryRepository) List(context.Context) ([]networkpolicy.TrackerSource, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.source == nil {
		return []networkpolicy.TrackerSource{}, nil
	}
	return []networkpolicy.TrackerSource{*m.source}, nil
}
func (m *memoryRepository) Get(context.Context, string) (*networkpolicy.TrackerSource, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.source == nil {
		return nil, nil
	}
	copy := *m.source
	return &copy, nil
}
func (m *memoryRepository) Create(_ context.Context, source *networkpolicy.TrackerSource) error {
	m.source = source
	return nil
}
func (m *memoryRepository) Update(_ context.Context, source *networkpolicy.TrackerSource) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.source = source
	return nil
}
func (m *memoryRepository) Delete(context.Context, string) error { return nil }
func (m *memoryRepository) Entries(context.Context, string) ([]string, error) {
	return append([]string(nil), m.entries...), nil
}
func (m *memoryRepository) EnabledEntries(context.Context) ([]string, error) {
	return append([]string(nil), m.entries...), nil
}
func (m *memoryRepository) ReplaceEntries(_ context.Context, source *networkpolicy.TrackerSource, entries []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = source
	m.entries = append([]string(nil), entries...)
	return nil
}
func (m *memoryRepository) RecordFailure(_ context.Context, _ string, message string, checkedAt time.Time) error {
	m.source.LastError = message
	m.source.LastCheckedAt = &checkedAt
	return nil
}

func TestRefreshConditionalsAndLastGoodEntries(t *testing.T) {
	var notModified bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if notModified {
			if r.Header.Get("If-None-Match") != `"v1"` {
				t.Error("missing ETag conditional")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("https://tracker.example/announce\nudp://tracker.example:80/announce\n"))
	}))
	defer server.Close()
	repo := &memoryRepository{source: &networkpolicy.TrackerSource{ID: "one", Name: "local", URL: server.URL, Enabled: true, RefreshIntervalSeconds: 900}}
	service := NewService(repo, nil)
	if _, err := service.Refresh(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if len(repo.entries) != 2 {
		t.Fatalf("entries=%v", repo.entries)
	}
	notModified = true
	if _, err := service.Refresh(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if len(repo.entries) != 2 {
		t.Fatal("304 must preserve last-good entries")
	}
}

func TestFailedRefreshPreservesLastGoodEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer server.Close()
	repo := &memoryRepository{
		source:  &networkpolicy.TrackerSource{ID: "one", Name: "local", URL: server.URL, Enabled: true, RefreshIntervalSeconds: 900},
		entries: []string{"https://last-good.example/announce"},
	}
	if _, err := NewService(repo, nil).Refresh(context.Background(), "one"); !errors.Is(err, ErrFetch) {
		t.Fatalf("expected classified refresh error, got %v", err)
	}
	if len(repo.entries) != 1 || repo.entries[0] != "https://last-good.example/announce" {
		t.Fatal("failure replaced last-good entries")
	}
}

func TestSourceUpdateEnableDisableAndCustomInterval(t *testing.T) {
	repo := &memoryRepository{source: &networkpolicy.TrackerSource{
		ID: "one", Name: "source", URL: "http://127.0.0.1/list", Enabled: true, RefreshIntervalSeconds: 900,
	}}
	service := NewService(repo, nil)
	updated, err := service.Update(context.Background(), "one", networkpolicy.TrackerSourceInput{
		Name: "edited", URL: "http://localhost/trackers", Enabled: false, RefreshIntervalSeconds: 1800,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.RefreshIntervalSeconds != 1800 || updated.Name != "edited" {
		t.Fatalf("unexpected disabled update: %+v", updated)
	}
	updated, err = service.Update(context.Background(), "one", networkpolicy.TrackerSourceInput{
		Name: "edited", URL: "http://localhost/trackers", Enabled: true, RefreshIntervalSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled || updated.RefreshIntervalSeconds != 900 {
		t.Fatalf("unexpected enabled update: %+v", updated)
	}
}

func TestSourceUpdateRejectsBelowMinimumInterval(t *testing.T) {
	repo := &memoryRepository{source: &networkpolicy.TrackerSource{
		ID: "one", Name: "source", URL: "http://127.0.0.1/list", Enabled: true, RefreshIntervalSeconds: 900,
	}}
	_, err := NewService(repo, nil).Update(context.Background(), "one", networkpolicy.TrackerSourceInput{
		Name: "source", URL: "http://127.0.0.1/list", Enabled: true, RefreshIntervalSeconds: 899,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if repo.source.RefreshIntervalSeconds != 900 {
		t.Fatalf("invalid update mutated repository: %+v", repo.source)
	}
}

func TestAutomaticRefreshSkipsDisabledAndHonorsInterval(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("https://tracker.example/announce\n"))
	}))
	defer server.Close()
	now := time.Now()
	repo := &memoryRepository{source: &networkpolicy.TrackerSource{
		ID: "one", Name: "source", URL: server.URL, Enabled: false, RefreshIntervalSeconds: 900,
	}}
	service := NewService(repo, nil)
	if failures := service.refreshDue(context.Background(), now); len(failures) != 0 || requests != 0 {
		t.Fatalf("disabled source refreshed: failures=%v requests=%d", failures, requests)
	}
	repo.source.Enabled = true
	repo.source.LastCheckedAt = &now
	if failures := service.refreshDue(context.Background(), now.Add(899*time.Second)); len(failures) != 0 || requests != 0 {
		t.Fatalf("source refreshed before interval: failures=%v requests=%d", failures, requests)
	}
	if failures := service.refreshDue(context.Background(), now.Add(900*time.Second)); len(failures) != 0 || requests != 1 {
		t.Fatalf("due source was not refreshed: failures=%v requests=%d", failures, requests)
	}
}
