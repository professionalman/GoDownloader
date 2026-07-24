package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

// startTorrentRequest is the request body for POST /api/v1/jobs/{id}/torrent/start.
type startTorrentRequest struct {
	Files             []job.TorrentFileSelection `json:"files"`
	SeedAfterComplete bool                       `json:"seedAfterComplete"`
}

// GetTorrentFiles handles GET /api/v1/jobs/{id}/torrent/files
func (h *Handler) GetTorrentFiles(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	files, err := h.manager.GetTorrentFiles(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, files)
}

// StartTorrent handles POST /api/v1/jobs/{id}/torrent/start
func (h *Handler) StartTorrent(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var req startTorrentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "file selections are required")
		return
	}

	j, err := h.manager.StartTorrent(r.Context(), id, req.Files, req.SeedAfterComplete)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// StopSeeding handles POST /api/v1/jobs/{id}/stop-seeding
func (h *Handler) StopSeeding(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	j, err := h.manager.StopSeeding(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// CreateTorrentJob handles POST /api/v1/jobs/torrent for .torrent file uploads
func (h *Handler) CreateTorrentJob(w http.ResponseWriter, r *http.Request) {
	// Limit request body size to 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid or oversized multipart form")
		return
	}

	file, header, err := r.FormFile("torrent")
	if err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTorrentFile, "torrent file is required")
		return
	}
	defer file.Close()

	if header.Size == 0 {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTorrentFile, "torrent file is empty")
		return
	}

	// Safe filename handling
	safeFilename := filepath.Base(header.Filename)
	if !strings.HasSuffix(strings.ToLower(safeFilename), ".torrent") {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTorrentFile, "file must have .torrent extension")
		return
	}

	// Read initial header bytes to validate bencoded torrent format (starts with 'd')
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTorrentFile, "failed to read torrent file header")
		return
	}
	if n == 0 || buf[0] != 'd' {
		writeError(w, http.StatusBadRequest, job.ErrInvalidTorrentFile, "invalid torrent file format: missing bencoded dictionary")
		return
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "godownloader-*.torrent")
	if err != nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to save torrent file")
		return
	}
	tmpPath := tmpFile.Name()

	// Write header bytes + remaining content
	if _, err := tmpFile.Write(buf[:n]); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to save torrent file")
		return
	}

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to save torrent file")
		return
	}
	tmpFile.Close()

	j, err := h.manager.CreateTorrentFromFile(r.Context(), tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, j)
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
