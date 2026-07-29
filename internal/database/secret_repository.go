package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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
