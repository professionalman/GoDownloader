package qbittorrent

import (
	"downloader/internal/job"
	"testing"
)

func TestMapQBState(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		expected job.JobStatus
	}{
		{"MetaDL", qbStateMetaDL, job.StatusAnalyzing},
		{"Downloading", qbStateDownloading, job.StatusDownloading},
		{"Uploading", qbStateUploading, statusSeeding},
		{"StoppedUP", qbStateStoppedUP, job.StatusCompleted},
		{"Error", qbStateError, job.StatusFailed},
		{"Unknown", "unknown_state", job.StatusFailed},
		{"PausedDL", qbStatePausedDL, job.StatusPaused},
		{"StalledDL", qbStateStalledDL, job.StatusDownloading},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapQBState(tt.state)
			if result != tt.expected {
				t.Errorf("mapQBState(%q) = %q, expected %q", tt.state, result, tt.expected)
			}
		})
	}
}

func TestPriorityMappings(t *testing.T) {
	toAppTests := []struct {
		priority int
		expected string
	}{
		{qbPrioritySkip, "skip"},
		{qbPriorityNormal, "normal"},
		{qbPriorityHigh, "high"},
		{qbPriorityMaximum, "maximum"},
		{999, "normal"},
	}

	for _, tt := range toAppTests {
		t.Run("ToApp_"+tt.expected, func(t *testing.T) {
			if got := mapQBPriorityToApp(tt.priority); got != tt.expected {
				t.Errorf("mapQBPriorityToApp(%d) = %q, expected %q", tt.priority, got, tt.expected)
			}
		})
	}

	toQBTests := []struct {
		priority string
		expected int
	}{
		{"skip", qbPrioritySkip},
		{"normal", qbPriorityNormal},
		{"high", qbPriorityHigh},
		{"maximum", qbPriorityMaximum},
		{"unknown", qbPriorityNormal},
	}

	for _, tt := range toQBTests {
		t.Run("ToQB_"+tt.priority, func(t *testing.T) {
			if got := mapAppPriorityToQB(tt.priority); got != tt.expected {
				t.Errorf("mapAppPriorityToQB(%q) = %d, expected %d", tt.priority, got, tt.expected)
			}
		})
	}
}

func TestNormalizeProgress(t *testing.T) {
	tests := []struct {
		progress float64
		expected float64
	}{
		{0.0, 0.0},
		{0.5, 50.0},
		{1.0, 100.0},
		{-0.1, 0.0},
		{1.1, 100.0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := normalizeProgress(tt.progress); got != tt.expected {
				t.Errorf("normalizeProgress(%f) = %f, expected %f", tt.progress, got, tt.expected)
			}
		})
	}
}

func TestNormalizeETA(t *testing.T) {
	tests := []struct {
		eta      int64
		expected int64
	}{
		{100, 100},
		{0, 0},
		{-1, 0},
		{8640000, 0},
		{8640001, 0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := normalizeETA(tt.eta); got != tt.expected {
				t.Errorf("normalizeETA(%d) = %d, expected %d", tt.eta, got, tt.expected)
			}
		})
	}
}
