//go:build windows

package build

// Packaging path-safety regression tests. Every test works in disposable temporary directories with a copy of the
// NSIS script, so the repository's own bin\ (and any existing executable or installer) is never read or written.
// Tests that need NSIS skip when makensis is not installed.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const installerName = "GoDownloader-0.9.1-beta-windows-amd64-setup.exe"

func findMakensis(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("makensis"); err == nil {
		return p
	}
	for _, p := range []string{os.Getenv("ProgramFiles(x86)") + `\NSIS\makensis.exe`, os.Getenv("ProgramFiles") + `\NSIS\makensis.exe`} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("makensis not installed")
	return ""
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("windows", rel))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// workspace copies installer.nsi and its icon into <root>\build\windows. NSIS's former default (..\..\bin) would
// resolve to <root>\bin, which the tests use as the "default folder" decoy.
func workspace(t *testing.T) (root, nsi string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "build", "windows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"installer.nsi", "icon.ico"} {
		data, err := os.ReadFile(repoFile(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(dir, "installer.nsi")
}

// randomFile writes n incompressible bytes, so the installer size reveals which payload NSIS packaged.
func randomFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(path), err)
	}
	return fi.Size()
}

func runPackage(t *testing.T, cwd, nsi, binDir string) (string, error) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", repoFile(t, "package-installer.ps1"),
		"-BinDir", binDir, "-NsisScript", nsi)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// decoys plants an old executable and an old installer in the former default folder (<root>\bin) and returns their
// hashes, so tests can prove they are neither packaged nor overwritten.
func decoys(t *testing.T, root string) map[string]string {
	t.Helper()
	randomFile(t, filepath.Join(root, "bin", "GoDownloader.exe"), 40_000)
	randomFile(t, filepath.Join(root, "bin", installerName), 1_000)
	return map[string]string{
		filepath.Join(root, "bin", "GoDownloader.exe"): fileHash(t, filepath.Join(root, "bin", "GoDownloader.exe")),
		filepath.Join(root, "bin", installerName):      fileHash(t, filepath.Join(root, "bin", installerName)),
	}
}

