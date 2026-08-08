package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"downloader/internal/job"
)

func TestWriteAppError_InsufficientDiskSpace_Returns507(t *testing.T) {
	w := httptest.NewRecorder()
	appErr := &job.AppError{
		Code:    job.ErrInsufficientDiskSpace,
		Message: "INSUFFICIENT_DISK_SPACE: insufficient free space in /downloads (free: 16000000000, required: 23000000000, reserve: 1073741824, remaining: 21474836480)",
	}
	writeAppError(w, appErr)

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("expected HTTP 507, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "INSUFFICIENT_DISK_SPACE") {
		t.Fatalf("response body should contain INSUFFICIENT_DISK_SPACE, got: %s", body)
	}
	if !strings.Contains(body, "free:") {
		t.Fatalf("response body should contain detailed disk info, got: %s", body)
	}
}

func TestWriteAppError_StorageError_Returns500(t *testing.T) {
	w := httptest.NewRecorder()
	appErr := &job.AppError{
		Code:    job.ErrStorageError,
		Message: "STORAGE_ERROR: failed to create directory",
	}
	writeAppError(w, appErr)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500, got %d", w.Code)
	}
}

func TestWriteAppError_UnhandledError_ReturnsSanitized500(t *testing.T) {
	w := httptest.NewRecorder()
	rawErr := fmt.Errorf("some raw internal error with path /secrets/key")
	writeAppError(w, rawErr)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500, got %d", w.Code)
	}
	body := w.Body.String()
	// Should NOT expose the raw error message
	if strings.Contains(body, "/secrets/key") {
		t.Fatalf("raw error details leaked to client: %s", body)
	}
	// Should contain sanitized message
	if !strings.Contains(body, "an internal error occurred") {
		t.Fatalf("expected sanitized message, got: %s", body)
	}
	if !strings.Contains(body, "INTERNAL_ERROR") {
		t.Fatalf("expected INTERNAL_ERROR code, got: %s", body)
	}
}
