package ytdlp

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"

	"downloader/internal/job"
)

// downloadState tracks an active yt-dlp download process.
type downloadState struct {
	cancel    context.CancelFunc
	progress  progressInfo
	mu        sync.Mutex
	done      bool
	cancelled bool
	err       string
	postProc  bool // true when FFmpeg is post-processing
}

var (
	_ job.IEngine        = (*Engine)(nil)
	_ job.IMediaAnalyzer = (*Engine)(nil)
)

// Engine implements job.IEngine and job.IMediaAnalyzer for yt-dlp.
type Engine struct {
	ytdlpPath  string
	ffmpegPath string

	mu        sync.RWMutex
	downloads map[string]*downloadState // keyed by job ID
}

// NewEngine creates a new yt-dlp engine.
func NewEngine(ytdlpPath, ffmpegPath string) *Engine {
	if ytdlpPath == "" {
		ytdlpPath = "yt-dlp"
	}
	return &Engine{
		ytdlpPath:  ytdlpPath,
		ffmpegPath: ffmpegPath,
		downloads:  make(map[string]*downloadState),
	}
}

// Available checks if yt-dlp is installed and accessible.
func (e *Engine) Available() bool {
	cmd := exec.Command(e.ytdlpPath, "--version")
	err := cmd.Run()
	return err == nil
}

// Start begins a new yt-dlp download. The engine ID is the job ID itself.
func (e *Engine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	// Build command arguments
	args := []string{
		"--newline", // Force progress on new lines
		"--progress-template", "download:%(progress._percent_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.speed)s|%(progress.eta)s",
		"--no-playlist", // Single video only
		"--no-overwrites",
		"-o", downloadDir + "/%(title)s.%(ext)s",
	}

	if e.ffmpegPath != "" {
		args = append(args, "--ffmpeg-location", e.ffmpegPath)
	}

	// Apply format selection if specified
	if j.MediaInfo != nil && j.MediaInfo.SelectedFmt != "" {
		args = append(args, "-f", j.MediaInfo.SelectedFmt)
	} else {
		// Default: best quality with audio+video merge
		args = append(args, "-f", "bestvideo+bestaudio/best")
	}

	args = append(args, j.Source)

	// Create application-owned cancellable context (survives HTTP request)
	dlCtx, cancel := context.WithCancel(context.Background())

	state := &downloadState{
		cancel: cancel,
	}

	e.mu.Lock()
	e.downloads[j.ID] = state
	e.mu.Unlock()

	// Launch download in background goroutine
	go e.runDownload(dlCtx, j.ID, state, args)

	return j.ID, nil
}

// Pause is not supported for yt-dlp downloads.
func (e *Engine) Pause(ctx context.Context, j *job.Job) error {
	return fmt.Errorf("pause is not supported for media downloads")
}

// Resume is not supported for yt-dlp downloads.
func (e *Engine) Resume(ctx context.Context, j *job.Job) error {
	return fmt.Errorf("resume is not supported for media downloads")
}

// Cancel stops a running yt-dlp download by cancelling its context.
func (e *Engine) Cancel(ctx context.Context, j *job.Job) error {
	e.mu.RLock()
	state, exists := e.downloads[j.ID]
	e.mu.RUnlock()

	if !exists {
		return nil // Already cleaned up
	}

	state.cancel()
	return nil
}

// Status returns the current progress of a yt-dlp download.
func (e *Engine) Status(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
	e.mu.RLock()
	state, exists := e.downloads[j.ID]
	e.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active download for job %s", j.ID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.done {
		if state.cancelled {
			return &job.EngineStatus{
				Status:   job.StatusCancelled,
				Progress: state.progress.Percent,
			}, nil
		}
		if state.err != "" {
			return &job.EngineStatus{
				Status:   job.StatusFailed,
				Error:    state.err,
				Progress: state.progress.Percent,
			}, nil
		}
		return &job.EngineStatus{
			Status:         job.StatusCompleted,
			Progress:       100,
			TotalBytes:     state.progress.TotalBytes,
			CompletedBytes: state.progress.TotalBytes,
		}, nil
	}

	if state.postProc {
		return &job.EngineStatus{
			Status:         job.StatusProcessing,
			Progress:       state.progress.Percent,
			TotalBytes:     state.progress.TotalBytes,
			CompletedBytes: state.progress.DownloadedBytes,
		}, nil
	}

	return &job.EngineStatus{
		Status:              job.StatusDownloading,
		Progress:            state.progress.Percent,
		TotalBytes:          state.progress.TotalBytes,
		CompletedBytes:      state.progress.DownloadedBytes,
		SpeedBytesPerSecond: state.progress.Speed,
		ETASeconds:          state.progress.ETASeconds,
	}, nil
}

// runDownload executes yt-dlp and parses its output in real-time.
func (e *Engine) runDownload(ctx context.Context, jobID string, state *downloadState, args []string) {
	defer func() {
		// Clean up after a delay to allow final status reads
		// The monitor will read the final status and clean up via removeActive
	}()

	cmd := exec.CommandContext(ctx, e.ytdlpPath, args...)

	// Capture stdout for progress
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.markFailed(state, fmt.Sprintf("failed to create stdout pipe: %v", err))
		return
	}

	// Capture stderr for errors
	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.markFailed(state, fmt.Sprintf("failed to create stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		e.markFailed(state, fmt.Sprintf("failed to start yt-dlp: %v", err))
		return
	}

	log.Printf("ytdlp: started download for job %s (pid=%d)", jobID, cmd.Process.Pid)

	// Read stderr in a separate goroutine to collect error messages
	var stderrLines []string
	var stderrMu sync.Mutex
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrMu.Lock()
			stderrLines = append(stderrLines, line)
			stderrMu.Unlock()
		}
	}()

	// Parse stdout progress in real time
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()

		// Check for post-processing markers
		if isPostProcessingLine(line) {
			state.mu.Lock()
			state.postProc = true
			state.mu.Unlock()
			log.Printf("ytdlp: job %s entered post-processing", jobID)
			continue
		}

		// Parse progress
		if prog := parseProgressLine(line); prog != nil {
			state.mu.Lock()
			state.progress = *prog
			state.mu.Unlock()
		}
	}

	// Wait for process to exit
	err = cmd.Wait()

	state.mu.Lock()
	state.done = true
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled via context
			state.cancelled = true
			state.err = ""
			state.mu.Unlock()
			log.Printf("ytdlp: job %s was cancelled", jobID)
			return
		}
		stderrMu.Lock()
		errMsg := "yt-dlp download failed"
		for i := len(stderrLines) - 1; i >= 0; i-- {
			cleaned := cleanError(stderrLines[i])
			if cleaned != "" && cleaned != "unknown error" {
				errMsg = cleaned
				break
			}
		}
		stderrMu.Unlock()
		state.err = errMsg
		state.mu.Unlock()
		log.Printf("ytdlp: job %s failed: %s", jobID, errMsg)
		return
	}
	state.mu.Unlock()

	log.Printf("ytdlp: job %s completed successfully", jobID)
}

// markFailed sets the download state to failed.
func (e *Engine) markFailed(state *downloadState, errMsg string) {
	state.mu.Lock()
	state.done = true
	state.err = errMsg
	state.mu.Unlock()
}

// Cleanup removes tracking data for a completed download.
func (e *Engine) Cleanup(jobID string) {
	e.mu.Lock()
	delete(e.downloads, jobID)
	e.mu.Unlock()
}
