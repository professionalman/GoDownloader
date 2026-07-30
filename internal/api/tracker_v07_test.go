package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
	"downloader/internal/tracker"

	"github.com/gorilla/mux"
)

type trackerRouteRepository struct {
	source    *networkpolicy.TrackerSource
	getErr    error
	createErr error
	updateErr error
	deleteErr error
}

func (r *trackerRouteRepository) List(context.Context) ([]networkpolicy.TrackerSource, error) {
	if r.source == nil {
		return []networkpolicy.TrackerSource{}, nil
	}
	return []networkpolicy.TrackerSource{*r.source}, nil
}
func (r *trackerRouteRepository) Get(context.Context, string) (*networkpolicy.TrackerSource, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.source == nil {
		return nil, nil
	}
	copySource := *r.source
	return &copySource, nil
}
func (r *trackerRouteRepository) Create(_ context.Context, source *networkpolicy.TrackerSource) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.source = source
	return nil
}
func (r *trackerRouteRepository) Update(_ context.Context, source *networkpolicy.TrackerSource) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.source = source
	return nil
}
func (r *trackerRouteRepository) Delete(context.Context, string) error {
	return r.deleteErr
}
func (r *trackerRouteRepository) Entries(context.Context, string) ([]string, error) {
	return nil, nil
}
func (r *trackerRouteRepository) EnabledEntries(context.Context) ([]string, error) {
	return nil, nil
}
func (r *trackerRouteRepository) ReplaceEntries(context.Context, *networkpolicy.TrackerSource, []string) error {
	return nil
}
func (r *trackerRouteRepository) RecordFailure(context.Context, string, string, time.Time) error {
	return nil
}

func trackerHandler(repository tracker.Repository) *Handler {
	handler := NewHandler(nil)
	handler.SetTrackerService(tracker.NewService(repository, nil))
	return handler
}

func trackerRequest(method, target, id, body string) (*http.Request, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if id != "" {
		request = mux.SetURLVars(request, map[string]string{"id": id})
	}
	return request, httptest.NewRecorder()
}

func TestUpdateTrackerSourceErrorClassification(t *testing.T) {
	t.Run("invalid update is 400", func(t *testing.T) {
		repository := &trackerRouteRepository{source: &networkpolicy.TrackerSource{ID: "one"}}
		request, response := trackerRequest(http.MethodPut, "/api/v1/tracker-sources/one", "one",
			`{"name":"source","url":"http://localhost/list","enabled":true,"refreshIntervalSeconds":899}`)
		trackerHandler(repository).UpdateTrackerSource(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"INVALID_REQUEST"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("missing source is 404", func(t *testing.T) {
		request, response := trackerRequest(http.MethodPut, "/api/v1/tracker-sources/missing", "missing",
			`{"name":"source","url":"http://localhost/list","enabled":true,"refreshIntervalSeconds":900}`)
		trackerHandler(&trackerRouteRepository{}).UpdateTrackerSource(response, request)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"TRACKER_SOURCE_NOT_FOUND"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("repository failure is 500 and sanitized", func(t *testing.T) {
		repository := &trackerRouteRepository{getErr: errors.New("database password leaked")}
		request, response := trackerRequest(http.MethodPut, "/api/v1/tracker-sources/one", "one",
			`{"name":"source","url":"http://localhost/list","enabled":true,"refreshIntervalSeconds":900}`)
		trackerHandler(repository).UpdateTrackerSource(response, request)
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"INTERNAL_ERROR"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "database password") {
			t.Fatalf("repository detail leaked: %s", response.Body.String())
		}
	})
}

func TestTrackerCreateDeleteAndRefreshRepositoryFailuresAreInternal(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		request, response := trackerRequest(http.MethodPost, "/api/v1/tracker-sources", "",
			`{"name":"source","url":"http://localhost/list","enabled":true,"refreshIntervalSeconds":900}`)
		trackerHandler(&trackerRouteRepository{createErr: errors.New("database unavailable")}).CreateTrackerSource(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("delete", func(t *testing.T) {
		repository := &trackerRouteRepository{
			source:    &networkpolicy.TrackerSource{ID: "one"},
			deleteErr: errors.New("database unavailable"),
		}
		request, response := trackerRequest(http.MethodDelete, "/api/v1/tracker-sources/one", "one", "")
		trackerHandler(repository).DeleteTrackerSource(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("refresh", func(t *testing.T) {
		request, response := trackerRequest(http.MethodPost, "/api/v1/tracker-sources/one/refresh", "one", "")
		trackerHandler(&trackerRouteRepository{getErr: errors.New("database unavailable")}).RefreshTrackerSource(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}
