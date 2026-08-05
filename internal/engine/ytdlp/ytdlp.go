package ytdlp

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

// downloadState tracks an active yt-dlp download process.
type downloadState struct {
	cancel              context.CancelFunc
	progress            progressInfo
	mu                  sync.Mutex
	done                bool
	cancelled           bool
	err                 string
	postProc            bool // true when FFmpeg is post-processing
	candidateOutputPath string
	finalOutputPath     string
}

var (
	_ job.IEngine             = (*Engine)(nil)
	_ job.IMediaAnalyzer      = (*Engine)(nil)
	_ job.IShutdownableEngine = (*Engine)(nil)
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

func (e *Engine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{
		Cancel: true, Retry: true, GlobalDownloadLimit: true,
		PerJobDownloadLimit: true, Proxy: true, UserAgent: true,
		CustomHeaders: true, RetryPolicy: true, TimeoutPolicy: true,
		ProxyProtocols: []networkpolicy.ProxyProtocol{
			networkpolicy.ProxyProtocolHTTP,
			networkpolicy.ProxyProtocolHTTPS,
			networkpolicy.ProxyProtocolSOCKS5,
		},
		StartupOnly: map[string]bool{
			"globalDownloadLimit": true, "downloadLimit": true, "proxy": true,
			"userAgent": true, "customHeaders": true, "retryPolicy": true, "timeoutPolicy": true,
		},
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
		"--progress",
		"--progress-delta", "0.25",
		"--progress-template", "download:__GODOWNLOADER_PROGRESS__:%(progress._percent_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress.total_bytes_estimate)s|%(progress.speed)s|%(progress.eta)s",
		"--print", "after_move:" + finalPathPrefix + "%(filepath)s",
		"--no-playlist", // Single video only
		"--no-overwrites",
		"-o", filepath.Join(downloadDir, "%(title)s.%(ext)s"),
	}

	if e.ffmpegPath != "" {
		args = append(args, "--ffmpeg-location", e.ffmpegPath)
	}
	args = appendNetworkArgs(args, j.RuntimeNetworkPolicy())

	// Apply format selection if specified (pairing video-only selections with bestaudio)
	args = append(args, "-f", buildFormatSelector(j))

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
	go e.runDownload(dlCtx, j.ID, state, downloadDir, args)

	return j.ID, nil
}

func appendNetworkArgs(args []string, runtime *networkpolicy.RuntimePolicy) []string {
	if runtime == nil {
		return args
	}
	p := runtime.Policy
	switch p.Proxy.Mode {
	case networkpolicy.ProxyModeDisabled:
		args = append(args, "--proxy", "")
	case networkpolicy.ProxyModeCustom:
		auth := ""
		if p.Proxy.Username != "" {
			auth = p.Proxy.Username
			if runtime.ProxyPassword != "" {
				auth += ":" + runtime.ProxyPassword
			}
			auth += "@"
		}
		args = append(args, "--proxy", fmt.Sprintf("%s://%s%s:%d", p.Proxy.Protocol, auth, p.Proxy.Host, p.Proxy.Port))
	}
	if p.DownloadLimitBytesPerSecond > 0 {
		args = append(args, "--limit-rate", strconv.FormatInt(p.DownloadLimitBytesPerSecond, 10))
	}
	if p.TimeoutPolicy.RequestTimeoutSeconds > 0 {
		args = append(args, "--socket-timeout", strconv.Itoa(p.TimeoutPolicy.RequestTimeoutSeconds))
	}
	if p.RetryPolicy.MaxAttempts > 0 {
		retries := strconv.Itoa(p.RetryPolicy.MaxAttempts - 1)
		args = append(args, "--retries", retries, "--fragment-retries", retries)
	}
	if p.RetryPolicy.RetryWaitSeconds > 0 {
		args = append(args, "--retry-sleep", strconv.Itoa(p.RetryPolicy.RetryWaitSeconds))
	}
	if p.UserAgent != "" {
		args = append(args, "--user-agent", p.UserAgent)
	}
	for _, h := range p.HTTPHeaders {
		value := h.Value
		if h.Sensitive {
			value = runtime.HeaderValues[strings.ToLower(h.Name)]
		}
		if value != "" {
			args = append(args, "--add-header", h.Name+":"+value)
		}
	}
	return args
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

	var outputPath string
	if state.finalOutputPath != "" {
		outputPath = state.finalOutputPath
	} else if state.candidateOutputPath != "" {
		outputPath = state.candidateOutputPath
	}

	var fileName string
	if outputPath != "" {
		fileName = filepath.Base(outputPath)
	}

	if state.done {
		if state.cancelled {
			return &job.EngineStatus{
				Status:     job.StatusCancelled,
				Progress:   state.progress.Percent,
				FileName:   fileName,
				OutputPath: outputPath,
			}, nil
		}
		if state.err != "" {
			return &job.EngineStatus{
				Status:     job.StatusFailed,
				Error:      state.err,
				Progress:   state.progress.Percent,
				FileName:   fileName,
				OutputPath: outputPath,
			}, nil
		}
		return &job.EngineStatus{
			Status:         job.StatusCompleted,
			Progress:       100,
			TotalBytes:     state.progress.TotalBytes,
			CompletedBytes: state.progress.TotalBytes,
			FileName:       fileName,
			OutputPath:     outputPath,
		}, nil
	}

	if state.postProc {
		return &job.EngineStatus{
			Status:         job.StatusProcessing,
			Progress:       state.progress.Percent,
			TotalBytes:     state.progress.TotalBytes,
			CompletedBytes: state.progress.DownloadedBytes,
			FileName:       fileName,
			OutputPath:     outputPath,
		}, nil
	}

	return &job.EngineStatus{
		Status:              job.StatusDownloading,
		Progress:            state.progress.Percent,
		TotalBytes:          state.progress.TotalBytes,
		CompletedBytes:      state.progress.DownloadedBytes,
		SpeedBytesPerSecond: state.progress.Speed,
		ETASeconds:          state.progress.ETASeconds,
		FileName:            fileName,
		OutputPath:          outputPath,
	}, nil
}

// validateFinalPath validates that path is non-empty, exists, is a regular file, and resides inside workDir.
func validateFinalPath(workDir, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if len(path) >= 2 {
		if (path[0] == '"' && path[len(path)-1] == '"') || (path[0] == '\'' && path[len(path)-1] == '\'') {
			path = path[1 : len(path)-1]
		}
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}

	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to get abs workDir: %v", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get abs path: %v", err)
	}

	rel, err := filepath.Rel(absWorkDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %s escapes work directory %s", absPath, absWorkDir)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("file does not exist: %v", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("path %s is not a regular file", absPath)
	}

	base := strings.ToLower(filepath.Base(absPath))
	if base == ".godownloader-workdir" ||
		strings.HasSuffix(base, ".part") ||
		strings.HasSuffix(base, ".ytdl") ||
		strings.HasSuffix(base, ".tmp") ||
		strings.HasSuffix(base, ".temp") {
		return "", fmt.Errorf("path %s is a temporary or partial file", absPath)
	}

	return absPath, nil
}

