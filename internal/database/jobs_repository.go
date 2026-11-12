package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"downloader/internal/job"
)

// IJobRepository defines the persistence interface for jobs.
type IJobRepository = job.IJobRepository

var _ job.IJobRepository = (*SQLiteJobRepository)(nil)

// SQLiteJobRepository implements job.IJobRepository using SQLite.
type SQLiteJobRepository struct {
	db *DB
}

// NewSQLiteJobRepository creates a new SQLite-backed job repository.
func NewSQLiteJobRepository(db *DB) *SQLiteJobRepository {
	return &SQLiteJobRepository{db: db}
}

const jobColumns = `id, source, name, status, total_bytes, completed_bytes, progress,
	speed_bytes_per_second, eta_seconds, error, engine, engine_id, type, media_info, priority, batch_id,
	category_id, destination_dir, work_dir, conflict_policy, final_path, created_at, updated_at, engine_cleanup_pending,
	network_policy_json, effective_download_limit_bps, effective_upload_limit_bps, network_reconcile_pending`

func scanJob(scanner interface{ Scan(...interface{}) error }) (job.Job, error) {
	var j job.Job
	var mediaInfoJSON string
	var networkPolicyJSON string
	err := scanner.Scan(
		&j.ID, &j.Source, &j.Name, &j.Status,
		&j.TotalBytes, &j.CompletedBytes, &j.Progress,
		&j.SpeedBytesPerSecond, &j.ETASeconds,
		&j.Error, &j.Engine, &j.EngineID,
		&j.Type, &mediaInfoJSON,
		&j.Priority, &j.BatchID,
		&j.CategoryID, &j.DestinationDir, &j.WorkDir, &j.ConflictPolicy, &j.FinalPath,
		&j.CreatedAt, &j.UpdatedAt, &j.EngineCleanupPending,
		&networkPolicyJSON, &j.EffectiveDownloadLimitBytesPerSecond,
		&j.EffectiveUploadLimitBytesPerSecond, &j.NetworkReconcilePending,
	)
	if err != nil {
		return j, err
	}
	if mediaInfoJSON != "" {
		var info job.MediaInfo
		if jsonErr := json.Unmarshal([]byte(mediaInfoJSON), &info); jsonErr == nil {
			j.MediaInfo = &info
		}
	}
	if j.Priority == "" {
		j.Priority = job.JobPriorityNormal
	}
	if j.ConflictPolicy == "" {
		j.ConflictPolicy = job.ConflictPolicyRename
	}
	if networkPolicyJSON != "" && networkPolicyJSON != "{}" {
		_ = json.Unmarshal([]byte(networkPolicyJSON), &j.NetworkPolicy)
	}
	return j, nil
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertJobExec(ctx context.Context, execer sqlExecer, j *job.Job) error {
	mediaInfoJSON := ""
	if j.MediaInfo != nil {
		if data, err := json.Marshal(j.MediaInfo); err == nil {
			mediaInfoJSON = string(data)
		}
	}
	priority := j.Priority
	if priority == "" {
		priority = job.JobPriorityNormal
	}
	conflictPolicy := j.ConflictPolicy
	if conflictPolicy == "" {
		conflictPolicy = job.ConflictPolicyRename
	}
	networkPolicyJSON, err := json.Marshal(j.NetworkPolicy)
	if err != nil {
		return fmt.Errorf("marshal network policy: %w", err)
	}
	query := fmt.Sprintf(`INSERT INTO jobs (%s) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobColumns)
	_, err = execer.ExecContext(ctx, query,
		j.ID, j.Source, j.Name, j.Status,
		j.TotalBytes, j.CompletedBytes, j.Progress,
		j.SpeedBytesPerSecond, j.ETASeconds,
		j.Error, j.Engine, j.EngineID,
		j.Type, mediaInfoJSON,
		priority, j.BatchID,
		j.CategoryID, j.DestinationDir, j.WorkDir, conflictPolicy, j.FinalPath,
		j.CreatedAt, j.UpdatedAt, j.EngineCleanupPending,
		string(networkPolicyJSON), j.EffectiveDownloadLimitBytesPerSecond,
		j.EffectiveUploadLimitBytesPerSecond, j.NetworkReconcilePending,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

// Create inserts a new job into the database.
func (r *SQLiteJobRepository) Create(ctx context.Context, j *job.Job) error {
	return insertJobExec(ctx, r.db.conn, j)
}

// Update updates an existing job in the database.
func (r *SQLiteJobRepository) Update(ctx context.Context, j *job.Job) error {
	_, err := r.updateJob(ctx, j, nil)
	return err
}

// UpdateIfStatus updates the full job row only if its persisted status equals expected, as one SQL statement.
// It returns false, nil when the status changed concurrently (nothing is written).
func (r *SQLiteJobRepository) UpdateIfStatus(ctx context.Context, j *job.Job, expected job.JobStatus) (bool, error) {
	return r.updateJob(ctx, j, &expected)
}

func (r *SQLiteJobRepository) updateJob(ctx context.Context, j *job.Job, expected *job.JobStatus) (bool, error) {
	mediaInfoJSON := ""
	if j.MediaInfo != nil {
		if data, err := json.Marshal(j.MediaInfo); err == nil {
			mediaInfoJSON = string(data)
		}
	}
	if j.Priority == "" {
		j.Priority = job.JobPriorityNormal
	}
	if j.ConflictPolicy == "" {
		j.ConflictPolicy = job.ConflictPolicyRename
	}
	networkPolicyJSON, marshalErr := json.Marshal(j.NetworkPolicy)
	if marshalErr != nil {
		return false, fmt.Errorf("marshal network policy: %w", marshalErr)
	}
	query := `UPDATE jobs SET
		source=?, name=?, status=?, total_bytes=?, completed_bytes=?, progress=?,
		speed_bytes_per_second=?, eta_seconds=?, error=?, engine=?, engine_id=?,
		type=?, media_info=?, priority=?, batch_id=?,
		category_id=?, destination_dir=?, work_dir=?, conflict_policy=?, final_path=?,
		updated_at=?, engine_cleanup_pending=?, network_policy_json=?,
		effective_download_limit_bps=?, effective_upload_limit_bps=?, network_reconcile_pending=?
		WHERE id=?`
	args := []any{
		j.Source, j.Name, j.Status, j.TotalBytes, j.CompletedBytes, j.Progress,
		j.SpeedBytesPerSecond, j.ETASeconds, j.Error, j.Engine, j.EngineID,
		j.Type, mediaInfoJSON, j.Priority, j.BatchID,
		j.CategoryID, j.DestinationDir, j.WorkDir, j.ConflictPolicy, j.FinalPath,
		j.UpdatedAt, j.EngineCleanupPending, string(networkPolicyJSON),
		j.EffectiveDownloadLimitBytesPerSecond, j.EffectiveUploadLimitBytesPerSecond,
		j.NetworkReconcilePending, j.ID,
	}
	if expected != nil {
		query += ` AND status=?`
		args = append(args, *expected)
	}
	res, err := r.db.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update job: %w", err)
	}
	if expected == nil {
		return true, nil
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update job rows affected: %w", err)
	}
	return rows > 0, nil
}

// UpdateJobPriorityAndQueuePosition updates a job's priority and its queue position in a single transaction.
func (r *SQLiteJobRepository) UpdateJobPriorityAndQueuePosition(ctx context.Context, jobID string, newPriority job.JobPriority, newPosition int64) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin priority tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	res1, err := tx.ExecContext(ctx, `UPDATE jobs SET priority = ?, updated_at = ? WHERE id = ?`, newPriority, now, jobID)
	if err != nil {
		return fmt.Errorf("update job priority: %w", err)
	}
	rows1, err := res1.RowsAffected()
	if err != nil || rows1 == 0 {
		return fmt.Errorf("job %s not found for priority update", jobID)
	}

	res2, err := tx.ExecContext(ctx, `UPDATE job_queue SET position = ?, updated_at = ? WHERE job_id = ?`, newPosition, now, jobID)
	if err != nil {
		return fmt.Errorf("update job queue position: %w", err)
	}
	rows2, err := res2.RowsAffected()
	if err != nil || rows2 == 0 {
		return fmt.Errorf("queue entry for job %s not found", jobID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit priority tx: %w", err)
	}
	return nil
}

// CompleteMediaAnalysis atomically transitions a job from StatusAnalyzing to StatusAwaitingSelection,
// persisting the extracted media metadata and title conditionally. If the job is no longer in StatusAnalyzing
// (e.g. cancelled, deleted, or transitioned), the update is aborted and returns false, nil.
func (r *SQLiteJobRepository) CompleteMediaAnalysis(ctx context.Context, jobID, title string, info *job.MediaInfo) (bool, error) {
	if info == nil {
		return false, fmt.Errorf("complete media analysis: media info is required")
	}
	data, err := json.Marshal(info)
	if err != nil {
		return false, fmt.Errorf("complete media analysis: marshal media info: %w", err)
	}
	mediaInfoJSON := string(data)
	now := time.Now()
	var res sql.Result
	if title != "" {
		query := `UPDATE jobs SET status = ?, media_info = ?, name = ?, updated_at = ? WHERE id = ? AND status = ?`
		res, err = r.db.conn.ExecContext(ctx, query, job.StatusAwaitingSelection, mediaInfoJSON, title, now, jobID, job.StatusAnalyzing)
	} else {
		query := `UPDATE jobs SET status = ?, media_info = ?, updated_at = ? WHERE id = ? AND status = ?`
		res, err = r.db.conn.ExecContext(ctx, query, job.StatusAwaitingSelection, mediaInfoJSON, now, jobID, job.StatusAnalyzing)
	}
	if err != nil {
		return false, fmt.Errorf("complete media analysis: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("complete media analysis rows affected: %w", err)
	}
	return rows > 0, nil
}

// SelectMediaFormat atomically transitions a job from StatusAwaitingSelection to StatusQueued with the selected
// media info and, when entry is non-nil, inserts its queue entry within the same transaction. The status predicate
// makes the transition a compare-and-set against concurrent cancellation; if it no longer matches, nothing is
// written and false, nil is returned. The queue position is computed inside the transaction.
func (r *SQLiteJobRepository) SelectMediaFormat(ctx context.Context, jobID string, info *job.MediaInfo, entry *job.QueueEntry) (bool, error) {
	if info == nil {
		return false, fmt.Errorf("select media format: media info is required")
	}
	data, err := json.Marshal(info)
	if err != nil {
		return false, fmt.Errorf("select media format: marshal media info: %w", err)
	}

	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin select format tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, media_info = ?, updated_at = ? WHERE id = ? AND status = ?`,
		job.StatusQueued, string(data), now, jobID, job.StatusAwaitingSelection)
	if err != nil {
		return false, fmt.Errorf("select media format: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("select media format rows affected: %w", err)
	}
	if rows == 0 {
		return false, nil
	}

	if entry != nil {
		var nextPos int64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(q.position), 0) + 1
			FROM job_queue q
			JOIN jobs j ON q.job_id = j.id
			WHERE j.priority = (SELECT priority FROM jobs WHERE id = ?)`, jobID).Scan(&nextPos)
		if err != nil {
			return false, fmt.Errorf("select media format next position: %w", err)
		}
		entry.JobID = jobID
		entry.Position = nextPos
		_, err = tx.ExecContext(ctx, `INSERT INTO job_queue (job_id, position, action, retry_count, not_before, enqueued_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(job_id) DO UPDATE SET
				position=excluded.position,
				action=excluded.action,
				retry_count=excluded.retry_count,
				not_before=excluded.not_before,
				updated_at=excluded.updated_at`,
			entry.JobID, entry.Position, entry.Action, entry.RetryCount, entry.NotBefore, entry.EnqueuedAt, entry.UpdatedAt)
		if err != nil {
			return false, fmt.Errorf("select media format enqueue: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit select format tx: %w", err)
	}
	return true, nil
}

// CancelAwaitingSelection atomically transitions a job from StatusAwaitingSelection to StatusCancelled and removes
// any queue entry within the same transaction. If the job is no longer awaiting selection (for example a concurrent
// SelectMediaFormat already queued it), nothing is written and false, nil is returned.
func (r *SQLiteJobRepository) CancelAwaitingSelection(ctx context.Context, jobID string) (bool, error) {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin cancel selection tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, speed_bytes_per_second = 0, eta_seconds = 0, updated_at = ? WHERE id = ? AND status = ?`,
		job.StatusCancelled, time.Now(), jobID, job.StatusAwaitingSelection)
	if err != nil {
		return false, fmt.Errorf("cancel awaiting selection: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("cancel awaiting selection rows affected: %w", err)
	}
	if rows == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_queue WHERE job_id = ?`, jobID); err != nil {
		return false, fmt.Errorf("cancel awaiting selection queue delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit cancel selection tx: %w", err)
	}
	return true, nil
}

// FailMediaAnalysis atomically transitions a job from StatusAnalyzing to StatusFailed with reason.
// If the job is no longer in StatusAnalyzing (e.g. cancelled or deleted), nothing is written and false, nil is returned.
func (r *SQLiteJobRepository) FailMediaAnalysis(ctx context.Context, jobID, reason string) (bool, error) {
	query := `UPDATE jobs SET status = ?, error = ?, updated_at = ? WHERE id = ? AND status = ?`
	res, err := r.db.conn.ExecContext(ctx, query, job.StatusFailed, reason, time.Now(), jobID, job.StatusAnalyzing)
	if err != nil {
		return false, fmt.Errorf("fail media analysis: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("fail media analysis rows affected: %w", err)
	}
	return rows > 0, nil
}

// GetByID retrieves a single job by ID. Returns nil if not found.
func (r *SQLiteJobRepository) GetByID(ctx context.Context, id string) (*job.Job, error) {
	query := fmt.Sprintf(`SELECT %s FROM jobs WHERE id = ?`, jobColumns)
	j, err := scanJob(r.db.conn.QueryRowContext(ctx, query, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}

// List retrieves all jobs ordered by creation time (newest first).
func (r *SQLiteJobRepository) List(ctx context.Context) ([]job.Job, error) {
	query := fmt.Sprintf(`SELECT %s FROM jobs ORDER BY created_at DESC`, jobColumns)
	rows, err := r.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ListRecoverable returns all jobs that should be recovered on startup.
// These are jobs with non-terminal, non-failed statuses.
func (r *SQLiteJobRepository) ListRecoverable(ctx context.Context) ([]job.Job, error) {
	query := fmt.Sprintf(`SELECT %s FROM jobs WHERE status IN ('queued', 'downloading', 'paused', 'analyzing', 'processing', 'awaiting_selection', 'seeding') ORDER BY created_at ASC`, jobColumns)
	rows, err := r.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list recoverable jobs: %w", err)
	}
	defer rows.Close()

	var jobs []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// CountDownloading returns the total number of jobs with status = 'downloading'.
func (r *SQLiteJobRepository) CountDownloading(ctx context.Context) (int, error) {
	var count int
	err := r.db.conn.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE status = 'downloading'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count downloading jobs: %w", err)
	}
	return count, nil
}

// ListPendingEngineCleanups returns all completed torrent jobs requiring engine cleanup.
func (r *SQLiteJobRepository) ListPendingEngineCleanups(ctx context.Context) ([]job.Job, error) {
	query := fmt.Sprintf(`SELECT %s FROM jobs WHERE status = 'completed' AND type = 'torrent' AND engine_cleanup_pending = 1 ORDER BY created_at ASC`, jobColumns)
	rows, err := r.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending engine cleanups: %w", err)
	}
	defer rows.Close()

	var jobs []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// DeleteJobCascade removes a job and all associated child records across all tables in a single SQLite transaction.
// It enforces child-before-parent deletion order and verifies that exactly one job row is removed.
func (r *SQLiteJobRepository) DeleteJobCascade(ctx context.Context, jobID string) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM http_checkpoint_segments WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from http_checkpoint_segments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM http_checkpoints WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from http_checkpoints: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM finalization_records WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from finalization_records: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_executions WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from job_executions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_queue WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from job_queue: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM torrent_files WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from torrent_files: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM torrent_jobs WHERE job_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from torrent_jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM encrypted_secrets WHERE scope = 'job' AND owner_id = ?`, jobID); err != nil {
		return fmt.Errorf("delete from encrypted_secrets: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("delete from jobs: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check affected rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("job %s not found", jobID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete transaction: %w", err)
	}
	return nil
}
