package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type memoryRepository struct {
	mu      sync.Mutex
	source  *networkpolicy.TrackerSource
	entries []string
}

func (m *memoryRepository) List(context.Context) ([]networkpolicy.TrackerSource, error) {
	return []networkpolicy.TrackerSource{*m.source}, nil
}
func (m *memoryRepository) Get(context.Context, string) (*networkpolicy.TrackerSource, error) {
	copy := *m.source
	return &copy, nil
}
func (m *memoryRepository) Create(_ context.Context, source *networkpolicy.TrackerSource) error {
	m.source = source
	return nil
}
func (m *memoryRepository) Update(_ context.Context, source *networkpolicy.TrackerSource) error {
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
	if _, err := NewService(repo, nil).Refresh(context.Background(), "one"); err == nil {
		t.Fatal("expected refresh error")
	}
	if len(repo.entries) != 1 || repo.entries[0] != "https://last-good.example/announce" {
		t.Fatal("failure replaced last-good entries")
	}
}
