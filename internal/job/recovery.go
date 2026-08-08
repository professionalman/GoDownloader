package job

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// recover attempts to reconnect to running engine downloads on startup.
func (m *Manager) recover(ctx context.Context) {
	jobs, err := m.repo.ListRecoverable(ctx)
	if err != nil {
		log.Printf("recovery: failed to list recoverable jobs: %v", err)
		return
	}

	if len(jobs) == 0 {
		log.Println("recovery: no jobs to recover")
		return
	}

	log.Printf("recovery: found %d jobs to recover", len(jobs))

	for i := range jobs {
		j := &jobs[i]
		if j.Type == TypeTorrent {
			if err := m.hydrateTorrentState(ctx, j); err != nil {
				log.Printf("recovery: failed to hydrate torrent state for job %s: %v", j.ID, err)
				j.Status = StatusFailed
				j.Error = "Torrent metadata state could not be recovered."
				j.SpeedBytesPerSecond = 0
				j.ETASeconds = 0
				j.UpdatedAt = time.Now()
				m.repo.Update(ctx, j)
				m.publish(EventJobFailed, j)
				continue
			}
		}
		m.hydrateJob(ctx, j)
		m.recoverJob(ctx, j)
	}
}

func (m *Manager) recoverJob(ctx context.Context, j *Job) {
	log.Printf("recovery: recovering job %s (status=%s, engine=%s, engineID=%s)", j.ID, j.Status, j.Engine, j.EngineID)

	// 1. QUEUED jobs: In V0.5, QUEUED jobs are ready to execute and waiting for Scheduler capacity.
	// They must remain QUEUED across backend restarts.
	if j.Status == StatusQueued {
		log.Printf("recovery: job %s is QUEUED (waiting for scheduler), preserving QUEUED status", j.ID)
		m.publish(EventJobUpdated, j)
		return
	}

	// 2. PAUSED jobs: Must remain PAUSED across backend restarts.
	if j.Status == StatusPaused {
		log.Printf("recovery: job %s is PAUSED, preserving PAUSED status", j.ID)
		m.publish(EventJobUpdated, j)
		return
	}

	// 2b. Torrent jobs in ANALYZING: resume metadata acquisition and reconciliation across backend restarts
	if (j.Type == TypeTorrent || j.Engine == "qbittorrent") && j.Status == StatusAnalyzing {
		torrentFilePath := ""
		if m.torrentRepo != nil {
			rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
			if err != nil {
				log.Printf("recovery: failed to load torrent job record for analyzing job %s: %v", j.ID, err)
			} else if rec != nil {
				torrentFilePath = rec.TorrentFilePath
			}
		}

		if strings.HasPrefix(j.Source, "torrent://") || filepath.Ext(j.Source) == ".torrent" {
			if torrentFilePath == "" {
				j.Status = StatusFailed
				j.Error = "Torrent metainfo record missing during restart recovery. Retry the job."
				j.UpdatedAt = time.Now()
				m.repo.Update(ctx, j)
				m.publish(EventJobFailed, j)
				return
			}
			if _, err := os.Stat(torrentFilePath); os.IsNotExist(err) {
				j.Status = StatusFailed
				j.Error = fmt.Sprintf("Torrent metainfo file missing at %s during restart recovery. Retry the job.", torrentFilePath)
				j.UpdatedAt = time.Now()
				m.repo.Update(ctx, j)
				m.publish(EventJobFailed, j)
				return
			}
		}

		log.Printf("recovery: resuming metadata acquisition for analyzing torrent job %s (source=%s, file=%s)", j.ID, j.Source, torrentFilePath)
		m.publish(EventJobUpdated, j)
		go m.acquireTorrentMetadata(j.ID, j.Source, torrentFilePath)
		return
	}

	// 3. Media subprocesses in DOWNLOADING or PROCESSING state do not survive backend restart.
	if j.Type == TypeMedia || j.Engine == "ytdlp" {
		log.Printf("recovery: active media job %s was in status %s during restart, marking failed", j.ID, j.Status)
		j.Status = StatusFailed
		j.Error = "Media download was interrupted by application restart. Retry the job."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// 4. Torrent jobs in AWAITING_SELECTION: keep state as-is
	if (j.Type == TypeTorrent || j.Engine == "qbittorrent") && j.Status == StatusAwaitingSelection {
		if j.EngineID == "" {
			j.Status = StatusFailed
			j.Error = "Torrent metadata was lost during restart. Retry the job."
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobFailed, j)
			return
		}
		eng, ok := m.engines.Get(j.Engine)
		if !ok {
			j.Status = StatusFailed
			j.Error = "qBittorrent engine not available."
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobFailed, j)
			return
		}
		_, err := eng.Status(ctx, j)
		if err != nil {
			j.Status = StatusFailed
			j.Error = "Torrent was removed from qBittorrent during restart. Retry the job."
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobFailed, j)
			return
		}
		log.Printf("recovery: torrent job %s still in awaiting_selection, keeping state", j.ID)
		m.publish(EventJobUpdated, j)
		return
	}

	// 5. Active running jobs (DOWNLOADING, SEEDING): require EngineID to reattach
	if j.EngineID == "" {
		log.Printf("recovery: active job %s has no engine ID, marking failed", j.ID)
		j.Status = StatusFailed
		j.Error = "Download engine state could not be recovered."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		log.Printf("recovery: engine %q not available for job %s, marking failed", j.Engine, j.ID)
		j.Status = StatusFailed
		j.Error = "Download engine not available."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}
	if j.Type == TypeTorrent && j.EngineID != "" {
		if controller, supported := eng.(ISeedingPolicyController); supported {
			if applyErr := controller.ApplySeedingPolicy(ctx, j, j.SeedingPolicy); applyErr != nil && m.torrentRepo != nil {
				if rec, recErr := m.torrentRepo.GetTorrentJob(ctx, j.ID); recErr == nil && rec != nil {
					rec = cloneTorrentRecord(rec)
					rec.SeedingReconcilePending = true
					_ = m.torrentRepo.UpdateTorrentJob(ctx, rec)
				}
			}
		}
	}

	status, err := eng.Status(ctx, j)
	if err != nil {
		log.Printf("recovery: engine status failed for job %s: %v", j.ID, err)
		j.Status = StatusFailed
		j.Error = "Download engine state could not be recovered."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	switch status.Status {
	case StatusDownloading, StatusQueued, StatusSeeding:
		log.Printf("recovery: job %s is active in engine (engineStatus=%s), reattaching", j.ID, status.Status)

		if j.Type == TypeTorrent && status.Status == StatusSeeding && !j.SeedAfterComplete {
			log.Printf("recovery: torrent job %s is seeding in engine but seedAfterComplete=false; completing and removing from daemon", j.ID)
			_ = m.finalizeCompletedTorrent(ctx, j)
			return
		}

		j.Status = status.Status
		j.TotalBytes = status.TotalBytes
		j.CompletedBytes = status.CompletedBytes
		j.SpeedBytesPerSecond = status.SpeedBytesPerSecond
		j.ETASeconds = status.ETASeconds
		j.Progress = status.Progress
		if status.FileName != "" {
			j.Name = status.FileName
		}
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.addActive(j)
		m.publish(EventJobUpdated, j)

	case StatusPaused:
		log.Printf("recovery: job %s is paused in engine", j.ID)
		j.Status = StatusPaused
		j.TotalBytes = status.TotalBytes
		j.CompletedBytes = status.CompletedBytes
		j.Progress = status.Progress
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		if status.FileName != "" {
			j.Name = status.FileName
		}
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobUpdated, j)

	case StatusCompleted:
		log.Printf("recovery: job %s completed in engine", j.ID)
		if j.Type == TypeDownload && status.FileName != "" {
			j.FinalPath = filepath.Join(j.DestinationDir, status.FileName)
		} else if j.Type == TypeTorrent {
			_ = m.finalizeCompletedTorrent(ctx, j)
			return
		}
		j.Status = StatusCompleted
		j.Progress = 100
		j.TotalBytes = status.TotalBytes
		j.CompletedBytes = status.CompletedBytes
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		if status.FileName != "" {
			j.Name = status.FileName
		}
		j.UpdatedAt = time.Now()
		if updateErr := m.repo.Update(ctx, j); updateErr != nil {
			log.Printf("recovery error: failed to persist COMPLETED status for job %s: %v", j.ID, updateErr)
			return
		}
		m.publish(EventJobCompleted, j)

	case StatusFailed:
		log.Printf("recovery: job %s failed in engine: %s", j.ID, status.Error)
		j.Status = StatusFailed
		j.Error = status.Error
		if j.Error == "" {
			j.Error = "Download failed during recovery."
		}
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)

	case StatusCancelled:
		log.Printf("recovery: job %s was cancelled in engine", j.ID)
		j.Status = StatusCancelled
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobCancelled, j)

	default:
		log.Printf("recovery: job %s has unknown engine status %q, marking failed", j.ID, status.Status)
		j.Status = StatusFailed
		j.Error = "Download engine state could not be recovered."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
	}
}
