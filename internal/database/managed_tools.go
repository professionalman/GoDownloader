package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"downloader/internal/job"
)

var _ job.IManagedToolLedger = (*SQLiteExecutionRepository)(nil)

// managedToolHistoryLimit bounds retired ledger rows kept per tool (active and LKG rows are always kept).
const managedToolHistoryLimit = 3

const managedToolColumns = `name, sha256, version, release_tag, file_name, signer_fingerprint, status,
	authenticated_at, created_at, updated_at`

// migrateToV091ManagedTools adds the authenticated managed tool ledger and normalises duplicate active rows in
// the generic tool_records observation table. It is additive: tool_records keeps its schema and rows, and no
// existing row is ever copied into the managed ledger (presence or a version probe is not authentication).
func (db *DB) migrateToV091ManagedTools() error {
	var done int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v091_managed_tools_migrated'`).Scan(&done); err == nil && done > 0 {
		return nil
	}

	// Same safety convention as the V0.8 migration: back up an existing database before changing row state.
	if db.hadExistingFile && db.path != "" && db.path != ":memory:" && !strings.Contains(db.path, "mode=memory") {
		backupPath := db.path + ".pre-v091.bak"
		if !IsUsableSQLiteBackup(backupPath) {
			_ = os.Remove(backupPath)
			cleanBackupPath := strings.ReplaceAll(filepath.ToSlash(backupPath), "'", "''")
			if _, err := db.conn.Exec("VACUUM INTO '" + cleanBackupPath + "'"); err != nil {
				return fmt.Errorf("create pre-migration backup %s: %w", backupPath, err)
			}
			if !IsUsableSQLiteBackup(backupPath) {
				return fmt.Errorf("pre-migration backup %s failed integrity verification", backupPath)
			}
		}
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin V0.9.1 managed tools migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS managed_tool_ledger (
		name TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		version TEXT NOT NULL,
		release_tag TEXT NOT NULL,
		file_name TEXT NOT NULL,
		signer_fingerprint TEXT NOT NULL,
		status TEXT NOT NULL,
		authenticated_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (name, sha256),
		CHECK (length(sha256) = 64),
		CHECK (status IN ('active', 'last_known_good', 'retired', 'quarantined'))
	)`); err != nil {
		return fmt.Errorf("create managed_tool_ledger: %w", err)
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_managed_tool_ledger_one_active
		ON managed_tool_ledger(name) WHERE status = 'active'`); err != nil {
		return fmt.Errorf("create managed active index: %w", err)
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_managed_tool_ledger_one_lkg
		ON managed_tool_ledger(name) WHERE status = 'last_known_good'`); err != nil {
		return fmt.Errorf("create managed LKG index: %w", err)
	}

	// Deterministically keep one active observation per tool in tool_records: the most recently updated row
	// (ties broken by version) stays active; older duplicates become 'deprecated'. No rows are deleted.
	if _, err := tx.Exec(`UPDATE tool_records SET status = 'deprecated'
		WHERE status = 'active' AND EXISTS (
			SELECT 1 FROM tool_records newer
			WHERE newer.name = tool_records.name AND newer.status = 'active'
			  AND (newer.updated_at > tool_records.updated_at
			       OR (newer.updated_at = tool_records.updated_at AND newer.version > tool_records.version))
		)`); err != nil {
		return fmt.Errorf("normalise duplicate active tool_records: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO app_settings (key, value, updated_at) VALUES ('v091_managed_tools_migrated', 'true', ?)`, time.Now()); err != nil {
		return fmt.Errorf("record V0.9.1 managed tools migration marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit V0.9.1 managed tools migration: %w", err)
	}
	return nil
}

