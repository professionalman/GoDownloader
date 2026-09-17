package database_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"downloader/internal/database"
	"downloader/internal/job"
)

// setupV072FixtureDB creates a database with the exact pre-FND-1 (V0.7.2) schema and representative data.
func setupV072FixtureDB(t *testing.T, dbPath string) {
	t.Helper()
	rawConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite db: %v", err)
	}
	defer rawConn.Close()

	schema := `
	CREATE TABLE jobs (
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
		engine_cleanup_pending BOOLEAN NOT NULL DEFAULT 0,
		category_id TEXT NOT NULL DEFAULT '',
		destination_dir TEXT NOT NULL DEFAULT '',
		work_dir TEXT NOT NULL DEFAULT '',
		conflict_policy TEXT NOT NULL DEFAULT 'rename',
		final_path TEXT NOT NULL DEFAULT '',
		priority TEXT NOT NULL DEFAULT 'normal',
		batch_id TEXT NOT NULL DEFAULT '',
		network_policy_json TEXT NOT NULL DEFAULT '{}',
		effective_download_limit_bps INTEGER NOT NULL DEFAULT 0,
		effective_upload_limit_bps INTEGER NOT NULL DEFAULT 0,
		network_reconcile_pending BOOLEAN NOT NULL DEFAULT 0
	);

	CREATE TABLE job_queue (
		job_id TEXT PRIMARY KEY,
		position INTEGER NOT NULL,
		action TEXT NOT NULL,
		enqueued_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE TABLE torrent_jobs (
		job_id TEXT PRIMARY KEY,
		info_hash TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		total_size INTEGER NOT NULL DEFAULT 0,
		seed_after_complete INTEGER NOT NULL DEFAULT 0,
		torrent_file_path TEXT NOT NULL DEFAULT '',
		seeding_mode TEXT NOT NULL DEFAULT 'none',
		seed_ratio_limit REAL,
		seed_time_limit_seconds INTEGER,
		seeding_started_at DATETIME,
		seeding_stop_reason TEXT NOT NULL DEFAULT '',
		seeding_reconcile_pending BOOLEAN NOT NULL DEFAULT 0,
		custom_trackers_json TEXT NOT NULL DEFAULT '[]',
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE TABLE torrent_files (
		job_id TEXT NOT NULL,
		file_index INTEGER NOT NULL,
		path TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL DEFAULT 0,
		selected INTEGER NOT NULL DEFAULT 1,
		priority TEXT NOT NULL DEFAULT 'normal',
		PRIMARY KEY (job_id, file_index),
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	);

	CREATE TABLE categories (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL COLLATE NOCASE,
		directory TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE encrypted_secrets (
		scope TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		field_name TEXT NOT NULL,
		ciphertext BLOB NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (scope, owner_id, field_name)
	);

	CREATE TABLE tracker_sources (
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
	);

	CREATE TABLE tracker_source_entries (
		source_id TEXT NOT NULL,
		tracker_url TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (source_id, tracker_url),
		FOREIGN KEY (source_id) REFERENCES tracker_sources(id) ON DELETE CASCADE
	);
	`
	if _, err := rawConn.Exec(schema); err != nil {
		t.Fatalf("create V0.7.2 fixture schema: %v", err)
	}

	// Insert realistic fixture records
	now := time.Now()

	// 1. Normal HTTP job
	_, err = rawConn.Exec(`INSERT INTO jobs (id, source, name, status, engine, type, created_at, updated_at)
		VALUES ('job-v7-http', 'https://example.com/test.zip', 'test.zip', 'queued', 'aria2', 'download', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert job-v7-http: %v", err)
	}

	// 2. Queue entry for http job
	_, err = rawConn.Exec(`INSERT INTO job_queue (job_id, position, action, enqueued_at, updated_at)
		VALUES ('job-v7-http', 1, 'start', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert job_queue: %v", err)
	}

	// 3. Torrent job with files
	_, err = rawConn.Exec(`INSERT INTO jobs (id, source, name, status, engine, type, created_at, updated_at)
		VALUES ('job-v7-torrent', 'magnet:?xt=urn:btih:abcdef', 'Ubuntu ISO', 'downloading', 'qbittorrent', 'torrent', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert job-v7-torrent: %v", err)
	}
	_, err = rawConn.Exec(`INSERT INTO torrent_jobs (job_id, info_hash, name, total_size, seed_after_complete, seeding_mode)
		VALUES ('job-v7-torrent', 'abcdef123456', 'Ubuntu ISO', 2048576, 0, 'none')`)
	if err != nil {
		t.Fatalf("insert torrent_jobs: %v", err)
	}
	_, err = rawConn.Exec(`INSERT INTO torrent_files (job_id, file_index, path, size, selected, priority)
		VALUES ('job-v7-torrent', 0, 'ubuntu.iso', 2048576, 1, 'normal')`)
	if err != nil {
		t.Fatalf("insert torrent_files: %v", err)
	}

	// 4. Category
	_, err = rawConn.Exec(`INSERT INTO categories (id, name, directory, created_at, updated_at)
		VALUES ('cat-v7-1', 'ISO Images', 'ISO', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert categories: %v", err)
	}

	// 5. App settings (including V0.7 migration marker)
	_, err = rawConn.Exec(`INSERT INTO app_settings (key, value, updated_at)
		VALUES ('v07_network_controls_migrated', '1', ?)`, now)
	if err != nil {
		t.Fatalf("insert app_settings: %v", err)
	}
	_, err = rawConn.Exec(`INSERT INTO app_settings (key, value, updated_at)
		VALUES ('max_concurrent_downloads', '5', ?)`, now)
	if err != nil {
		t.Fatalf("insert max_concurrent_downloads: %v", err)
	}

	// 6. Encrypted secret
	_, err = rawConn.Exec(`INSERT INTO encrypted_secrets (scope, owner_id, field_name, ciphertext, updated_at)
		VALUES ('job', 'job-v7-http', 'cookie', X'DEADBEEF', ?)`, now)
	if err != nil {
		t.Fatalf("insert encrypted_secrets: %v", err)
	}

	// 7. Tracker source and entry
	_, err = rawConn.Exec(`INSERT INTO tracker_sources (id, name, url, refresh_interval_seconds, tracker_count, created_at, updated_at)
		VALUES ('ts-1', 'Public Trackers', 'https://trackers.example/list.txt', 3600, 1, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert tracker_sources: %v", err)
	}
	_, err = rawConn.Exec(`INSERT INTO tracker_source_entries (source_id, tracker_url, created_at)
		VALUES ('ts-1', 'udp://tracker.example:1337/announce', ?)`, now)
	if err != nil {
		t.Fatalf("insert tracker_source_entries: %v", err)
	}
}

func TestV08Migration_FromRealV072Fixture(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v072_migration.db")

	// 1. Create real V0.7.2 DB fixture
	setupV072FixtureDB(t, dbPath)

	// 2. Open with database.New (runs all migrations including migrateToV08Fnd1)
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New on V0.7.2 fixture failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 3. Verify V0.8 migration marker is recorded
	var markerVal string
	err = db.Conn().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&markerVal)
	if err != nil || markerVal != "1" {
		t.Fatalf("expected v08_fnd1_migrated = '1', got %q (err=%v)", markerVal, err)
	}

	// 3a. Verify pre-migration backup file was created
	backupPath := dbPath + ".pre-v08.bak"
	backupFi, err := os.Stat(backupPath)
	if err != nil || backupFi.Size() == 0 {
		t.Fatalf("expected non-empty pre-migration backup at %s, got fi=%v, err=%v", backupPath, backupFi, err)
	}

	// Verify backup can be opened and contains the pre-migration V0.7.2 state (no FND-1 tables yet)
	bakConn, err := sql.Open("sqlite3", backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer bakConn.Close()
	var bakTableCount int
	err = bakConn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='job_executions'").Scan(&bakTableCount)
	if err != nil || bakTableCount != 0 {
		t.Fatalf("expected backup to have 0 job_executions tables (pre-migration), got %d (err=%v)", bakTableCount, err)
	}

	// 4. Verify existing jobs preserved
	jobRepo := database.NewSQLiteJobRepository(db)
	jHTTP, err := jobRepo.GetByID(ctx, "job-v7-http")
	if err != nil || jHTTP == nil {
		t.Fatalf("expected job-v7-http preserved, got %+v (err=%v)", jHTTP, err)
	}
	if jHTTP.Name != "test.zip" || jHTTP.Engine != "aria2" {
		t.Fatalf("job-v7-http fields mismatch: %+v", jHTTP)
	}

	jTorrent, err := jobRepo.GetByID(ctx, "job-v7-torrent")
	if err != nil || jTorrent == nil {
		t.Fatalf("expected job-v7-torrent preserved, got %+v (err=%v)", jTorrent, err)
	}

	// 5. Verify existing queue preserved and new columns present with correct defaults
	queueRepo := database.NewSQLiteQueueRepository(db)
	qEntry, err := queueRepo.Get(ctx, "job-v7-http")
	if err != nil || qEntry == nil {
		t.Fatalf("expected queue entry preserved, got %+v (err=%v)", qEntry, err)
	}
	if qEntry.Position != 1 || qEntry.Action != job.QueueActionStart {
		t.Fatalf("queue entry fields mismatch: %+v", qEntry)
	}
	if qEntry.RetryCount != 0 {
		t.Fatalf("expected default retry_count = 0, got %d", qEntry.RetryCount)
	}
	if qEntry.NotBefore != nil {
		t.Fatalf("expected default not_before = nil, got %v", qEntry.NotBefore)
	}

	// 6. Verify existing settings preserved
	var maxConcurrent string
	err = db.Conn().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'max_concurrent_downloads'`).Scan(&maxConcurrent)
	if err != nil || maxConcurrent != "5" {
		t.Fatalf("expected max_concurrent_downloads = 5, got %q (err=%v)", maxConcurrent, err)
	}

	// 7. Verify existing secrets preserved
	var secretBytes []byte
	err = db.Conn().QueryRowContext(ctx, `SELECT ciphertext FROM encrypted_secrets WHERE owner_id = 'job-v7-http'`).Scan(&secretBytes)
	if err != nil || len(secretBytes) == 0 {
		t.Fatalf("expected encrypted secret preserved, got %x (err=%v)", secretBytes, err)
	}

	// 8. Verify all 6 new FND-1 tables exist
	tables := []string{
		"job_executions",
		"http_checkpoints",
		"http_checkpoint_segments",
		"tool_records",
		"event_cursors",
		"finalization_records",
	}
	for _, tbl := range tables {
		var count int
		err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("expected table %s to exist, got count=%d (err=%v)", tbl, count, err)
		}
	}

	// 9. Reopen database and verify idempotent rerun
	db.Close()
	dbReopened, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen database failed: %v", err)
	}
	defer dbReopened.Close()

	// Verify marker is still '1'
	err = dbReopened.Conn().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&markerVal)
	if err != nil || markerVal != "1" {
		t.Fatalf("after reopen: expected v08_fnd1_migrated = '1', got %q (err=%v)", markerVal, err)
	}
}

