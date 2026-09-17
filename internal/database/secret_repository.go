package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"downloader/internal/securestore"
)

type SQLiteSecretRepository struct{ db *DB }

func NewSQLiteSecretRepository(db *DB) *SQLiteSecretRepository {
	return &SQLiteSecretRepository{db: db}
}

func (r *SQLiteSecretRepository) GetSecret(ctx context.Context, scope, owner, field string) ([]byte, error) {
	var value []byte
	err := r.db.conn.QueryRowContext(ctx, `SELECT ciphertext FROM encrypted_secrets
		WHERE scope=? AND owner_id=? AND field_name=?`, scope, owner, field).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get encrypted secret: %w", err)
	}
	return value, nil
}

func (r *SQLiteSecretRepository) SetSecret(ctx context.Context, scope, owner, field string, value []byte) error {
	_, err := r.db.conn.ExecContext(ctx, `INSERT INTO encrypted_secrets
		(scope, owner_id, field_name, ciphertext, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(scope, owner_id, field_name) DO UPDATE SET ciphertext=excluded.ciphertext,
		updated_at=excluded.updated_at`, scope, owner, field, value, time.Now())
	if err != nil {
		return fmt.Errorf("persist encrypted secret: %w", err)
	}
	return nil
}

func (r *SQLiteSecretRepository) DeleteSecret(ctx context.Context, scope, owner, field string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM encrypted_secrets
		WHERE scope=? AND owner_id=? AND field_name=?`, scope, owner, field)
	return err
}

func (r *SQLiteSecretRepository) HasSecret(ctx context.Context, scope, owner, field string) (bool, error) {
	var count int
	err := r.db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM encrypted_secrets
		WHERE scope=? AND owner_id=? AND field_name=?`, scope, owner, field).Scan(&count)
	return count > 0, err
}

func (r *SQLiteSecretRepository) CountSecrets(ctx context.Context) (int, error) {
	var count int
	err := r.db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM encrypted_secrets`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count encrypted secrets: %w", err)
	}
	return count, nil
}

func (r *SQLiteSecretRepository) GetAllSecrets(ctx context.Context) ([]securestore.EncryptedRecord, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT scope, owner_id, field_name, ciphertext FROM encrypted_secrets`)
	if err != nil {
		return nil, fmt.Errorf("query all encrypted secrets: %w", err)
	}
	defer rows.Close()

	var records []securestore.EncryptedRecord
	for rows.Next() {
		var rec securestore.EncryptedRecord
		if err := rows.Scan(&rec.Scope, &rec.Owner, &rec.Field, &rec.Ciphertext); err != nil {
			return nil, fmt.Errorf("scan encrypted secret: %w", err)
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted secrets: %w", err)
	}
	return records, nil
}
