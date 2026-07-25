package storage

import (
	"context"
	"time"
)

// FilenameConflictPolicy defines the strategy when a destination file already exists.
type FilenameConflictPolicy string

const (
	ConflictPolicyRename        FilenameConflictPolicy = "rename"
	ConflictPolicyOverwrite     FilenameConflictPolicy = "overwrite"
	ConflictPolicyFail          FilenameConflictPolicy = "fail"
	ConflictPolicyEngineManaged FilenameConflictPolicy = "engine_managed"
)

// ValidConflictPolicy returns true if the policy string is a valid user-facing policy.
func ValidConflictPolicy(p FilenameConflictPolicy) bool {
	switch p {
	case ConflictPolicyRename, ConflictPolicyOverwrite, ConflictPolicyFail, ConflictPolicyEngineManaged:
		return true
	}
	return false
}

// Category defines a download destination preset.
type Category struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CategoryResponse includes both raw directory and resolved absolute directory for UI consumption.
type CategoryResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Directory         string    `json:"directory"`
	ResolvedDirectory string    `json:"resolvedDirectory"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// StorageResolution holds the resolved storage fields for a new Job snapshot.
type StorageResolution struct {
	CategoryID     string                 `json:"categoryId,omitempty"`
	DestinationDir string                 `json:"destinationDir"`
	WorkDir        string                 `json:"workDir,omitempty"`
	ConflictPolicy FilenameConflictPolicy `json:"conflictPolicy"`
}

// IStorageService manages path resolution, preflight checks, file finalization, and workdir lifecycles.
type IStorageService interface {
	GetEffectiveDefaultDownloadDir(ctx context.Context) string
	ResolveDestination(ctx context.Context, categoryID, customDest string, policy FilenameConflictPolicy, jobID string, isMedia bool) (*StorageResolution, error)
	PrepareWorkDir(ctx context.Context, jobID, workDir string) error
	Preflight(ctx context.Context, destinationDir, workDir string, totalBytes, completedBytes int64) error
	FinalizeFile(ctx context.Context, srcPath, destinationDir string, policy FilenameConflictPolicy) (finalPath string, err error)
	CleanupWorkDir(ctx context.Context, jobID, workDir string) error
	CleanupStaleWorkDirs(ctx context.Context, activeJobIDs map[string]bool) error
}

// ICategoryRepository manages persistence for download categories in SQLite.
type ICategoryRepository interface {
	Create(ctx context.Context, cat *Category) error
	GetByID(ctx context.Context, id string) (*Category, error)
	GetByName(ctx context.Context, name string) (*Category, error)
	List(ctx context.Context) ([]Category, error)
	Update(ctx context.Context, cat *Category) error
	Delete(ctx context.Context, id string) error
}

// IFreeSpaceProvider fetches available disk free space for a given path.
type IFreeSpaceProvider interface {
	FreeBytes(path string) (int64, error)
}
