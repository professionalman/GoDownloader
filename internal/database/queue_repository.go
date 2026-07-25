package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"downloader/internal/job"
)

var _ job.IQueueRepository = (*SQLiteQueueRepository)(nil)

// SQLiteQueueRepository implements job.IQueueRepository using SQLite.
type SQLiteQueueRepository struct {
	db *DB
}

// NewSQLiteQueueRepository creates a new SQLite-backed queue repository.
func NewSQLiteQueueRepository(db *DB) *SQLiteQueueRepository {
	return &SQLiteQueueRepository{db: db}
}

// Enqueue inserts or updates a job's queue entry.
func (r *SQLiteQueueRepository) Enqueue(ctx context.Context, entry *job.QueueEntry) error {
	query := `INSERT INTO job_queue (job_id, position, action, enqueued_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			position=excluded.position,
			action=excluded.action,
			updated_at=excluded.updated_at`
	_, err := r.db.conn.ExecContext(ctx, query,
		entry.JobID, entry.Position, entry.Action, entry.EnqueuedAt, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue job %s: %w", entry.JobID, err)
	}
	return nil
}

// Get retrieves a queue entry by JobID. Returns nil if not found.
func (r *SQLiteQueueRepository) Get(ctx context.Context, jobID string) (*job.QueueEntry, error) {
	query := `SELECT job_id, position, action, enqueued_at, updated_at FROM job_queue WHERE job_id = ?`
	var entry job.QueueEntry
	err := r.db.conn.QueryRowContext(ctx, query, jobID).Scan(
		&entry.JobID, &entry.Position, &entry.Action, &entry.EnqueuedAt, &entry.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get queue entry %s: %w", jobID, err)
	}
	return &entry, nil
}

// Delete removes a job from job_queue.
func (r *SQLiteQueueRepository) Delete(ctx context.Context, jobID string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM job_queue WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("delete queue entry %s: %w", jobID, err)
	}
	return nil
}

// NextPosition returns the next position integer for a given priority lane.
func (r *SQLiteQueueRepository) NextPosition(ctx context.Context, priority job.JobPriority) (int64, error) {
	query := `SELECT COALESCE(MAX(q.position), 0) + 1
		FROM job_queue q
		JOIN jobs j ON q.job_id = j.id
		WHERE j.priority = ?`
	var nextPos int64
	err := r.db.conn.QueryRowContext(ctx, query, priority).Scan(&nextPos)
	if err != nil {
		return 1, fmt.Errorf("get next position: %w", err)
	}
	return nextPos, nil
}

// NextRunnable retrieves the highest-priority runnable queued job (status = 'queued').
func (r *SQLiteQueueRepository) NextRunnable(ctx context.Context) (*job.QueuedJob, error) {
	query := fmt.Sprintf(`SELECT %s, q.position, q.action, q.enqueued_at, q.updated_at
		FROM jobs j
		JOIN job_queue q ON j.id = q.job_id
		WHERE j.status = 'queued'
		ORDER BY
			CASE j.priority
				WHEN 'high' THEN 0
				WHEN 'normal' THEN 1
				WHEN 'low' THEN 2
				ELSE 1
			END ASC,
			q.position ASC,
			q.enqueued_at ASC,
			j.id ASC
		LIMIT 1`, jobColumnsPrefix("j."))

	row := r.db.conn.QueryRowContext(ctx, query)
	qj, err := scanQueuedJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("next runnable job: %w", err)
	}
	return &qj, nil
}

// List retrieves all queued/paused jobs with their queue entries, ordered by priority lane and position.
func (r *SQLiteQueueRepository) List(ctx context.Context) ([]job.QueuedJob, error) {
	query := fmt.Sprintf(`SELECT %s, q.position, q.action, q.enqueued_at, q.updated_at
		FROM jobs j
		JOIN job_queue q ON j.id = q.job_id
		ORDER BY
			CASE j.priority
				WHEN 'high' THEN 0
				WHEN 'normal' THEN 1
				WHEN 'low' THEN 2
				ELSE 1
			END ASC,
			q.position ASC,
			q.enqueued_at ASC,
			j.id ASC`, jobColumnsPrefix("j."))

	rows, err := r.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list queued jobs: %w", err)
	}
	defer rows.Close()

	var result []job.QueuedJob
	for rows.Next() {
		qj, err := scanQueuedJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan queued job: %w", err)
		}
		result = append(result, qj)
	}
	return result, rows.Err()
}

// Reorder updates positions for all jobs within a priority lane in a single transaction.
func (r *SQLiteQueueRepository) Reorder(ctx context.Context, priority job.JobPriority, orderedJobIDs []string) error {
	tx, err := r.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reorder tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE job_queue SET position = ? WHERE job_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare reorder stmt: %w", err)
	}
	defer stmt.Close()

	for i, jobID := range orderedJobIDs {
		pos := int64(i + 1)
		if _, err := stmt.ExecContext(ctx, pos, jobID); err != nil {
			return fmt.Errorf("update position for %s: %w", jobID, err)
		}
	}

	return tx.Commit()
}

func jobColumnsPrefix(prefix string) string {
	return prefix + "id, " + prefix + "source, " + prefix + "name, " + prefix + "status, " +
		prefix + "total_bytes, " + prefix + "completed_bytes, " + prefix + "progress, " +
		prefix + "speed_bytes_per_second, " + prefix + "eta_seconds, " + prefix + "error, " +
		prefix + "engine, " + prefix + "engine_id, " + prefix + "type, " + prefix + "media_info, " +
		prefix + "priority, " + prefix + "batch_id, " + prefix + "created_at, " + prefix + "updated_at"
}

func scanQueuedJob(scanner interface{ Scan(...interface{}) error }) (job.QueuedJob, error) {
	var qj job.QueuedJob
	var j job.Job
	var mediaInfoJSON string
	var queueUpdatedAt interface{}
	err := scanner.Scan(
		&j.ID, &j.Source, &j.Name, &j.Status,
		&j.TotalBytes, &j.CompletedBytes, &j.Progress,
		&j.SpeedBytesPerSecond, &j.ETASeconds,
		&j.Error, &j.Engine, &j.EngineID,
		&j.Type, &mediaInfoJSON, &j.Priority, &j.BatchID,
		&j.CreatedAt, &j.UpdatedAt,
		&qj.Position, &qj.Action, &qj.EnqueuedAt, &queueUpdatedAt,
	)
	if err != nil {
		return qj, err
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
	qj.JobID = j.ID
	qj.Job = j
	return qj, nil
}
