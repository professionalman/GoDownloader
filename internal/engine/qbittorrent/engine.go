package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"downloader/internal/job"
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

type Engine struct {
	client *Client
	mu     sync.RWMutex
}

func NewEngine(baseURL, username, password string, timeoutSecs int) *Engine {
	return &Engine{
		client: NewClient(baseURL, username, password, time.Duration(timeoutSecs)*time.Second),
	}
}

// Ensure Engine implements job.Engine
var _ job.Engine = (*Engine)(nil)

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

	return &job.EngineStatus{
		Status:             mapQBState(info.State),
		Progress:           normalizeProgress(info.Progress),
		SpeedBytesPerSecond: info.DLSpeed,
		CompletedBytes:     info.CompletedSize,
		TotalBytes:         info.TotalSize,
		ETASeconds:         normalizeETA(info.ETA),
		UploadSpeed:        info.UPSpeed,
		Uploaded:           info.Uploaded,
		Ratio:              info.Ratio,
		Seeders:            info.NumSeeds,
		Leechers:           info.NumLeechs,
		FileName:           info.Name,
	}, nil
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

	err = e.client.AddMagnet(ctx, magnet, savePath, CategoryName, []string{jobID}, true)
	if err != nil {
		return "", err
	}

	return strings.ToLower(hash), nil
}

func (e *Engine) AddTorrentFile(ctx context.Context, filePath, savePath string, jobID string) (string, error) {
	if err := e.ensureCategory(ctx); err != nil {
		// ignore
	}

	err := e.client.AddTorrentFile(ctx, filePath, savePath, CategoryName, []string{jobID}, true)
	if err != nil {
		return "", err
	}

	// List torrents to find the new one by tag (jobID)
	infos, err := e.client.GetTorrents(ctx, CategoryName)
	if err != nil {
		return "", err
	}

	for _, info := range infos {
		tags := strings.Split(info.Tags, ",")
		for _, tag := range tags {
			if strings.TrimSpace(tag) == jobID {
				return strings.ToLower(info.Hash), nil
			}
        }
	}

	return "", errors.New("torrent added but info hash not found")
}

func (e *Engine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFileInfo, error) {
	files, err := e.client.GetTorrentFiles(ctx, infoHash)
	if err != nil {
		return nil, err
	}

	var result []TorrentFileInfo
	for _, f := range files {
		result = append(result, TorrentFileInfo{
			Index:    f.Index,
			Path:     f.Name,
			Size:     f.Size,
			Progress: normalizeProgress(f.Progress),
			Priority: mapQBPriorityToApp(f.Priority),
		})
	}
	return result, nil
}

func (e *Engine) SetFilePriorities(ctx context.Context, infoHash string, priorities map[int]string) error {
	// Group files by priority to batch updates
	prioGroups := make(map[int][]int)
	for fileID, prioStr := range priorities {
		qbPrio := mapAppPriorityToQB(prioStr)
		prioGroups[qbPrio] = append(prioGroups[qbPrio], fileID)
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
	_, err := e.client.GetVersion(ctx)
	return err
}

func (e *Engine) GetTorrentMetadata(ctx context.Context, infoHash string) (*TorrentMetadata, error) {
	info, err := e.client.GetTorrentInfo(ctx, infoHash)
	if err != nil {
		return nil, err
	}

	return &TorrentMetadata{
		Name:      info.Name,
		InfoHash:  info.Hash,
		TotalSize: info.TotalSize,
		Seeders:   info.NumSeeds,
		Leechers:  info.NumLeechs,
	}, nil
}

func extractMagnetHash(magnet string) (string, error) {
	// Simple extraction for urn:btih:
	const prefix = "urn:btih:"
	idx := strings.Index(magnet, prefix)
	if idx == -1 {
		return "", errors.New("invalid magnet link: missing btih")
	}

	hashPart := magnet[idx+len(prefix):]
	ampIdx := strings.Index(hashPart, "&")
	if ampIdx != -1 {
		hashPart = hashPart[:ampIdx]
	}

	if len(hashPart) == 40 || len(hashPart) == 32 {
		return hashPart, nil
	}

	return "", errors.New("invalid info hash length in magnet link")
}
