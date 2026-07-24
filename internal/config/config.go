package config

import (
	"os"
	"path/filepath"
)

// Config holds the application configuration.
type Config struct {
	ListenAddr  string
	DownloadDir string
	Aria2RPCURL string
	Aria2Secret string
	WebDir      string
	YtdlpPath   string
	FFmpegPath  string
}

// New creates a Config populated from environment variables with sensible defaults.
func New() *Config {
	downloadDir := getEnv("DOWNLOAD_DIR", "./downloads")
	absDir, err := filepath.Abs(downloadDir)
	if err != nil {
		absDir = downloadDir
	}
	os.MkdirAll(absDir, 0755)

	return &Config{
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		DownloadDir: absDir,
		Aria2RPCURL: getEnv("ARIA2_RPC_URL", "http://localhost:6800/jsonrpc"),
		Aria2Secret: getEnv("ARIA2_SECRET", ""),
		WebDir:      getEnv("WEB_DIR", "./web/dist"),
		YtdlpPath:   getEnv("YTDLP_PATH", "yt-dlp"),
		FFmpegPath:  getEnv("FFMPEG_PATH", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
