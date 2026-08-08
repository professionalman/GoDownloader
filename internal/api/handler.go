package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/tracker"
)

// createJobRequest is the request body for POST /api/v1/jobs.
type createJobRequest struct {
	Source         string                                  `json:"source"`
	Priority       job.JobPriority                         `json:"priority,omitempty"`
	CategoryID     string                                  `json:"categoryId,omitempty"`
	DestinationDir string                                  `json:"destinationDir,omitempty"`
	ConflictPolicy job.FilenameConflictPolicy              `json:"conflictPolicy,omitempty"`
	NetworkPolicy  *networkpolicy.JobNetworkPolicyOverride `json:"networkPolicy,omitempty"`
	SeedingPolicy  *networkpolicy.SeedingPolicy            `json:"seedingPolicy,omitempty"`
	Trackers       []string                                `json:"trackers,omitempty"`
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
	manager      *job.Manager
	settings     *settings.SettingsService
	categoryRepo storage.ICategoryRepository
	trackers     *tracker.Service
}

// NewHandler creates a new API handler.
func NewHandler(manager *job.Manager, settingsService ...*settings.SettingsService) *Handler {
	h := &Handler{manager: manager}
	if len(settingsService) > 0 {
		h.settings = settingsService[0]
	}
	return h
}

// SetSettingsService wires the settings service.
func (h *Handler) SetSettingsService(s *settings.SettingsService) {
	h.settings = s
}

func (h *Handler) SetTrackerService(service *tracker.Service) {
	h.trackers = service
}

// CreateJob handles POST /api/v1/jobs
func (h *Handler) CreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if req.Source == "" {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "source URL is required")
		return
	}

	j, err := h.manager.CreateWithOptions(r.Context(), req.Source, job.CreateOptions{
		Priority:       req.Priority,
		CategoryID:     req.CategoryID,
		DestinationDir: req.DestinationDir,
		ConflictPolicy: req.ConflictPolicy,
		NetworkPolicy:  req.NetworkPolicy,
		SeedingPolicy:  req.SeedingPolicy,
		Trackers:       req.Trackers,
	})
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
	Files             []job.TorrentFileSelection   `json:"files"`
	SeedAfterComplete *bool                        `json:"seedAfterComplete,omitempty"`
	SeedingPolicy     *networkpolicy.SeedingPolicy `json:"seedingPolicy,omitempty"`
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
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "file selections are required")
		return
	}

	if req.SeedingPolicy != nil && req.SeedAfterComplete != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "seedingPolicy and seedAfterComplete cannot both be supplied")
		return
	}
	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	if req.SeedingPolicy != nil {
		policy = *req.SeedingPolicy
	} else if req.SeedAfterComplete != nil && *req.SeedAfterComplete {
		policy.Mode = networkpolicy.SeedingModeUnlimited
	}
	j, err := h.manager.StartTorrentWithPolicy(r.Context(), id, req.Files, policy)
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

	priorityStr := r.FormValue("priority")
	categoryID := r.FormValue("categoryId")
	destinationDir := r.FormValue("destinationDir")
	var networkOverride *networkpolicy.JobNetworkPolicyOverride
	var seedingPolicy *networkpolicy.SeedingPolicy
	var customTrackers []string
	if raw := r.FormValue("networkPolicy"); raw != "" {
		networkOverride = &networkpolicy.JobNetworkPolicyOverride{}
		if err := decodeStrictBytes([]byte(raw), networkOverride); err != nil {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadRequest, job.ErrInvalidNetworkPolicy, "invalid networkPolicy")
			return
		}
	}
	if raw := r.FormValue("seedingPolicy"); raw != "" {
		seedingPolicy = &networkpolicy.SeedingPolicy{}
		if err := decodeStrictBytes([]byte(raw), seedingPolicy); err != nil {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadRequest, job.ErrInvalidSeedingPolicy, "invalid seedingPolicy")
			return
		}
	}
	if raw := r.FormValue("trackers"); raw != "" {
		if err := decodeStrictBytes([]byte(raw), &customTrackers); err != nil {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadRequest, job.ErrInvalidTrackerURL, "invalid trackers")
			return
		}
	}

	p := job.JobPriorityNormal
	if priorityStr != "" {
		p = job.JobPriority(priorityStr)
		if !job.ValidJobPriority(p) {
			os.Remove(tmpPath)
			writeError(w, http.StatusBadRequest, job.ErrInvalidPriority, fmt.Sprintf("invalid priority: %s", priorityStr))
			return
		}
	}

	j, err := h.manager.CreateTorrentFromFileWithOptions(r.Context(), tmpPath, job.CreateOptions{
		Priority:       p,
		CategoryID:     categoryID,
		DestinationDir: destinationDir,
		ConflictPolicy: job.ConflictPolicyEngineManaged,
		NetworkPolicy:  networkOverride,
		SeedingPolicy:  seedingPolicy,
		Trackers:       customTrackers,
	})
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
		case job.ErrCapabilityNotSupported, job.ErrPrivateTorrentTrackerRejected:
			httpStatus = http.StatusUnprocessableEntity
		case job.ErrInvalidJobState, job.ErrNetworkSettingStateAmbiguous, job.ErrSeedingPolicyStateAmbiguous,
			job.ErrTorrentAlreadyManaged, job.ErrTorrentAlreadyExistsExternally:
			httpStatus = http.StatusConflict
		case job.ErrNetworkSettingApplicationFailed, job.ErrSeedingPolicyApplicationFailed:
			httpStatus = http.StatusServiceUnavailable
		case job.ErrSecretStorageUnavailable:
			httpStatus = http.StatusServiceUnavailable
		case job.ErrInsufficientDiskSpace:
			httpStatus = http.StatusInsufficientStorage
		case job.ErrStorageError:
			httpStatus = http.StatusInternalServerError
		}
		log.Printf("api: %s: %s", appErr.Code, appErr.Message)
		writeError(w, httpStatus, appErr.Code, appErr.Message)
		return
	}
	log.Printf("api: unhandled error: type=%T err=%v", err, err)
	writeError(w, http.StatusInternalServerError, job.ErrInternalError, "an internal error occurred")
}

