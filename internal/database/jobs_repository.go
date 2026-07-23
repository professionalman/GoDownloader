package database

import (
	"context"
	"database/sql"
	"fmt"

	"downloader/internal/job"
)

// JobRepository defines the persistence interface for jobs.
type JobRepository interface {
	Create(ctx context.Context, j *job.Job) error
	Update(ctx context.Context, j *job.Job) error
	GetByID(ctx context.Context, id string) (*job.Job, error)
	List(ctx context.Context) ([]job.Job, error)
	ListRecoverable(ctx context.Context) ([]job.Job, error)
}

// SQLiteJobRepository implements JobRepository using SQLite.
type SQLiteJobRepository struct {
	db *DB
}

// NewSQLiteJobRepository creates a new SQLite-backed job repository.
func NewSQLiteJobRepository(db *DB) *SQLiteJobRepository {
	return &SQLiteJobRepository{db: db}
}

const jobColumns = `id, source, name, status, total_bytes, completed_bytes, progress,
	speed_bytes_per_second, eta_seconds, error, engine, engine_id, created_at, updated_at`

func scanJob(scanner interface{ Scan(...interface{}) error }) (job.Job, error) {
	var j job.Job
	err := scanner.Scan(
		&j.ID, &j.Source, &j.Name, &j.Status,
		&j.TotalBytes, &j.CompletedBytes, &j.Progress,
		&j.SpeedBytesPerSecond, &j.ETASeconds,
		&j.Error, &j.Engine, &j.EngineID,
		&j.CreatedAt, &j.UpdatedAt,
	)
	return j, err
}

// Create inserts a new job into the database.
func (r *SQLiteJobRepository) Create(ctx context.Context, j *job.Job) error {
	query := fmt.Sprintf(`INSERT INTO jobs (%s) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobColumns)
	_, err := r.db.conn.ExecContext(ctx, query,
		j.ID, j.Source, j.Name, j.Status,
		j.TotalBytes, j.CompletedBytes, j.Progress,
		j.SpeedBytesPerSecond, j.ETASeconds,
		j.Error, j.Engine, j.EngineID,
		j.CreatedAt, j.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

// Update updates an existing job in the database.
func (r *SQLiteJobRepository) Update(ctx context.Context, j *job.Job) error {
	query := `UPDATE jobs SET
		source=?, name=?, status=?, total_bytes=?, completed_bytes=?, progress=?,
		speed_bytes_per_second=?, eta_seconds=?, error=?, engine=?, engine_id=?,
		updated_at=?
		WHERE id=?`
	_, err := r.db.conn.ExecContext(ctx, query,
		j.Source, j.Name, j.Status, j.TotalBytes, j.CompletedBytes, j.Progress,
		j.SpeedBytesPerSecond, j.ETASeconds, j.Error, j.Engine, j.EngineID,
		j.UpdatedAt, j.ID,
	)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
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
	query := fmt.Sprintf(`SELECT %s FROM jobs WHERE status IN ('queued', 'downloading', 'paused') ORDER BY created_at ASC`, jobColumns)
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