// discoverFinalMediaFile scans workDir for plausible media files when no authoritative after_move path was captured.
func discoverFinalMediaFile(workDir string) (string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", fmt.Errorf("failed to read workDir %s: %v", workDir, err)
	}

	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lowerName := strings.ToLower(name)

		if lowerName == ".godownloader-workdir" ||
			strings.HasSuffix(lowerName, ".part") ||
			strings.HasSuffix(lowerName, ".ytdl") ||
			strings.HasSuffix(lowerName, ".tmp") ||
			strings.HasSuffix(lowerName, ".temp") ||
			strings.HasSuffix(lowerName, ".jpg") ||
			strings.HasSuffix(lowerName, ".jpeg") ||
			strings.HasSuffix(lowerName, ".png") ||
			strings.HasSuffix(lowerName, ".webp") ||
			strings.HasSuffix(lowerName, ".vtt") ||
			strings.HasSuffix(lowerName, ".srt") ||
			strings.HasSuffix(lowerName, ".ass") ||
			strings.HasSuffix(lowerName, ".info.json") ||
			strings.HasSuffix(lowerName, ".description") ||
			strings.HasSuffix(lowerName, ".txt") ||
			strings.HasSuffix(lowerName, ".lock") {
			continue
		}

		fullPath := filepath.Join(workDir, name)
		if fi, err := os.Stat(fullPath); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			candidates = append(candidates, fullPath)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no plausible media output files found in %s", workDir)
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("multiple plausible media output files found in %s: %v", workDir, candidates)
	}

	return candidates[0], nil
}

func (e *Engine) handleYTDLPLine(jobID string, state *downloadState, line string, source string) {
	if os.Getenv("GODOWNLOADER_DEBUG_YTDLP_PROGRESS") == "1" {
		log.Printf("[ytdlp debug %s job=%s] %s", source, jobID, line)
	}

	if finalPath := parseFinalPathLine(line); finalPath != "" {
		state.mu.Lock()
		if state.finalOutputPath == "" {
			state.finalOutputPath = finalPath
			log.Printf("ytdlp: captured final output path for job %s: %s", jobID, finalPath)
		}
		state.mu.Unlock()
	}

	if outPath := parseOutputPathLine(line); outPath != "" {
		state.mu.Lock()
		state.candidateOutputPath = outPath
		state.mu.Unlock()
	}

	if isPostProcessingLine(line) {
		state.mu.Lock()
		state.postProc = true
		state.mu.Unlock()
		log.Printf("ytdlp: job %s entered post-processing", jobID)
		return
	}

	if prog := parseProgressLine(line); prog != nil {
		state.mu.Lock()
		defer state.mu.Unlock()

		if state.postProc {
			return
		}

		if prog.Percent >= state.progress.Percent {
			state.progress = *prog
		} else {
			// Secondary stream starting at lower percentage: preserve higher percent and total size, update speed and ETA
			state.progress.Speed = prog.Speed
			state.progress.ETASeconds = prog.ETASeconds
			if prog.DownloadedBytes > state.progress.DownloadedBytes {
				state.progress.DownloadedBytes = prog.DownloadedBytes
			}
		}

		if os.Getenv("GODOWNLOADER_DEBUG_YTDLP_PROGRESS") == "1" {
			log.Printf("[ytdlp debug parsed job=%s] percent=%.1f%% dl=%d total=%d speed=%d eta=%d",
				jobID, state.progress.Percent, state.progress.DownloadedBytes, state.progress.TotalBytes, state.progress.Speed, state.progress.ETASeconds)
		}
	}
}

