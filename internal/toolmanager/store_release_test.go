package toolmanager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- store

func newTestStore(t *testing.T) *managedStore {
	t.Helper()
	s := newManagedStore(filepath.Join(t.TempDir(), "data"))
	if err := s.ensureRoot(); err != nil {
		t.Fatal(err)
	}
	return s
}

func stage(t *testing.T, s *managedStore, content []byte) string {
	t.Helper()
	f, err := s.createStaging()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(content)
	_ = f.Close()
	return f.Name()
}

func TestStore_PromoteCreatesContentAddressedFile(t *testing.T) {
	s := newTestStore(t)
	content := []byte("authentic")
	p, reused, err := s.promote(stage(t, s, content), sha256Hex(content))
	if err != nil {
		t.Fatal(err)
	}
	if reused || filepath.Base(p) != sha256Hex(content)+".exe" {
		t.Fatalf("unexpected name %s", p)
	}
}

func TestStore_ExistingValidFileReusedAfterRehash(t *testing.T) {
	s := newTestStore(t)
	content := []byte("authentic")
	sha := sha256Hex(content)
	first, _, _ := s.promote(stage(t, s, content), sha)
	info1, _ := os.Stat(first)
	staging := stage(t, s, content)
	if _, reused, err := s.promote(staging, sha); err != nil || !reused {
		t.Fatalf("expected reuse of valid file: reused=%v err=%v", reused, err)
	}
	info2, _ := os.Stat(first)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("valid existing content-addressed file was rewritten")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatal("redundant staging file should be discarded")
	}
}

func TestStore_CorruptExistingFileMovedAsideNotOverwrittenInPlace(t *testing.T) {
	s := newTestStore(t)
	content := []byte("authentic")
	sha := sha256Hex(content)
	dest, _ := s.pathFor(sha)
	_ = os.WriteFile(dest, []byte("corrupt"), 0o644)
	if _, reused, err := s.promote(stage(t, s, content), sha); err != nil || reused {
		t.Fatalf("corrupt file must not be reused: reused=%v err=%v", reused, err)
	}
	if got, _, _ := hashRegularFile(dest); got != sha {
		t.Fatal("destination does not hold verified content")
	}
	entries, _ := os.ReadDir(s.root)
	aside := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), quarantinePrefix) {
			data, _ := os.ReadFile(filepath.Join(s.root, e.Name()))
			aside = string(data) == "corrupt"
		}
	}
	if !aside {
		t.Fatal("corrupt file should be preserved under a quarantine name")
	}
}

