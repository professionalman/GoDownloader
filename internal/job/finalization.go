package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"downloader/internal/storage"

	"github.com/google/uuid"
)

// validateArtifactFile checks whether an artifact exists at filePath and satisfies size and digest requirements.
func validateArtifactFile(filePath string, expectedSize int64, expectedDigest string) bool {
	if filePath == "" {
		return false
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		return false
	}
	if expectedSize > 0 {
		if fi.Size() != expectedSize {
			return false
		}
	} else if fi.Size() <= 0 {
		return false
	}

	if expectedDigest != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return false
		}
		defer f.Close()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return false
		}
		sum := hex.EncodeToString(h.Sum(nil))
		normalizedExpected := strings.TrimPrefix(strings.ToLower(expectedDigest), "sha256:")
		if !strings.EqualFold(sum, normalizedExpected) {
			return false
		}
	}

	return true
}

// fileExists checks whether a non-directory file exists at filePath.
func fileExists(filePath string) bool {
	if filePath == "" {
		return false
	}
	fi, err := os.Stat(filePath)
	return err == nil && !fi.IsDir()
}

// findRenamedArtifact searches destDir for collision-renamed candidates matching expectedSize and expectedDigest.
// Invariant: An artifact path that was NOT durably recorded before the crash must not be adopted
// solely because its filename matches the collision naming pattern and its size matches ExpectedSize.
// Reconnection is permitted ONLY when ExpectedDigest is non-empty and cryptographically verified.
func findRenamedArtifact(destDir, originalDestPath string, expectedSize int64, expectedDigest string) (string, bool) {
	if expectedDigest == "" {
		return "", false
	}

	filename := filepath.Base(originalDestPath)
	base, ext := storage.SplitFilenameExt(filename)

	cleanBase := base
	if idx := strings.LastIndex(base, " ("); idx != -1 && strings.HasSuffix(base, ")") {
		numStr := base[idx+2 : len(base)-1]
		isDigits := true
		for _, r := range numStr {
			if r < '0' || r > '9' {
				isDigits = false
				break
			}
		}
		if isDigits && len(numStr) > 0 {
			cleanBase = base[:idx]
		}
	}

	for counter := 1; counter <= 1000; counter++ {
		candidate := fmt.Sprintf("%s (%d)%s", cleanBase, counter, ext)
		candPath := filepath.Join(destDir, candidate)
		if fileExists(candPath) && validateArtifactFile(candPath, expectedSize, expectedDigest) {
			return candPath, true
		}
	}
	return "", false
}

// failJobWithReason marks a job as FAILED with the given reason, updates DB, cleans up active state, and kicks scheduler.
func (m *Manager) failJobWithReason(ctx context.Context, j *Job, reason string) {
	j.Status = StatusFailed
	j.Error = reason
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	if updateErr := m.repo.Update(ctx, j); updateErr != nil {
		log.Printf("failJobWithReason: failed to persist FAILED status for job %s: %v", j.ID, updateErr)
		return
	}
	m.removeActive(j.ID)
	m.publish(EventJobFailed, j)
	m.cleanupTerminalEngineState(j)
	if m.scheduler != nil {
		m.scheduler.Kick()
	}
}

