package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
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
		updated_at DATETIME NOT NULL,
		engine_cleanup_pending BOOLEAN NOT NULL DEFAULT 0
	);`
	if _, err := db.conn.Exec(createTable); err != nil {
		return fmt.Errorf("create table jobs: %w", err)
	}

	// Migrate from V0.1 schema if needed
	if err := db.migrateFromV01(); err != nil {
		return fmt.Errorf("migrate from V0.1: %w", err)
	}

	if err := db.migrateToV03(); err != nil {
		return fmt.Errorf("migrate to V0.3: %w", err)
	}
	if err := db.migrateToV04(); err != nil {
		return fmt.Errorf("migrate to V0.4: %w", err)
	}
	if err := db.migrateToV05(); err != nil {
		return fmt.Errorf("migrate to V0.5: %w", err)
	}
	if err := db.migrateToV06(); err != nil {
		return fmt.Errorf("migrate to V0.6: %w", err)
	}
	if err := db.migrateV06EngineCleanupPending(); err != nil {
		return fmt.Errorf("migrate V0.6 engine cleanup lifecycle: %w", err)
	}
	if err := db.migrateToV07NetworkControls(); err != nil {
		return fmt.Errorf("migrate to V0.7 network controls: %w", err)
	}

	return nil
}

func (db *DB) migrateToV07NetworkControls() error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin V0.7 migration: %w", err)
	}
	defer tx.Rollback()
	var migrationDone int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v07_network_controls_migrated'`).Scan(&migrationDone); err != nil {
		return fmt.Errorf("read V0.7 migration marker: %w", err)
	}

	addColumn := func(table, name, ddl string) error {
		rows, err := tx.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var cid, notNull, pk int
			var colName, typ string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &colName, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			if colName == name {
				found = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if found {
			return nil
		}
		_, err = tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl)
		return err
	}

	jobColumns := []struct{ name, ddl string }{
		{"network_policy_json", "network_policy_json TEXT NOT NULL DEFAULT '{}'"},
		{"effective_download_limit_bps", "effective_download_limit_bps INTEGER NOT NULL DEFAULT 0"},
		{"effective_upload_limit_bps", "effective_upload_limit_bps INTEGER NOT NULL DEFAULT 0"},
		{"network_reconcile_pending", "network_reconcile_pending BOOLEAN NOT NULL DEFAULT 0"},
	}
	for _, col := range jobColumns {
		if err := addColumn("jobs", col.name, col.ddl); err != nil {
			return fmt.Errorf("add jobs.%s: %w", col.name, err)
		}
	}

	torrentColumns := []struct{ name, ddl string }{
		{"seeding_mode", "seeding_mode TEXT NOT NULL DEFAULT 'none'"},
		{"seed_ratio_limit", "seed_ratio_limit REAL"},
		{"seed_time_limit_seconds", "seed_time_limit_seconds INTEGER"},
		{"seeding_started_at", "seeding_started_at DATETIME"},
		{"seeding_stop_reason", "seeding_stop_reason TEXT NOT NULL DEFAULT ''"},
		{"seeding_reconcile_pending", "seeding_reconcile_pending BOOLEAN NOT NULL DEFAULT 0"},
		{"custom_trackers_json", "custom_trackers_json TEXT NOT NULL DEFAULT '[]'"},
	}
	for _, col := range torrentColumns {
		if err := addColumn("torrent_jobs", col.name, col.ddl); err != nil {
			return fmt.Errorf("add torrent_jobs.%s: %w", col.name, err)
		}
	}

	if migrationDone == 0 {
		if _, err := tx.Exec(`UPDATE torrent_jobs
			SET seeding_mode = CASE WHEN seed_after_complete = 1 THEN 'unlimited' ELSE 'none' END`); err != nil {
			return fmt.Errorf("backfill seeding policy: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO app_settings (key, value, updated_at)
			VALUES ('v07_network_controls_migrated', '1', ?)`, time.Now()); err != nil {
			return fmt.Errorf("write V0.7 migration marker: %w", err)
		}
	}

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS encrypted_secrets (
		scope TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		field_name TEXT NOT NULL,
		ciphertext BLOB NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (scope, owner_id, field_name)
	)`); err != nil {
		return fmt.Errorf("create encrypted_secrets: %w", err)
	}

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS tracker_sources (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		url TEXT NOT NULL UNIQUE,
		enabled BOOLEAN NOT NULL DEFAULT 1,
		refresh_interval_seconds INTEGER NOT NULL,
		etag TEXT NOT NULL DEFAULT '',
		last_modified TEXT NOT NULL DEFAULT '',
		last_checked_at DATETIME,
		last_success_at DATETIME,
		last_error TEXT NOT NULL DEFAULT '',
		tracker_count INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("create tracker_sources: %w", err)
	}
	if err := addColumn("tracker_sources", "tracker_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("add tracker_sources.tracker_count: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS tracker_source_entries (
		source_id TEXT NOT NULL,
		tracker_url TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (source_id, tracker_url),
		FOREIGN KEY (source_id) REFERENCES tracker_sources(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("create tracker_source_entries: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_tracker_sources_due
		ON tracker_sources(enabled, last_checked_at)`); err != nil {
		return fmt.Errorf("create tracker source index: %w", err)
	}

	return tx.Commit()
}

// migrateFromV01 handles migration from V0.1 schema.
func (db *DB) migrateFromV01() error {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return fmt.Errorf("query pragma table_info(jobs): %w", err)
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
			return fmt.Errorf("scan pragma table_info(jobs): %w", err)
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
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err pragma table_info(jobs): %w", err)
	}

	// Add missing columns for V0.2
	if !hasEngineID && hasAria2GID {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN engine_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column engine_id: %w", err)
		}
		if _, err := db.conn.Exec("UPDATE jobs SET engine_id = aria2_gid WHERE aria2_gid != ''"); err != nil {
			return fmt.Errorf("update engine_id from aria2_gid: %w", err)
		}
	}
	if !hasEngine {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN engine TEXT NOT NULL DEFAULT 'aria2'"); err != nil {
			return fmt.Errorf("add column engine: %w", err)
		}
	}
	if !hasSpeedNew && hasSpeedOld {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN speed_bytes_per_second INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add column speed_bytes_per_second: %w", err)
		}
		if _, err := db.conn.Exec("UPDATE jobs SET speed_bytes_per_second = speed"); err != nil {
			return fmt.Errorf("update speed_bytes_per_second: %w", err)
		}
	}
	if !hasETASeconds {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN eta_seconds INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add column eta_seconds: %w", err)
		}
	}

	// Convert uppercase statuses to lowercase (V0.1 → V0.2)
	if _, err := db.conn.Exec("UPDATE jobs SET status = LOWER(status) WHERE status != LOWER(status)"); err != nil {
		return fmt.Errorf("lowercase job status: %w", err)
	}

	return nil
}

// migrateToV03 adds V0.3 media download columns.
func (db *DB) migrateToV03() error {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return fmt.Errorf("query pragma table_info(jobs): %w", err)
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
			return fmt.Errorf("scan pragma table_info(jobs): %w", err)
		}
		switch name {
		case "type":
			hasType = true
		case "media_info":
			hasMediaInfo = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err pragma table_info(jobs): %w", err)
	}

	if !hasType {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN type TEXT NOT NULL DEFAULT 'download'"); err != nil {
			return fmt.Errorf("add column type: %w", err)
		}
	}
	if !hasMediaInfo {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN media_info TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column media_info: %w", err)
		}
	}

	return nil
}

func (db *DB) migrateToV04() error {
	// Create torrent_jobs table
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS torrent_jobs (
		job_id TEXT PRIMARY KEY,
		info_hash TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		total_size INTEGER NOT NULL DEFAULT 0,
		seed_after_complete INTEGER NOT NULL DEFAULT 0,
		torrent_file_path TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	)`); err != nil {
		return fmt.Errorf("create table torrent_jobs: %w", err)
	}

	// Create torrent_files table
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS torrent_files (
		job_id TEXT NOT NULL,
		file_index INTEGER NOT NULL,
		path TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL DEFAULT 0,
		selected INTEGER NOT NULL DEFAULT 1,
		priority TEXT NOT NULL DEFAULT 'normal',
		PRIMARY KEY (job_id, file_index),
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	)`); err != nil {
		return fmt.Errorf("create table torrent_files: %w", err)
	}

	// Add index for looking up torrent jobs by info_hash
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_torrent_jobs_info_hash ON torrent_jobs(info_hash)`); err != nil {
		return fmt.Errorf("create index idx_torrent_jobs_info_hash: %w", err)
	}

	return nil
}

func (db *DB) migrateToV05() error {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return fmt.Errorf("query pragma table_info(jobs): %w", err)
	}
	defer rows.Close()

	hasPriority := false
	hasBatchID := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan pragma table_info(jobs): %w", err)
		}
		switch name {
		case "priority":
			hasPriority = true
		case "batch_id":
			hasBatchID = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err pragma table_info(jobs): %w", err)
	}

	if !hasPriority {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'"); err != nil {
			return fmt.Errorf("add column priority: %w", err)
		}
	}
	if !hasBatchID {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN batch_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column batch_id: %w", err)
		}
	}

	// Create job_queue table
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS job_queue (
		job_id TEXT PRIMARY KEY,
		position INTEGER NOT NULL,
		action TEXT NOT NULL,
		enqueued_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	)`); err != nil {
		return fmt.Errorf("create table job_queue: %w", err)
	}

	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_job_queue_position ON job_queue(position)`); err != nil {
		return fmt.Errorf("create index idx_job_queue_position: %w", err)
	}

	// Create app_settings table
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("create table app_settings: %w", err)
	}

	// Insert default max_concurrent_downloads if missing
	now := time.Now()
	if _, err := db.conn.Exec(`INSERT OR IGNORE INTO app_settings (key, value, updated_at) VALUES ('max_concurrent_downloads', '3', ?)`, now); err != nil {
		return fmt.Errorf("insert default app_settings: %w", err)
	}

	// Backfill legacy QUEUED jobs that do not have a job_queue row yet
	queuedRows, err := db.conn.Query("SELECT j.id, j.created_at FROM jobs j LEFT JOIN job_queue q ON j.id = q.job_id WHERE j.status = 'queued' AND q.job_id IS NULL ORDER BY j.created_at ASC, j.id ASC")
	if err != nil {
		return fmt.Errorf("query backfill legacy queued jobs: %w", err)
	}
	defer queuedRows.Close()

	type queuedBackfill struct {
		id        string
		createdAt time.Time
	}
	var backfills []queuedBackfill
	for queuedRows.Next() {
		var b queuedBackfill
		if scanErr := queuedRows.Scan(&b.id, &b.createdAt); scanErr != nil {
			return fmt.Errorf("scan backfill legacy queued job: %w", scanErr)
		}
		backfills = append(backfills, b)
	}
	if err := queuedRows.Err(); err != nil {
		return fmt.Errorf("rows.Err backfill legacy queued jobs: %w", err)
	}

	var pos int64 = 1
	for _, b := range backfills {
		if _, execErr := db.conn.Exec(`INSERT OR IGNORE INTO job_queue (job_id, position, action, enqueued_at, updated_at) VALUES (?, ?, 'start', ?, ?)`, b.id, pos, b.createdAt, b.createdAt); execErr != nil {
			return fmt.Errorf("insert backfill legacy queued job %s: %w", b.id, execErr)
		}
		pos++
	}

	return nil
}

