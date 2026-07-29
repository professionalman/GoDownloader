package aria2

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"downloader/internal/engine"
	"downloader/internal/job"
	"downloader/internal/networkpolicy"
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

func (e *Aria2Engine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{
		Pause: true, Resume: true, Cancel: true, Retry: true,
		GlobalDownloadLimit: true, PerJobDownloadLimit: true,
		Proxy: true, UserAgent: true, CustomHeaders: true, RetryPolicy: true,
		TimeoutPolicy: true, Connections: true,
		ProxyProtocols: []networkpolicy.ProxyProtocol{networkpolicy.ProxyProtocolHTTP},
	}
}

// Start begins a new download via aria2.
func (e *Aria2Engine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	opts := map[string]any{
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

	runtime := j.RuntimeNetworkPolicy()
	if runtime == nil {
		runtime = &networkpolicy.RuntimePolicy{Policy: j.NetworkPolicy}
	}
	applyAria2Policy(opts, runtime)

	gid, err := e.client.AddURIWithOptions(j.Source, opts)
	if err != nil {
		return "", fmt.Errorf("aria2 addUri failed: %w", err)
	}
	return gid, nil
}

func applyAria2Policy(opts map[string]any, runtime *networkpolicy.RuntimePolicy) {
	p := runtime.Policy
	if p.DownloadLimitBytesPerSecond > 0 {
		opts["max-download-limit"] = strconv.FormatInt(p.DownloadLimitBytesPerSecond, 10)
	}
	if p.DirectConnections != nil {
		opts["split"] = strconv.Itoa(p.DirectConnections.Split)
		opts["max-connection-per-server"] = strconv.Itoa(p.DirectConnections.MaxConnectionsPerServer)
		opts["min-split-size"] = strconv.FormatInt(p.DirectConnections.MinSplitSizeBytes, 10)
	}
	switch p.Proxy.Mode {
	case networkpolicy.ProxyModeDisabled:
		opts["all-proxy"] = ""
	case networkpolicy.ProxyModeCustom:
		host := fmt.Sprintf("http://%s:%d", p.Proxy.Host, p.Proxy.Port)
		opts["all-proxy"] = host
		if p.Proxy.Username != "" {
			opts["all-proxy-user"] = p.Proxy.Username
		}
		if runtime.ProxyPassword != "" {
			opts["all-proxy-passwd"] = runtime.ProxyPassword
		}
	}
	if p.UserAgent != "" {
		opts["user-agent"] = p.UserAgent
	}
	headers := make([]string, 0, len(p.HTTPHeaders))
	for _, h := range p.HTTPHeaders {
		value := h.Value
		if h.Sensitive {
			value = runtime.HeaderValues[strings.ToLower(h.Name)]
		}
		if value != "" {
			headers = append(headers, h.Name+": "+value)
		}
	}
	if len(headers) > 0 {
		opts["header"] = headers
	}
	if p.RetryPolicy.MaxAttempts > 0 {
		opts["max-tries"] = strconv.Itoa(p.RetryPolicy.MaxAttempts)
	}
	if p.RetryPolicy.RetryWaitSeconds > 0 {
		opts["retry-wait"] = strconv.Itoa(p.RetryPolicy.RetryWaitSeconds)
	}
	if p.TimeoutPolicy.ConnectTimeoutSeconds > 0 {
		opts["connect-timeout"] = strconv.Itoa(p.TimeoutPolicy.ConnectTimeoutSeconds)
	}
	if p.TimeoutPolicy.RequestTimeoutSeconds > 0 {
		opts["timeout"] = strconv.Itoa(p.TimeoutPolicy.RequestTimeoutSeconds)
	}
}

func (e *Aria2Engine) SetGlobalDownloadLimit(_ context.Context, bytesPerSecond int64) error {
	return e.client.ChangeGlobalOption(map[string]any{"max-overall-download-limit": strconv.FormatInt(bytesPerSecond, 10)})
}

func (e *Aria2Engine) GetGlobalDownloadLimit(_ context.Context) (int64, error) {
	options, err := e.client.GetGlobalOption()
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(options["max-overall-download-limit"], 10, 64)
}

func (e *Aria2Engine) SetDownloadLimit(_ context.Context, j *job.Job, bytesPerSecond int64) error {
	if j.EngineID == "" {
		return fmt.Errorf("no engine ID for job %s", j.ID)
	}
	return e.client.ChangeOption(j.EngineID, map[string]any{"max-download-limit": strconv.FormatInt(bytesPerSecond, 10)})
}

func (e *Aria2Engine) GetDownloadLimit(_ context.Context, j *job.Job) (int64, error) {
	if j.EngineID == "" {
		return 0, fmt.Errorf("no engine ID for job %s", j.ID)
	}
	options, err := e.client.GetOption(j.EngineID)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(options["max-download-limit"], 10, 64)
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
