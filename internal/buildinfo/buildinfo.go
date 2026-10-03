package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// Version is the application version, optionally injected via -ldflags.
// Defaults to "(devel)".
var Version = "(devel)"

// Commit is the VCS revision, optionally injected via -ldflags.
var Commit = ""

// Date is the build date timestamp, optionally injected via -ldflags.
var Date = ""

var hexRevisionRegex = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// Info represents truthful build identity information.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
}

// sanitizeRevision validates that the revision is a bounded hexadecimal commit hash.
// If valid, returns the lowercase hex string. Otherwise, returns "unknown".
func sanitizeRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if hexRevisionRegex.MatchString(rev) {
		return strings.ToLower(rev)
	}
	return "unknown"
}

// Current returns the resolved truthful build info for the running binary.
// Invariants:
// - Never shells out to git.
// - Never reads .git repository files.
// - Never parses build/config.yml or external configs at runtime.
// - Never hardcodes or guesses unproven versions.
func Current() Info {
	v := strings.TrimSpace(Version)
	c := strings.TrimSpace(Commit)
	dirty := false

	readInfo, ok := debug.ReadBuildInfo()
	if ok && readInfo != nil {
		// If Version was not injected, inspect debug.ReadBuildInfo
		if v == "" || v == "(devel)" {
			if readInfo.Main.Version != "" && readInfo.Main.Version != "(devel)" {
				v = readInfo.Main.Version
			} else {
				v = "(devel)"
			}
		}

		// Inspect VCS settings if Commit was not injected
		if c == "" {
			for _, setting := range readInfo.Settings {
				switch setting.Key {
				case "vcs.revision":
					c = setting.Value
				case "vcs.modified":
					if setting.Value == "true" {
						dirty = true
					}
				}
			}
		}
	} else {
		if v == "" {
			v = "(devel)"
		}
	}

	sanitizedCommit := sanitizeRevision(c)
	if sanitizedCommit != "unknown" && dirty {
		sanitizedCommit += "-dirty"
	}

	return Info{
		Version: v,
		Commit:  sanitizedCommit,
		Dirty:   dirty,
	}
}