func assertUnchanged(t *testing.T, files map[string]string) {
	t.Helper()
	for p, want := range files {
		if got := fileHash(t, p); got != want {
			t.Fatalf("%s was modified", filepath.Base(filepath.Dir(p))+`\`+filepath.Base(p))
		}
	}
}

// 1. Default output directory (the Taskfile's BIN_DIR "bin", relative to the repository root).
func TestPackaging_DefaultOutputDirectory(t *testing.T) {
	findMakensis(t)
	root, nsi := workspace(t)
	randomFile(t, filepath.Join(root, "bin", "GoDownloader.exe"), 300_000)
	if out, err := runPackage(t, root, nsi, "bin"); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if size := fileSize(t, filepath.Join(root, "bin", installerName)); size < 300_000 {
		t.Fatalf("installer (%d bytes) does not contain the 300 KB payload", size)
	}
}

// 2, 3, 6, 8. A custom directory (with spaces) is used for both the payload and the installer; the former default
// folder's old executable is not packaged and its old installer is not overwritten.
func TestPackaging_CustomDirectoryWithSpacesIgnoresDefaultFolder(t *testing.T) {
	findMakensis(t)
	root, nsi := workspace(t)
	untouched := decoys(t, root)
	custom := filepath.Join(t.TempDir(), "candidate out dir")
	randomFile(t, filepath.Join(custom, "GoDownloader.exe"), 300_000)

	if out, err := runPackage(t, root, nsi, custom); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if size := fileSize(t, filepath.Join(custom, installerName)); size < 300_000 {
		t.Fatalf("installer (%d bytes) does not contain the selected 300 KB payload: the default folder's 40 KB executable was packaged", size)
	}
	assertUnchanged(t, untouched)
}

// 4, 8. A missing executable fails before NSIS runs; unrelated existing artifacts stay unchanged.
func TestPackaging_MissingExecutableFailsBeforePackaging(t *testing.T) {
	findMakensis(t)
	root, nsi := workspace(t)
	untouched := decoys(t, root)
	custom := filepath.Join(t.TempDir(), "empty output")
	randomFile(t, filepath.Join(custom, installerName), 2_000) // an older installer in the selected folder
	untouched[filepath.Join(custom, installerName)] = fileHash(t, filepath.Join(custom, installerName))

	out, err := runPackage(t, root, nsi, custom)
	if err == nil || !strings.Contains(out, "was not found in the selected output directory") {
		t.Fatalf("expected refusal, got err=%v\n%s", err, out)
	}
	if strings.Contains(out, "Processing script file") {
		t.Fatal("makensis ran although the executable was missing")
	}
	assertUnchanged(t, untouched)
}

// 5. A non-existent output directory fails safely; nothing is written.
func TestPackaging_MissingOutputDirectoryFails(t *testing.T) {
	findMakensis(t)
	root, nsi := workspace(t)
	untouched := decoys(t, root)
	missing := filepath.Join(t.TempDir(), "does not exist")
	out, err := runPackage(t, root, nsi, missing)
	if err == nil || !strings.Contains(out, "does not exist; refusing to package") {
		t.Fatalf("expected refusal, got err=%v\n%s", err, out)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatal("the missing output directory was created")
	}
	assertUnchanged(t, untouched)
}

// The installer script has no default output directory: invoked without /DBIN_DIR it fails to compile instead of
// packaging the default folder's executable and overwriting its installer. (This is the pre-fix failure mode.)
func TestPackaging_InstallerScriptRequiresExplicitBinDir(t *testing.T) {
	makensis := findMakensis(t)
	root, nsi := workspace(t)
	untouched := decoys(t, root)
	cmd := exec.Command(makensis, nsi)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("makensis without /DBIN_DIR succeeded; the script still has a default output directory\n%s", out)
	}
	if !strings.Contains(string(out), "BIN_DIR is not defined") {
		t.Fatalf("unexpected failure: %s", out)
	}
	assertUnchanged(t, untouched)
}

// 10. The build, package and manifest steps all use the same {{.BIN_DIR}} and installer name, and the installer
// name matches installer.nsi's OutFile.
func TestPackaging_TaskfileStepsAgreeOnArtifactLocations(t *testing.T) {
	data, err := os.ReadFile(repoFile(t, "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tf := string(data)
	for _, want := range []string{
		`-o "{{.BIN_DIR}}/{{.APP_NAME}}.exe"`,
		`package-installer.ps1 -BinDir "{{.BIN_DIR}}" -AppName "{{.APP_NAME}}" -InstallerName "` + installerName + `"`,
		`-Artifacts "{{.BIN_DIR}}/{{.APP_NAME}}.exe","{{.BIN_DIR}}/` + installerName + `"`,
	} {
		if !strings.Contains(tf, want) {
			t.Errorf("Taskfile is missing %s", want)
		}
	}
	if regexp.MustCompile(`(?m)^\s*- cmd:[^\n]*makensis[^\n]*installer\.nsi`).MatchString(tf) {
		t.Error("Taskfile still invokes makensis directly (without the selected output directory)")
	}
	nsi, err := os.ReadFile(repoFile(t, "installer.nsi"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nsi), `OutFile "${BIN_DIR}\`+installerName+`"`) ||
		!strings.Contains(string(nsi), `File "/oname=GoDownloader.exe" "${BIN_DIR}\GoDownloader.exe"`) {
		t.Error("installer.nsi OutFile/File no longer use ${BIN_DIR} with the expected names")
	}
}

// helperSource answers --print-build-info like the desktop executable (it contains the flag literal the manifest
// script requires before executing anything) and is used as the packaged payload.
const helperSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--print-build-info" {
		fmt.Printf("{\"version\":\"0.9.1\",\"commit\":\"%s\",\"dirty\":false,\"platform\":\"windows/amd64\",\"configuration\":\"production\",\"commitSource\":\"injected\"}\n", os.Getenv("FAKE_COMMIT"))
	}
}
`

// 7, 9. End to end in a clean disposable git repository: package into a custom directory with spaces, write the
// manifest for the same two artifacts, and check its hashes against the files. Stale provenance is refused.
func TestPackaging_ManifestHashesTheSelectedArtifacts(t *testing.T) {
	findMakensis(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, nsi := workspace(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=packaging-test", "-c", "user.email=packaging-test@invalid", "-c", "core.autocrlf=false"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "build")
	git("commit", "-q", "-m", "fixture")
	head := git("rev-parse", "HEAD")

	custom := filepath.Join(t.TempDir(), "candidate out dir")
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(helperSource), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(custom, "GoDownloader.exe")
	build := exec.Command("go", "build", "-o", exe, "main.go")
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GO111MODULE=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build helper: %v %s", err, out)
	}
	if out, err := runPackage(t, root, nsi, custom); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}

	manifest := func(commit string) (string, error) {
		mf := filepath.Join(custom, "manifest.json")
		_ = os.Remove(mf)
		cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", repoFile(t, "write-build-manifest.ps1"),
			"-Version", "0.9.1", "-Invocation", "packaging-test", "-ProbeTimeoutSeconds", "10",
			"-Artifacts", exe+","+filepath.Join(custom, installerName), "-OutFile", mf)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "FAKE_COMMIT="+commit)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := manifest(head); err != nil {
		t.Fatalf("manifest: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(custom, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ReleaseCandidate bool `json:"releaseCandidate"`
		Artifacts        []struct {
			FileName string `json:"fileName"`
			SHA256   string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	want := map[string]string{
		"GoDownloader.exe": fileHash(t, exe),
		installerName:      fileHash(t, filepath.Join(custom, installerName)),
	}
	if len(m.Artifacts) != 2 || !m.ReleaseCandidate {
		t.Fatalf("unexpected manifest: %s", raw)
	}
	for _, a := range m.Artifacts {
		if want[a.FileName] != a.SHA256 {
			t.Fatalf("manifest hash for %s = %s, file hash = %s", a.FileName, a.SHA256, want[a.FileName])
		}
	}
	if strings.Contains(string(raw), custom) || strings.Contains(string(raw), root) {
		t.Fatal("manifest contains an absolute path")
	}

	// Stale provenance: an executable reporting another commit is refused and no manifest is written.
	time.Sleep(10 * time.Millisecond)
	if out, err := manifest("0123456789abcdef0123456789abcdef01234567"); err == nil || !strings.Contains(out, "stale or foreign build") {
		t.Fatalf("stale provenance accepted: err=%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("a manifest was written for stale provenance")
	}
}
