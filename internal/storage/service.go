package storage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"downloader/internal/settings"
)

var (
	removeAllFunc = os.RemoveAll
)

var (
	ErrInvalidStorageSelection = errors.New("INVALID_STORAGE_SELECTION")
	ErrInvalidDestination      = errors.New("INVALID_DESTINATION")
	ErrCategoryNotFound        = errors.New("CATEGORY_NOT_FOUND")
	ErrCategoryNameConflict    = errors.New("CATEGORY_NAME_CONFLICT")
	ErrInsufficientDiskSpace   = errors.New("INSUFFICIENT_DISK_SPACE")
	ErrFileConflict            = errors.New("FILE_CONFLICT")
	ErrStorageError            = errors.New("STORAGE_ERROR")
)

var _ IStorageService = (*StorageService)(nil)

// StorageService implements IStorageService.
type StorageService struct {
	categoryRepo       ICategoryRepository
	settingsService    *settings.SettingsService
	freeSpaceProvider  IFreeSpaceProvider
	defaultDownloadDir string
	dataDir            string
}

// NewStorageService creates a new StorageService.
func NewStorageService(catRepo ICategoryRepository, settingsSvc *settings.SettingsService, freeSpace IFreeSpaceProvider, defaultDownloadDir, dataDir string) *StorageService {
	if freeSpace == nil {
		freeSpace = NewOSFreeSpaceProvider()
	}
	return &StorageService{
		categoryRepo:       catRepo,
		settingsService:    settingsSvc,
		freeSpaceProvider:  freeSpace,
		defaultDownloadDir: defaultDownloadDir,
		dataDir:            dataDir,
	}
}

// GetEffectiveDefaultDownloadDir returns the resolved effective default download directory.
func (s *StorageService) GetEffectiveDefaultDownloadDir(ctx context.Context) string {
	defaultDir, _, _, _ := s.getEffectiveDefaults(ctx)
	return defaultDir
}

func (s *StorageService) getEffectiveDefaults(ctx context.Context) (defaultDir, tempDir string, minFreeSpace int64, defaultPolicy FilenameConflictPolicy) {
	defaultDir = s.defaultDownloadDir
	tempDir = filepath.Join(s.dataDir, "tmp")
	minFreeSpace = 1073741824 // 1 GiB
	defaultPolicy = ConflictPolicyRename

	if s.settingsService != nil {
		appSettings, err := s.settingsService.GetSettings(ctx)
		if err == nil && appSettings != nil {
			if appSettings.Storage.EffectiveDefaultDownloadDirectory != "" {
				defaultDir = appSettings.Storage.EffectiveDefaultDownloadDirectory
			}
			if appSettings.Storage.EffectiveTemporaryDirectory != "" {
				tempDir = appSettings.Storage.EffectiveTemporaryDirectory
			}
			if appSettings.Storage.EffectiveMinimumFreeSpaceBytes >= 0 {
				minFreeSpace = appSettings.Storage.EffectiveMinimumFreeSpaceBytes
			}
			if appSettings.Storage.EffectiveDefaultConflictPolicy != "" {
				defaultPolicy = FilenameConflictPolicy(appSettings.Storage.EffectiveDefaultConflictPolicy)
			}
		}
	}

	absDefault, err := filepath.Abs(defaultDir)
	if err == nil {
		defaultDir = absDefault
	}
	absTemp, err := filepath.Abs(tempDir)
	if err == nil {
		tempDir = absTemp
	}

	return defaultDir, tempDir, minFreeSpace, defaultPolicy
}