func scanManagedTool(scan func(dest ...any) error) (*job.ManagedToolRecord, error) {
	var rec job.ManagedToolRecord
	if err := scan(&rec.Name, &rec.SHA256, &rec.Version, &rec.ReleaseTag, &rec.FileName, &rec.SignerFingerprint,
		&rec.Status, &rec.AuthenticatedAt, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return nil, err
	}
	return &rec, nil
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getManagedToolByStatus(ctx context.Context, q rowQuerier, name, status string) (*job.ManagedToolRecord, error) {
	row := q.QueryRowContext(ctx, `SELECT `+managedToolColumns+` FROM managed_tool_ledger WHERE name = ? AND status = ?`, name, status)
	rec, err := scanManagedTool(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get managed tool %s (%s): %w", name, status, err)
	}
	return rec, nil
}

// GetManagedTool returns the managed ledger record for name in status, or nil if none exists.
func (r *SQLiteExecutionRepository) GetManagedTool(ctx context.Context, name, status string) (*job.ManagedToolRecord, error) {
	return getManagedToolByStatus(ctx, r.db.conn, name, status)
}

// ListManagedTools returns all retained managed ledger rows for name, newest first.
func (r *SQLiteExecutionRepository) ListManagedTools(ctx context.Context, name string) ([]job.ManagedToolRecord, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT `+managedToolColumns+` FROM managed_tool_ledger WHERE name = ? ORDER BY updated_at DESC, sha256`, name)
	if err != nil {
		return nil, fmt.Errorf("list managed tools %s: %w", name, err)
	}
	defer rows.Close()
	var out []job.ManagedToolRecord
	for rows.Next() {
		rec, err := scanManagedTool(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan managed tool: %w", err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func validateManagedRecord(rec *job.ManagedToolRecord) error {
	if rec == nil || rec.Name == "" || len(rec.SHA256) != 64 || rec.Version == "" || rec.FileName == "" ||
		rec.SignerFingerprint == "" || rec.AuthenticatedAt.IsZero() {
		return fmt.Errorf("incomplete managed tool record")
	}
	if strings.ContainsAny(rec.FileName, `/\:`) || strings.Contains(rec.FileName, "..") {
		return fmt.Errorf("managed tool file name must be a bare content-addressed name")
	}
	return nil
}

// CommitToolActivation atomically promotes newActive, demotes the expected previous active record to
// last_known_good, and retires any older last_known_good. History beyond managedToolHistoryLimit retired rows
// is pruned. Any mismatch between previousActive and the stored active record aborts with no change.
func (r *SQLiteExecutionRepository) CommitToolActivation(ctx context.Context, newActive, previousActive *job.ManagedToolRecord) error {
	if err := validateManagedRecord(newActive); err != nil {
		return err
	}
	if previousActive != nil && (previousActive.Name != newActive.Name || previousActive.SHA256 == newActive.SHA256) {
		return fmt.Errorf("invalid previous active record for activation")
	}

	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tool activation: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	// Take the write lock first so the active-row check below cannot race another writer.
	if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET updated_at = updated_at WHERE name = ?`, newActive.Name); err != nil {
		return fmt.Errorf("lock managed ledger: %w", err)
	}
	current, err := getManagedToolByStatus(ctx, tx, newActive.Name, job.ManagedToolStatusActive)
	if err != nil {
		return err
	}
	switch {
	case previousActive == nil && current != nil:
		return fmt.Errorf("%w: an active record already exists", job.ErrManagedLedgerConflict)
	case previousActive != nil && (current == nil || current.SHA256 != previousActive.SHA256):
		return fmt.Errorf("%w: active record differs from expected previous", job.ErrManagedLedgerConflict)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET status = ?, updated_at = ? WHERE name = ? AND status = ?`,
		job.ManagedToolStatusRetired, now, newActive.Name, job.ManagedToolStatusLastKnownGood); err != nil {
		return fmt.Errorf("retire previous LKG: %w", err)
	}
	if current != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET status = ?, updated_at = ? WHERE name = ? AND sha256 = ?`,
			job.ManagedToolStatusLastKnownGood, now, current.Name, current.SHA256); err != nil {
			return fmt.Errorf("demote active to LKG: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_tool_ledger (`+managedToolColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name, sha256) DO UPDATE SET
			version = excluded.version, release_tag = excluded.release_tag, file_name = excluded.file_name,
			signer_fingerprint = excluded.signer_fingerprint, status = excluded.status,
			authenticated_at = excluded.authenticated_at, updated_at = excluded.updated_at`,
		newActive.Name, newActive.SHA256, newActive.Version, newActive.ReleaseTag, newActive.FileName,
		newActive.SignerFingerprint, job.ManagedToolStatusActive, newActive.AuthenticatedAt, now, now); err != nil {
		return fmt.Errorf("promote new active: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM managed_tool_ledger WHERE name = ? AND status IN (?, ?) AND sha256 NOT IN (
			SELECT sha256 FROM managed_tool_ledger WHERE name = ? AND status IN (?, ?) ORDER BY updated_at DESC, sha256 LIMIT ?)`,
		newActive.Name, job.ManagedToolStatusRetired, job.ManagedToolStatusQuarantined,
		newActive.Name, job.ManagedToolStatusRetired, job.ManagedToolStatusQuarantined, managedToolHistoryLimit); err != nil {
		return fmt.Errorf("prune managed history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tool activation: %w", err)
	}
	return nil
}

// RestoreLastKnownGood atomically quarantines the failed active record and promotes the LKG record.
func (r *SQLiteExecutionRepository) RestoreLastKnownGood(ctx context.Context, name, failedActiveSHA, lkgSHA string) error {
	if name == "" || len(lkgSHA) != 64 {
		return fmt.Errorf("invalid LKG restoration request")
	}
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin LKG restoration: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET updated_at = updated_at WHERE name = ?`, name); err != nil {
		return fmt.Errorf("lock managed ledger: %w", err)
	}
	active, err := getManagedToolByStatus(ctx, tx, name, job.ManagedToolStatusActive)
	if err != nil {
		return err
	}
	lkg, err := getManagedToolByStatus(ctx, tx, name, job.ManagedToolStatusLastKnownGood)
	if err != nil {
		return err
	}
	if lkg == nil || lkg.SHA256 != lkgSHA {
		return fmt.Errorf("%w: last-known-good record differs from expected", job.ErrManagedLedgerConflict)
	}
	if (failedActiveSHA == "") != (active == nil) || (active != nil && active.SHA256 != failedActiveSHA) {
		return fmt.Errorf("%w: active record differs from expected", job.ErrManagedLedgerConflict)
	}
	if active != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET status = ?, updated_at = ? WHERE name = ? AND sha256 = ?`,
			job.ManagedToolStatusQuarantined, now, name, active.SHA256); err != nil {
			return fmt.Errorf("quarantine failed active: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE managed_tool_ledger SET status = ?, updated_at = ? WHERE name = ? AND sha256 = ?`,
		job.ManagedToolStatusActive, now, name, lkgSHA); err != nil {
		return fmt.Errorf("promote LKG: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit LKG restoration: %w", err)
	}
	return nil
}