func (db *DB) migrateToV06() error {
	// Create categories table
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS categories (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE,
		directory TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("create table categories: %w", err)
	}

	if _, err := db.conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_name ON categories(name COLLATE NOCASE)`); err != nil {
		return fmt.Errorf("create index idx_categories_name: %w", err)
	}

	// PRAGMA table_info to check missing V0.6 columns in jobs
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return fmt.Errorf("query pragma table_info(jobs): %w", err)
	}
	defer rows.Close()

	hasCategoryID := false
	hasDestinationDir := false
	hasWorkDir := false
	hasConflictPolicy := false
	hasFinalPath := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan pragma table_info(jobs): %w", err)
		}
		switch name {
		case "category_id":
			hasCategoryID = true
		case "destination_dir":
			hasDestinationDir = true
		case "work_dir":
			hasWorkDir = true
		case "conflict_policy":
			hasConflictPolicy = true
		case "final_path":
			hasFinalPath = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err pragma table_info(jobs): %w", err)
	}

	if !hasCategoryID {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN category_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column category_id: %w", err)
		}
	}
	if !hasDestinationDir {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN destination_dir TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column destination_dir: %w", err)
		}
	}
	if !hasWorkDir {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN work_dir TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column work_dir: %w", err)
		}
	}
	if !hasConflictPolicy {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN conflict_policy TEXT NOT NULL DEFAULT 'rename'"); err != nil {
			return fmt.Errorf("add column conflict_policy: %w", err)
		}
	}
	if !hasFinalPath {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN final_path TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column final_path: %w", err)
		}
	}

	// Seed initial categories ONLY on first migration to V0.6 (when category_id column was missing)
	if !hasCategoryID {
		now := time.Now()
		seeds := []struct {
			name string
			dir  string
		}{
			{"Video", "Video"},
			{"Music", "Music"},
			{"Archives", "Archives"},
			{"Torrents", "Torrents"},
		}
		for _, seed := range seeds {
			catID := uuid.New().String()
			if _, err := db.conn.Exec("INSERT OR IGNORE INTO categories (id, name, directory, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
				catID, seed.name, seed.dir, now, now); err != nil {
				return fmt.Errorf("seed default category %s: %w", seed.name, err)
			}
		}
	}

	return nil
}

func (db *DB) migrateV06EngineCleanupPending() error {
	rows, err := db.conn.Query("PRAGMA table_info(jobs)")
	if err != nil {
		return fmt.Errorf("query pragma table_info(jobs): %w", err)
	}
	defer rows.Close()

	hasEngineCleanupPending := false

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan pragma table_info(jobs): %w", err)
		}
		if name == "engine_cleanup_pending" {
			hasEngineCleanupPending = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows.Err pragma table_info(jobs): %w", err)
	}

	if !hasEngineCleanupPending {
		if _, err := db.conn.Exec("ALTER TABLE jobs ADD COLUMN engine_cleanup_pending BOOLEAN NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add column engine_cleanup_pending: %w", err)
		}
	}

	return nil
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
