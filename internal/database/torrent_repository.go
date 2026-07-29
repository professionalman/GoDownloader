package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

// ITorrentRepository defines the persistence interface for torrent-specific data.
type ITorrentRepository = job.ITorrentRepository

var _ job.ITorrentRepository = (*SQLiteTorrentRepository)(nil)

// SQLiteTorrentRepository implements job.ITorrentRepository using SQLite.
type SQLiteTorrentRepository struct {
	db *DB
}

// NewSQLiteTorrentRepository creates a new SQLite-backed torrent repository.
func NewSQLiteTorrentRepository(db *DB) *SQLiteTorrentRepository {
	return &SQLiteTorrentRepository{db: db}
}

const torrentJobColumns = `job_id, info_hash, name, total_size, seed_after_complete, torrent_file_path,
	seeding_mode, seed_ratio_limit, seed_time_limit_seconds, seeding_started_at,
	seeding_stop_reason, seeding_reconcile_pending, custom_trackers_json`

func scanTorrentRecord(scanner interface{ Scan(...interface{}) error }) (job.TorrentJobRecord, error) {
	var rec job.TorrentJobRecord
	var seedAfterComplete, reconcilePending bool
	var ratio sql.NullFloat64
	var duration sql.NullInt64
	var started sql.NullTime
	var trackersJSON string
	err := scanner.Scan(&rec.JobID, &rec.InfoHash, &rec.Name, &rec.TotalSize, &seedAfterComplete,
		&rec.TorrentFilePath, &rec.SeedingPolicy.Mode, &ratio, &duration, &started,
		&rec.SeedingStopReason, &reconcilePending, &trackersJSON)
	if err != nil {
		return rec, err
	}
	rec.SeedAfterComplete = seedAfterComplete
	rec.SeedingReconcilePending = reconcilePending
	if ratio.Valid {
		rec.SeedingPolicy.RatioLimit = &ratio.Float64
	}
	if duration.Valid {
		rec.SeedingPolicy.TimeLimitSeconds = &duration.Int64
	}
	if started.Valid {
		t := started.Time
		rec.SeedingStartedAt = &t
	}
	if rec.SeedingPolicy.Mode == "" {
		if rec.SeedAfterComplete {
			rec.SeedingPolicy.Mode = networkpolicy.SeedingModeUnlimited
		} else {
			rec.SeedingPolicy.Mode = networkpolicy.SeedingModeNone
		}
	}
	_ = json.Unmarshal([]byte(trackersJSON), &rec.CustomTrackers)
	return rec, nil
}

func torrentPolicyValues(rec *job.TorrentJobRecord) (any, any, any, string, error) {
	var ratio any
	if rec.SeedingPolicy.RatioLimit != nil {
		ratio = *rec.SeedingPolicy.RatioLimit
	}
	var duration any
	if rec.SeedingPolicy.TimeLimitSeconds != nil {
		duration = *rec.SeedingPolicy.TimeLimitSeconds
	}
	var started any
	if rec.SeedingStartedAt != nil {
		started = *rec.SeedingStartedAt
	}
	data, err := json.Marshal(rec.CustomTrackers)
	return ratio, duration, started, string(data), err
}

// CreateTorrentJob inserts a new torrent job record.
func (r *SQLiteTorrentRepository) CreateTorrentJob(ctx context.Context, rec *job.TorrentJobRecord) error {
	query := `INSERT INTO torrent_jobs (` + torrentJobColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	seedInt := 0
	if rec.SeedAfterComplete {
		seedInt = 1
	}
	if rec.SeedingPolicy.Mode == "" {
		rec.SeedingPolicy.Mode = networkpolicy.SeedingModeNone
	}
	ratio, duration, started, trackersJSON, marshalErr := torrentPolicyValues(rec)
	if marshalErr != nil {
		return fmt.Errorf("marshal custom trackers: %w", marshalErr)
	}
	_, err := r.db.conn.ExecContext(ctx, query, rec.JobID, rec.InfoHash, rec.Name,
		rec.TotalSize, seedInt, rec.TorrentFilePath, rec.SeedingPolicy.Mode, ratio,
		duration, started, rec.SeedingStopReason, rec.SeedingReconcilePending, trackersJSON)
	if err != nil {
		return fmt.Errorf("insert torrent job: %w", err)
	}
	return nil
}