func TestV08Migration_QueueRetryScheduling(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_retry_sched.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer db.Close()

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)

	now := time.Now()
	// Job 1: queued with not_before in future (+1 hour)
	future := now.Add(1 * time.Hour)
	j1 := &job.Job{
		ID:        "job-future-retry",
		Source:    "https://example.com/1",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(ctx, j1); err != nil {
		t.Fatalf("create j1: %v", err)
	}
	if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j1.ID,
		Position:   1,
		Action:     job.QueueActionResume,
		RetryCount: 2,
		NotBefore:  &future,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("enqueue j1: %v", err)
	}

	// Job 2: queued with not_before in past (-1 minute)
	past := now.Add(-1 * time.Minute)
	j2 := &job.Job{
		ID:        "job-ready-retry",
		Source:    "https://example.com/2",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(ctx, j2); err != nil {
		t.Fatalf("create j2: %v", err)
	}
	if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j2.ID,
		Position:   2,
		Action:     job.QueueActionResume,
		RetryCount: 1,
		NotBefore:  &past,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("enqueue j2: %v", err)
	}

	// NextRunnable should skip j1 (future) and pick j2 (ready)
	next, err := queueRepo.NextRunnable(ctx)
	if err != nil {
		t.Fatalf("NextRunnable: %v", err)
	}
	if next == nil || next.JobID != "job-ready-retry" {
		t.Fatalf("expected job-ready-retry as NextRunnable, got %+v", next)
	}
	if next.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", next.RetryCount)
	}
}

