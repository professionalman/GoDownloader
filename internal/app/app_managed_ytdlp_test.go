package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
	"downloader/internal/toolmanager"
)

// buildVersionHelper compiles a tiny real executable that prints version, standing in for yt-dlp.exe.
func buildVersionHelper(t *testing.T, version string) []byte {
	t.Helper()
	src := t.TempDir()
	main := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"" + version + "\") }\n"
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(src, "helper.exe")
	cmd := exec.Command("go", "build", "-o", out, "main.go")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GO111MODULE=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build helper executable: %v %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Restart after a committed activation: App.New hash-verifies the ledger-selected binary and registers it as the
// authenticated managed yt-dlp engine, with no network access.
func TestApp_RestartRegistersAuthenticatedManagedYtdlp(t *testing.T) {
	exe := buildVersionHelper(t, "2026.10.26")
	sum := sha256.Sum256(exe)
	digest := hex.EncodeToString(sum[:])

	t.Setenv("PATH", t.TempDir()) // no system yt-dlp
	cfg := newTestConfig(t)
	cfg.YtdlpPath = ""
	dbPath := filepath.Join(t.TempDir(), "managed.db")

	storeDir := filepath.Join(cfg.DataDir, "bin", "yt-dlp")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, digest+".exe"), exe, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.NewSQLiteExecutionRepository(db).CommitToolActivation(context.Background(), &job.ManagedToolRecord{
		Name: toolmanager.ToolYtdlp, SHA256: digest, Version: "2026.10.26", ReleaseTag: "2026.10.26",
		FileName: digest + ".exe", SignerFingerprint: toolmanager.YtdlpSigningKeyFingerprint, AuthenticatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	ctx := context.Background()
	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("App.New: %v", err)
	}
	defer func() { _ = application.Shutdown(ctx) }()

	if _, ok := application.Registry().Get("ytdlp"); !ok {
		t.Fatal("authenticated managed yt-dlp was not registered at startup")
	}
	info := application.ToolManager().Get(toolmanager.ToolYtdlp)
	if info == nil || info.VerificationStatus != toolmanager.VerificationStatusAuthenticated ||
		info.Provenance != toolmanager.ProvenanceManagedRelease || info.SHA256 != digest {
		t.Fatalf("startup resolution lost authenticated provenance: %+v", info)
	}
	st := application.ToolManager().YtdlpStatus(ctx)
	if !st.Available || !st.Authenticated || st.Ownership != toolmanager.StatusOwnershipManaged || st.RestartRequired {
		t.Fatalf("unexpected status: %+v", st)
	}
}

// A tampered ledger-selected binary is never registered as authenticated; with no other yt-dlp the engine stays
// unregistered and media URLs keep returning YT_DLP_UNAVAILABLE.
func TestApp_TamperedManagedYtdlpIsNotRegistered(t *testing.T) {
	exe := buildVersionHelper(t, "2026.10.26")
	sum := sha256.Sum256(exe)
	digest := hex.EncodeToString(sum[:])

	t.Setenv("PATH", t.TempDir())
	cfg := newTestConfig(t)
	cfg.YtdlpPath = ""
	dbPath := filepath.Join(t.TempDir(), "managed.db")
	storeDir := filepath.Join(cfg.DataDir, "bin", "yt-dlp")
	_ = os.MkdirAll(storeDir, 0o755)
	tampered := append(append([]byte(nil), exe...), 0x00)
	_ = os.WriteFile(filepath.Join(storeDir, digest+".exe"), tampered, 0o755)
	db, _ := database.New(dbPath)
	_ = database.NewSQLiteExecutionRepository(db).CommitToolActivation(context.Background(), &job.ManagedToolRecord{
		Name: toolmanager.ToolYtdlp, SHA256: digest, Version: "2026.10.26", ReleaseTag: "2026.10.26",
		FileName: digest + ".exe", SignerFingerprint: toolmanager.YtdlpSigningKeyFingerprint, AuthenticatedAt: time.Now().UTC(),
	}, nil)
	_ = db.Close()

	ctx := context.Background()
	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("App.New: %v", err)
	}
	defer func() { _ = application.Shutdown(ctx) }()
	if _, ok := application.Registry().Get("ytdlp"); ok {
		t.Fatal("tampered managed binary was registered")
	}
	_, err = application.Manager().Create(ctx, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if appErr, ok := err.(*job.AppError); !ok || appErr.Code != job.ErrYtDlpUnavailable {
		t.Fatalf("expected YT_DLP_UNAVAILABLE, got %v", err)
	}
}