// CreateBatchJobs handles POST /api/v1/jobs/batch
func (h *Handler) CreateBatchJobs(w http.ResponseWriter, r *http.Request) {
	var req job.CreateBatchRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	resp, err := h.manager.CreateBatch(r.Context(), req)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// BulkAction handles POST /api/v1/jobs/bulk
func (h *Handler) BulkAction(w http.ResponseWriter, r *http.Request) {
	var req job.BulkActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	resp, err := h.manager.BulkAction(r.Context(), req)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// SetJobPriority handles PUT /api/v1/jobs/{id}/priority
func (h *Handler) SetJobPriority(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	var req job.SetPriorityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	j, err := h.manager.SetJobPriority(r.Context(), id, req.Priority)
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, j)
}

// GetQueueSnapshot handles GET /api/v1/queue
func (h *Handler) GetQueueSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.manager.GetQueueSnapshot(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// ReorderQueue handles PUT /api/v1/queue/reorder
func (h *Handler) ReorderQueue(w http.ResponseWriter, r *http.Request) {
	var req job.QueueReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	if err := h.manager.ReorderQueue(r.Context(), req.Priority, req.JobIDs); err != nil {
		writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetSettings handles GET /api/v1/settings
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "settings service not available")
		return
	}

	st, err := h.settings.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, st)
}

// UpdateSettings handles PUT /api/v1/settings
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "settings service not available")
		return
	}

	var req settings.UpdateSettingsRequest
	if err := decodeStrictJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	var st *settings.AppSettings
	var err error
	if err := h.settings.ValidateUpdate(r.Context(), &req); err != nil {
		status := http.StatusBadRequest
		code := job.ErrInvalidRequest
		if errors.Is(err, securestore.ErrUnavailable) {
			status = http.StatusServiceUnavailable
			code = job.ErrSecretStorageUnavailable
		}
		writeError(w, status, code, err.Error())
		return
	}

	if req.Queue != nil {
		st, err = h.settings.UpdateQueueSettings(r.Context(), req.Queue.MaxConcurrentDownloads)
		if err != nil {
			writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, err.Error())
			return
		}
	}

	if req.Storage != nil {
		st, err = h.settings.UpdateStorageSettings(r.Context(), &req)
		if err != nil {
			writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, err.Error())
			return
		}
	}

	if req.Network != nil || req.Torrent != nil {
		st, err = h.settings.UpdatePowerSettings(r.Context(), &req)
		if err != nil {
			code := job.ErrInvalidNetworkPolicy
			status := http.StatusBadRequest
			if errors.Is(err, securestore.ErrUnavailable) {
				code = job.ErrSecretStorageUnavailable
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, code, err.Error())
			return
		}
		if h.manager != nil {
			st.ApplicationResults = append(st.ApplicationResults, h.manager.ReconcileNetworkPoliciesWithResults(r.Context())...)
		}
	}

	if st == nil {
		st, err = h.settings.GetSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, job.ErrInternalError, err.Error())
			return
		}
	}

	if m := h.manager; m != nil {
		if s := m.GetScheduler(); s != nil {
			s.Kick()
		}
	}

	writeJSON(w, http.StatusOK, st)
}
