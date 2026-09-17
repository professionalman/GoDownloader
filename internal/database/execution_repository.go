package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"downloader/internal/job"
)

var _ job.IExecutionRepository = (*SQLiteExecutionRepository)(nil)

// SQLiteExecutionRepository implements job.IExecutionRepository using SQLite.
type SQLiteExecutionRepository struct {
	db       *DB
	cursorMu sync.Mutex
}

// NewSQLiteExecutionRepository creates a new SQLite-backed execution repository.
func NewSQLiteExecutionRepository(db *DB) *SQLiteExecutionRepository {
	return &SQLiteExecutionRepository{db: db}
}

// --- Execution Attempts ---

// CreateExecution persists a new job execution attempt.
func (r *SQLiteExecutionRepository) CreateExecution(ctx context.Context, exec *job.JobExecution) error {
	if exec.ID == "" {
		exec.ID = uuid.New().String()
	}
	now := time.Now()
	if exec.StartedAt.IsZero() {
		exec.StartedAt = now
	}
	if exec.UpdatedAt.IsZero() {
		exec.UpdatedAt = now
	}
	if exec.AttemptNumber < 1 {
		exec.AttemptNumber = 1
	}
	if exec.Status == "" {
		exec.Status = job.ExecutionStatusActive
	}

	query := `INSERT INTO job_executions (
		id, job_id, attempt_number, engine_family, runtime_mode,
		engine_correlation_id, status, failure_classification, failure_detail,
		started_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := r.db.conn.ExecContext(ctx, query,
		exec.ID, exec.JobID, exec.AttemptNumber, string(exec.EngineFamily), string(exec.RuntimeMode),
		exec.EngineCorrelationID, string(exec.Status), exec.FailureClassification, exec.FailureDetail,
		exec.StartedAt, exec.UpdatedAt, exec.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("create job execution %s: %w", exec.ID, err)
	}
	return nil
}

// UpdateExecution updates an existing job execution record.
func (r *SQLiteExecutionRepository) UpdateExecution(ctx context.Context, exec *job.JobExecution) error {
	exec.UpdatedAt = time.Now()
	query := `UPDATE job_executions SET
		attempt_number = ?,
		engine_family = ?,
		runtime_mode = ?,
		engine_correlation_id = ?,
		status = ?,
		failure_classification = ?,
		failure_detail = ?,
		updated_at = ?,
		completed_at = ?
		WHERE id = ?`

	res, err := r.db.conn.ExecContext(ctx, query,
		exec.AttemptNumber, string(exec.EngineFamily), string(exec.RuntimeMode),
		exec.EngineCorrelationID, string(exec.Status), exec.FailureClassification, exec.FailureDetail,
		exec.UpdatedAt, exec.CompletedAt, exec.ID,
	)
	if err != nil {
		return fmt.Errorf("update job execution %s: %w", exec.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check affected rows for execution %s: %w", exec.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("job execution %s not found", exec.ID)
	}
	return nil
}

// GetLatestExecution retrieves the most recent execution attempt for a job.
func (r *SQLiteExecutionRepository) GetLatestExecution(ctx context.Context, jobID string) (*job.JobExecution, error) {
	query := `SELECT id, job_id, attempt_number, engine_family, runtime_mode,
		engine_correlation_id, status, failure_classification, failure_detail,
		started_at, updated_at, completed_at
		FROM job_executions
		WHERE job_id = ?
		ORDER BY attempt_number DESC, started_at DESC
		LIMIT 1`

	var exec job.JobExecution
	var engineFamily, runtimeMode, status string
	err := r.db.conn.QueryRowContext(ctx, query, jobID).Scan(
		&exec.ID, &exec.JobID, &exec.AttemptNumber, &engineFamily, &runtimeMode,
		&exec.EngineCorrelationID, &status, &exec.FailureClassification, &exec.FailureDetail,
		&exec.StartedAt, &exec.UpdatedAt, &exec.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest execution for job %s: %w", jobID, err)
	}
	exec.EngineFamily = job.EngineFamily(engineFamily)
	exec.RuntimeMode = job.RuntimeMode(runtimeMode)
	exec.Status = job.ExecutionStatus(status)
	return &exec, nil
}

// GetExecutionByID retrieves a specific execution by ID.
func (r *SQLiteExecutionRepository) GetExecutionByID(ctx context.Context, id string) (*job.JobExecution, error) {
	query := `SELECT id, job_id, attempt_number, engine_family, runtime_mode,
		engine_correlation_id, status, failure_classification, failure_detail,
		started_at, updated_at, completed_at
		FROM job_executions
		WHERE id = ?`

	var exec job.JobExecution
	var engineFamily, runtimeMode, status string
	err := r.db.conn.QueryRowContext(ctx, query, id).Scan(
		&exec.ID, &exec.JobID, &exec.AttemptNumber, &engineFamily, &runtimeMode,
		&exec.EngineCorrelationID, &status, &exec.FailureClassification, &exec.FailureDetail,
		&exec.StartedAt, &exec.UpdatedAt, &exec.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get execution %s: %w", id, err)
	}
	exec.EngineFamily = job.EngineFamily(engineFamily)
	exec.RuntimeMode = job.RuntimeMode(runtimeMode)
	exec.Status = job.ExecutionStatus(status)
	return &exec, nil
}

// ListExecutions retrieves all execution attempts for a job ordered by attempt number.
func (r *SQLiteExecutionRepository) ListExecutions(ctx context.Context, jobID string) ([]job.JobExecution, error) {
	query := `SELECT id, job_id, attempt_number, engine_family, runtime_mode,
		engine_correlation_id, status, failure_classification, failure_detail,
		started_at, updated_at, completed_at
		FROM job_executions
		WHERE job_id = ?
		ORDER BY attempt_number ASC`

	rows, err := r.db.conn.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("list executions for job %s: %w", jobID, err)
	}
	defer rows.Close()

	var result []job.JobExecution
	for rows.Next() {
		var exec job.JobExecution
		var engineFamily, runtimeMode, status string
		if err := rows.Scan(
			&exec.ID, &exec.JobID, &exec.AttemptNumber, &engineFamily, &runtimeMode,
			&exec.EngineCorrelationID, &status, &exec.FailureClassification, &exec.FailureDetail,
			&exec.StartedAt, &exec.UpdatedAt, &exec.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		exec.EngineFamily = job.EngineFamily(engineFamily)
		exec.RuntimeMode = job.RuntimeMode(runtimeMode)
		exec.Status = job.ExecutionStatus(status)
		result = append(result, exec)
	}
	return result, rows.Err()
}

// --- HTTP Checkpoints and Segments ---

// SaveHTTPCheckpoint persists or updates an HTTP checkpoint and its segment topology atomically.
func (r *SQLiteExecutionRepository) SaveHTTPCheckpoint(ctx context.Context, cp *job.HTTPCheckpoint, segments []job.HTTPCheckpointSegment) error {
	return r.db.Transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		if cp.UpdatedAt.IsZero() {
			cp.UpdatedAt = now
		}
		if cp.CheckpointVersion < 1 {
			cp.CheckpointVersion = 1
		}

		var executionID sql.NullString
		if cp.ExecutionID != "" {
			executionID = sql.NullString{String: cp.ExecutionID, Valid: true}
		}

		cpQuery := `INSERT INTO http_checkpoints (
			job_id, execution_id, checkpoint_version, effective_url, etag,
			last_modified, content_length, range_supported, staging_path,
			verified_bytes, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			execution_id = excluded.execution_id,
			checkpoint_version = excluded.checkpoint_version,
			effective_url = excluded.effective_url,
			etag = excluded.etag,
			last_modified = excluded.last_modified,
			content_length = excluded.content_length,
			range_supported = excluded.range_supported,
			staging_path = excluded.staging_path,
			verified_bytes = excluded.verified_bytes,
			updated_at = excluded.updated_at`

		if _, err := tx.ExecContext(ctx, cpQuery,
			cp.JobID, executionID, cp.CheckpointVersion, cp.EffectiveURL, cp.ETag,
			cp.LastModified, cp.ContentLength, cp.RangeSupported, cp.StagingPath,
			cp.VerifiedBytes, cp.UpdatedAt,
		); err != nil {
			return fmt.Errorf("upsert http checkpoint for job %s: %w", cp.JobID, err)
		}

		if len(segments) > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM http_checkpoint_segments WHERE job_id = ?`, cp.JobID); err != nil {
				return fmt.Errorf("clear existing segments for job %s: %w", cp.JobID, err)
			}

			stmt, err := tx.PrepareContext(ctx, `INSERT INTO http_checkpoint_segments (
				job_id, segment_index, start_offset, current_offset, end_offset,
				verified_bytes, completed, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
			if err != nil {
				return fmt.Errorf("prepare insert segment stmt: %w", err)
			}
			defer stmt.Close()

			for _, seg := range segments {
				segUpdatedAt := seg.UpdatedAt
				if segUpdatedAt.IsZero() {
					segUpdatedAt = now
				}
				if _, err := stmt.ExecContext(ctx,
					cp.JobID, seg.SegmentIndex, seg.StartOffset, seg.CurrentOffset, seg.EndOffset,
					seg.VerifiedBytes, seg.Completed, segUpdatedAt,
				); err != nil {
					return fmt.Errorf("insert segment %d for job %s: %w", seg.SegmentIndex, cp.JobID, err)
				}
			}
		}
		return nil
	})
}

// GetHTTPCheckpoint retrieves an HTTP checkpoint and all associated segments.
func (r *SQLiteExecutionRepository) GetHTTPCheckpoint(ctx context.Context, jobID string) (*job.HTTPCheckpoint, []job.HTTPCheckpointSegment, error) {
	cpQuery := `SELECT job_id, execution_id, checkpoint_version, effective_url, etag,
		last_modified, content_length, range_supported, staging_path, verified_bytes, updated_at
		FROM http_checkpoints
		WHERE job_id = ?`

	var cp job.HTTPCheckpoint
	var execID sql.NullString
	err := r.db.conn.QueryRowContext(ctx, cpQuery, jobID).Scan(
		&cp.JobID, &execID, &cp.CheckpointVersion, &cp.EffectiveURL, &cp.ETag,
		&cp.LastModified, &cp.ContentLength, &cp.RangeSupported, &cp.StagingPath,
		&cp.VerifiedBytes, &cp.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get http checkpoint for job %s: %w", jobID, err)
	}
	if execID.Valid {
		cp.ExecutionID = execID.String
	}

	segQuery := `SELECT job_id, segment_index, start_offset, current_offset, end_offset,
		verified_bytes, completed, updated_at
		FROM http_checkpoint_segments
		WHERE job_id = ?
		ORDER BY segment_index ASC`

	rows, err := r.db.conn.QueryContext(ctx, segQuery, jobID)
	if err != nil {
		return nil, nil, fmt.Errorf("list http segments for job %s: %w", jobID, err)
	}
	defer rows.Close()

	var segments []job.HTTPCheckpointSegment
	for rows.Next() {
		var seg job.HTTPCheckpointSegment
		if err := rows.Scan(
			&seg.JobID, &seg.SegmentIndex, &seg.StartOffset, &seg.CurrentOffset, &seg.EndOffset,
			&seg.VerifiedBytes, &seg.Completed, &seg.UpdatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan segment: %w", err)
		}
		segments = append(segments, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate segments: %w", err)
	}

	return &cp, segments, nil
}

// UpdateHTTPSegmentProgress updates the offset and verified bytes for a single segment.
func (r *SQLiteExecutionRepository) UpdateHTTPSegmentProgress(ctx context.Context, jobID string, segmentIndex int, currentOffset int64, verifiedBytes int64, completed bool) error {
	now := time.Now()
	query := `UPDATE http_checkpoint_segments SET
		current_offset = ?,
		verified_bytes = ?,
		completed = ?,
		updated_at = ?
		WHERE job_id = ? AND segment_index = ?`

	res, err := r.db.conn.ExecContext(ctx, query, currentOffset, verifiedBytes, completed, now, jobID, segmentIndex)
	if err != nil {
		return fmt.Errorf("update segment %d progress for job %s: %w", segmentIndex, jobID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check segment update rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("segment %d for job %s not found", segmentIndex, jobID)
	}
	return nil
}

// --- Finalization Records ---

// SaveFinalizationRecord creates or updates an artifact finalization record.
func (r *SQLiteExecutionRepository) SaveFinalizationRecord(ctx context.Context, rec *job.FinalizationRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	now := time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	if rec.Phase == "" {
		rec.Phase = job.FinalizationPhasePrepared
	}

	var executionID sql.NullString
	if rec.ExecutionID != "" {
		executionID = sql.NullString{String: rec.ExecutionID, Valid: true}
	}

	query := `INSERT INTO finalization_records (
		id, job_id, execution_id, staging_path, destination_path, expected_size,
		expected_digest, conflict_policy, phase, cleanup_path, error,
		created_at, updated_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		staging_path = excluded.staging_path,
		destination_path = excluded.destination_path,
		expected_size = excluded.expected_size,
		expected_digest = excluded.expected_digest,
		conflict_policy = excluded.conflict_policy,
		phase = excluded.phase,
		cleanup_path = excluded.cleanup_path,
		error = excluded.error,
		updated_at = excluded.updated_at,
		completed_at = excluded.completed_at`

	_, err := r.db.conn.ExecContext(ctx, query,
		rec.ID, rec.JobID, executionID, rec.StagingPath, rec.DestinationPath, rec.ExpectedSize,
		rec.ExpectedDigest, rec.ConflictPolicy, string(rec.Phase), rec.CleanupPath, rec.Error,
		rec.CreatedAt, rec.UpdatedAt, rec.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("save finalization record %s: %w", rec.ID, err)
	}
	return nil
}

// UpdateFinalizationPhase updates the phase and error string of a finalization record.
func (r *SQLiteExecutionRepository) UpdateFinalizationPhase(ctx context.Context, id string, phase job.FinalizationPhase, errStr string) error {
	now := time.Now()
	var completedAt *time.Time
	if phase == job.FinalizationPhaseComplete || phase == job.FinalizationPhaseFailed {
		completedAt = &now
	}

	query := `UPDATE finalization_records SET
		phase = ?,
		error = ?,
		updated_at = ?,
		completed_at = CASE WHEN ? IS NOT NULL THEN ? ELSE completed_at END
		WHERE id = ?`

	res, err := r.db.conn.ExecContext(ctx, query, string(phase), errStr, now, completedAt, completedAt, id)
	if err != nil {
		return fmt.Errorf("update finalization phase %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check finalization rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("finalization record %s not found", id)
	}
	return nil
}

// GetPendingFinalizations retrieves all finalization records whose phase is not complete.
func (r *SQLiteExecutionRepository) GetPendingFinalizations(ctx context.Context) ([]job.FinalizationRecord, error) {
	query := `SELECT id, job_id, execution_id, staging_path, destination_path, expected_size,
		expected_digest, conflict_policy, phase, cleanup_path, error,
		created_at, updated_at, completed_at
		FROM finalization_records
		WHERE phase NOT IN ('complete')
		ORDER BY created_at ASC`

	rows, err := r.db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending finalizations: %w", err)
	}
	defer rows.Close()

	var result []job.FinalizationRecord
	for rows.Next() {
		var rec job.FinalizationRecord
		var execID sql.NullString
		var phase string
		if err := rows.Scan(
			&rec.ID, &rec.JobID, &execID, &rec.StagingPath, &rec.DestinationPath, &rec.ExpectedSize,
			&rec.ExpectedDigest, &rec.ConflictPolicy, &phase, &rec.CleanupPath, &rec.Error,
			&rec.CreatedAt, &rec.UpdatedAt, &rec.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan finalization record: %w", err)
		}
		if execID.Valid {
			rec.ExecutionID = execID.String
		}
		rec.Phase = job.FinalizationPhase(phase)
		result = append(result, rec)
	}
	return result, rows.Err()
}

// GetFinalizationByJobID retrieves the latest finalization record for a job.
func (r *SQLiteExecutionRepository) GetFinalizationByJobID(ctx context.Context, jobID string) (*job.FinalizationRecord, error) {
	query := `SELECT id, job_id, execution_id, staging_path, destination_path, expected_size,
		expected_digest, conflict_policy, phase, cleanup_path, error,
		created_at, updated_at, completed_at
		FROM finalization_records
		WHERE job_id = ?
		ORDER BY created_at DESC
		LIMIT 1`

	var rec job.FinalizationRecord
	var execID sql.NullString
	var phase string
	err := r.db.conn.QueryRowContext(ctx, query, jobID).Scan(
		&rec.ID, &rec.JobID, &execID, &rec.StagingPath, &rec.DestinationPath, &rec.ExpectedSize,
		&rec.ExpectedDigest, &rec.ConflictPolicy, &phase, &rec.CleanupPath, &rec.Error,
		&rec.CreatedAt, &rec.UpdatedAt, &rec.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get finalization record for job %s: %w", jobID, err)
	}
	if execID.Valid {
		rec.ExecutionID = execID.String
	}
	rec.Phase = job.FinalizationPhase(phase)
	return &rec, nil
}

// --- Tool Ledger Records ---

// SaveToolRecord inserts or updates a tool ledger entry.
func (r *SQLiteExecutionRepository) SaveToolRecord(ctx context.Context, rec *job.ToolRecord) error {
	now := time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	if rec.OwnershipMode == "" {
		rec.OwnershipMode = "system"
	}
	if rec.VerificationStatus == "" {
		rec.VerificationStatus = "unverified"
	}
	if rec.Status == "" {
		rec.Status = "active"
	}

	query := `INSERT INTO tool_records (
		name, version, platform_arch, ownership_mode, executable_path,
		sha256, verification_status, status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(name, version) DO UPDATE SET
		platform_arch = excluded.platform_arch,
		ownership_mode = excluded.ownership_mode,
		executable_path = excluded.executable_path,
		sha256 = excluded.sha256,
		verification_status = excluded.verification_status,
		status = excluded.status,
		updated_at = excluded.updated_at`

	_, err := r.db.conn.ExecContext(ctx, query,
		rec.Name, rec.Version, rec.PlatformArch, rec.OwnershipMode, rec.ExecutablePath,
		rec.SHA256, rec.VerificationStatus, rec.Status, rec.CreatedAt, rec.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("save tool record %s@%s: %w", rec.Name, rec.Version, err)
	}
	return nil
}

// GetActiveTool retrieves the active tool record by tool name.
func (r *SQLiteExecutionRepository) GetActiveTool(ctx context.Context, name string) (*job.ToolRecord, error) {
	query := `SELECT name, version, platform_arch, ownership_mode, executable_path,
		sha256, verification_status, status, created_at, updated_at
		FROM tool_records
		WHERE name = ? AND status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1`

	var rec job.ToolRecord
	err := r.db.conn.QueryRowContext(ctx, query, name).Scan(
		&rec.Name, &rec.Version, &rec.PlatformArch, &rec.OwnershipMode, &rec.ExecutablePath,
		&rec.SHA256, &rec.VerificationStatus, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active tool %s: %w", name, err)
	}
	return &rec, nil
}

// ListToolRecords retrieves all versions of a tool by name.
func (r *SQLiteExecutionRepository) ListToolRecords(ctx context.Context, name string) ([]job.ToolRecord, error) {
	query := `SELECT name, version, platform_arch, ownership_mode, executable_path,
		sha256, verification_status, status, created_at, updated_at
		FROM tool_records
		WHERE name = ?
		ORDER BY created_at DESC`

	rows, err := r.db.conn.QueryContext(ctx, query, name)
	if err != nil {
		return nil, fmt.Errorf("list tool records for %s: %w", name, err)
	}
	defer rows.Close()

	var result []job.ToolRecord
	for rows.Next() {
		var rec job.ToolRecord
		if err := rows.Scan(
			&rec.Name, &rec.Version, &rec.PlatformArch, &rec.OwnershipMode, &rec.ExecutablePath,
			&rec.SHA256, &rec.VerificationStatus, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tool record: %w", err)
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// --- Event Cursors ---

// GetEventCursor retrieves the current sequence number for a given scope.
func (r *SQLiteExecutionRepository) GetEventCursor(ctx context.Context, scope string) (int64, error) {
	var seq int64
	err := r.db.conn.QueryRowContext(ctx, `SELECT sequence_number FROM event_cursors WHERE scope = ?`, scope).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get event cursor for scope %s: %w", scope, err)
	}
	return seq, nil
}

// AdvanceEventCursor atomically increments and returns the next sequence number for a scope.
func (r *SQLiteExecutionRepository) AdvanceEventCursor(ctx context.Context, scope string) (int64, error) {
	r.cursorMu.Lock()
	defer r.cursorMu.Unlock()

	var nextSeq int64
	for attempt := 0; attempt < 5; attempt++ {
		err := r.db.Transaction(ctx, func(tx *sql.Tx) error {
			now := time.Now()
			var currentSeq int64
			err := tx.QueryRowContext(ctx, `SELECT sequence_number FROM event_cursors WHERE scope = ?`, scope).Scan(&currentSeq)
			if err == sql.ErrNoRows {
				nextSeq = 1
				_, err = tx.ExecContext(ctx, `INSERT INTO event_cursors (scope, sequence_number, updated_at) VALUES (?, ?, ?)`,
					scope, nextSeq, now)
				if err != nil {
					return fmt.Errorf("insert initial cursor for scope %s: %w", scope, err)
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("query current cursor for scope %s: %w", scope, err)
			}

			nextSeq = currentSeq + 1
			_, err = tx.ExecContext(ctx, `UPDATE event_cursors SET sequence_number = ?, updated_at = ? WHERE scope = ?`,
				nextSeq, now, scope)
			if err != nil {
				return fmt.Errorf("update cursor for scope %s: %w", scope, err)
			}
			return nil
		})
		if err == nil {
			return nextSeq, nil
		}
		if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
			time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
			continue
		}
		return 0, err
	}
	return 0, fmt.Errorf("advance cursor for scope %s: database busy", scope)
}
