package config

import (
	"os"
	"path/filepath"
	"strconv"
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
	QBitURL      string
	QBitUsername string
	QBitPassword string
	QBitTimeout  int
	DataDir      string
}

// New creates a Config populated from environment variables with sensible defaults.
func New() *Config {
	downloadDir := getEnv("DOWNLOAD_DIR", "./downloads")
	absDir, err := filepath.Abs(downloadDir)
	if err != nil {
		absDir = downloadDir
	}
	os.MkdirAll(absDir, 0755)

	dataDir := getEnv("DATA_DIR", "./data")
	absDataDir, err2 := filepath.Abs(dataDir)
	if err2 != nil {
		absDataDir = dataDir
	}
	os.MkdirAll(absDataDir, 0755)
	os.MkdirAll(filepath.Join(absDataDir, "torrents"), 0755)

	qbitTimeout, _ := strconv.Atoi(getEnv("QBIT_TIMEOUT", "30"))
	if qbitTimeout == 0 {
		qbitTimeout = 30
	}

	return &Config{
		ListenAddr:  getEnv("LISTEN_ADDR", ":8080"),
		DownloadDir: absDir,
		Aria2RPCURL: getEnv("ARIA2_RPC_URL", "http://localhost:6800/jsonrpc"),
		Aria2Secret: getEnv("ARIA2_SECRET", ""),
		WebDir:      getEnv("WEB_DIR", "./web/dist"),
		YtdlpPath:   getEnv("YTDLP_PATH", "yt-dlp"),
		FFmpegPath:  getEnv("FFMPEG_PATH", ""),
		QBitURL:      getEnv("QBIT_URL", "http://127.0.0.1:8081"),
		QBitUsername: getEnv("QBIT_USERNAME", "admin"),
		QBitPassword: getEnv("QBIT_PASSWORD", ""),
		QBitTimeout:  qbitTimeout,
		DataDir:      absDataDir,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
