package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"

	"downloader/internal/job"
)

// createJobRequest is the request body for POST /api/v1/jobs.
type createJobRequest struct {
	Source string `json:"source"`
}

type selectFormatRequest struct {
	FormatID string `json:"formatId"`
}

// apiError is the consistent error response format.
type apiError struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Handler contains HTTP handlers for the download API.
type Handler struct {
	manager *job.Manager
}

// NewHandler creates a new API handler.
func NewHandler(manager *job.Manager) *Handler {
	return &Handler{manager: manager}
}

// CreateJob handles POST /api/v1/jobs
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if req.Source == "" {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "source URL is required")
		return
	}

	j, err := h.manager.Create(r.Context(), req.Source)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, j)
}

// GetJobs handles GET /api/v1/jobs
func (h *Handler) GetJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.manager.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to fetch jobs")
		return
	}

	if jobs == nil {
		jobs = []job.Job{}
	}

	writeJSON(w, http.StatusOK, jobs)
}

// GetJob handles GET /api/v1/jobs/{id}
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if j == nil {
		writeError(w, http.StatusNotFound, job.ErrJobNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// PauseJob handles POST /api/v1/jobs/{id}/pause
func (h *Handler) PauseJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.Pause(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// ResumeJob handles POST /api/v1/jobs/{id}/resume
func (h *Handler) ResumeJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.Resume(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// RetryJob handles POST /api/v1/jobs/{id}/retry
func (h *Handler) RetryJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.Retry(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// CancelJob handles POST /api/v1/jobs/{id}/cancel
func (h *Handler) CancelJob(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.Cancel(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// SelectFormat handles POST /api/v1/jobs/{id}/format
func (h *Handler) SelectFormat(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var req selectFormatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if req.FormatID == "" {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "formatId is required")
		return
	}

	j, err := h.manager.SelectFormat(r.Context(), id, req.FormatID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, httpStatus int, code, message string) {
	writeJSON(w, httpStatus, apiError{
		Error: errorBody{Code: code, Message: message},
	})
}

func writeAppError(w http.ResponseWriter, err error) {
	if appErr, ok := err.(*job.AppError); ok {
		httpStatus := http.StatusBadRequest
		switch appErr.Code {
		case job.ErrJobNotFound:
			httpStatus = http.StatusNotFound
		case job.ErrInternalError:
			httpStatus = http.StatusInternalServerError
		case job.ErrEngineError:
			httpStatus = http.StatusServiceUnavailable
		}
		writeError(w, httpStatus, appErr.Code, appErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, job.ErrInternalError, "an internal error occurred")
}
