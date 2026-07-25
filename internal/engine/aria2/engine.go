package aria2

import (
	"context"
	"fmt"
	"path/filepath"

	"downloader/internal/engine"
	"downloader/internal/job"
)

var _ job.IEngine = (*Aria2Engine)(nil)

// Aria2Engine implements the engine.IEngine interface using aria2c.
type Aria2Engine struct {
	client *Client
}

// NewEngine creates a new aria2 engine.
func NewEngine(rpcURL, secret string) *Aria2Engine {
	return &Aria2Engine{
		client: NewClient(rpcURL, secret),
	}
}

// Start begins a new download via aria2.
func (e *Aria2Engine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	opts := map[string]string{
		"dir": downloadDir,
	}

	switch j.ConflictPolicy {
	case job.ConflictPolicyOverwrite:
		opts["auto-file-renaming"] = "false"
		opts["allow-overwrite"] = "true"
	case job.ConflictPolicyFail:
		opts["auto-file-renaming"] = "false"
		opts["allow-overwrite"] = "false"
	case job.ConflictPolicyRename, job.ConflictPolicyEngineManaged, "":
		opts["auto-file-renaming"] = "true"
		opts["allow-overwrite"] = "false"
	}

	gid, err := e.client.AddURIWithOptions(j.Source, opts)
	if err != nil {
		return "", fmt.Errorf("aria2 addUri failed: %w", err)
	}
	return gid, nil
}

// Pause pauses an active aria2 download.
func (e *Aria2Engine) Pause(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return fmt.Errorf("no engine ID for job %s", j.ID)
	}
	if err := e.client.Pause(j.EngineID); err != nil {
		return fmt.Errorf("aria2 pause failed: %w", err)
	}
	return nil
}

// Resume resumes a paused aria2 download.
func (e *Aria2Engine) Resume(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return fmt.Errorf("no engine ID for job %s", j.ID)
	}
	if err := e.client.Unpause(j.EngineID); err != nil {
		return fmt.Errorf("aria2 unpause failed: %w", err)
	}
	return nil
}

// Cancel cancels an aria2 download.
func (e *Aria2Engine) Cancel(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return fmt.Errorf("no engine ID for job %s", j.ID)
	}
	if err := e.client.ForceRemove(j.EngineID); err != nil {
		return fmt.Errorf("aria2 cancel failed: %w", err)
	}
	return nil
}

// Status retrieves the current status of an aria2 download.
func (e *Aria2Engine) Status(ctx context.Context, j *job.Job) (*engine.EngineStatus, error) {
	if j.EngineID == "" {
		return nil, fmt.Errorf("no engine ID for job %s", j.ID)
	}

	info, err := e.client.TellStatus(j.EngineID)
	if err != nil {
		return nil, fmt.Errorf("aria2 tellStatus failed: %w", err)
	}

	status := mapAria2Status(info.Status)

	totalBytes := ParseInt64(info.TotalLength)
	completedBytes := ParseInt64(info.CompletedLength)
	speed := ParseInt64(info.DownloadSpeed)

	var progress float64
	if totalBytes > 0 {
		progress = float64(completedBytes) / float64(totalBytes) * 100
	}

	var etaSeconds int64
	if speed > 0 && totalBytes > 0 {
		remaining := totalBytes - completedBytes
		if remaining > 0 {
			etaSeconds = remaining / speed
		}
	}

	var errorMsg string
	if status == job.StatusFailed {
		errorMsg = normalizeError(info.ErrorCode, info.ErrorMessage)
	}

	var fileName string
	if len(info.Files) > 0 && info.Files[0].Path != "" {
		fileName = filepath.Base(info.Files[0].Path)
	}

	return &engine.EngineStatus{
		Status:              status,
		TotalBytes:          totalBytes,
		CompletedBytes:      completedBytes,
		SpeedBytesPerSecond: speed,
		ETASeconds:          etaSeconds,
		Progress:            progress,
		Error:               errorMsg,
		FileName:            fileName,
	}, nil
}