// ResolveDestination resolves an immutable StorageResolution for a new job.
func (s *StorageService) ResolveDestination(ctx context.Context, categoryID, customDest string, policy FilenameConflictPolicy, jobID string, isMedia bool) (*StorageResolution, error) {
	categoryID = strings.TrimSpace(categoryID)
	customDest = strings.TrimSpace(customDest)

	if categoryID != "" && customDest != "" {
		return nil, fmt.Errorf("%w: cannot specify both categoryId and custom destinationDir", ErrInvalidStorageSelection)
	}

	effectiveDefaultDir, effectiveTempDir, _, defaultPolicy := s.getEffectiveDefaults(ctx)

	resolvedDest := effectiveDefaultDir
	resolvedCategoryID := ""

	if customDest != "" {
		cleaned := filepath.Clean(customDest)
		if cleaned == "" || cleaned == "." || strings.ContainsRune(cleaned, 0) {
			return nil, fmt.Errorf("%w: invalid custom destination path", ErrInvalidDestination)
		}

		if filepath.IsAbs(cleaned) {
			resolvedDest = cleaned
		} else {
			// Relative custom dest resolved under effectiveDefaultDir
			relResolved := filepath.Join(effectiveDefaultDir, cleaned)
			rel, err := filepath.Rel(effectiveDefaultDir, relResolved)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("%w: relative destination path escapes base directory", ErrInvalidDestination)
			}
			resolvedDest = relResolved
		}
	} else if categoryID != "" {
		cat, err := s.categoryRepo.GetByID(ctx, categoryID)
		if err != nil {
			return nil, fmt.Errorf("get category failed: %w", err)
		}
		if cat == nil {
			return nil, fmt.Errorf("%w: category %s not found", ErrCategoryNotFound, categoryID)
		}
		resolvedCategoryID = cat.ID
		catDir := strings.TrimSpace(cat.Directory)
		cleaned := filepath.Clean(catDir)

		if filepath.IsAbs(cleaned) {
			resolvedDest = cleaned
		} else {
			relResolved := filepath.Join(effectiveDefaultDir, cleaned)
			rel, err := filepath.Rel(effectiveDefaultDir, relResolved)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("%w: category directory path escapes base directory", ErrInvalidDestination)
			}
			resolvedDest = relResolved
		}
	}

	resolvedPolicy := policy
	if resolvedPolicy == "" {
		resolvedPolicy = defaultPolicy
	}
	if !ValidConflictPolicy(resolvedPolicy) {
		return nil, fmt.Errorf("%w: invalid conflict policy %s", ErrInvalidDestination, resolvedPolicy)
	}

	resolvedWorkDir := ""
	if isMedia {
		resolvedWorkDir = filepath.Join(effectiveTempDir, jobID)
	}

	return &StorageResolution{
		CategoryID:     resolvedCategoryID,
		DestinationDir: resolvedDest,
		WorkDir:        resolvedWorkDir,
		ConflictPolicy: resolvedPolicy,
	}, nil
}

// PrepareWorkDir creates the job's temporary work directory and writes the safety marker.
func (s *StorageService) PrepareWorkDir(ctx context.Context, jobID, workDir string) error {
	if workDir == "" {
		return nil
	}
	return WriteWorkDirMarker(workDir, jobID)
}

// Preflight checks that destinationDir and workDir have sufficient free space before engine execution.
func (s *StorageService) Preflight(ctx context.Context, destinationDir, workDir string, totalBytes, completedBytes int64) error {
	_, _, minFreeSpace, _ := s.getEffectiveDefaults(ctx)

	var remainingBytes int64
	if totalBytes > 0 {
		rem := totalBytes - completedBytes
		if rem > 0 {
			remainingBytes = rem
		}
	}

	requiredBytes := remainingBytes + minFreeSpace

	// Verify & create destination directory if needed
	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return fmt.Errorf("%w: failed to create destination directory %s: %v", ErrStorageError, destinationDir, err)
	}

	destFree, err := s.freeSpaceProvider.FreeBytes(destinationDir)
	if err != nil {
		return fmt.Errorf("%w: failed to check free space for %s: %v", ErrStorageError, destinationDir, err)
	}

	if destFree < requiredBytes {
		return fmt.Errorf("%w: insufficient free space in %s (free: %d, required: %d, reserve: %d, remaining: %d)",
			ErrInsufficientDiskSpace, destinationDir, destFree, requiredBytes, minFreeSpace, remainingBytes)
	}

	if workDir != "" {
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return fmt.Errorf("%w: failed to create work directory %s: %v", ErrStorageError, workDir, err)
		}

		workFree, err := s.freeSpaceProvider.FreeBytes(workDir)
		if err != nil {
			return fmt.Errorf("%w: failed to check free space for workdir %s: %v", ErrStorageError, workDir, err)
		}

		if workFree < requiredBytes {
			return fmt.Errorf("%w: insufficient free space in workdir %s (free: %d, required: %d, reserve: %d, remaining: %d)",
				ErrInsufficientDiskSpace, workDir, workFree, requiredBytes, minFreeSpace, remainingBytes)
		}
	}

	return nil
}