// finalizeMediaArtifact orchestrates durable journaled finalization for completed media downloads.
func (m *Manager) finalizeMediaArtifact(ctx context.Context, j *Job, srcFile string, expectedSize int64) {
	targetPath := filepath.Join(j.DestinationDir, filepath.Base(srcFile))
	if m.storageService != nil {
		resolved, err := m.storageService.ResolveFinalPath(srcFile, j.DestinationDir, storage.FilenameConflictPolicy(j.ConflictPolicy))
		if err != nil {
			log.Printf("finalizeMediaArtifact: resolve final path failed for job %s: %v", j.ID, err)
			m.failJobWithReason(ctx, j, fmt.Sprintf("file finalization failed: %v", err))
			return
		}
		targetPath = resolved
	}

	var rec *FinalizationRecord

	if m.execRepo != nil {
		var execID string
		if latest, err := m.execRepo.GetLatestExecution(ctx, j.ID); err == nil && latest != nil {
			execID = latest.ID
		}

		rec = &FinalizationRecord{
			ID:              uuid.New().String(),
			JobID:           j.ID,
			ExecutionID:     execID,
			StagingPath:     srcFile,
			DestinationPath: targetPath,
			ExpectedSize:    expectedSize,
			ConflictPolicy:  string(j.ConflictPolicy),
			Phase:           FinalizationPhasePrepared,
			CleanupPath:     j.WorkDir,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		if err := m.execRepo.SaveFinalizationRecord(ctx, rec); err != nil {
			log.Printf("finalizeMediaArtifact: failed to save prepared finalization record for job %s: %v", j.ID, err)
		}
	}

	if rec != nil && m.execRepo != nil {
		rec.Phase = FinalizationPhaseInProgress
		rec.UpdatedAt = time.Now()
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseInProgress, "")
	}

	// Invariant: The path durably recorded in the journal before source-removing filesystem mutation
	// is the exact path targeted by that mutation. Move to targetPath without re-running conflict resolution.
	allowOverwrite := storage.FilenameConflictPolicy(j.ConflictPolicy) == storage.ConflictPolicyOverwrite
	var finErr error
	if m.storageService != nil {
		finErr = m.storageService.FinalizeFileToPath(ctx, srcFile, targetPath, allowOverwrite)
	} else {
		finErr = storage.MoveOrCopyFileExact(srcFile, targetPath, allowOverwrite)
	}

	if finErr != nil {
		log.Printf("finalizeMediaArtifact: media finalization failed for job %s: %v", j.ID, finErr)
		if rec != nil && m.execRepo != nil {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, finErr.Error())
		}
		m.failJobWithReason(ctx, j, fmt.Sprintf("file finalization failed: %v", finErr))
		return
	}

	finalPath := targetPath

	if !validateArtifactFile(finalPath, expectedSize, "") {
		valErr := fmt.Sprintf("finalized file validation failed for job %s at %s", j.ID, finalPath)
		log.Printf("finalizeMediaArtifact: %s", valErr)
		if rec != nil && m.execRepo != nil {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, valErr)
		}
		m.failJobWithReason(ctx, j, valErr)
		return
	}

	if rec != nil && m.execRepo != nil {
		rec.Phase = FinalizationPhaseValidated
		rec.UpdatedAt = time.Now()
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseValidated, "")
	}

	j.FinalPath = finalPath
	j.Name = filepath.Base(finalPath)
	if expectedSize > 0 {
		j.TotalBytes = expectedSize
		j.CompletedBytes = expectedSize
	}
	m.updateActiveJobFinalization(j.ID, j.FinalPath, j.Name)

	if subErr := m.finalizeMediaSubtitles(ctx, j, srcFile); subErr != nil {
		log.Printf("finalizeMediaArtifact: subtitle finalization failed for job %s: %v", j.ID, subErr)
		if rec != nil && m.execRepo != nil {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, subErr.Error())
		}
		m.failJobWithReason(ctx, j, fmt.Sprintf("subtitle finalization failed: %v", subErr))
		return
	}

	j.Status = StatusCompleted
	j.Progress = 100
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("finalizeMediaArtifact: failed to update job %s to COMPLETED: %v", j.ID, err)
		if rec != nil && m.execRepo != nil {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, fmt.Sprintf("db commit error: %v", err))
		}
		return
	}

	if rec != nil && m.execRepo != nil {
		rec.Phase = FinalizationPhaseDBCommitted
		rec.UpdatedAt = time.Now()
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseDBCommitted, "")
	}

	m.removeActive(j.ID)
	m.publish(EventJobCompleted, j)
	m.cleanupTerminalEngineState(j)

	if rec != nil && m.execRepo != nil {
		rec.Phase = FinalizationPhaseCleanupPending
		rec.UpdatedAt = time.Now()
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseCleanupPending, "")
	}

	if m.storageService != nil && j.WorkDir != "" {
		if err := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); err != nil {
			log.Printf("finalizeMediaArtifact: cleanup workdir error for completed job %s: %v", j.ID, err)
		}
	}

	if rec != nil && m.execRepo != nil {
		if err := m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseComplete, ""); err != nil {
			log.Printf("finalizeMediaArtifact: failed to update finalization phase to COMPLETE for job %s: %v", j.ID, err)
		} else {
			rec.Phase = FinalizationPhaseComplete
			rec.UpdatedAt = time.Now()
			now := time.Now()
			rec.CompletedAt = &now
		}
	}

	if m.scheduler != nil {
		m.scheduler.Kick()
	}
}

