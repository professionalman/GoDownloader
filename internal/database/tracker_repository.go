package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"downloader/internal/networkpolicy"
	"downloader/internal/tracker"
)

var _ tracker.Repository = (*SQLiteTrackerRepository)(nil)

type SQLiteTrackerRepository struct {
	db *DB
}

func NewSQLiteTrackerRepository(db *DB) *SQLiteTrackerRepository {
	return &SQLiteTrackerRepository{db: db}
}

const trackerSourceColumns = `id, name, url, enabled, refresh_interval_seconds,
	last_checked_at, last_success_at, etag, last_modified, last_error,
	tracker_count, created_at, updated_at`

func scanTrackerSource(scanner interface{ Scan(...any) error }) (*networkpolicy.TrackerSource, error) {
	var source networkpolicy.TrackerSource
	var checked, success sql.NullTime
	err := scanner.Scan(&source.ID, &source.Name, &source.URL, &source.Enabled,
		&source.RefreshIntervalSeconds, &checked, &success, &source.ETag,
		&source.LastModified, &source.LastError, &source.TrackerCount,
		&source.CreatedAt, &source.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if checked.Valid {
		source.LastCheckedAt = &checked.Time
	}
	if success.Valid {
		source.LastSuccessAt = &success.Time
	}
	return &source, nil
}

func (r *SQLiteTrackerRepository) List(ctx context.Context) ([]networkpolicy.TrackerSource, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT `+trackerSourceColumns+` FROM tracker_sources ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]networkpolicy.TrackerSource, 0)
	for rows.Next() {
		source, err := scanTrackerSource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *source)
	}
	return result, rows.Err()
}

func (r *SQLiteTrackerRepository) Get(ctx context.Context, id string) (*networkpolicy.TrackerSource, error) {
	source, err := scanTrackerSource(r.db.conn.QueryRowContext(ctx, `SELECT `+trackerSourceColumns+` FROM tracker_sources WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return source, err
}

func (r *SQLiteTrackerRepository) Create(ctx context.Context, source *networkpolicy.TrackerSource) error {
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO tracker_sources (`+trackerSourceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		source.ID, source.Name, source.URL, source.Enabled, source.RefreshIntervalSeconds,
		source.LastCheckedAt, source.LastSuccessAt, source.ETag, source.LastModified,
		source.LastError, source.TrackerCount, source.CreatedAt, source.UpdatedAt)
	return err
}

func (r *SQLiteTrackerRepository) Update(ctx context.Context, source *networkpolicy.TrackerSource) error {
	res, err := r.db.conn.ExecContext(ctx, `UPDATE tracker_sources SET name=?, url=?, enabled=?,
		refresh_interval_seconds=?, last_checked_at=?, last_success_at=?, etag=?,
		last_modified=?, last_error=?, tracker_count=?, updated_at=? WHERE id=?`,
		source.Name, source.URL, source.Enabled, source.RefreshIntervalSeconds,
		source.LastCheckedAt, source.LastSuccessAt, source.ETag, source.LastModified,
		source.LastError, source.TrackerCount, source.UpdatedAt, source.ID)
	if err != nil {
		return err
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		return fmt.Errorf("tracker source not found")
	}
	return nil
}

func (r *SQLiteTrackerRepository) Delete(ctx context.Context, id string) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tracker_source_entries WHERE source_id=?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM tracker_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return fmt.Errorf("tracker source not found")
	}
	return tx.Commit()
}

func (r *SQLiteTrackerRepository) Entries(ctx context.Context, sourceID string) ([]string, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT tracker_url FROM tracker_source_entries WHERE source_id=? ORDER BY tracker_url`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *SQLiteTrackerRepository) EnabledEntries(ctx context.Context) ([]string, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT DISTINCT e.tracker_url FROM tracker_source_entries e
		JOIN tracker_sources s ON s.id=e.source_id WHERE s.enabled=1 ORDER BY e.tracker_url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *SQLiteTrackerRepository) ReplaceEntries(ctx context.Context, source *networkpolicy.TrackerSource, entries []string) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tracker_source_entries WHERE source_id=?`, source.ID); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_source_entries(source_id, tracker_url, created_at) VALUES (?, ?, ?)`, source.ID, entry, source.UpdatedAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tracker_sources SET last_checked_at=?, last_success_at=?,
		etag=?, last_modified=?, last_error='', tracker_count=?, updated_at=? WHERE id=?`,
		source.LastCheckedAt, source.LastSuccessAt, source.ETag, source.LastModified,
		len(entries), source.UpdatedAt, source.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteTrackerRepository) RecordFailure(ctx context.Context, id, message string, checkedAt time.Time) error {
	_, err := r.db.conn.ExecContext(ctx, `UPDATE tracker_sources SET last_checked_at=?, last_error=?, updated_at=? WHERE id=?`,
		checkedAt, message, checkedAt, id)
	return err
}
