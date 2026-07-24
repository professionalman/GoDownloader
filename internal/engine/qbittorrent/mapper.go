package qbittorrent

import "downloader/internal/job"

var (
	statusSeeding           = job.JobStatus("seeding")
	statusAwaitingSelection = job.JobStatus("awaiting_selection")
)

// mapQBState maps qBittorrent states to GoDownloader job statuses.
func mapQBState(state string) job.JobStatus {
	switch state {
	case qbStateMetaDL, qbStateForcedMetaDL:
		return job.StatusAnalyzing
	case qbStateDownloading, qbStateForcedDL, qbStateStalledDL, qbStateAllocating, qbStateQueuedDL, qbStateCheckingDL, qbStateCheckingResumeData, qbStateMoving:
		return job.StatusDownloading
	case qbStateStoppedDL, qbStatePausedDL:
		return job.StatusPaused
	case qbStateUploading, qbStateForcedUP, qbStateStalledUP, qbStateQueuedUP, qbStateCheckingUP:
		return statusSeeding
	case qbStateStoppedUP, qbStatePausedUP:
		return job.StatusCompleted
	case qbStateError, qbStateMissingFiles:
		return job.StatusFailed
	default:
		return job.StatusFailed
	}
}

func mapQBPriorityToApp(qbPriority int) string {
	switch qbPriority {
	case qbPrioritySkip:
		return "skip"
	case qbPriorityNormal:
		return "normal"
	case qbPriorityHigh:
		return "high"
	case qbPriorityMaximum:
		return "maximum"
	default:
		return "normal"
	}
}

func mapAppPriorityToQB(priority string) int {
	switch priority {
	case "skip":
		return qbPrioritySkip
	case "normal":
		return qbPriorityNormal
	case "high":
		return qbPriorityHigh
	case "maximum":
		return qbPriorityMaximum
	default:
		return qbPriorityNormal
	}
}

func normalizeProgress(progress float64) float64 {
	p := progress * 100
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

func normalizeETA(eta int64) int64 {
	if eta < 0 || eta >= 8640000 {
		return 0
	}
	return eta
}
