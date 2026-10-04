package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_NewIsPure(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Calling New must NOT create ./downloads or ./data in the current working directory
	cfg := New()
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}

	if _, err := os.Stat("downloads"); !os.IsNotExist(err) {
		t.Errorf("expected downloads directory NOT to be created by New(), but it exists")
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Errorf("expected data directory NOT to be created by New(), but it exists")
	}

	// Calling EnsureDirectories must create them
	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	if _, err := os.Stat("downloads"); err != nil {
		t.Errorf("expected downloads directory to exist after EnsureDirectories(), got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "torrents")); err != nil {
		t.Errorf("expected data/torrents directory to exist after EnsureDirectories(), got error: %v", err)
	}
}
