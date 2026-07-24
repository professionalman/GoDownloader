package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"downloader/internal/job"
)

// DB wraps a SQLite connection.
type DB struct {
	conn *sql.DB
}

// New opens a SQLite database at dbPath and runs migrations.
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

func (db *DB) migrate() error {
	// Create table if not exists with V0.2 schema
	createTable := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'queued',
		total_bytes INTEGER NOT NULL DEFAULT 0,
		completed_bytes INTEGER NOT NULL DEFAULT 0,
		progress REAL NOT NULL DEFAULT 0,
		speed_bytes_per_second INTEGER NOT NULL DEFAULT 0,
		eta_seconds INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		engine TEXT NOT NULL DEFAULT 'aria2',
		engine_id TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL DEFAULT 'download',
		media_info TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`
	if _, err := db.conn.Exec(createTable); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// Migrate from V0.1 schema if needed
	db.migrateFromV01()

	db.migrateToV03()

	return nil
}

// migrateFromV01 handles migration from V0.1 schema.
func (db *DB) migrateFromV01() {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return
	}
	defer rows.Close()

	hasAria2GID := false
	hasEngineID := false
	hasSpeedOld := false
	hasSpeedNew := false
	hasETASeconds := false
	hasEngine := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			continue
		}
		switch name {
		case "aria2_gid":
			hasAria2GID = true
		case "engine_id":
			hasEngineID = true
		case "speed":
			hasSpeedOld = true
		case "speed_bytes_per_second":
			hasSpeedNew = true
		case "eta_seconds":
			hasETASeconds = true
		case "engine":
			hasEngine = true
		}
	}

	// Add missing columns for V0.2
	if !hasEngineID && hasAria2GID {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN engine_id TEXT NOT NULL DEFAULT ''")
		db.conn.Exec("UPDATE jobs SET engine_id = aria2_gid WHERE aria2_gid != ''")
	}
	if !hasEngine {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN engine TEXT NOT NULL DEFAULT 'aria2'")
	}
	if !hasSpeedNew && hasSpeedOld {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN speed_bytes_per_second INTEGER NOT NULL DEFAULT 0")
		db.conn.Exec("UPDATE jobs SET speed_bytes_per_second = speed")
	}
	if !hasETASeconds {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN eta_seconds INTEGER NOT NULL DEFAULT 0")
	}

	// Convert uppercase statuses to lowercase (V0.1 → V0.2)
	db.conn.Exec("UPDATE jobs SET status = LOWER(status) WHERE status != LOWER(status)")
}

// migrateToV03 adds V0.3 media download columns.
func (db *DB) migrateToV03() {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return
	}
	defer rows.Close()

	hasType := false
	hasMediaInfo := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			continue
		}
		switch name {
		case "type":
			hasType = true
		case "media_info":
			hasMediaInfo = true
		}
	}

	if !hasType {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN type TEXT NOT NULL DEFAULT 'download'")
	}
	if !hasMediaInfo {
		db.conn.Exec("ALTER TABLE jobs ADD COLUMN media_info TEXT NOT NULL DEFAULT ''")
	}
}

// Conn returns the underlying sql.DB connection.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Transaction executes a function within a database transaction.
func (db *DB) Transaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// --- Legacy V0.1 compat methods that delegate to repository ---

// CreateJob inserts a new job into the database.
func (db *DB) CreateJob(j *job.Job) error {
	repo := NewSQLiteJobRepository(db)
	return repo.Create(context.Background(), j)
}

// GetJob retrieves a single job by ID.
func (db *DB) GetJob(id string) (*job.Job, error) {
	repo := NewSQLiteJobRepository(db)
	return repo.GetByID(context.Background(), id)
}

// GetAllJobs retrieves all jobs.
func (db *DB) GetAllJobs() ([]*job.Job, error) {
	repo := NewSQLiteJobRepository(db)
	jobs, err := repo.List(context.Background())
	if err != nil {
		return nil, err
	}
	result := make([]*job.Job, len(jobs))
	for i := range jobs {
		result[i] = &jobs[i]
	}
	return result, nil
}

// UpdateJob updates an existing job.
func (db *DB) UpdateJob(j *job.Job) error {
	j.UpdatedAt = time.Now()
	repo := NewSQLiteJobRepository(db)
	return repo.Update(context.Background(), j)
}
