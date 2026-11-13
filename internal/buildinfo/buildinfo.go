package buildinfo

import (
	"regexp"
	"runtime"
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

// Modified is "true" when the injected Commit was built from a working tree with uncommitted changes, "false"
// when it was clean, and empty when not injected. It is only consulted together with an injected Commit.
var Modified = ""

// Configuration names the build configuration (for example "production"), injected via -ldflags. Empty means a
// development build.
var Configuration = ""

var hexRevisionRegex = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// Info represents truthful build identity information.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Dirty   bool   `json:"dirty"`
	// Platform is the compile target (GOOS/GOARCH) of the running binary.
	Platform string `json:"platform"`
	// Configuration is the build configuration ("production" for packaged builds, "development" otherwise).
	Configuration string `json:"configuration"`
	// CommitSource tells how Commit was obtained: "injected" (release build metadata), "vcs" (Go toolchain VCS
	// stamping), or "none".
	CommitSource string `json:"commitSource"`
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
	source := "none"
	if c != "" {
		source = "injected"
		dirty = strings.TrimSpace(Modified) == "true"
	}

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
					source = "vcs"
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
	if sanitizedCommit == "unknown" {
		source = "none"
		dirty = false
	} else if dirty {
		sanitizedCommit += "-dirty"
	}

	config := strings.TrimSpace(Configuration)
	if config == "" {
		config = "development"
	}

	return Info{
		Version:       v,
		Commit:        sanitizedCommit,
		Dirty:         dirty,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		Configuration: config,
		CommitSource:  source,
	}
}
