package database

import (
	"context"
	"database/sql"
	"fmt"

	"downloader/internal/job"
)

// ITorrentRepository defines the persistence interface for torrent-specific data.
type ITorrentRepository = job.ITorrentRepository
type TorrentRepository = job.ITorrentRepository

var _ job.ITorrentRepository = (*SQLiteTorrentRepository)(nil)

// SQLiteTorrentRepository implements job.ITorrentRepository using SQLite.
type SQLiteTorrentRepository struct {
	db *DB
}

// NewSQLiteTorrentRepository creates a new SQLite-backed torrent repository.
func NewSQLiteTorrentRepository(db *DB) *SQLiteTorrentRepository {
	return &SQLiteTorrentRepository{db: db}
}

// CreateTorrentJob inserts a new torrent job record.
func (r *SQLiteTorrentRepository) CreateTorrentJob(ctx context.Context, rec *job.TorrentJobRecord) error {
	query := `INSERT INTO torrent_jobs (job_id, info_hash, name, total_size, seed_after_complete, torrent_file_path)
		VALUES (?, ?, ?, ?, ?, ?)`
	seedInt := 0
	if rec.SeedAfterComplete {
		seedInt = 1
	}
	_, err := r.db.conn.ExecContext(ctx, query,
		rec.JobID, rec.InfoHash, rec.Name, rec.TotalSize, seedInt, rec.TorrentFilePath)
	if err != nil {
		return fmt.Errorf("insert torrent job: %w", err)
	}
	return nil
}

// GetTorrentJob retrieves a torrent job record by job ID.
func (r *SQLiteTorrentRepository) GetTorrentJob(ctx context.Context, jobID string) (*job.TorrentJobRecord, error) {
	query := `SELECT job_id, info_hash, name, total_size, seed_after_complete, torrent_file_path
		FROM torrent_jobs WHERE job_id = ?`
	var rec job.TorrentJobRecord
	var seedInt int
	err := r.db.conn.QueryRowContext(ctx, query, jobID).Scan(
		&rec.JobID, &rec.InfoHash, &rec.Name, &rec.TotalSize, &seedInt, &rec.TorrentFilePath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get torrent job: %w", err)
	}
	rec.SeedAfterComplete = seedInt != 0
	return &rec, nil
}

// UpdateTorrentJob updates an existing torrent job record.
func (r *SQLiteTorrentRepository) UpdateTorrentJob(ctx context.Context, rec *job.TorrentJobRecord) error {
	query := `UPDATE torrent_jobs SET info_hash=?, name=?, total_size=?, seed_after_complete=?, torrent_file_path=?
		WHERE job_id=?`
	seedInt := 0
	if rec.SeedAfterComplete {
		seedInt = 1
	}
	_, err := r.db.conn.ExecContext(ctx, query,
		rec.InfoHash, rec.Name, rec.TotalSize, seedInt, rec.TorrentFilePath, rec.JobID)
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
	query := `SELECT job_id, info_hash, name, total_size, seed_after_complete, torrent_file_path
		FROM torrent_jobs WHERE info_hash = ?`
	var rec job.TorrentJobRecord
	var seedInt int
	err := r.db.conn.QueryRowContext(ctx, query, infoHash).Scan(
		&rec.JobID, &rec.InfoHash, &rec.Name, &rec.TotalSize, &seedInt, &rec.TorrentFilePath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get torrent job by hash: %w", err)
	}
	rec.SeedAfterComplete = seedInt != 0
	return &rec, nil
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