// GetTorrentJob retrieves a torrent job record by job ID.
func (r *SQLiteTorrentRepository) GetTorrentJob(ctx context.Context, jobID string) (*job.TorrentJobRecord, error) {
	query := `SELECT ` + torrentJobColumns + ` FROM torrent_jobs WHERE job_id = ?`
	rec, err := scanTorrentRecord(r.db.conn.QueryRowContext(ctx, query, jobID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get torrent job: %w", err)
	}
	return &rec, nil
}

// UpdateTorrentJob updates an existing torrent job record.
func (r *SQLiteTorrentRepository) UpdateTorrentJob(ctx context.Context, rec *job.TorrentJobRecord) error {
	query := `UPDATE torrent_jobs SET info_hash=?, name=?, total_size=?, seed_after_complete=?, torrent_file_path=?,
		seeding_mode=?, seed_ratio_limit=?, seed_time_limit_seconds=?, seeding_started_at=?,
		seeding_stop_reason=?, seeding_reconcile_pending=?, custom_trackers_json=?
		WHERE job_id=?`
	seedInt := 0
	if rec.SeedAfterComplete {
		seedInt = 1
	}
	ratio, duration, started, trackersJSON, marshalErr := torrentPolicyValues(rec)
	if marshalErr != nil {
		return fmt.Errorf("marshal custom trackers: %w", marshalErr)
	}
	_, err := r.db.conn.ExecContext(ctx, query, rec.InfoHash, rec.Name, rec.TotalSize,
		seedInt, rec.TorrentFilePath, rec.SeedingPolicy.Mode, ratio, duration, started,
		rec.SeedingStopReason, rec.SeedingReconcilePending, trackersJSON, rec.JobID)
	if err != nil {
		return fmt.Errorf("update torrent job: %w", err)
	}
	return nil
}

// DeleteTorrentJob deletes a torrent job record and its associated files.
func (r *SQLiteTorrentRepository) DeleteTorrentJob(ctx context.Context, jobID string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM torrent_files WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("delete torrent files: %w", err)
	}
	_, err = r.db.conn.ExecContext(ctx, `DELETE FROM torrent_jobs WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("delete torrent job: %w", err)
	}
	return nil
}

// GetTorrentJobByInfoHash finds a torrent job by its info hash.
func (r *SQLiteTorrentRepository) GetTorrentJobByInfoHash(ctx context.Context, infoHash string) (*job.TorrentJobRecord, error) {
	query := `SELECT ` + torrentJobColumns + `
		FROM torrent_jobs WHERE job_id IN (SELECT job_id FROM torrent_jobs WHERE info_hash = ?) ORDER BY rowid DESC LIMIT 1`
	rec, err := scanTorrentRecord(r.db.conn.QueryRowContext(ctx, query, infoHash))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get torrent job: %w", err)
	}
	return &rec, nil
}

// GetActiveTorrentJobByInfoHash finds an active/non-terminal torrent job by its info hash.
func (r *SQLiteTorrentRepository) GetActiveTorrentJobByInfoHash(ctx context.Context, infoHash string) (*job.TorrentJobRecord, error) {
	query := `SELECT tj.job_id, tj.info_hash, tj.name, tj.total_size, tj.seed_after_complete, tj.torrent_file_path,
		tj.seeding_mode, tj.seed_ratio_limit, tj.seed_time_limit_seconds, tj.seeding_started_at,
		tj.seeding_stop_reason, tj.seeding_reconcile_pending, tj.custom_trackers_json
		FROM torrent_jobs tj
		JOIN jobs j ON j.id = tj.job_id
		WHERE tj.info_hash = ?
		  AND j.status NOT IN ('failed', 'cancelled', 'completed')
		LIMIT 1`
	rec, err := scanTorrentRecord(r.db.conn.QueryRowContext(ctx, query, infoHash))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get active torrent job by info hash: %w", err)
	}
	return &rec, nil
}

// FinalizeTorrent atomically persists the durable completed state and torrent stop metadata.
func (r *SQLiteTorrentRepository) FinalizeTorrent(ctx context.Context, j *job.Job, stopReason string) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	networkJSON, err := json.Marshal(j.NetworkPolicy)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET status=?, progress=?, speed_bytes_per_second=0,
		eta_seconds=0, final_path=?, engine_cleanup_pending=1, updated_at=?,
		network_policy_json=?, effective_download_limit_bps=?, effective_upload_limit_bps=?,
		network_reconcile_pending=? WHERE id=?`,
		j.Status, j.Progress, j.FinalPath, j.UpdatedAt, string(networkJSON),
		j.EffectiveDownloadLimitBytesPerSecond, j.EffectiveUploadLimitBytesPerSecond,
		j.NetworkReconcilePending, j.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE torrent_jobs SET seeding_stop_reason=?,
		seeding_reconcile_pending=0 WHERE job_id=?`, stopReason, j.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SaveTorrentFiles saves the file list for a torrent job (replaces existing).
func (r *SQLiteTorrentRepository) SaveTorrentFiles(ctx context.Context, jobID string, files []job.TorrentFileRecord) error {
	// Delete existing files first
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM torrent_files WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("clear torrent files: %w", err)
	}

	for _, f := range files {
		selectedInt := 0
		if f.Selected {
			selectedInt = 1
		}
		_, err := r.db.conn.ExecContext(ctx,
			`INSERT INTO torrent_files (job_id, file_index, path, size, selected, priority) VALUES (?, ?, ?, ?, ?, ?)`,
			jobID, f.FileIndex, f.Path, f.Size, selectedInt, f.Priority)
		if err != nil {
			return fmt.Errorf("insert torrent file: %w", err)
		}
	}
	return nil
}

// GetTorrentFiles retrieves the file list for a torrent job.
func (r *SQLiteTorrentRepository) GetTorrentFiles(ctx context.Context, jobID string) ([]job.TorrentFileRecord, error) {
	query := `SELECT job_id, file_index, path, size, selected, priority
		FROM torrent_files WHERE job_id = ? ORDER BY file_index ASC`
	rows, err := r.db.conn.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("list torrent files: %w", err)
	}
	defer rows.Close()

	var files []job.TorrentFileRecord
	for rows.Next() {
		var f job.TorrentFileRecord
		var selectedInt int
		if err := rows.Scan(&f.JobID, &f.FileIndex, &f.Path, &f.Size, &selectedInt, &f.Priority); err != nil {
			return nil, fmt.Errorf("scan torrent file: %w", err)
		}
		f.Selected = selectedInt != 0
		files = append(files, f)
	}
	return files, rows.Err()
}

// UpdateTorrentFileSelections updates the selected/priority state for specific files.
func (r *SQLiteTorrentRepository) UpdateTorrentFileSelections(ctx context.Context, jobID string, selections []job.TorrentFileRecord) error {
	for _, s := range selections {
		selectedInt := 0
		if s.Selected {
			selectedInt = 1
		}
		_, err := r.db.conn.ExecContext(ctx,
			`UPDATE torrent_files SET selected=?, priority=? WHERE job_id=? AND file_index=?`,
			selectedInt, s.Priority, jobID, s.FileIndex)
		if err != nil {
			return fmt.Errorf("update torrent file selection: %w", err)
		}
	}
	return nil
}
