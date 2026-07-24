package engine

import (
	"testing"
)

func TestRegistry_Detect(t *testing.T) {
	r := NewRegistry()
	r.engines["aria2"] = nil
	r.engines["ytdlp"] = nil
	r.engines["qbittorrent"] = nil

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"Magnet URI", "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678", "qbittorrent"},
		{"Magnet URI uppercase", "MAGNET:?XT=URN:BTIH:1234567890ABCDEF1234567890ABCDEF12345678", "qbittorrent"},
		{"Direct ZIP", "https://example.com/files/archive.zip", "aria2"},
		{"Direct ISO", "https://releases.ubuntu.com/22.04/ubuntu-22.04-desktop-amd64.iso", "aria2"},
		{"Direct PDF", "https://example.com/docs/paper.pdf", "aria2"},
		{"YouTube Video", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "ytdlp"},
		{"YouTube Short", "https://youtu.be/dQw4w9WgXcQ", "ytdlp"},
		{"Vimeo Video", "https://vimeo.com/123456789", "ytdlp"},
		{"Extensionless direct URL", "https://example.com/download?id=123", "aria2"},
		{"Extensionless file link", "https://files.example.com/get/ABC123", "aria2"},
		{"URL with word magnet but HTTP scheme", "https://example.com/magnetic-file.zip", "aria2"},
		{"URL with word magnet in path", "https://example.com/magnet/file.iso", "aria2"},
		{"Unsupported scheme", "ftp://example.com/file.txt", "aria2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Detect(tt.url)
			if got != tt.expected {
				t.Errorf("Detect(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}