// reconcileFinalizationJournal queries pending finalization records and recovers each towards completion.
func (m *Manager) reconcileFinalizationJournal(ctx context.Context, summary *RecoverySummary) {
	if m.execRepo == nil {
		return
	}

	pending, err := m.execRepo.GetPendingFinalizations(ctx)
	if err != nil {
		log.Printf("reconcileFinalizationJournal: failed to query pending finalizations: %v", err)
		return
	}

	if len(pending) == 0 {
		return
	}

	log.Printf("reconcileFinalizationJournal: found %d pending finalization records to reconcile", len(pending))

	for i := range pending {
		rec := &pending[i]
		m.reconcileFinalizationRecord(ctx, rec, summary)
	}
}

// reconcileFinalizationRecord resolves a single finalization journal entry according to disk and DB state.
func (m *Manager) reconcileFinalizationRecord(ctx context.Context, rec *FinalizationRecord, summary *RecoverySummary) {
	if rec.Phase == FinalizationPhaseComplete || rec.Phase == FinalizationPhaseFailed {
		return
	}

	j, err := m.repo.GetByID(ctx, rec.JobID)
	if err != nil || j == nil {
		log.Printf("reconcileFinalizationRecord: job %s not found for record %s: %v", rec.JobID, rec.ID, err)
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "job not found")
		if summary != nil {
			summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
		}
		return
	}

	srcExists := fileExists(rec.StagingPath)
	dstExists := fileExists(rec.DestinationPath)
	dstValid := dstExists && validateArtifactFile(rec.DestinationPath, rec.ExpectedSize, rec.ExpectedDigest)

	switch rec.Phase {
	case FinalizationPhasePrepared:
		if srcExists {
			m.resumeFinalizationFromSource(ctx, j, rec, summary)
		} else if dstValid && rec.ExpectedDigest != "" {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseValidated, "")
			rec.Phase = FinalizationPhaseValidated
			m.commitAndCleanupFinalization(ctx, j, rec, summary)
		} else {
			log.Printf("reconcileFinalizationRecord: job %s staging file missing in prepared phase", j.ID)
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "staging file missing")
			m.failJobWithReason(ctx, j, "staging file missing during finalization recovery")
			if summary != nil {
				summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
			}
		}

	case FinalizationPhaseInProgress:
		if srcExists {
			m.resumeFinalizationFromSource(ctx, j, rec, summary)
		} else if dstValid {
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseValidated, "")
			rec.Phase = FinalizationPhaseValidated
			m.commitAndCleanupFinalization(ctx, j, rec, summary)
		} else if rec.ConflictPolicy == string(storage.ConflictPolicyRename) || rec.ConflictPolicy == string(storage.ConflictPolicyEngineManaged) || rec.ConflictPolicy == "" {
			if candPath, ok := findRenamedArtifact(j.DestinationDir, rec.DestinationPath, rec.ExpectedSize, rec.ExpectedDigest); ok {
				log.Printf("reconcileFinalizationRecord: job %s recovered collision-renamed destination artifact at %s", j.ID, candPath)
				rec.DestinationPath = candPath
				rec.UpdatedAt = time.Now()
				_ = m.execRepo.SaveFinalizationRecord(ctx, rec)
				_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseValidated, "")
				rec.Phase = FinalizationPhaseValidated
				m.commitAndCleanupFinalization(ctx, j, rec, summary)
			} else if dstExists {
				log.Printf("reconcileFinalizationRecord: job %s destination invalid and staging missing; preserving destination", j.ID)
				_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "destination file invalid and staging missing")
				m.failJobWithReason(ctx, j, "finalization failed: destination invalid and staging missing")
				if summary != nil {
					summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
				}
			} else {
				log.Printf("reconcileFinalizationRecord: job %s staging and destination missing in in_progress phase", j.ID)
				_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "artifacts missing")
				m.failJobWithReason(ctx, j, "finalization failed: artifacts missing")
				if summary != nil {
					summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
				}
			}
		} else if dstExists && !dstValid {
			// Invariant: Do NOT overwrite or delete user destination when source is missing
			log.Printf("reconcileFinalizationRecord: job %s destination invalid and staging missing; preserving destination", j.ID)
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "destination file invalid and staging missing")
			m.failJobWithReason(ctx, j, "finalization failed: destination invalid and staging missing")
			if summary != nil {
				summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
			}
		} else {
			log.Printf("reconcileFinalizationRecord: job %s staging and destination missing in in_progress phase", j.ID)
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "artifacts missing")
			m.failJobWithReason(ctx, j, "finalization failed: artifacts missing")
			if summary != nil {
				summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
			}
		}

	case FinalizationPhaseValidated:
		if dstValid {
			m.commitAndCleanupFinalization(ctx, j, rec, summary)
		} else if srcExists {
			m.resumeFinalizationFromSource(ctx, j, rec, summary)
		} else {
			log.Printf("reconcileFinalizationRecord: job %s validated destination missing and staging missing", j.ID)
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, "validated destination missing and staging missing")
			m.failJobWithReason(ctx, j, "finalization failed: validated destination missing")
			if summary != nil {
				summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
			}
		}

	case FinalizationPhaseDBCommitted:
		if j.Status != StatusCompleted {
			j.Status = StatusCompleted
			j.Progress = 100
			j.FinalPath = rec.DestinationPath
			j.Name = filepath.Base(rec.DestinationPath)
			if rec.ExpectedSize > 0 {
				j.TotalBytes = rec.ExpectedSize
				j.CompletedBytes = rec.ExpectedSize
			}
			j.UpdatedAt = time.Now()
			_ = m.repo.Update(ctx, j)
		}
		_ = m.executeCleanupAndComplete(ctx, j, rec, summary)

	case FinalizationPhaseCleanupPending:
		_ = m.executeCleanupAndComplete(ctx, j, rec, summary)
	}
}