func TestV08Migration_TransactionRollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v08_rollback.db")

	// 1. Create real V0.7.2 DB fixture
	setupV072FixtureDB(t, dbPath)

	rawConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}

	// 2. Insert a conflicting VIEW named 'job_executions' so CREATE TABLE job_executions will fail
	if _, err := rawConn.Exec(`CREATE VIEW job_executions AS SELECT 1 AS id`); err != nil {
		t.Fatalf("create conflicting view: %v", err)
	}
	rawConn.Close()

	// 3. Opening database.New should fail because migrateToV08Fnd1 fails on CREATE TABLE job_executions
	_, err = database.New(dbPath)
	if err == nil {
		t.Fatal("expected database.New to fail due to conflicting view, but it succeeded")
	}

	// 4. Verify transaction rollback: marker 'v08_fnd1_migrated' must NOT be recorded
	rawConnCheck, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("reopen raw db for check: %v", err)
	}
	defer rawConnCheck.Close()

	var markerCount int
	err = rawConnCheck.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&markerCount)
	if err != nil {
		t.Fatalf("query marker count: %v", err)
	}
	if markerCount != 0 {
		t.Fatalf("expected marker not recorded on rollback, but count=%d", markerCount)
	}

	// Verify pre-migration backup was created and preserved intact after rollback
	backupPath := dbPath + ".pre-v08.bak"
	backupBytesBefore, err := os.ReadFile(backupPath)
	if err != nil || len(backupBytesBefore) == 0 {
		t.Fatalf("expected pre-migration backup preserved after rollback, got %d bytes, err=%v", len(backupBytesBefore), err)
	}

	// 5. Drop the conflicting view and verify that migration subsequently succeeds completely
	// without requiring manual deletion of the backup file.
	if _, err := rawConnCheck.Exec(`DROP VIEW job_executions`); err != nil {
		t.Fatalf("drop conflicting view: %v", err)
	}
	rawConnCheck.Close()

	dbRecovered, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("subsequent database.New failed: %v", err)
	}
	defer dbRecovered.Close()

	var finalMarker string
	err = dbRecovered.Conn().QueryRow(`SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&finalMarker)
	if err != nil || finalMarker != "1" {
		t.Fatalf("expected marker = '1' after recovery, got %q (err=%v)", finalMarker, err)
	}

	// Verify pre-migration backup was preserved intact (not deleted, not modified)
	backupBytesAfter, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup after retry: %v", err)
	}
	if !bytes.Equal(backupBytesBefore, backupBytesAfter) {
		t.Fatalf("expected backup to be preserved unchanged across retry, but content changed")
	}
}

func TestV08Migration_BackupRetryPreservationAndReopen(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "backup_retry_test.db")

	// 1. Create real V0.7.2 DB fixture
	setupV072FixtureDB(t, dbPath)

	// Inject a failure condition in migration by creating a VIEW that collides with FND-1 tables
	rawConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := rawConn.Exec(`CREATE VIEW job_executions AS SELECT 1 AS id`); err != nil {
		t.Fatalf("create collision view: %v", err)
	}
	rawConn.Close()

	// 2. Open with database.New — this creates backup via VACUUM INTO, then fails inside migration transaction
	failDB, err := database.New(dbPath)
	if err == nil {
		failDB.Close()
		t.Fatal("expected migration to fail due to colliding job_executions view")
	}

	// 3. Confirm migration marker is absent
	checkConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db for check: %v", err)
	}
	var markerCount int
	err = checkConn.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&markerCount)
	if err != nil || markerCount != 0 {
		t.Fatalf("expected 0 migrated marker on failure, got %d, err=%v", markerCount, err)
	}

	// 4. Confirm .pre-v08.bak exists and is non-empty
	backupPath := dbPath + ".pre-v08.bak"
	originalBackupBytes, err := os.ReadFile(backupPath)
	if err != nil || len(originalBackupBytes) == 0 {
		t.Fatalf("expected valid non-empty backup file, got %d bytes, err=%v", len(originalBackupBytes), err)
	}
	originalHash := sha256.Sum256(originalBackupBytes)

	// Verify backup can be opened and reflects pre-migration state (no job_executions table)
	bakConn, err := sql.Open("sqlite3", backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	var bakExecCount int
	err = bakConn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='job_executions'`).Scan(&bakExecCount)
	bakConn.Close()
	if err != nil || bakExecCount != 0 {
		t.Fatalf("backup should not have job_executions table, got count=%d, err=%v", bakExecCount, err)
	}

	// 5. Remove the injected failure (drop colliding view)
	if _, err := checkConn.Exec(`DROP VIEW job_executions`); err != nil {
		t.Fatalf("drop colliding view: %v", err)
	}
	checkConn.Close()

	// 6. Retry: reopen database WITHOUT deleting the backup file
	dbRetry, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("retry database.New failed: %v", err)
	}

	// 7. Verify migration succeeded on retry
	var retryMarker string
	err = dbRetry.Conn().QueryRow(`SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&retryMarker)
	if err != nil || retryMarker != "1" {
		t.Fatalf("expected marker '1' on retry, got %q (err=%v)", retryMarker, err)
	}

	// Verify all FND-1 tables exist now
	for _, tbl := range []string{"job_executions", "http_checkpoints", "http_checkpoint_segments", "tool_records", "event_cursors", "finalization_records"} {
		var cnt int
		err := dbRetry.Conn().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&cnt)
		if err != nil || cnt != 1 {
			t.Fatalf("expected table %s after retry, got %d (err=%v)", tbl, cnt, err)
		}
	}
	dbRetry.Close()

	// 8. Verify the original backup file was preserved byte-for-byte and not overwritten or deleted
	retryBackupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup after retry: %v", err)
	}
	retryHash := sha256.Sum256(retryBackupBytes)
	if originalHash != retryHash {
		t.Fatalf("backup hash changed after retry: expected %x, got %x", originalHash, retryHash)
	}

	// 9. Reopen database third time (normal subsequent run with marker present)
	dbSubsequent, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("subsequent database.New failed: %v", err)
	}
	defer dbSubsequent.Close()

	var subMarker string
	err = dbSubsequent.Conn().QueryRow(`SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&subMarker)
	if err != nil || subMarker != "1" {
		t.Fatalf("expected marker '1' on subsequent start, got %q (err=%v)", subMarker, err)
	}

	// Backup still intact
	subBackupBytes, err := os.ReadFile(backupPath)
	if err != nil || sha256.Sum256(subBackupBytes) != originalHash {
		t.Fatalf("backup hash changed after subsequent start")
	}
}

