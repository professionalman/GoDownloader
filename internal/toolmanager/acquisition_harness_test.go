package toolmanager

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"downloader/internal/database"
)

// cdnSecret stands in for the temporary authorization query on signed release-CDN redirects.
const cdnSecret = "X-Amz-Signature=SUPERSECRETTOKEN"

const (
	assetIDExe      = 9001
	assetIDManifest = 9002
	assetIDSig      = 9003
)

// fakeExeBytes returns distinct fake executable content per version (never really executed).
func fakeExeBytes(version string) []byte {
	return []byte("MZ-fake-yt-dlp-" + version + strings.Repeat("\x00", 512))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeRelease is a local TLS stand-in for the GitHub release API and release CDN.
type fakeRelease struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	tag      string
	exe      []byte
	manifest []byte
	sig      []byte
	redirect bool
	// metaMutator may rewrite the release JSON before it is served.
	metaMutator func(meta map[string]any)
	// exeServe may override how the executable body is written (truncation, oversize, stalls).
	exeServe func(w http.ResponseWriter)
	hits     map[string]int
	authSeen []string
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	fr := &fakeRelease{t: t, hits: map[string]int{}, redirect: true}
	fr.srv = httptest.NewTLSServer(http.HandlerFunc(fr.serve))
	t.Cleanup(fr.srv.Close)
	return fr
}

// publish sets the release served by the fake: tag, executable bytes, and a manifest signed by signer.
func (fr *fakeRelease) publish(tag string, exe []byte, manifest []byte, sig []byte) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.tag, fr.exe, fr.manifest, fr.sig = tag, exe, manifest, sig
}

func (fr *fakeRelease) hit(name string) int {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return fr.hits[name]
}

func (fr *fakeRelease) serve(w http.ResponseWriter, r *http.Request) {
	fr.mu.Lock()
	if a := r.Header.Get("Authorization"); a != "" {
		fr.authSeen = append(fr.authSeen, r.URL.Path)
	}
	tag, exe, manifest, sig := fr.tag, fr.exe, fr.manifest, fr.sig
	fr.mu.Unlock()

	switch {
	case r.URL.Path == "/repos/yt-dlp/yt-dlp/releases/latest":
		meta := map[string]any{
			"tag_name":   tag,
			"draft":      false,
			"prerelease": false,
			"assets": []any{
				map[string]any{"id": assetIDExe, "name": "yt-dlp.exe", "size": len(exe)},
				map[string]any{"id": assetIDManifest, "name": "SHA2-256SUMS", "size": len(manifest)},
				map[string]any{"id": assetIDSig, "name": "SHA2-256SUMS.sig", "size": len(sig)},
				map[string]any{"id": 9100, "name": "yt-dlp", "size": 10},
			},
		}
		if fr.metaMutator != nil {
			fr.metaMutator(meta)
		}
		_ = json.NewEncoder(w).Encode(meta)
	case strings.HasPrefix(r.URL.Path, "/repos/yt-dlp/yt-dlp/releases/assets/"):
		id := strings.TrimPrefix(r.URL.Path, "/repos/yt-dlp/yt-dlp/releases/assets/")
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "wrong accept", http.StatusUnsupportedMediaType)
			return
		}
		if fr.redirect {
			http.Redirect(w, r, fr.srv.URL+"/cdn/"+id+"?"+cdnSecret, http.StatusFound)
			return
		}
		fr.writeAsset(w, id, exe, manifest, sig)
	case strings.HasPrefix(r.URL.Path, "/cdn/"):
		fr.writeAsset(w, strings.TrimPrefix(r.URL.Path, "/cdn/"), exe, manifest, sig)
	default:
		http.NotFound(w, r)
	}
}

func (fr *fakeRelease) writeAsset(w http.ResponseWriter, id string, exe, manifest, sig []byte) {
	n, _ := strconv.Atoi(id)
	fr.mu.Lock()
	switch n {
	case assetIDExe:
		fr.hits["exe"]++
	case assetIDManifest:
		fr.hits["manifest"]++
	case assetIDSig:
		fr.hits["sig"]++
	}
	serveExe := fr.exeServe
	fr.mu.Unlock()
	switch n {
	case assetIDExe:
		if serveExe != nil {
			serveExe(w)
			return
		}
		_, _ = w.Write(exe)
	case assetIDManifest:
		_, _ = w.Write(manifest)
	case assetIDSig:
		_, _ = w.Write(sig)
	default:
		http.NotFound(w, nil)
	}
}

