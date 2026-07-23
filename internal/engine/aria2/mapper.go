package aria2

import (
	"strings"

	"downloader/internal/job"
)

// mapAria2Status maps raw aria2 status strings to application job status.
func mapAria2Status(raw string) job.JobStatus {
	switch raw {
	case "active":
		return job.StatusDownloading
	case "waiting":
		return job.StatusQueued
	case "paused":
		return job.StatusPaused
	case "complete":
		return job.StatusCompleted
	case "error":
		return job.StatusFailed
	case "removed":
		return job.StatusCancelled
	default:
		return job.StatusFailed
	}
}

// normalizeError converts aria2 error codes and messages into user-friendly strings.
func normalizeError(code, message string) string {
	// Common aria2 error codes
	switch code {
	case "0":
		return "" // no error
	case "1":
		return "An unknown error occurred."
	case "2":
		return "Connection timed out."
	case "3":
		return "The resource was not found (404)."
	case "4":
		return "The resource was not found. aria2 received a 404 status."
	case "5":
		return "Download speed was too slow. Connection closed."
	case "6":
		return "A network problem occurred."
	case "7":
		return "There were unfinished downloads. Forced shutdown."
	case "9":
		return "Not enough disk space available."
	case "10":
		return "The piece length was different from the expected value."
	case "13":
		return "A file already exists at the download path."
	case "19":
		return "DNS resolution failed."
	case "21":
		return "FTP command failed."
	case "22":
		return "HTTP response header was bad or unexpected."
	case "23":
		return "Too many redirects occurred."
	case "24":
		return "HTTP authorization failed."
	case "25":
		return "Could not parse bencoded file."
	case "30":
		return "Connection to the remote server was refused."
	}

	// Fallback: try to provide the message if available
	if message != "" {
		// Clean up common aria2 error prefixes
		clean := strings.TrimPrefix(message, "error ")
		clean = strings.TrimPrefix(clean, "Error: ")
		if clean != "" {
			return clean
		}
	}

	if code != "" {
		return "Download failed with error code " + code + "."
	}

	return "An unknown download error occurred."
}