func TestStore_RejectsStagingOutsideStoreAndBadAddresses(t *testing.T) {
	s := newTestStore(t)
	outside := filepath.Join(t.TempDir(), stagingPrefix+"x.exe")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	if _, _, err := s.promote(outside, sha256Hex([]byte("x"))); err == nil {
		t.Fatal("staging file outside the store accepted")
	}
	if _, err := s.pathFor("../../etc"); err == nil {
		t.Fatal("non-hex content address accepted")
	}
	sha := strings.Repeat("a", 64)
	for _, name := range []string{`..\` + sha + ".exe", sha + ".EXE", strings.Repeat("b", 64) + ".exe", "C:\\x\\" + sha + ".exe"} {
		if _, err := s.resolveRecordPath(name, sha); err == nil {
			t.Fatalf("resolveRecordPath accepted %q", name)
		}
	}
}

func TestStore_JunctionAsStoreRootRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reparse-point junctions are Windows-specific")
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	target := filepath.Join(t.TempDir(), "elsewhere")
	_ = os.MkdirAll(filepath.Join(dataDir, "bin"), 0o755)
	_ = os.MkdirAll(target, 0o755)
	link := filepath.Join(dataDir, "bin", "yt-dlp")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create junction: %v %s", err, out)
	}
	s := newManagedStore(dataDir)
	if err := s.ensureRoot(); err == nil {
		t.Fatal("junctioned store root accepted; writes could escape managed storage")
	}
	if _, err := s.createStaging(); err == nil {
		t.Fatal("staging created through a junction")
	}
}

func TestStore_GarbageCollectionScope(t *testing.T) {
	s := newTestStore(t)
	keepName := strings.Repeat("a", 64) + ".exe"
	retiredName := strings.Repeat("b", 64) + ".exe"
	unknownName := strings.Repeat("c", 64) + ".exe" // not in this ledger: another DB or an orphan candidate
	files := []string{keepName, retiredName, unknownName, stagingPrefix + "old.exe", stagingPrefix + "new.exe",
		quarantinePrefix + "old", quarantinePrefix + "new", "readme.txt"}
	for _, n := range files {
		_ = os.WriteFile(filepath.Join(s.root, n), []byte(n), 0o644)
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Join(s.root, stagingPrefix+"old.exe"), now.Add(-25*time.Hour), now.Add(-25*time.Hour))
	_ = os.Chtimes(filepath.Join(s.root, quarantinePrefix+"old"), now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour))

	// Without the acquisition ownership lock, scratch files are ambiguous: even very old ones are kept.
	removed, _ := s.collectGarbage(map[string]bool{retiredName: true, keepName: true}, map[string]bool{keepName: true}, now, false)
	if len(removed) != 1 || removed[0] != retiredName {
		t.Fatalf("without ownership only the retired binary may go; removed=%v", removed)
	}
	for _, n := range []string{stagingPrefix + "old.exe", stagingPrefix + "new.exe", quarantinePrefix + "old"} {
		if _, err := os.Stat(filepath.Join(s.root, n)); err != nil {
			t.Fatalf("age alone must not remove %s", n)
		}
	}

	// With ownership proven, any staging file is abandoned regardless of age; quarantine keeps its retention.
	removed, _ = s.collectGarbage(map[string]bool{retiredName: true, keepName: true}, map[string]bool{keepName: true}, now, true)
	got := map[string]bool{}
	for _, n := range removed {
		got[n] = true
	}
	want := []string{stagingPrefix + "old.exe", stagingPrefix + "new.exe", quarantinePrefix + "old"}
	if len(removed) != len(want) {
		t.Fatalf("unexpected removals: %v", removed)
	}
	for _, n := range want {
		if !got[n] {
			t.Fatalf("%s should be removed; removed=%v", n, removed)
		}
	}
	for _, n := range []string{keepName, unknownName, quarantinePrefix + "new", "readme.txt"} {
		if _, err := os.Stat(filepath.Join(s.root, n)); err != nil {
			t.Fatalf("%s must be kept", n)
		}
	}
}

// Regression (finding 5): cleanup must not follow a junctioned store root into another directory.
func TestStore_GarbageCollectionRefusesJunctionedRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reparse-point junctions are Windows-specific")
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	target := filepath.Join(t.TempDir(), "victim")
	_ = os.MkdirAll(filepath.Join(dataDir, "bin"), 0o755)
	_ = os.MkdirAll(target, 0o755)
	victim := strings.Repeat("d", 64) + ".exe"
	_ = os.WriteFile(filepath.Join(target, victim), []byte("victim"), 0o644)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dataDir, "bin", "yt-dlp"), target).CombinedOutput(); err != nil {
		t.Skipf("cannot create junction: %v %s", err, out)
	}
	s := newManagedStore(dataDir)
	removed, _ := s.collectGarbage(map[string]bool{victim: true}, nil, time.Now(), true)
	if len(removed) != 0 {
		t.Fatalf("cleanup followed a junction and removed %v", removed)
	}
	if _, err := os.Stat(filepath.Join(target, victim)); err != nil {
		t.Fatal("file outside managed storage was deleted")
	}
}

func TestStore_GarbageCollectionDefersRunningExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("relies on Windows refusing to delete a running image")
	}
	s := newTestStore(t)
	src := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("PING.EXE unavailable: %v", err)
	}
	name := sha256Hex(data) + ".exe"
	running := filepath.Join(s.root, name)
	_ = os.WriteFile(running, data, 0o755)
	cmd := exec.Command(running, "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start copy: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(200 * time.Millisecond) // let the image map
	_, deferred := s.collectGarbage(map[string]bool{name: true}, nil, time.Now(), true)
	if len(deferred) != 1 || deferred[0] != name {
		t.Fatalf("running executable should be deferred, got deferred=%v", deferred)
	}
	if _, err := os.Stat(running); err != nil {
		t.Fatal("running executable was deleted")
	}
}

// ---------------------------------------------------------------- release client

func testClient(t *testing.T, h http.HandlerFunc) (*releaseClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	c := newReleaseClient(srv.Client().Transport)
	c.apiBase = srv.URL
	c.allowedHosts = map[string]bool{"127.0.0.1": true}
	return c, srv
}

var testAsset = releaseAsset{ID: 7, Name: "SHA2-256SUMS", Size: 5}

func TestReleaseClient_DirectAndRedirectedDelivery(t *testing.T) {
	var srvURL string
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/yt-dlp/yt-dlp/releases/assets/7":
			http.Redirect(w, r, srvURL+"/cdn/7?"+cdnSecret, http.StatusFound)
		case "/repos/yt-dlp/yt-dlp/releases/assets/8":
			_, _ = w.Write([]byte("hello"))
		case "/cdn/7":
			_, _ = w.Write([]byte("hello"))
		}
	})
	srvURL = srv.URL
	for _, a := range []releaseAsset{testAsset, {ID: 8, Name: "x", Size: 5}} {
		data, err := c.fetchSmallAsset(context.Background(), a, 64)
		if err != nil || string(data) != "hello" {
			t.Fatalf("asset %d: %q %v", a.ID, data, err)
		}
	}
}

func TestReleaseClient_RedirectPolicy(t *testing.T) {
	cases := map[string]func(srvURL string) string{
		"http downgrade":     func(u string) string { return strings.Replace(u, "https://", "http://", 1) + "/cdn/7" },
		"unlisted host":      func(u string) string { return strings.Replace(u, "127.0.0.1", "localhost", 1) + "/cdn/7?" + cdnSecret },
		"wildcard lookalike": func(string) string { return "https://evil.githubusercontent.com/cdn/7" },
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			var srvURL string
			c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					http.Redirect(w, r, target(srvURL), http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("hello"))
			})
			srvURL = srv.URL
			_, err := c.fetchSmallAsset(context.Background(), testAsset, 64)
			if !errors.Is(err, ErrAcquisitionNetwork) {
				t.Fatalf("expected redirect refusal, got %v", err)
			}
			if strings.Contains(err.Error(), "SUPERSECRETTOKEN") {
				t.Fatalf("error leaks redirect token: %v", err)
			}
		})
	}
}

func TestReleaseClient_RedirectLimit(t *testing.T) {
	hops := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	if _, err := c.fetchSmallAsset(context.Background(), testAsset, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("expected redirect limit error, got %v", err)
	}
	if hops > maxRedirectHops+1 {
		t.Fatalf("followed %d hops", hops)
	}
}

func TestReleaseClient_RedirectStripsCredentialHeaders(t *testing.T) {
	c := newReleaseClient(nil)
	req, _ := http.NewRequest(http.MethodGet, "https://release-assets.githubusercontent.com/x", nil)
	req.Header.Set("Authorization", "token secret")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("Proxy-Authorization", "basic x")
	if err := c.checkRedirect(req, []*http.Request{{}}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		if req.Header.Get(h) != "" {
			t.Fatalf("%s forwarded to release CDN", h)
		}
	}
}

func TestReleaseClient_ProductionPolicyIsHTTPSAllowlistOnly(t *testing.T) {
	c := newReleaseClient(nil)
	if c.apiBase != "https://api.github.com" || !c.requireHTTPS {
		t.Fatal("production client must target https://api.github.com")
	}
	if len(c.allowedHosts) != 2 || !c.allowedHosts["api.github.com"] || !c.allowedHosts["release-assets.githubusercontent.com"] {
		t.Fatalf("unexpected production allowlist: %v", c.allowedHosts)
	}
	c.apiBase = "http://api.github.com"
	if _, err := c.latestRelease(context.Background()); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("plain HTTP must be refused, got %v", err)
	}
	if assetPath(releaseAsset{ID: 42}) != "/repos/yt-dlp/yt-dlp/releases/assets/42" {
		t.Fatal("asset path must be built from the fixed repository and numeric ID")
	}
}

func TestReleaseClient_StatusTimeoutCancellationAndBounds(t *testing.T) {
	block := make(chan struct{})
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/yt-dlp/yt-dlp/releases/assets/1":
			w.WriteHeader(http.StatusForbidden)
		case "/repos/yt-dlp/yt-dlp/releases/assets/2":
			<-block
		case "/repos/yt-dlp/yt-dlp/releases/assets/3":
			_, _ = w.Write([]byte("abc")) // shorter than metadata size
		case "/repos/yt-dlp/yt-dlp/releases/assets/4":
			_, _ = w.Write([]byte(strings.Repeat("x", 200)))
		}
	})
	// Registered after the server so it runs first: unblock the stalled handler before srv.Close waits on it.
	t.Cleanup(func() { close(block) })
	if _, err := c.fetchSmallAsset(context.Background(), releaseAsset{ID: 1, Size: 5}, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("status: %v", err)
	}
	c.smallTimeout = 100 * time.Millisecond
	if _, err := c.fetchSmallAsset(context.Background(), releaseAsset{ID: 2, Size: 5}, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("timeout: %v", err)
	}
	c.smallTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := c.fetchSmallAsset(ctx, releaseAsset{ID: 2, Size: 5}, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := c.fetchSmallAsset(context.Background(), releaseAsset{ID: 3, Size: 5}, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("truncated: %v", err)
	}
	if _, err := c.fetchSmallAsset(context.Background(), releaseAsset{ID: 4, Size: 200}, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("oversized: %v", err)
	}
	if _, err := c.streamAsset(context.Background(), releaseAsset{ID: 4, Size: 200}, io.Discard, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("oversized stream: %v", err)
	}
	if _, err := c.streamAsset(context.Background(), releaseAsset{ID: 3, Size: 5}, io.Discard, 64); !errors.Is(err, ErrAcquisitionNetwork) {
		t.Fatalf("truncated stream: %v", err)
	}
}

func TestReleaseClient_MetadataValidation(t *testing.T) {
	assets := `[{"id":1,"name":"yt-dlp.exe","size":100},{"id":2,"name":"SHA2-256SUMS","size":10},{"id":3,"name":"SHA2-256SUMS.sig","size":10}]`
	cases := map[string]string{
		"draft":          `{"tag_name":"2026.10.26","draft":true,"assets":` + assets + `}`,
		"prerelease":     `{"tag_name":"2026.10.26","prerelease":true,"assets":` + assets + `}`,
		"nightly tag":    `{"tag_name":"nightly","assets":` + assets + `}`,
		"missing exe":    `{"tag_name":"2026.10.26","assets":[{"id":2,"name":"SHA2-256SUMS","size":10},{"id":3,"name":"SHA2-256SUMS.sig","size":10}]}`,
		"duplicate exe":  `{"tag_name":"2026.10.26","assets":[{"id":1,"name":"yt-dlp.exe","size":100},{"id":9,"name":"yt-dlp.exe","size":100},{"id":2,"name":"SHA2-256SUMS","size":10},{"id":3,"name":"SHA2-256SUMS.sig","size":10}]}`,
		"oversized exe":  `{"tag_name":"2026.10.26","assets":[{"id":1,"name":"yt-dlp.exe","size":99999999999},{"id":2,"name":"SHA2-256SUMS","size":10},{"id":3,"name":"SHA2-256SUMS.sig","size":10}]}`,
		"invalid json":   `{`,
		"oversized meta": `{"pad":"` + strings.Repeat("x", maxReleaseMetadataBytes) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			if _, err := c.latestRelease(context.Background()); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"2026.10.26","assets":` + assets + `}`))
	})
	rel, err := c.latestRelease(context.Background())
	if err != nil || rel.Tag != "2026.10.26" || rel.Exe.ID != 1 || rel.Manifest.ID != 2 || rel.Signature.ID != 3 {
		t.Fatalf("valid metadata: %+v %v", rel, err)
	}
}