// testSigner wraps a synthetic OpenPGP signing identity and its trust root.
type testSigner struct {
	root   *trustRoot
	signFn func(data []byte) []byte
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	entity, armored := syntheticSigner(t)
	root, err := newTrustRoot(armored, strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint)))
	if err != nil {
		t.Fatal(err)
	}
	return &testSigner{root: root, signFn: func(data []byte) []byte { return signDetached(t, entity, data, crypto.SHA256) }}
}

// releaseFor builds a signed manifest for exe and publishes it.
func (fr *fakeRelease) publishSigned(signer *testSigner, tag string, exe []byte) {
	manifest := []byte(sha256Hex([]byte("linux binary")) + "  yt-dlp\n" + sha256Hex(exe) + "  yt-dlp.exe\n")
	fr.publish(tag, exe, manifest, signer.signFn(manifest))
}

// execRecorder fakes version probes and records the SHA-256 of every file at the instant it would execute.
type execRecorder struct {
	mu       sync.Mutex
	executed []string // sha256 of executed file content
	versions map[string]string
	failAll  bool
}

func (r *execRecorder) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	data, err := os.ReadFile(name)
	sum := "unreadable"
	if err == nil {
		sum = sha256Hex(data)
	}
	r.mu.Lock()
	r.executed = append(r.executed, sum)
	version, ok := r.versions[sum]
	fail := r.failAll
	r.mu.Unlock()
	if fail || !ok {
		return exec.CommandContext(ctx, "cmd", "/c", "exit 1")
	}
	return exec.CommandContext(ctx, "cmd", "/c", "echo "+version)
}

func (r *execRecorder) executedHashes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.executed...)
}

// fakeGate is a minimal UsageCoordinator.
type fakeGate struct {
	mu        sync.Mutex
	inUse     bool
	exclusive bool
	releases  int
}

func (g *fakeGate) TryExclusive() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse || g.exclusive {
		return nil, errors.New("in use")
	}
	g.exclusive = true
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.exclusive = false
			g.releases++
			g.mu.Unlock()
		})
	}, nil
}

func (g *fakeGate) state() (exclusive bool, releases int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.exclusive, g.releases
}

type acquisitionEnv struct {
	dataDir  string
	dbPath   string
	db       *database.DB
	repo     *database.SQLiteExecutionRepository
	rel      *fakeRelease
	signer   *testSigner
	recorder *execRecorder
	gate     *fakeGate
}

func newAcquisitionEnv(t *testing.T) *acquisitionEnv {
	t.Helper()
	dir := t.TempDir()
	env := &acquisitionEnv{
		dataDir:  filepath.Join(dir, "data"),
		dbPath:   filepath.Join(dir, "test.db"),
		rel:      newFakeRelease(t),
		signer:   newTestSigner(t),
		recorder: &execRecorder{versions: map[string]string{}},
		gate:     &fakeGate{},
	}
	env.openDB(t)
	return env
}

func (env *acquisitionEnv) openDB(t *testing.T) {
	t.Helper()
	db, err := database.New(env.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env.db = db
	env.repo = database.NewSQLiteExecutionRepository(db)
}

// knownVersion registers fake executable content as reporting version when probed.
func (env *acquisitionEnv) knownVersion(version string) []byte {
	exe := fakeExeBytes(version)
	env.recorder.mu.Lock()
	env.recorder.versions[sha256Hex(exe)] = version
	env.recorder.mu.Unlock()
	return exe
}

// manager builds a ToolManager wired to the fake release, synthetic trust root, recorder, and gate.
// It simulates a fresh backend process sharing the same data directory and database.
func (env *acquisitionEnv) manager(t *testing.T) *Manager {
	t.Helper()
	m := New(env.dataDir, env.repo)
	m.release = newReleaseClient(env.rel.srv.Client().Transport)
	m.release.apiBase = env.rel.srv.URL
	m.release.allowedHosts = map[string]bool{"127.0.0.1": true}
	m.platformSupportedFn = func() bool { return true }
	m.trustRootFn = func() (*trustRoot, error) { return env.signer.root, nil }
	m.execCommandFn = env.recorder.command
	m.lookPathFn = func(string) (string, error) { return "", fmt.Errorf("not on PATH") }
	m.SetUsageCoordinator(env.gate)
	return m
}

// assertOnlyAuthenticatedExecuted fails if any executed file's content is not in authenticated.
func (env *acquisitionEnv) assertOnlyAuthenticatedExecuted(t *testing.T, authenticated ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, a := range authenticated {
		allowed[a] = true
	}
	for _, h := range env.recorder.executedHashes() {
		if !allowed[h] {
			t.Fatalf("executed unauthenticated content %s", h)
		}
	}
}

func storeFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "bin", "yt-dlp"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Name() == "acquisition.runtime.lock" { // persistent cross-process ownership lock, not a binary
			continue
		}
		names = append(names, e.Name())
	}
	return names
}
