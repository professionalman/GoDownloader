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
		return fmt.Errorf("marshal network policy: %w", marshalErr)
	}
	query := `UPDATE jobs SET
		source=?, name=?, status=?, total_bytes=?, completed_bytes=?, progress=?,
		speed_bytes_per_second=?, eta_seconds=?, error=?, engine=?, engine_id=?,
		type=?, media_info=?, priority=?, batch_id=?,
		category_id=?, destination_dir=?, work_dir=?, conflict_policy=?, final_path=?,
		updated_at=?, engine_cleanup_pending=?, network_policy_json=?,
		effective_download_limit_bps=?, effective_upload_limit_bps=?, network_reconcile_pending=?
		WHERE id=?`
	_, err := r.db.conn.ExecContext(ctx, query,
		j.Source, j.Name, j.Status, j.TotalBytes, j.CompletedBytes, j.Progress,
		j.SpeedBytesPerSecond, j.ETASeconds, j.Error, j.Engine, j.EngineID,
		j.Type, mediaInfoJSON, j.Priority, j.BatchID,
		j.CategoryID, j.DestinationDir, j.WorkDir, j.ConflictPolicy, j.FinalPath,
		j.UpdatedAt, j.EngineCleanupPending, string(networkPolicyJSON),
		j.EffectiveDownloadLimitBytesPerSecond, j.EffectiveUploadLimitBytesPerSecond,
		j.NetworkReconcilePending, j.ID,
	)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return nil
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
