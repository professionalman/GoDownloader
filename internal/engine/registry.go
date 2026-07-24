package engine

import (
	"net/url"
	"strings"

	"downloader/internal/job"
)

// Known media host patterns that should use yt-dlp.
var mediaHosts = []string{
	"youtube.com",
	"youtu.be",
	"vimeo.com",
	"dailymotion.com",
	"twitch.tv",
	"soundcloud.com",
	"bandcamp.com",
	"tiktok.com",
	"instagram.com",
	"facebook.com",
	"twitter.com",
	"x.com",
	"reddit.com",
	"bilibili.com",
	"nicovideo.jp",
	"crunchyroll.com",
	"rumble.com",
	"odysee.com",
	"bitchute.com",
	"rutube.ru",
	"streamable.com",
	"v.redd.it",
}

// Common direct-download extensions handled by aria2.
var directExtensions = []string{
	".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
	".exe", ".msi", ".dmg", ".deb", ".rpm", ".appimage",
	".iso", ".img",
	".pdf", ".doc", ".docx", ".xls", ".xlsx",
	".bin", ".dat",
}

// Registry maps engine names to implementations and detects which engine to use.
type Registry struct {
	engines map[string]job.Engine
}

// NewRegistry creates a new engine registry.
func NewRegistry() *Registry {
	return &Registry{
		engines: make(map[string]job.Engine),
	}
}

// Register adds an engine under the given name.
func (r *Registry) Register(name string, eng job.Engine) {
	r.engines[name] = eng
}

// Get returns the engine registered under the given name.
func (r *Registry) Get(name string) (job.Engine, bool) {
	eng, ok := r.engines[name]
	return eng, ok
}

// Detect determines which engine should handle the given URL.
func (r *Registry) Detect(rawURL string) string {
	// Check for magnet URI
	if strings.HasPrefix(strings.ToLower(rawURL), "magnet:") {
		if _, ok := r.engines["qbittorrent"]; ok {
			return "qbittorrent"
		}
		return "aria2" // fallback
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "aria2"
	}

	host := strings.ToLower(u.Hostname())

	// Check for known media hosts
	for _, mediaHost := range mediaHosts {
		if host == mediaHost || strings.HasSuffix(host, "."+mediaHost) {
			// Only use ytdlp if the engine is registered
			if _, ok := r.engines["ytdlp"]; ok {
				return "ytdlp"
			}
			return "aria2"
		}
	}

	// Check for direct download extensions
	path := strings.ToLower(u.Path)
	for _, ext := range directExtensions {
		if strings.HasSuffix(path, ext) {
			return "aria2"
		}
	}

	// Default: if ytdlp is available and URL doesn't look like a direct file, try ytdlp
	// for unknown URLs that might be media sites yt-dlp supports
	if _, ok := r.engines["ytdlp"]; ok {
		// If no file extension or unrecognized extension, assume media
		lastDot := strings.LastIndex(path, ".")
		if lastDot == -1 || lastDot < strings.LastIndex(path, "/") {
			return "ytdlp"
		}
	}

	return "aria2"
}
