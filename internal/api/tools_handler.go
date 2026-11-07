package api

import (
	"context"
	"net/http"

	"downloader/internal/toolmanager"
)

// ManagedToolService is the narrow managed yt-dlp surface exposed over HTTP. Requests carry no URLs,
// repositories, channels, keys, checksums, or paths: trust and ownership stay inside ToolManager.
type ManagedToolService interface {
	YtdlpStatus(ctx context.Context) toolmanager.ManagedToolStatus
	InstallYtdlp(ctx context.Context) (*toolmanager.AcquisitionResult, error)
	UpdateYtdlp(ctx context.Context) (*toolmanager.AcquisitionResult, error)
	CheckYtdlpUpdate(ctx context.Context) (*toolmanager.UpdateCheckResult, error)
}

// SetManagedToolService wires the managed tool service.
func (h *Handler) SetManagedToolService(svc ManagedToolService) {
	h.tools = svc
}

func (h *Handler) requireTools(w http.ResponseWriter) bool {
	if h.tools == nil {
		writeError(w, http.StatusServiceUnavailable, "TOOLS_UNAVAILABLE", "managed tool service is not available")
		return false
	}
	return true
}

// writeAcquisitionError maps acquisition failures to stable codes with safe, path- and URL-free messages.
func writeAcquisitionError(w http.ResponseWriter, err error) {
	code := toolmanager.AcquisitionErrorCode(err)
	status := http.StatusBadGateway
	switch code {
	case "UNSUPPORTED_PLATFORM":
		status = http.StatusNotImplemented
	case "RESTART_REQUIRED", "ACQUISITION_IN_PROGRESS", "TOOL_IN_USE", "DOWNGRADE_REFUSED":
		status = http.StatusConflict
	case "ACTIVATION_FAILED":
		status = http.StatusInternalServerError
	}
	writeError(w, status, code, toolmanager.AcquisitionErrorMessage(code))
}

// GetYtdlpTool handles GET /api/v1/tools/yt-dlp.
func (h *Handler) GetYtdlpTool(w http.ResponseWriter, r *http.Request) {
	if !h.requireTools(w) {
		return
	}
	writeJSON(w, http.StatusOK, h.tools.YtdlpStatus(r.Context()))
}

// InstallYtdlpTool handles POST /api/v1/tools/yt-dlp/install.
func (h *Handler) InstallYtdlpTool(w http.ResponseWriter, r *http.Request) {
	if !h.requireTools(w) {
		return
	}
	res, err := h.tools.InstallYtdlp(r.Context())
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// UpdateYtdlpTool handles POST /api/v1/tools/yt-dlp/update.
func (h *Handler) UpdateYtdlpTool(w http.ResponseWriter, r *http.Request) {
	if !h.requireTools(w) {
		return
	}
	res, err := h.tools.UpdateYtdlp(r.Context())
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// CheckYtdlpToolUpdate handles POST /api/v1/tools/yt-dlp/check-update.
func (h *Handler) CheckYtdlpToolUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.requireTools(w) {
		return
	}
	res, err := h.tools.CheckYtdlpUpdate(r.Context())
	if err != nil {
		writeAcquisitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
