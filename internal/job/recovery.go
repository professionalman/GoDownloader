package job

import (
	"context"
	"fmt"
	"log"
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
			m.hydrateJob(ctx, j)
		}
		m.recoverJob(ctx, j)
	}
}

func (m *Manager) recoverJob(ctx context.Context, j *Job) {
	log.Printf("recovery: recovering job %s (status=%s, engine=%s, engineID=%s)", j.ID, j.Status, j.Engine, j.EngineID)

	// Media subprocesses do not survive backend restart
	if j.Type == TypeMedia || j.Engine == "ytdlp" {
		log.Printf("recovery: media job %s was in status %s during restart, marking failed", j.ID, j.Status)
		j.Status = StatusFailed
		j.Error = "Media download was interrupted by application restart. Retry the job."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Torrent jobs can be recovered via qBittorrent (which is a long-running daemon)
	if j.Type == TypeTorrent || j.Engine == "qbittorrent" {
		log.Printf("recovery: torrent job %s in status %s, attempting reattachment", j.ID, j.Status)

		// For awaiting_selection, keep state as-is (user hasn't picked files yet)
		if j.Status == StatusAwaitingSelection {
			if j.EngineID == "" {
				// No info hash — can't recover
				j.Status = StatusFailed
				j.Error = "Torrent metadata was lost during restart. Retry the job."
				j.UpdatedAt = time.Now()
				m.repo.Update(ctx, j)
				m.publish(EventJobFailed, j)
				return
			}
			// Query qBittorrent to see if torrent still exists
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
				// Torrent no longer in qBittorrent
				j.Status = StatusFailed
				j.Error = "Torrent was removed from qBittorrent during restart. Retry the job."
				j.UpdatedAt = time.Now()
				m.repo.Update(ctx, j)
				m.publish(EventJobFailed, j)
				return
			}
			// Torrent still exists, keep awaiting_selection
			log.Printf("recovery: torrent job %s still in awaiting_selection, keeping state", j.ID)
			m.publish(EventJobUpdated, j)
			return
		}

		// For downloading/seeding, fall through to the standard engine recovery below
		// qBittorrent is a daemon — it survives backend restart
		// The standard recovery logic will query Status() and reattach
	}

	// Case 4: No engine ID — cannot recover
	if j.EngineID == "" {
		log.Printf("recovery: job %s has no engine ID, marking failed", j.ID)
		j.Status = StatusFailed
		j.Error = "Download engine state could not be recovered."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Look up engine by name
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

	// Query engine for current status
	status, err := eng.Status(ctx, j)
	if err != nil {
		// Case 5: Engine unavailable or GID unknown
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
		// Case 1: Engine still knows the download and it's active
		log.Printf("recovery: job %s is still active in engine, reattaching", j.ID)

		if j.Type == TypeTorrent && status.Status == StatusSeeding && !j.SeedAfterComplete {
			log.Printf("recovery: torrent job %s is seeding in engine but seedAfterComplete=false; removing from daemon and completing", j.ID)
			if te, ok := eng.(ITorrentEngine); ok {
				if err := te.RemoveTorrent(ctx, j.EngineID, false); err != nil {
					log.Printf("recovery error: failed to remove torrent %s from daemon: %v", j.EngineID, err)
					j.Status = StatusFailed
					j.Error = fmt.Sprintf("failed to remove completed torrent from qBittorrent during recovery: %v", err)
					j.SpeedBytesPerSecond = 0
					j.ETASeconds = 0
					j.UpdatedAt = time.Now()
					m.repo.Update(ctx, j)
					m.publish(EventJobFailed, j)
					return
				}
			}
			j.Status = StatusCompleted
			j.Progress = 100
			j.SpeedBytesPerSecond = 0
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobCompleted, j)
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
		// Engine has it paused
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
		// Case 2: Engine reports completed
		log.Printf("recovery: job %s completed in engine", j.ID)
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
		m.repo.Update(ctx, j)
		m.publish(EventJobCompleted, j)

	case StatusFailed:
		// Case 3: Engine reports error
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
		// Unknown status — mark failed
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
