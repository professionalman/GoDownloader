package database

import (
	"context"
	"testing"
	"time"
)

func TestV07MigrationBackfillsBothLegacySeedingValuesAndIsIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now()
	for _, item := range []struct {
		id   string
		seed int
		want string
	}{{"legacy-none", 0, "none"}, {"legacy-seed", 1, "unlimited"}} {
		if _, err := db.conn.ExecContext(ctx, `INSERT INTO jobs(id, source, name, status, engine, created_at, updated_at) VALUES (?, '', '', 'completed', 'qbittorrent', ?, ?)`, item.id, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.conn.ExecContext(ctx, `INSERT INTO torrent_jobs(job_id, seed_after_complete, seeding_mode) VALUES (?, ?, 'none')`, item.id, item.seed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.conn.ExecContext(ctx, `DELETE FROM app_settings WHERE key='v07_network_controls_migrated'`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateToV07NetworkControls(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id   string
		want string
	}{{"legacy-none", "none"}, {"legacy-seed", "unlimited"}} {
		var got string
		if err := db.conn.QueryRowContext(ctx, `SELECT seeding_mode FROM torrent_jobs WHERE job_id=?`, item.id).Scan(&got); err != nil || got != item.want {
			t.Fatalf("%s mode=%q err=%v", item.id, got, err)
		}
	}
	if err := db.migrateToV07NetworkControls(); err != nil {
		t.Fatalf("idempotent rerun failed: %v", err)
	}
}
