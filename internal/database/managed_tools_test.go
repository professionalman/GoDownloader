package database

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"downloader/internal/job"
)

const testFP = "AC0CBBE6848D6A873464AF4E57CF65933B5A7581"

func managedRec(version, shaChar string) *job.ManagedToolRecord {
	sha := strings.Repeat(shaChar, 64)
	return &job.ManagedToolRecord{
		Name: "yt-dlp", SHA256: sha, Version: version, ReleaseTag: version, FileName: sha + ".exe",
		SignerFingerprint: testFP, AuthenticatedAt: time.Now().UTC(),
	}
}

func newLedgerRepo(t *testing.T) (*SQLiteExecutionRepository, *DB) {
	t.Helper()
	db, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	return NewSQLiteExecutionRepository(db), db
}

func statuses(t *testing.T, repo *SQLiteExecutionRepository) map[string]string {
	t.Helper()
	recs, err := repo.ListManagedTools(context.Background(), "yt-dlp")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range recs {
		out[r.Version] = r.Status
	}
	return out
}

func TestManagedLedger_ActivationLifecycle(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	a, b, c := managedRec("2026.08.01", "a"), managedRec("2026.09.01", "b"), managedRec("2026.10.26", "c")

	if err := repo.CommitToolActivation(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitToolActivation(ctx, b, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitToolActivation(ctx, c, b); err != nil {
		t.Fatal(err)
	}
	got := statuses(t, repo)
	want := map[string]string{"2026.08.01": "retired", "2026.09.01": "last_known_good", "2026.10.26": "active"}
	for v, s := range want {
		if got[v] != s {
			t.Fatalf("%s: status %q, want %q (all: %v)", v, got[v], s, got)
		}
	}
}

func TestManagedLedger_ConflictsAbortWithoutChange(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	a, b := managedRec("2026.09.01", "a"), managedRec("2026.10.26", "b")
	if err := repo.CommitToolActivation(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	// Stale decision: caller believes nothing is active.
	if err := repo.CommitToolActivation(ctx, b, nil); !errors.Is(err, job.ErrManagedLedgerConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	// Stale decision: caller believes a different record is active.
	if err := repo.CommitToolActivation(ctx, b, managedRec("2026.01.01", "f")); !errors.Is(err, job.ErrManagedLedgerConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if got := statuses(t, repo); len(got) != 1 || got["2026.09.01"] != "active" {
		t.Fatalf("conflicting commits changed the ledger: %v", got)
	}
	// Invalid records are refused before any write.
	bad := managedRec("2026.10.26", "c")
	bad.FileName = `..\evil.exe`
	if err := repo.CommitToolActivation(ctx, bad, a); err == nil {
		t.Fatal("path-like file name accepted")
	}
}

func TestManagedLedger_RollbackOnInjectedFailure(t *testing.T) {
	repo, db := newLedgerRepo(t)
	ctx := context.Background()
	a, b := managedRec("2026.09.01", "a"), managedRec("2026.10.26", "b")
	if err := repo.CommitToolActivation(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	// Fail the promotion INSERT after the demotion UPDATE already ran inside the same transaction.
	if _, err := db.conn.Exec(`CREATE TRIGGER fail_promote BEFORE INSERT ON managed_tool_ledger
		WHEN NEW.version = '2026.10.26' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitToolActivation(ctx, b, a); err == nil {
		t.Fatal("expected injected failure")
	}
	if got := statuses(t, repo); len(got) != 1 || got["2026.09.01"] != "active" {
		t.Fatalf("partial activation persisted: %v", got)
	}
}

func TestManagedLedger_DatabaseEnforcesSingleActiveAndLKG(t *testing.T) {
	repo, db := newLedgerRepo(t)
	ctx := context.Background()
	if err := repo.CommitToolActivation(ctx, managedRec("2026.09.01", "a"), nil); err != nil {
		t.Fatal(err)
	}
	insert := func(status, shaChar string) error {
		_, err := db.conn.Exec(`INSERT INTO managed_tool_ledger (`+managedToolColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			"yt-dlp", strings.Repeat(shaChar, 64), "x", "x", "f", testFP, status, time.Now(), time.Now(), time.Now())
		return err
	}
	if err := insert("active", "d"); err == nil {
		t.Fatal("second active row accepted by the database")
	}
	if err := insert("last_known_good", "e"); err != nil {
		t.Fatal(err)
	}
	if err := insert("last_known_good", "f"); err == nil {
		t.Fatal("second LKG row accepted by the database")
	}
	if err := insert("bogus", "0"); err == nil {
		t.Fatal("invalid status accepted")
	}
}

func TestManagedLedger_RestoreLastKnownGood(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	a, b := managedRec("2026.09.01", "a"), managedRec("2026.10.26", "b")
	_ = repo.CommitToolActivation(ctx, a, nil)
	_ = repo.CommitToolActivation(ctx, b, a)

	if err := repo.RestoreLastKnownGood(ctx, "yt-dlp", strings.Repeat("9", 64), a.SHA256); !errors.Is(err, job.ErrManagedLedgerConflict) {
		t.Fatalf("expected conflict for wrong failed-active digest, got %v", err)
	}
	if err := repo.RestoreLastKnownGood(ctx, "yt-dlp", b.SHA256, a.SHA256); err != nil {
		t.Fatal(err)
	}
	got := statuses(t, repo)
	if got["2026.09.01"] != "active" || got["2026.10.26"] != "quarantined" {
		t.Fatalf("restoration result: %v", got)
	}
	if err := repo.RestoreLastKnownGood(ctx, "yt-dlp", a.SHA256, b.SHA256); !errors.Is(err, job.ErrManagedLedgerConflict) {
		t.Fatalf("restoring without an LKG must conflict, got %v", err)
	}
}

func TestManagedLedger_HistoryIsBounded(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	var prev *job.ManagedToolRecord
	for i, ch := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		rec := managedRec(fmt.Sprintf("2026.0%d.01", i+1), ch)
		if err := repo.CommitToolActivation(ctx, rec, prev); err != nil {
			t.Fatal(err)
		}
		prev = rec
	}
	recs, _ := repo.ListManagedTools(ctx, "yt-dlp")
	if len(recs) > 2+managedToolHistoryLimit {
		t.Fatalf("history unbounded: %d rows", len(recs))
	}
}

func TestToolRecords_GenericObservationCannotClobberOrForgeManagedAuthority(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	managed := managedRec("2026.10.26", "a")
	_ = repo.CommitToolActivation(ctx, managed, nil)

	// Same tool, same version, discovered on PATH.
	if err := repo.SaveToolRecord(ctx, &job.ToolRecord{Name: "yt-dlp", Version: "2026.10.26", OwnershipMode: "external_user",
		ExecutablePath: `C:\Tools\yt-dlp.exe`, VerificationStatus: "version_probed", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	active, _ := repo.GetManagedTool(ctx, "yt-dlp", job.ManagedToolStatusActive)
	if active == nil || active.SHA256 != managed.SHA256 || active.FileName != managed.FileName {
		t.Fatalf("external observation altered managed authority: %+v", active)
	}
	if err := repo.SaveToolRecord(ctx, &job.ToolRecord{Name: "yt-dlp", Version: "2026.10.27",
		VerificationStatus: "authenticated_release", Status: "active"}); err == nil {
		t.Fatal("generic observation was allowed to claim authenticated_release")
	}
}

func TestToolRecords_SingleActiveObservation(t *testing.T) {
	repo, _ := newLedgerRepo(t)
	ctx := context.Background()
	for _, v := range []string{"2026.01.01", "2026.02.01", "2026.03.01"} {
		if err := repo.SaveToolRecord(ctx, &job.ToolRecord{Name: "yt-dlp", Version: v, VerificationStatus: "version_probed", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := repo.ListToolRecords(ctx, "yt-dlp")
	active := 0
	for _, r := range recs {
		if r.Status == "active" {
			active++
		}
	}
	if active != 1 || len(recs) != 3 {
		t.Fatalf("expected 3 rows with 1 active, got %d rows / %d active", len(recs), active)
	}
	if a, _ := repo.GetActiveTool(ctx, "yt-dlp"); a == nil || a.Version != "2026.03.01" {
		t.Fatalf("latest observation should be active: %+v", a)
	}
}

// A database created before V0.9.1 (with duplicate active tool_records) migrates deterministically, is backed
// up first, keeps every row, and never seeds the managed ledger from unauthenticated observations.
func TestMigrationV091_LegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to the pre-V0.9.1 state: no managed ledger, no marker, duplicate active rows.
	for _, stmt := range []string{
		`DROP TABLE managed_tool_ledger`,
		`DELETE FROM app_settings WHERE key = 'v091_managed_tools_migrated'`,
	} {
		if _, err := db.conn.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		version, ownership, status string
		at                         time.Time
	}{
		{"2025.01.01", "managed", "active", old},
		{"2025.06.01", "external_user", "active", newer},
		{"2024.12.01", "managed", "last_known_good", old},
	} {
		if _, err := db.conn.Exec(`INSERT INTO tool_records (name, version, ownership_mode, verification_status, status, created_at, updated_at)
			VALUES ('yt-dlp', ?, ?, 'version_probed', ?, ?, ?)`, row.version, row.ownership, row.status, row.at, row.at); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	db2, err := New(path)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	defer db2.Close()
	if !IsUsableSQLiteBackup(path + ".pre-v091.bak") {
		t.Fatal("pre-migration backup missing or unusable")
	}
	repo := NewSQLiteExecutionRepository(db2)
	recs, _ := repo.ListToolRecords(context.Background(), "yt-dlp")
	byVersion := map[string]string{}
	for _, r := range recs {
		byVersion[r.Version] = r.Status
	}
	if len(recs) != 3 || byVersion["2025.06.01"] != "active" || byVersion["2025.01.01"] != "deprecated" || byVersion["2024.12.01"] != "last_known_good" {
		t.Fatalf("unexpected migrated tool_records: %v", byVersion)
	}
	managed, _ := repo.ListManagedTools(context.Background(), "yt-dlp")
	if len(managed) != 0 {
		t.Fatal("unauthenticated legacy rows must not seed the managed ledger")
	}
	_ = db2.Close()

	// Idempotent: reopening changes nothing and keeps the marker.
	db3, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db3.Close()
	var marker int
	_ = db3.conn.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'v091_managed_tools_migrated'`).Scan(&marker)
	if marker != 1 {
		t.Fatal("migration marker missing")
	}
}
