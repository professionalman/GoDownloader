package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteSecretRepository_CRUDAndCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "secrets_test.db")
	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	repo := NewSQLiteSecretRepository(db)
	ctx := context.Background()

	// 1. Initial count must be zero
	count, err := repo.CountSecrets(ctx)
	if err != nil {
		t.Fatalf("CountSecrets error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected count 0, got %d", count)
	}

	// HasSecret should be false
	has, err := repo.HasSecret(ctx, "settings", "global", "proxy_password")
	if err != nil || has {
		t.Fatalf("expected has=false, got %v, err=%v", has, err)
	}

	// 2. SetSecret
	secret1 := []byte("v1-test-ciphertext-1")
	if err := repo.SetSecret(ctx, "settings", "global", "proxy_password", secret1); err != nil {
		t.Fatalf("SetSecret failed: %v", err)
	}

	count, err = repo.CountSecrets(ctx)
	if err != nil || count != 1 {
		t.Fatalf("expected count 1, got %d, err=%v", count, err)
	}

	has, err = repo.HasSecret(ctx, "settings", "global", "proxy_password")
	if err != nil || !has {
		t.Fatalf("expected has=true, got %v, err=%v", has, err)
	}

	// 3. GetSecret
	val, err := repo.GetSecret(ctx, "settings", "global", "proxy_password")
	if err != nil {
		t.Fatalf("GetSecret failed: %v", err)
	}
	if string(val) != string(secret1) {
		t.Fatalf("GetSecret mismatch: got %s, want %s", string(val), string(secret1))
	}

	// 4. Set second secret
	secret2 := []byte("v1-test-ciphertext-2")
	if err := repo.SetSecret(ctx, "mediaauth", "default", "cookies.txt", secret2); err != nil {
		t.Fatalf("SetSecret 2 failed: %v", err)
	}

	count, err = repo.CountSecrets(ctx)
	if err != nil || count != 2 {
		t.Fatalf("expected count 2, got %d, err=%v", count, err)
	}

	// 5. Update secret1 (conflict resolution)
	secret1Updated := []byte("v1-test-ciphertext-1-updated")
	if err := repo.SetSecret(ctx, "settings", "global", "proxy_password", secret1Updated); err != nil {
		t.Fatalf("SetSecret update failed: %v", err)
	}
	count, _ = repo.CountSecrets(ctx)
	if count != 2 {
		t.Fatalf("count should still be 2 after update, got %d", count)
	}
	val, _ = repo.GetSecret(ctx, "settings", "global", "proxy_password")
	if string(val) != string(secret1Updated) {
		t.Fatalf("GetSecret after update mismatch: got %s", string(val))
	}

	// 6. Delete secret1
	if err := repo.DeleteSecret(ctx, "settings", "global", "proxy_password"); err != nil {
		t.Fatalf("DeleteSecret failed: %v", err)
	}

	count, err = repo.CountSecrets(ctx)
	if err != nil || count != 1 {
		t.Fatalf("expected count 1 after deletion, got %d, err=%v", count, err)
	}

	has, err = repo.HasSecret(ctx, "settings", "global", "proxy_password")
	if err != nil || has {
		t.Fatalf("expected has=false after deletion, got %v", has)
	}

	// 7. Get non-existent secret returns nil, nil
	val, err = repo.GetSecret(ctx, "non", "existent", "field")
	if err != nil || val != nil {
		t.Fatalf("expected nil, nil for non-existent secret, got val=%v, err=%v", val, err)
	}

	// 8. GetAllSecrets
	records, err := repo.GetAllSecrets(ctx)
	if err != nil {
		t.Fatalf("GetAllSecrets failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record remaining, got %d", len(records))
	}
	if records[0].Scope != "mediaauth" || records[0].Owner != "default" || records[0].Field != "cookies.txt" {
		t.Fatalf("unexpected record: %+v", records[0])
	}
	if string(records[0].Ciphertext) != string(secret2) {
		t.Fatalf("unexpected ciphertext: %s", string(records[0].Ciphertext))
	}
}
