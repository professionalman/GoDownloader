package main

import (
	"os"
	"path/filepath"
)

// DataRootInfo holds metadata about resolved desktop data directories.
type DataRootInfo struct {
	ExecutablePath   string `json:"executablePath"`
	WorkingDir       string `json:"workingDir"`
	ResolvedDataRoot string `json:"resolvedDataRoot"`
	IsDeterministic  bool   `json:"isDeterministic"`
}

// ResolveDesktopDataRoot determines the deterministic per-user Windows application
// data root (%APPDATA%\GoDownloader), completely independent of launch CWD.
func ResolveDesktopDataRoot() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}

	dataRoot := filepath.Join(appData, "GoDownloader")
	if err := os.MkdirAll(dataRoot, 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dataRoot, "data"), 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dataRoot, "data", "torrents"), 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dataRoot, "downloads"), 0755); err != nil {
		return "", err
	}

	return dataRoot, nil
}