// resumeFinalizationFromSource moves/copies the staging artifact to the destination and completes finalization.
func (m *Manager) resumeFinalizationFromSource(ctx context.Context, j *Job, rec *FinalizationRecord, summary *RecoverySummary) {
	policy := storage.FilenameConflictPolicy(rec.ConflictPolicy)
	targetPath := rec.DestinationPath
	if m.storageService != nil {
		resolved, err := m.storageService.ResolveFinalPath(rec.StagingPath, j.DestinationDir, policy)
		if err != nil {
			log.Printf("resumeFinalizationFromSource: resolve path failed for job %s: %v", j.ID, err)
			_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, err.Error())
			m.failJobWithReason(ctx, j, fmt.Sprintf("file finalization recovery failed: %v", err))
			if summary != nil {
				summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
			}
			return
		}
		targetPath = resolved
	}

	if targetPath != rec.DestinationPath {
		rec.DestinationPath = targetPath
		rec.UpdatedAt = time.Now()
		_ = m.execRepo.SaveFinalizationRecord(ctx, rec)
	}

	_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseInProgress, "")
	rec.Phase = FinalizationPhaseInProgress

	// Invariant: The path durably recorded in the journal before source-removing filesystem mutation
	// is the exact path targeted by that mutation. Move to targetPath without re-running conflict resolution.
	allowOverwrite := policy == storage.ConflictPolicyOverwrite
	var finErr error
	if m.storageService != nil {
		finErr = m.storageService.FinalizeFileToPath(ctx, rec.StagingPath, targetPath, allowOverwrite)
	} else {
		finErr = storage.MoveOrCopyFileExact(rec.StagingPath, targetPath, allowOverwrite)
	}

	if finErr != nil {
		log.Printf("resumeFinalizationFromSource: finalization failed for job %s: %v", j.ID, finErr)
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, finErr.Error())
		m.failJobWithReason(ctx, j, fmt.Sprintf("file finalization recovery failed: %v", finErr))
		if summary != nil {
			summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
		}
		return
	}

	finalPath := targetPath

	if !validateArtifactFile(finalPath, rec.ExpectedSize, rec.ExpectedDigest) {
		valErr := fmt.Sprintf("resumed finalization file validation failed for job %s at %s", j.ID, finalPath)
		log.Printf("resumeFinalizationFromSource: %s", valErr)
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, valErr)
		m.failJobWithReason(ctx, j, valErr)
		if summary != nil {
			summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
		}
		return
	}

	_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseValidated, "")
	rec.Phase = FinalizationPhaseValidated

	m.commitAndCleanupFinalization(ctx, j, rec, summary)
}

