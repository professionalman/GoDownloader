package toolmanager

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"downloader/internal/runtimeguard"
)

// errNotRegularFile reports a link, directory, or other non-regular file where a binary was expected.
var errNotRegularFile = errors.New("not a regular file")

// contentAddressedName matches the only executable names the managed store ever creates.
var contentAddressedName = regexp.MustCompile(`^[0-9a-f]{64}\.exe$`)

const (
	stagingPrefix    = ".partial-"
	quarantinePrefix = ".quarantine-"
)

// managedStore is the content-addressed, immutable yt-dlp store at <dataDir>/bin/yt-dlp/<sha256>.exe.
// Files are never modified in place; the authenticated ledger, not file presence, selects the active binary.
type managedStore struct {
	dataDir string
	root    string
}

func newManagedStore(dataDir string) *managedStore {
	return &managedStore{dataDir: dataDir, root: filepath.Join(dataDir, "bin", "yt-dlp")}
}

// ensureRoot creates the store directory and refuses to operate through a symlink or reparse point anywhere
// between dataDir and the store root, so no write, rename, or delete can be redirected outside managed storage.
func (s *managedStore) ensureRoot() error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create managed store: %w", err)
	}
	return s.checkRoot()
}

// checkRoot verifies, without creating anything, that bin and the store root are real directories rather than
// links or reparse points. Every destructive operation calls it first.
func (s *managedStore) checkRoot() error {
	for _, dir := range []string{filepath.Join(s.dataDir, "bin"), s.root} {
		fi, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect managed store: %w", err)
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
			return fmt.Errorf("managed store path %s is a link or reparse point; refusing to use it", filepath.Base(dir))
		}
	}
	return nil
}

// pathFor returns the content-addressed path for a lowercase hex SHA-256 digest.
func (s *managedStore) pathFor(sha string) (string, error) {
	name := sha + ".exe"
	if !contentAddressedName.MatchString(name) {
		return "", fmt.Errorf("invalid content address %q", sha)
	}
	return filepath.Join(s.root, name), nil
}

// resolveRecordPath maps a ledger file name to its path, accepting only a bare content-addressed name whose
// digest matches sha. This rejects traversal and any path outside the store.
func (s *managedStore) resolveRecordPath(fileName, sha string) (string, error) {
	if fileName != sha+".exe" || !contentAddressedName.MatchString(fileName) {
		return "", fmt.Errorf("ledger file name is not the content address of its digest")
	}
	return filepath.Join(s.root, fileName), nil
}

// hashRegularFile streams path through SHA-256. It refuses links, reparse points, and non-regular files.
func hashRegularFile(path string) (string, int64, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %s", errNotRegularFile, filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// createStaging opens a new uniquely named staging file inside the store (same filesystem as the final path).
func (s *managedStore) createStaging() (*os.File, error) {
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	return os.CreateTemp(s.root, stagingPrefix+"*.exe")
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// promote moves a verified staging file to its content-addressed name. An existing file at that name is
// re-hashed: reused (and the staging file discarded) only if its content really matches; otherwise it is moved
// aside to a quarantine name rather than overwritten in place. reused reports that a valid file already existed.
func (s *managedStore) promote(stagingPath, sha string) (path string, reused bool, err error) {
	if filepath.Dir(filepath.Clean(stagingPath)) != filepath.Clean(s.root) ||
		!strings.HasPrefix(filepath.Base(stagingPath), stagingPrefix) {
		return "", false, fmt.Errorf("staging file is outside the managed store")
	}
	if err := s.checkRoot(); err != nil {
		return "", false, err
	}
	dest, err := s.pathFor(sha)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(dest); err == nil {
		existing, _, hashErr := hashRegularFile(dest)
		if hashErr == nil && existing == sha {
			_ = os.Remove(stagingPath)
			return dest, true, nil
		}
		aside := filepath.Join(s.root, quarantinePrefix+sha[:12]+"-"+randomSuffix())
		if err := os.Rename(dest, aside); err != nil {
			return "", false, fmt.Errorf("move corrupt content-addressed file aside: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect content-addressed destination: %w", err)
	}
	if err := os.Rename(stagingPath, dest); err != nil {
		return "", false, fmt.Errorf("promote staged executable: %w", err)
	}
	syncDir(s.root)
	return dest, false, nil
}

// staleQuarantineAge keeps moved-aside corrupt files for diagnosis before they become removable.
const staleQuarantineAge = 7 * 24 * time.Hour

// acquireOwnership takes the store-wide acquisition ownership lock (an exclusive OS lock released automatically
// if the process dies). Every acquisition holds it from before its staging file exists until promotion,
// activation, and cleanup finish, in any process sharing this data directory. Holding it therefore proves that
// no live acquisition owns any staging file. It fails with runtimeguard.ErrRuntimeAlreadyOwned if held elsewhere.
func (s *managedStore) acquireOwnership() (io.Closer, error) {
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	return runtimeguard.Acquire(filepath.Join(s.root, "acquisition"))
}

// collectGarbage removes only files whose ownership and obsolescence are provable:
//   - content-addressed executables named in retired (binaries this ledger itself selected and later retired
//     or quarantined) and not in keep;
//   - staging files, and quarantine files older than staleQuarantineAge, but only when scratchOwned (the caller
//     holds the acquisition ownership lock). File age alone never proves a staging file is abandoned.
//
// Content-addressed files unknown to this ledger are never removed: another database sharing the data
// directory, or an orphaned verified candidate, may own them. Unknown names are never touched. Nothing is
// removed if the store root is a link or reparse point. Removal failures (for example an image still mapped by a
// running process) are reported as deferred rather than retried.
func (s *managedStore) collectGarbage(retired, keep map[string]bool, now time.Time, scratchOwned bool) (removed, deferred []string) {
	if err := s.checkRoot(); err != nil {
		return nil, nil
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || keep[name] {
			continue
		}
		var obsolete bool
		switch {
		case contentAddressedName.MatchString(name):
			obsolete = retired[name]
		case strings.HasPrefix(name, stagingPrefix):
			obsolete = scratchOwned
		case strings.HasPrefix(name, quarantinePrefix):
			if info, err := e.Info(); err == nil {
				obsolete = scratchOwned && now.Sub(info.ModTime()) > staleQuarantineAge
			}
		}
		if !obsolete {
			continue
		}
		if err := os.Remove(filepath.Join(s.root, name)); err != nil {
			deferred = append(deferred, name)
			continue
		}
		removed = append(removed, name)
	}
	return removed, deferred
}