func TestV08Migration_ZeroByteBackupReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "zerobyte_backup.db")

	// 1. Create real V0.7.2 DB fixture
	setupV072FixtureDB(t, dbPath)

	// 2. Pre-create a 0-byte corrupt backup file
	backupPath := dbPath + ".pre-v08.bak"
	if err := os.WriteFile(backupPath, []byte{}, 0644); err != nil {
		t.Fatalf("write zero-byte backup: %v", err)
	}

	// 3. Open with database.New — should clean up the 0-byte backup and create a valid one
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New with 0-byte backup failed: %v", err)
	}
	defer db.Close()

	// Verify backup is now non-empty and valid
	fi, err := os.Stat(backupPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty backup replaced, got fi=%v, err=%v", fi, err)
	}

	// Verify migration marker is '1'
	var marker string
	err = db.Conn().QueryRow(`SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&marker)
	if err != nil || marker != "1" {
		t.Fatalf("expected marker '1', got %q (err=%v)", marker, err)
	}
}

func TestIsUsableSQLiteBackup(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Non-existent file
	if database.IsUsableSQLiteBackup(filepath.Join(tmpDir, "non_existent.db")) {
		t.Errorf("expected non-existent file to be reported as unusable")
	}

	// 2. Zero-byte file
	zeroPath := filepath.Join(tmpDir, "zero.db")
	_ = os.WriteFile(zeroPath, []byte{}, 0644)
	if database.IsUsableSQLiteBackup(zeroPath) {
		t.Errorf("expected zero-byte file to be reported as unusable")
	}

	// 3. Text/garbage bytes (non-empty)
	garbagePath := filepath.Join(tmpDir, "garbage.db")
	_ = os.WriteFile(garbagePath, []byte("NOT A SQLITE DATABASE - RANDOM GARBAGE DATA 1234567890"), 0644)
	if database.IsUsableSQLiteBackup(garbagePath) {
		t.Errorf("expected garbage file to be reported as unusable")
	}

	// 4. Truncated SQLite header (non-empty)
	truncatedPath := filepath.Join(tmpDir, "truncated.db")
	_ = os.WriteFile(truncatedPath, []byte("SQLite format 3\x00\x10\x00\x01\x01\x00@  "), 0644)
	if database.IsUsableSQLiteBackup(truncatedPath) {
		t.Errorf("expected truncated sqlite header file to be reported as unusable")
	}

	// 5. Valid SQLite file without jobs table
	emptySqlitePath := filepath.Join(tmpDir, "empty_sqlite.db")
	rawConn, err := sql.Open("sqlite3", emptySqlitePath)
	if err != nil {
		t.Fatalf("create empty sqlite: %v", err)
	}
	_, _ = rawConn.Exec(`CREATE TABLE other_table (id TEXT PRIMARY KEY)`)
	_ = rawConn.Close()
	if database.IsUsableSQLiteBackup(emptySqlitePath) {
		t.Errorf("expected sqlite db without jobs table to be reported as unusable")
	}

	// 6. Valid V0.7.2 DB fixture
	validFixturePath := filepath.Join(tmpDir, "valid_fixture.db")
	setupV072FixtureDB(t, validFixturePath)
	if !database.IsUsableSQLiteBackup(validFixturePath) {
		t.Errorf("expected valid V0.7.2 fixture to be reported as usable")
	}
}

func TestV08Migration_CorruptBackupRecreationAndPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "corrupt_backup_test.db")

	// 1. Valid V0.7.2 database
	setupV072FixtureDB(t, dbPath)

	// 2. Create <db>.pre-v08.bak containing non-zero invalid/corrupt bytes
	backupPath := dbPath + ".pre-v08.bak"
	corruptBytes := []byte("INVALID_NON_SQLITE_CORRUPT_BYTES_XYZ_1234567890")
	if err := os.WriteFile(backupPath, corruptBytes, 0644); err != nil {
		t.Fatalf("write corrupt backup: %v", err)
	}

	// 3. Verify application does NOT accept that file as a valid backup
	if database.IsUsableSQLiteBackup(backupPath) {
		t.Fatal("expected IsUsableSQLiteBackup to reject non-zero corrupt file")
	}

	// 4. Start migration with database.New
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New failed on corrupt backup recovery: %v", err)
	}
	defer db.Close()

	// 5. Verify selected safe policy: valid backup was recreated and migration succeeded
	var marker string
	err = db.Conn().QueryRow(`SELECT value FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&marker)
	if err != nil || marker != "1" {
		t.Fatalf("expected marker '1', got %q (err=%v)", marker, err)
	}

	// 6. Verify main database / user data remains safe
	jobRepo := database.NewSQLiteJobRepository(db)
	jHTTP, err := jobRepo.GetByID(context.Background(), "job-v7-http")
	if err != nil || jHTTP == nil || jHTTP.Name != "test.zip" {
		t.Fatalf("expected job-v7-http preserved in main db, got %+v (err=%v)", jHTTP, err)
	}
	jTorrent, err := jobRepo.GetByID(context.Background(), "job-v7-torrent")
	if err != nil || jTorrent == nil {
		t.Fatalf("expected job-v7-torrent preserved in main db, got %+v (err=%v)", jTorrent, err)
	}

	// 7. Verify recreated backup:
	// - passes chosen integrity validation
	if !database.IsUsableSQLiteBackup(backupPath) {
		t.Fatalf("expected recreated backup to pass IsUsableSQLiteBackup")
	}

	// - opens as SQLite
	bakConn, err := sql.Open("sqlite3", backupPath)
	if err != nil {
		t.Fatalf("open recreated backup as SQLite: %v", err)
	}
	defer bakConn.Close()

	// - represents pre-FND-1 schema (no job_executions table)
	var bakExecCount int
	err = bakConn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='job_executions'`).Scan(&bakExecCount)
	if err != nil || bakExecCount != 0 {
		t.Fatalf("expected backup to have 0 job_executions tables (pre-FND-1), got %d (err=%v)", bakExecCount, err)
	}

	// - preserves original data in backup
	var httpNameInBak string
	err = bakConn.QueryRow(`SELECT name FROM jobs WHERE id = 'job-v7-http'`).Scan(&httpNameInBak)
	if err != nil || httpNameInBak != "test.zip" {
		t.Fatalf("expected job-v7-http in backup with name test.zip, got %q (err=%v)", httpNameInBak, err)
	}
	var torrentSizeInBak int64
	err = bakConn.QueryRow(`SELECT total_size FROM torrent_jobs WHERE job_id = 'job-v7-torrent'`).Scan(&torrentSizeInBak)
	if err != nil || torrentSizeInBak != 2048576 {
		t.Fatalf("expected torrent_jobs size in backup, got %d (err=%v)", torrentSizeInBak, err)
	}
}

func TestV08Migration_BackupFailureFailsClosedBeforeDDL(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fail_closed_test.db")

	// 1. Valid V0.7.2 database
	setupV072FixtureDB(t, dbPath)

	// 2. Make backup path an undeletable directory so VACUUM INTO / creation fails
	backupPath := dbPath + ".pre-v08.bak"
	nestedDir := filepath.Join(backupPath, "nested_dir")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("create backup blocking dir: %v", err)
	}

	// 3. Opening database.New must fail closed because backup cannot be created
	failedDB, err := database.New(dbPath)
	if err == nil {
		failedDB.Close()
		t.Fatal("expected database.New to fail closed when backup cannot be created")
	}

	// 4. Verify main database remains strictly in pre-migration state (no DDL run)
	rawConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer rawConn.Close()

	// Migration marker must NOT exist
	var markerCount int
	err = rawConn.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v08_fnd1_migrated'`).Scan(&markerCount)
	if err != nil || markerCount != 0 {
		t.Fatalf("expected 0 migrated marker on backup failure, got %d (err=%v)", markerCount, err)
	}

	// FND-1 tables must NOT exist
	for _, tbl := range []string{"job_executions", "http_checkpoints", "http_checkpoint_segments", "tool_records", "event_cursors", "finalization_records"} {
		var cnt int
		err := rawConn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&cnt)
		if err != nil || cnt != 0 {
			t.Fatalf("expected table %s not to exist when backup fails, got cnt=%d", tbl, cnt)
		}
	}

	// Original data remains safe
	var jobCount int
	err = rawConn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id IN ('job-v7-http', 'job-v7-torrent')`).Scan(&jobCount)
	if err != nil || jobCount != 2 {
		t.Fatalf("expected original jobs preserved, got %d (err=%v)", jobCount, err)
	}
}