// commitAndCleanupFinalization marks the job completed in DB, publishes event, and initiates workdir cleanup.
func (m *Manager) commitAndCleanupFinalization(ctx context.Context, j *Job, rec *FinalizationRecord, summary *RecoverySummary) {
	j.FinalPath = rec.DestinationPath
	j.Name = filepath.Base(rec.DestinationPath)
	if rec.ExpectedSize > 0 {
		j.TotalBytes = rec.ExpectedSize
		j.CompletedBytes = rec.ExpectedSize
	} else if fi, statErr := os.Stat(rec.DestinationPath); statErr == nil && fi.Size() > 0 {
		j.TotalBytes = fi.Size()
		j.CompletedBytes = fi.Size()
	}
	m.updateActiveJobFinalization(j.ID, j.FinalPath, j.Name)

	// Finalize standalone subtitle sidecars if staging path is known and workdir exists
	if rec.StagingPath != "" {
		_ = m.finalizeMediaSubtitles(ctx, j, rec.StagingPath)
	}

	j.Status = StatusCompleted
	j.Progress = 100
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("commitAndCleanupFinalization: failed to update job %s to COMPLETED: %v", j.ID, err)
		_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseFailed, fmt.Sprintf("db commit error: %v", err))
		if summary != nil {
			summary.Issues = append(summary.Issues, RecoveryIssue{JobID: rec.JobID, Kind: RecoveryIssueFinalizationFailed})
		}
		return
	}

	_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseDBCommitted, "")
	rec.Phase = FinalizationPhaseDBCommitted

	m.removeActive(j.ID)
	m.publish(EventJobCompleted, j)
	m.cleanupTerminalEngineState(j)

	_ = m.executeCleanupAndComplete(ctx, j, rec, summary)
}

// executeCleanupAndComplete cleans up the workdir and transitions journal to completed state.
func (m *Manager) executeCleanupAndComplete(ctx context.Context, j *Job, rec *FinalizationRecord, summary *RecoverySummary) error {
	if rec == nil || m.execRepo == nil {
		return nil
	}

	_ = m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseCleanupPending, "")
	rec.Phase = FinalizationPhaseCleanupPending

	cleanupPath := rec.CleanupPath
	if cleanupPath == "" {
		cleanupPath = j.WorkDir
	}

	if m.storageService != nil && cleanupPath != "" {
		if err := m.storageService.CleanupWorkDir(ctx, j.ID, cleanupPath); err != nil {
			log.Printf("executeCleanupAndComplete: cleanup workdir error for job %s: %v", j.ID, err)
		}
	}

	if err := m.execRepo.UpdateFinalizationPhase(ctx, rec.ID, FinalizationPhaseComplete, ""); err != nil {
		log.Printf("executeCleanupAndComplete: failed to update finalization phase to COMPLETE for job %s: %v", j.ID, err)
		return err
	}
	rec.Phase = FinalizationPhaseComplete
	now := time.Now()
	rec.CompletedAt = &now
	if summary != nil {
		summary.ReconciledFinalizations++
	}
	log.Printf("executeCleanupAndComplete: artifact finalization successfully completed for job %s", j.ID)
	return nil
}
