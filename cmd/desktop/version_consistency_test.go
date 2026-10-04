package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/buildinfo"
)

func TestSourceMetadata_VersionConsistency(t *testing.T) {
	// Find project root
	root := "../.."

	// 1. Check build/config.yml contains version: "0.9.1"
	configBytes, err := os.ReadFile(filepath.Join(root, "build", "config.yml"))
	if err != nil {
		t.Fatalf("failed to read build/config.yml: %v", err)
	}
	configStr := string(configBytes)
	if !strings.Contains(configStr, `version: "0.9.1"`) {
		t.Errorf("build/config.yml missing version: \"0.9.1\"")
	}
	if !strings.Contains(configStr, `description: "GoDownloader Windows Desktop Beta"`) {
		t.Errorf("build/config.yml missing Beta description")
	}

	// 2. Check build/windows/info.json contains 0.9.1
	infoBytes, err := os.ReadFile(filepath.Join(root, "build", "windows", "info.json"))
	if err != nil {
		t.Fatalf("failed to read build/windows/info.json: %v", err)
	}
	var infoMap map[string]any
	if err := json.Unmarshal(infoBytes, &infoMap); err != nil {
		t.Fatalf("failed to parse info.json: %v", err)
	}
	fixed, ok := infoMap["fixed"].(map[string]any)
	fileVer, _ := fixed["file_version"].(string)
	if !ok || !strings.HasPrefix(fileVer, "0.9.1") {
		t.Errorf("expected info.json fixed.file_version to be 0.9.1 or 0.9.1.0, got %v", fixed)
	}

	// 3. Check build/windows/wails.exe.manifest contains 0.9.1.0
	manifestBytes, err := os.ReadFile(filepath.Join(root, "build", "windows", "wails.exe.manifest"))
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}
	if !strings.Contains(string(manifestBytes), `version="0.9.1.0"`) {
		t.Errorf("manifest missing version=\"0.9.1.0\"")
	}

	// 4. Check build/windows/installer.nsi contains 0.9.1 and setup filename
	nsiBytes, err := os.ReadFile(filepath.Join(root, "build", "windows", "installer.nsi"))
	if err != nil {
		t.Fatalf("failed to read installer.nsi: %v", err)
	}
	nsiStr := string(nsiBytes)
	if !strings.Contains(nsiStr, `GoDownloader-0.9.1-beta-windows-amd64-setup.exe`) {
		t.Errorf("installer.nsi missing setup artifact filename")
	}
	if !strings.Contains(nsiStr, `"DisplayVersion" "0.9.1"`) {
		t.Errorf("installer.nsi missing DisplayVersion 0.9.1")
	}

	// 5. Hard invariant: internal/buildinfo source default MUST remain "(devel)"
	if current := buildinfo.Current().Version; current != "(devel)" {
		t.Errorf("expected source buildinfo.Current().Version to be (devel), got %q", current)
	}
}