// FinalizeFile safely moves a completed file from srcPath to destinationDir according to policy.
func (s *StorageService) FinalizeFile(ctx context.Context, srcPath, destinationDir string, policy FilenameConflictPolicy) (string, error) {
	finalizationMu.Lock()
	defer finalizationMu.Unlock()

	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("%w: source file %s does not exist: %v", ErrStorageError, srcPath, err)
	}

	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return "", fmt.Errorf("%w: failed to create destination directory %s: %v", ErrStorageError, destinationDir, err)
	}

	filename := filepath.Base(srcPath)
	targetPath := filepath.Join(destinationDir, filename)

	exists := false
	if _, err := os.Stat(targetPath); err == nil {
		exists = true
	}

	finalFilename := filename
	switch policy {
	case ConflictPolicyFail:
		if exists {
			return "", fmt.Errorf("%w: destination file %s already exists", ErrFileConflict, targetPath)
		}
	case ConflictPolicyOverwrite:
		// Overwrite existing file
	case ConflictPolicyRename, ConflictPolicyEngineManaged, "":
		if exists {
			finalFilename = GenerateUniqueFilename(destinationDir, filename)
		}
	}

	finalPath := filepath.Join(destinationDir, finalFilename)

	if err := MoveOrCopyFile(srcPath, finalPath); err != nil {
		return "", fmt.Errorf("%w: failed to move file to final destination %s: %v", ErrStorageError, finalPath, err)
	}

	return finalPath, nil
}

// CleanupWorkDir removes jobID's temporary work directory after validating the safety marker.
func (s *StorageService) CleanupWorkDir(ctx context.Context, jobID, workDir string) error {
	if workDir == "" {
		return nil
	}

	if err := ValidateWorkDirMarker(workDir, jobID); err != nil {
		return fmt.Errorf("refusing workdir cleanup for job %s: %w", jobID, err)
	}

	return removeAllFunc(workDir)
}

// CleanupStaleWorkDirs removes orphaned work directories in tempDir whose job is inactive or absent.
func (s *StorageService) CleanupStaleWorkDirs(ctx context.Context, activeJobIDs map[string]bool) error {
	_, tempDir, _, _ := s.getEffectiveDefaults(ctx)

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read tempDir: %w", err)
	}

	cleanTempDir := filepath.Clean(tempDir)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workDir := filepath.Join(tempDir, entry.Name())
		// Ensure workDir is an immediate child directory of tempDir
		if filepath.Dir(filepath.Clean(workDir)) != cleanTempDir {
			continue
		}

		markerPath := filepath.Join(workDir, WorkDirMarkerFilename)
		data, err := os.ReadFile(markerPath)
		if err != nil {
			continue // Not a GoDownloader workdir or missing marker
		}

		jobID := strings.TrimSpace(string(data))
		if jobID == "" {
			continue // Invalid or empty marker
		}

		if !activeJobIDs[jobID] {
			if err := removeAllFunc(workDir); err != nil {
				log.Printf("CleanupStaleWorkDirs: failed to remove stale workdir %s for job %s: %v", workDir, jobID, err)
			}
		}
	}

	return nil
}
