package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

const (
	EngineName   = "qbittorrent"
	CategoryName = "godownloader"
)

// TorrentFileInfo is the normalized file info returned to callers.
type TorrentFileInfo struct {
	Index    int     `json:"index"`
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Priority string  `json:"priority"`
}

// TorrentMetadata is the normalized torrent metadata.
type TorrentMetadata struct {
	Name      string `json:"name"`
	InfoHash  string `json:"infoHash"`
	TotalSize int64  `json:"totalSize"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
}

var (
	_ job.IEngine        = (*Engine)(nil)
	_ job.ITorrentEngine = (*Engine)(nil)
)

type Engine struct {
	client *Client
	mu     sync.RWMutex
}

func NewEngine(baseURL, username, password string, timeoutSecs int) *Engine {
	return &Engine{
		client: NewClient(baseURL, username, password, time.Duration(timeoutSecs)*time.Second),
	}
}

func (e *Engine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{
		Pause: true, Resume: true, Cancel: true, Retry: true,
		GlobalDownloadLimit: true, PerJobDownloadLimit: true, PerJobUploadLimit: true,
		FileSelection: true, Trackers: true, SeedingPolicy: true,
	}
}

func (e *Engine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	if j.EngineID == "" {
		return "", errors.New("qbittorrent engine start requires an existing info hash (EngineID)")
	}
	err := e.client.StartTorrents(ctx, []string{j.EngineID})
	if err != nil {
		return "", err
	}
	return j.EngineID, nil
}

func (e *Engine) Pause(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return nil
	}
	return e.client.StopTorrents(ctx, []string{j.EngineID})
}

func (e *Engine) Resume(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return nil
	}
	return e.client.StartTorrents(ctx, []string{j.EngineID})
}

func (e *Engine) Cancel(ctx context.Context, j *job.Job) error {
	if j.EngineID == "" {
		return nil
	}
	return e.client.DeleteTorrents(ctx, []string{j.EngineID}, false)
}

func (e *Engine) Status(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
	if j.EngineID == "" {
		return nil, errors.New("missing info hash")
	}
	info, err := e.client.GetTorrentInfo(ctx, j.EngineID)
	if err != nil {
		return nil, err
	}

	status := &job.EngineStatus{
		Status:              mapQBState(info.State),
		RawState:            info.State,
		Progress:            normalizeProgress(info.Progress),
		SpeedBytesPerSecond: info.DLSpeed,
		CompletedBytes:      info.CompletedSize,
		TotalBytes:          info.Size,
		ETASeconds:          normalizeETA(info.ETA),
		UploadSpeed:         info.UPSpeed,
		Uploaded:            info.Uploaded,
		Ratio:               info.Ratio,
		Seeders:             info.NumSeeds,
		Leechers:            info.NumLeechs,
		FileName:            info.Name,
	}
	if properties, propErr := e.client.GetTorrentProperties(ctx, j.EngineID); propErr == nil {
		status.SeedingTimeSeconds = properties.SeedingTime
		status.TorrentPrivate = &properties.IsPrivate
	}
	return status, nil
}

func (e *Engine) GetRawState(ctx context.Context, infoHash string) (string, error) {
	info, err := e.client.GetTorrentInfo(ctx, infoHash)
	if err != nil {
		return "", err
	}
	return info.State, nil
}

func (e *Engine) SetDownloadLimit(ctx context.Context, j *job.Job, bytesPerSecond int64) error {
	if j.EngineID == "" {
		return errors.New("missing persisted torrent hash")
	}
	return e.client.SetDownloadLimit(ctx, j.EngineID, bytesPerSecond)
}

func (e *Engine) GetDownloadLimit(ctx context.Context, j *job.Job) (int64, error) {
	info, err := e.client.GetTorrentInfo(ctx, j.EngineID)
	if err != nil {
		return 0, err
	}
	return info.DLLimit, nil
}

func (e *Engine) SetUploadLimit(ctx context.Context, j *job.Job, bytesPerSecond int64) error {
	if j.EngineID == "" {
		return errors.New("missing persisted torrent hash")
	}
	return e.client.SetUploadLimit(ctx, j.EngineID, bytesPerSecond)
}

func (e *Engine) GetUploadLimit(ctx context.Context, j *job.Job) (int64, error) {
	info, err := e.client.GetTorrentInfo(ctx, j.EngineID)
	if err != nil {
		return 0, err
	}
	return info.UPLimit, nil
}

func (e *Engine) GetTrackers(ctx context.Context, j *job.Job) ([]networkpolicy.Tracker, error) {
	if j.EngineID == "" {
		return nil, errors.New("missing persisted torrent hash")
	}
	raw, err := e.client.GetTrackers(ctx, j.EngineID)
	if err != nil {
		return nil, err
	}
	result := make([]networkpolicy.Tracker, 0, len(raw))
	for _, tracker := range raw {
		if strings.Contains(tracker.URL, "://") || strings.HasPrefix(tracker.URL, "http") || strings.HasPrefix(tracker.URL, "udp") || strings.HasPrefix(tracker.URL, "wss") {
			result = append(result, networkpolicy.Tracker{
				URL:    tracker.URL,
				Status: tracker.Status,
				Msg:    tracker.Msg,
			})
		}
	}
	return result, nil
}

func (e *Engine) AddTrackers(ctx context.Context, j *job.Job, trackers []string) error {
	if j.EngineID == "" {
		return errors.New("missing persisted torrent hash")
	}
	return e.client.AddTrackers(ctx, j.EngineID, trackers)
}

func (e *Engine) GetTorrentPrivacy(ctx context.Context, j *job.Job) (bool, error) {
	if j.EngineID == "" {
		return false, errors.New("missing persisted torrent hash")
	}
	properties, err := e.client.GetTorrentProperties(ctx, j.EngineID)
	if err != nil {
		return false, err
	}
	return properties.IsPrivate, nil
}

func (e *Engine) ApplySeedingPolicy(ctx context.Context, j *job.Job, policy networkpolicy.SeedingPolicy) error {
	if j.EngineID == "" {
		return errors.New("missing persisted torrent hash")
	}
	if policy.Mode == networkpolicy.SeedingModeNone {
		return nil
	}
	ratio := float64(-1)
	minutes := int64(-1)
	switch policy.Mode {
	case networkpolicy.SeedingModeUnlimited:
		ratio = -1
		minutes = -1
	case networkpolicy.SeedingModeRatio:
		ratio = *policy.RatioLimit
	case networkpolicy.SeedingModeDuration:
		minutes = int64(math.Ceil(float64(*policy.TimeLimitSeconds) / 60))
	case networkpolicy.SeedingModeRatioOrDuration:
		ratio = *policy.RatioLimit
		minutes = int64(math.Ceil(float64(*policy.TimeLimitSeconds) / 60))
	}
	return e.client.SetShareLimits(ctx, j.EngineID, ratio, minutes)
}

func (e *Engine) GetTorrentOwnership(ctx context.Context, infoHash string) (*job.TorrentOwnership, error) {
	if infoHash == "" {
		return nil, nil
	}
	info, err := e.client.GetTorrentInfo(ctx, infoHash)
	if err != nil {
		if errors.Is(err, ErrTorrentNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, nil
		}
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	rawTags := strings.Split(info.Tags, ",")
	tags := make([]string, 0, len(rawTags))
	for _, t := range rawTags {
		trimmed := strings.TrimSpace(t)
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return &job.TorrentOwnership{
		Hash:     strings.ToLower(info.Hash),
		Category: strings.TrimSpace(info.Category),
		Tags:     tags,
	}, nil
}

func (e *Engine) AdoptTorrent(ctx context.Context, infoHash, jobID string) error {
	if infoHash == "" {
		return errors.New("info hash is required to adopt torrent")
	}
	// 1. Stop torrent to ensure no background downloading occurs before file selection
	if err := e.client.StopTorrents(ctx, []string{infoHash}); err != nil {
		return fmt.Errorf("failed to stop torrent during adoption: %w", err)
	}

	// Verify stopped state using existing raw-state/status mechanism where practical
	info, err := e.client.GetTorrentInfo(ctx, infoHash)
	if err != nil {
		return fmt.Errorf("failed to verify torrent state after adoption: %w", err)
	}
	if info != nil && strings.TrimSpace(info.Category) != CategoryName {
		if catErr := e.client.SetCategory(ctx, []string{infoHash}, CategoryName); catErr != nil {
			return fmt.Errorf("failed to set category during adoption: %w", catErr)
		}
	}

	// 2. Associate current job tag (fatal if fails)
	if jobID != "" {
		if err := e.client.AddTags(ctx, []string{infoHash}, []string{jobID}); err != nil {
			return fmt.Errorf("failed to tag adopted torrent: %w", err)
		}
	}

	// 3. Remove stale GoDownloader job tags if present (surface failure)
	if info != nil {
		rawTags := strings.Split(info.Tags, ",")
		var staleTags []string
		for _, t := range rawTags {
			trimmed := strings.TrimSpace(t)
			if trimmed != "" && trimmed != jobID && strings.HasPrefix(trimmed, "job_") {
				staleTags = append(staleTags, trimmed)
			}
		}
		if len(staleTags) > 0 {
			if rmErr := e.client.RemoveTags(ctx, []string{infoHash}, staleTags); rmErr != nil {
				log.Printf("AdoptTorrent: warning: failed to remove stale tags %v from %s: %v", staleTags, infoHash, rmErr)
				return fmt.Errorf("failed to cleanup stale job tags during adoption: %w", rmErr)
			}
		}
	}

	return nil
}

func (e *Engine) ListTorrentOwnership(ctx context.Context) ([]job.TorrentOwnership, error) {
	torrents, err := e.client.GetTorrents(ctx, "")
	if err != nil {
		return nil, err
	}
	result := make([]job.TorrentOwnership, 0, len(torrents))
	for _, torrent := range torrents {
		tags := strings.Split(torrent.Tags, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
		result = append(result, job.TorrentOwnership{Hash: strings.ToLower(torrent.Hash), Category: torrent.Category, Tags: tags})
	}
	return result, nil
}

func (e *Engine) ApplyManagedProxy(ctx context.Context, runtime *networkpolicy.RuntimePolicy) error {
	if runtime == nil {
		return errors.New("managed proxy policy is required")
	}
	policy := runtime.Policy.Proxy
	preferences := qbPreferences{}
	switch policy.Mode {
	case networkpolicy.ProxyModeDisabled:
		preferences.ProxyType = 0
	case networkpolicy.ProxyModeCustom:
		switch policy.Protocol {
		case networkpolicy.ProxyProtocolHTTP:
			if runtime.ProxyPassword != "" {
				preferences.ProxyType = 3
			} else {
				preferences.ProxyType = 1
			}
		case networkpolicy.ProxyProtocolSOCKS5:
			if runtime.ProxyPassword != "" {
				preferences.ProxyType = 4
			} else {
				preferences.ProxyType = 2
			}
		default:
			return errors.New("qBittorrent managed proxy supports only HTTP and SOCKS5")
		}
		preferences.ProxyIP = policy.Host
		preferences.ProxyPort = policy.Port
		preferences.ProxyAuthEnabled = policy.Username != "" || runtime.ProxyPassword != ""
		preferences.ProxyUsername = policy.Username
		preferences.ProxyPassword = runtime.ProxyPassword
		preferences.ProxyHostnameLookup = true
		preferences.ProxyBittorrent = true
		preferences.ProxyPeerConnections = true
	default:
		return errors.New("qBittorrent system proxy mode is unsupported")
	}
	if err := e.client.SetPreferences(ctx, preferences); err != nil {
		return err
	}
	applied, err := e.client.GetPreferences(ctx)
	if err != nil {
		return err
	}
	if applied.ProxyType != preferences.ProxyType || applied.ProxyIP != preferences.ProxyIP ||
		applied.ProxyPort != preferences.ProxyPort || applied.ProxyUsername != preferences.ProxyUsername ||
		applied.ProxyBittorrent != preferences.ProxyBittorrent {
		return errors.New("qBittorrent proxy verification failed")
	}
	return nil
}

func (e *Engine) ensureCategory(ctx context.Context) error {
	return e.client.CreateCategory(ctx, CategoryName, "")
}

func (e *Engine) AddMagnet(ctx context.Context, magnet, savePath string, jobID string) (string, error) {
	if err := e.ensureCategory(ctx); err != nil {
		// ignore errors about category already existing
	}

	hash, err := extractMagnetHash(magnet)
	if err != nil {
		return "", fmt.Errorf("failed to extract info hash from magnet: %w", err)
	}
	expectedHash := strings.ToLower(hash)

	err = e.client.AddMagnet(ctx, magnet, savePath, CategoryName, []string{jobID}, false)
	if err != nil {
		return "", err
	}

	if err := e.waitForTorrentVisible(ctx, expectedHash, 3*time.Second); err != nil {
		return "", err
	}

	return expectedHash, nil
}

func (e *Engine) AddTorrentFile(ctx context.Context, filePath, savePath string, jobID string) (string, error) {
	if err := e.ensureCategory(ctx); err != nil {
		// ignore
	}

	identity, err := job.ExtractTorrentIdentityFromFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to extract torrent info hash from file: %w", err)
	}
	expectedHash := identity.QBitTorrentID
	if expectedHash == "" {
		return "", errors.New("failed to derive canonical qBittorrent info hash from file")
	}

	err = e.client.AddTorrentFile(ctx, filePath, savePath, CategoryName, []string{jobID}, true)
	if err != nil {
		return "", err
	}

	if err := e.waitForTorrentVisible(ctx, expectedHash, 3*time.Second); err != nil {
		return "", err
	}

	return expectedHash, nil
}

func (e *Engine) waitForTorrentVisible(ctx context.Context, expectedHash string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	pollInterval := 50 * time.Millisecond

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		info, err := e.client.GetTorrentInfo(ctx, expectedHash)
		if err == nil && info != nil {
			if strings.EqualFold(info.Hash, expectedHash) {
				return nil
			}
		}

		if err != nil && !errors.Is(err, ErrTorrentNotFound) && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			log.Printf("waitForTorrentVisible: non-404 status query for %s: %v", expectedHash, err)
		}

		if time.Now().After(deadline) {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("torrent was accepted by qBittorrent but visibility could not be confirmed within %v (hash: %s)", timeout, expectedHash)
}

func (e *Engine) GetFiles(ctx context.Context, infoHash string) ([]job.TorrentFile, error) {
	files, err := e.client.GetTorrentFiles(ctx, infoHash)
	if err != nil {
		return nil, err
	}

	var result []job.TorrentFile
	for _, f := range files {
		p := mapQBPriorityToApp(f.Priority)
		result = append(result, job.TorrentFile{
			Index:    f.Index,
			Path:     f.Name,
			Size:     f.Size,
			Progress: normalizeProgress(f.Progress),
			Priority: job.TorrentFilePriority(p),
			Selected: f.Priority != qbPrioritySkip,
		})
	}
	return result, nil
}

func (e *Engine) SetFilePriorities(ctx context.Context, infoHash string, selections []job.TorrentFileSelection) error {
	// Group files by priority to batch updates
	prioGroups := make(map[int][]int)
	for _, sel := range selections {
		qbPrio := mapAppPriorityToQB(string(sel.Priority))
		prioGroups[qbPrio] = append(prioGroups[qbPrio], sel.Index)
	}

	for qbPrio, fileIDs := range prioGroups {
		if len(fileIDs) > 0 {
			if err := e.client.SetFilePriority(ctx, infoHash, fileIDs, qbPrio); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) StartDownload(ctx context.Context, infoHash string) error {
	return e.client.StartTorrents(ctx, []string{infoHash})
}

func (e *Engine) StopDownload(ctx context.Context, infoHash string) error {
	return e.client.StopTorrents(ctx, []string{infoHash})
}

func (e *Engine) RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error {
	return e.client.DeleteTorrents(ctx, []string{infoHash}, deleteFiles)
}

func (e *Engine) HealthCheck(ctx context.Context) error {
	return e.client.ValidateCompatibility(ctx)
}

func (e *Engine) GetTorrentInfo(ctx context.Context, infoHash string) (*job.TorrentInfo, error) {
	info, err := e.client.GetTorrentInfo(ctx, infoHash)
	if err != nil {
		return nil, err
	}

	return &job.TorrentInfo{
		Name:        info.Name,
		InfoHash:    info.Hash,
		TotalSize:   info.TotalSize,
		Seeders:     info.NumSeeds,
		Leechers:    info.NumLeechs,
		Uploaded:    info.Uploaded,
		UploadSpeed: info.UPSpeed,
		Ratio:       info.Ratio,
	}, nil
}

func extractMagnetHash(magnet string) (string, error) {
	return job.ExtractMagnetHash(magnet)
}

type DiscoveryDiagnostics struct {
	DHTEnabled       bool   `json:"dhtEnabled"`
	PEXEnabled       bool   `json:"pexEnabled"`
	LSDEnabled       bool   `json:"lsdEnabled"`
	NetworkInterface string `json:"networkInterface"`
	ProxyMode        string `json:"proxyMode"`
}

func (e *Engine) GetDiscoveryDiagnostics(ctx context.Context) (*DiscoveryDiagnostics, error) {
	prefs, err := e.client.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	proxyMode := "disabled"
	if prefs.ProxyType > 0 {
		proxyMode = "custom"
	}
	return &DiscoveryDiagnostics{
		DHTEnabled:       prefs.DHT,
		PEXEnabled:       prefs.PEX,
		LSDEnabled:       prefs.LSD,
		NetworkInterface: prefs.NetworkInterface,
		ProxyMode:        proxyMode,
	}, nil
}
