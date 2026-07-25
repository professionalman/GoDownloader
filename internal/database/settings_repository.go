package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"downloader/internal/settings"
)

var _ settings.ISettingsRepository = (*SQLiteSettingsRepository)(nil)

// SQLiteSettingsRepository implements settings.ISettingsRepository using SQLite.
type SQLiteSettingsRepository struct {
	db *DB
}

// NewSQLiteSettingsRepository creates a new SQLite-backed settings repository.
func NewSQLiteSettingsRepository(db *DB) *SQLiteSettingsRepository {
	return &SQLiteSettingsRepository{db: db}
}

// Get retrieves a setting value by key. Returns "" if key does not exist.
func (r *SQLiteSettingsRepository) Get(ctx context.Context, key string) (string, error) {
	var val string
	err := r.db.conn.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return val, nil
}

// Set inserts or updates a setting value by key.
func (r *SQLiteSettingsRepository) Set(ctx context.Context, key, value string) error {
	now := time.Now()
	query := `INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`
	_, err := r.db.conn.ExecContext(ctx, query, key, value, now)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}
