package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/gorilla/mux"

	"downloader/internal/job"
	"downloader/internal/storage"
)

type createCategoryRequest struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
}

type updateCategoryRequest struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
}

// SetCategoryRepository wires the category repository on Handler.
func (h *Handler) SetCategoryRepository(repo storage.ICategoryRepository) {
	h.categoryRepo = repo
}

// GetCategories handles GET /api/v1/categories
func (h *Handler) GetCategories(w http.ResponseWriter, r *http.Request) {
	if h.categoryRepo == nil {
		writeJSON(w, http.StatusOK, []storage.CategoryResponse{})
		return
	}

	cats, err := h.categoryRepo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "failed to list categories")
		return
	}

	// Resolve directories for UI response
	defaultDir := h.manager.GetEffectiveDefaultDownloadDir(r.Context())
	resp := make([]storage.CategoryResponse, 0, len(cats))
	for _, c := range cats {
		resDir := c.Directory
		if !filepath.IsAbs(c.Directory) {
			resDir = filepath.Join(defaultDir, c.Directory)
		}
		resp = append(resp, storage.CategoryResponse{
			ID:                c.ID,
			Name:              c.Name,
			Directory:         c.Directory,
			ResolvedDirectory: resDir,
			CreatedAt:         c.CreatedAt,
			UpdatedAt:         c.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// CreateCategory handles POST /api/v1/categories
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if h.categoryRepo == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "category repository not configured")
		return
	}

	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	cat := &storage.Category{
		Name:      req.Name,
		Directory: req.Directory,
	}

	if err := h.categoryRepo.Create(r.Context(), cat); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrCategoryNameConflict, err.Error())
		return
	}

	defaultDir := h.manager.GetEffectiveDefaultDownloadDir(r.Context())
	resDir := cat.Directory
	if !filepath.IsAbs(cat.Directory) {
		resDir = filepath.Join(defaultDir, cat.Directory)
	}

	writeJSON(w, http.StatusCreated, storage.CategoryResponse{
		ID:                cat.ID,
		Name:              cat.Name,
		Directory:         cat.Directory,
		ResolvedDirectory: resDir,
		CreatedAt:         cat.CreatedAt,
		UpdatedAt:         cat.UpdatedAt,
	})
}

// UpdateCategory handles PUT /api/v1/categories/{id}
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	if h.categoryRepo == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "category repository not configured")
		return
	}

	id := mux.Vars(r)["id"]
	if id == "" {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "category ID is required")
		return
	}

	var req updateCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "invalid request body")
		return
	}

	existing, err := h.categoryRepo.GetByID(r.Context(), id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, job.ErrCategoryNotFound, "category not found")
		return
	}

	existing.Name = req.Name
	existing.Directory = req.Directory

	if err := h.categoryRepo.Update(r.Context(), existing); err != nil {
		writeError(w, http.StatusBadRequest, job.ErrCategoryNameConflict, err.Error())
		return
	}

	defaultDir := h.manager.GetEffectiveDefaultDownloadDir(r.Context())
	resDir := existing.Directory
	if !filepath.IsAbs(existing.Directory) {
		resDir = filepath.Join(defaultDir, existing.Directory)
	}

	writeJSON(w, http.StatusOK, storage.CategoryResponse{
		ID:                existing.ID,
		Name:              existing.Name,
		Directory:         existing.Directory,
		ResolvedDirectory: resDir,
		CreatedAt:         existing.CreatedAt,
		UpdatedAt:         existing.UpdatedAt,
	})
}

// DeleteCategory handles DELETE /api/v1/categories/{id}
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	if h.categoryRepo == nil {
		writeError(w, http.StatusInternalServerError, job.ErrInternalError, "category repository not configured")
		return
	}

	id := mux.Vars(r)["id"]
	if id == "" {
		writeError(w, http.StatusBadRequest, job.ErrInvalidRequest, "category ID is required")
		return
	}

	if err := h.categoryRepo.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, job.ErrCategoryNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
