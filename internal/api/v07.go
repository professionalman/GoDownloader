package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
	"downloader/internal/tracker"

	"github.com/gorilla/mux"
)

func (h *Handler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": h.manager.CapabilityProfiles()})
}

func (h *Handler) ResolveCapabilities(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Source json.RawMessage `json:"source"`
	}
	if err := decodeStrictJSON(r, &request); err != nil || len(request.Source) == 0 {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "source is required")
		return
	}
	var sources []string
	if err := json.Unmarshal(request.Source, &sources); err != nil {
		var source string
		if stringErr := json.Unmarshal(request.Source, &source); stringErr != nil || source == "" {
			writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "source must be a string or string array")
			return
		}
		sources = []string{source}
	}
	if len(sources) == 0 {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "at least one source is required")
		return
	}
	writeJSON(w, http.StatusOK, h.manager.ResolveCapabilities(sources))
}

func (h *Handler) GetJobCapabilities(w http.ResponseWriter, r *http.Request) {
	result, err := h.manager.JobCapabilities(r.Context(), mux.Vars(r)["id"])
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) UpdateJobNetwork(w http.ResponseWriter, r *http.Request) {
	var request job.NetworkLimitUpdate
	if err := decodeStrictJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidNetworkPolicy, "invalid network limit request")
		return
	}
	result, err := h.manager.UpdateNetworkLimits(r.Context(), mux.Vars(r)["id"], request)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) AddTorrentTrackers(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Trackers []string `json:"trackers"`
	}
	if err := decodeStrictJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTrackerURL, "invalid tracker request")
		return
	}
	result, err := h.manager.AddTorrentTrackers(r.Context(), mux.Vars(r)["id"], request.Trackers)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trackers": result})
}

func (h *Handler) UpdateSeedingPolicy(w http.ResponseWriter, r *http.Request) {
	var policy networkpolicy.SeedingPolicy
	if err := decodeStrictJSON(r, &policy); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidSeedingPolicy, "invalid seeding policy")
		return
	}
	result, err := h.manager.UpdateSeedingPolicy(r.Context(), mux.Vars(r)["id"], policy)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetTrackerSources(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	sources, err := h.trackers.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to list tracker sources")
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

func (h *Handler) CreateTrackerSource(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	var input networkpolicy.TrackerSourceInput
	if err := decodeStrictJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid tracker source")
		return
	}
	source, err := h.trackers.Create(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, source)
}

func (h *Handler) UpdateTrackerSource(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	var input networkpolicy.TrackerSourceInput
	if err := decodeStrictJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid tracker source")
		return
	}
	source, err := h.trackers.Update(r.Context(), mux.Vars(r)["id"], input)
	if err != nil || source == nil {
		writeError(w, http.StatusNotFound, job.ErrTrackerSourceNotFound, "tracker source not found")
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) DeleteTrackerSource(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	if err := h.trackers.Delete(r.Context(), mux.Vars(r)["id"]); err != nil {
		if errors.Is(err, tracker.ErrNotFound) {
			writeError(w, http.StatusNotFound, job.ErrTrackerSourceNotFound, "tracker source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to delete tracker source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RefreshTrackerSource(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	source, err := h.trackers.Refresh(r.Context(), mux.Vars(r)["id"])
	if err != nil {
		if errors.Is(err, tracker.ErrNotFound) {
			writeError(w, http.StatusNotFound, job.ErrTrackerSourceNotFound, "tracker source not found")
			return
		}
		writeError(w, http.StatusServiceUnavailable, job.ErrTrackerSourceFetchFailed, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) RefreshAllTrackerSources(w http.ResponseWriter, r *http.Request) {
	if h.trackers == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "tracker service unavailable")
		return
	}
	failures := h.trackers.RefreshAll(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"failureCount": len(failures)})
}