// runDownload executes yt-dlp and parses its output in real-time.
func (e *Engine) runDownload(ctx context.Context, jobID string, state *downloadState, downloadDir string, args []string) {
	cmd := exec.CommandContext(ctx, e.ytdlpPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.markFailed(state, fmt.Sprintf("failed to create stdout pipe: %v", err))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.markFailed(state, fmt.Sprintf("failed to create stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		e.markFailed(state, fmt.Sprintf("failed to start yt-dlp: %v", err))
		return
	}

	log.Printf("ytdlp: started download for job %s (pid=%d, workDir=%s)", jobID, cmd.Process.Pid, downloadDir)

	var stderrLines []string
	var stderrMu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			e.handleYTDLPLine(jobID, state, line, "stdout")
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrMu.Lock()
			stderrLines = append(stderrLines, line)
			stderrMu.Unlock()
			e.handleYTDLPLine(jobID, state, line, "stderr")
		}
	}()

	wg.Wait()

	// Wait for process to exit
	err = cmd.Wait()

	state.mu.Lock()
	defer state.mu.Unlock()

	if err != nil {
		state.done = true
		if ctx.Err() != nil {
			state.cancelled = true
			state.err = ""
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
		log.Printf("ytdlp: job %s failed: %s", jobID, errMsg)
		return
	}

	// Process exited cleanly (exit code 0): resolve final output path
	var resolvedPath string
	var resolveErr error

	if state.finalOutputPath != "" {
		resolvedPath, resolveErr = validateFinalPath(downloadDir, state.finalOutputPath)
	}
	if resolvedPath == "" && state.candidateOutputPath != "" {
		resolvedPath, resolveErr = validateFinalPath(downloadDir, state.candidateOutputPath)
	}
	if resolvedPath == "" {
		resolvedPath, resolveErr = discoverFinalMediaFile(downloadDir)
	}

	if resolveErr != nil || resolvedPath == "" {
		state.done = true
		state.err = fmt.Sprintf("yt-dlp exited successfully, but the final media output could not be found in %s: %v", downloadDir, resolveErr)
		log.Printf("ytdlp: job %s finalization error: %s", jobID, state.err)
		return
	}

	if fi, statErr := os.Stat(resolvedPath); statErr == nil && fi.Size() > 0 {
		state.progress.TotalBytes = fi.Size()
		state.progress.DownloadedBytes = fi.Size()
		state.progress.Percent = 100.0
	}

	state.finalOutputPath = resolvedPath
	state.done = true
	log.Printf("ytdlp: job %s completed successfully with final output path %s (%d bytes)", jobID, resolvedPath, state.progress.TotalBytes)
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

// Shutdown cancels all active yt-dlp subprocesses on application exit.
func (e *Engine) Shutdown() {
	e.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.downloads))
	for id, state := range e.downloads {
		if state != nil && state.cancel != nil {
			cancels = append(cancels, state.cancel)
		}
		delete(e.downloads, id)
	}
	e.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// buildFormatSelector constructs a yt-dlp format selector string based on job media metadata.
// For video-only formats, it automatically pairs the selected video format ID with +bestaudio/best
// so that FFmpeg merges both streams into the final output.
func buildFormatSelector(j *job.Job) string {
	if j == nil || j.MediaInfo == nil || j.MediaInfo.SelectedFmt == "" {
		return "bestvideo+bestaudio/best"
	}

	var targetFmt *job.MediaFormat
	for i := range j.MediaInfo.Formats {
		if j.MediaInfo.Formats[i].FormatID == j.MediaInfo.SelectedFmt {
			targetFmt = &j.MediaInfo.Formats[i]
			break
		}
	}

	if targetFmt == nil {
		return j.MediaInfo.SelectedFmt
	}

	hasVideo := targetFmt.VCodec != "" && targetFmt.VCodec != "none"
	hasAudio := targetFmt.ACodec != "" && targetFmt.ACodec != "none"

	if hasVideo && !hasAudio {
		return j.MediaInfo.SelectedFmt + "+bestaudio/best"
	}

	return j.MediaInfo.SelectedFmt
}
