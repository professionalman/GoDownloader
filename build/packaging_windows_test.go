//go:build windows

package build

// Packaging path-safety regression tests. Every test works in disposable temporary directories with a copy of the
// NSIS script, so the repository's own bin\ (and any existing executable or installer) is never read or written.
// Tests that need NSIS skip when makensis is not installed.

import (
	"crypto/rand"
	"crypto/sha256"
	"debug/pe"
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
// script requires before executing anything) and is used as the packaged payload. When PROBE_MARKER is set it also
// records that it was executed.
const helperSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if marker := os.Getenv("PROBE_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("executed"), 0o644)
	}
	if len(os.Args) > 1 && os.Args[1] == "--print-build-info" {
		fmt.Printf("{\"version\":\"0.9.1\",\"commit\":\"%s\",\"dirty\":false,\"platform\":\"windows/amd64\",\"configuration\":\"production\",\"commitSource\":\"injected\"}\n", os.Getenv("FAKE_COMMIT"))
	}
}
`

// buildHelper compiles helperSource to exe with the given -ldflags ("" for none). "-H windowsgui" links it for the
// Windows GUI subsystem like the production desktop executable; without it Go links a console program.
func buildHelper(t *testing.T, exe, ldflags string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(helperSource), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"build", "-o", exe}
	if ldflags != "" {
		args = append(args, "-ldflags="+ldflags)
	}
	build := exec.Command("go", append(args, "main.go")...)
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GO111MODULE=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cannot build helper: %v %s", err, out)
	}
}

// peSubsystem reads the Subsystem field of a compiled executable's PE optional header with debug/pe.
func peSubsystem(t *testing.T, path string) uint16 {
	t.Helper()
	f, err := pe.Open(path)
	if err != nil {
		t.Fatalf("%s is not a PE image: %v", filepath.Base(path), err)
	}
	defer f.Close()
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return oh.Subsystem
	case *pe.OptionalHeader32:
		return oh.Subsystem
	}
	t.Fatalf("%s has no optional header", filepath.Base(path))
	return 0
}

// runManifest runs write-build-manifest.ps1 from cwd for the given artifacts and extra environment.
func runManifest(t *testing.T, cwd, outFile string, env []string, artifacts ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", repoFile(t, "write-build-manifest.ps1"),
		"-Version", "0.9.1", "-Invocation", "packaging-test", "-ProbeTimeoutSeconds", "10",
		"-Artifacts", strings.Join(artifacts, ","), "-OutFile", outFile)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// productionLdflags returns the -ldflags value of the Taskfile's production (non-DEV) desktop build, with the
// template variables filled in.
func productionLdflags(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(repoFile(t, "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`BUILD_FLAGS: '\{\{if eq \.DEV "true"\}\}[^\n]*?\{\{else\}\}([^\n]*?)\{\{end\}\}'`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("production BUILD_FLAGS not found in Taskfile.yml")
	}
	ld := regexp.MustCompile(`-ldflags="([^"]*)"`).FindStringSubmatch(m[1])
	if ld == nil {
		t.Fatalf("production BUILD_FLAGS has no -ldflags: %s", m[1])
	}
	flags := strings.NewReplacer("{{.GIT_COMMIT}}", strings.Repeat("0", 40), "{{.GIT_MODIFIED}}", "false").Replace(ld[1])
	if strings.Contains(flags, "{{") {
		t.Fatalf("unexpected template variable in production ldflags: %s", flags)
	}
	return flags
}

// The production linker flags, applied to a real compiled executable, produce a Windows GUI-subsystem image (no
// console window). Removing -H windowsgui from the Taskfile makes this fail with subsystem 3 (console).
func TestPackaging_ProductionLinkerFlagsProduceGUISubsystem(t *testing.T) {
	flags := productionLdflags(t)
	for _, keep := range []string{"-w", "-s", "buildinfo.Version=0.9.1", "buildinfo.Commit=", "buildinfo.Modified=", "buildinfo.Configuration=production"} {
		if !strings.Contains(flags, keep) {
			t.Errorf("production ldflags lost %q: %s", keep, flags)
		}
	}
	exe := filepath.Join(t.TempDir(), "GoDownloader.exe")
	buildHelper(t, exe, flags)
	if got := peSubsystem(t, exe); got != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatalf("production linker flags produce PE subsystem %d, want %d (WINDOWS_GUI); ldflags: %s", got, pe.IMAGE_SUBSYSTEM_WINDOWS_GUI, flags)
	}
}

// A console-subsystem executable can never become a release candidate: the manifest gate refuses it from its PE
// header alone, before executing it, and writes no manifest.
func TestPackaging_ManifestRefusesConsoleSubsystemExecutable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "candidate out dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "GoDownloader.exe")
	buildHelper(t, exe, "-w -s")
	if got := peSubsystem(t, exe); got != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Fatalf("fixture is subsystem %d, want a console (3) build", got)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	mf := filepath.Join(dir, "manifest.json")
	out, err := runManifest(t, t.TempDir(), mf, []string{"PROBE_MARKER=" + marker, "FAKE_COMMIT=" + strings.Repeat("a", 40)}, exe)
	if err == nil || !strings.Contains(out, "is not a Windows GUI executable (PE subsystem 3") {
		t.Fatalf("console-subsystem executable accepted: err=%v\n%s", err, out)
	}
	if _, statErr := os.Stat(mf); !os.IsNotExist(statErr) {
		t.Fatal("a manifest was written for a console-subsystem executable")
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("the console-subsystem executable was executed before being refused")
	}
}

// A file that is not a well-formed PE image is refused by the header check (and never executed).
func TestPackaging_ManifestRefusesInvalidExecutable(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"not-pe":    []byte(strings.Repeat("not a PE image --print-build-info ", 64)),
		"truncated": append([]byte("MZ"), make([]byte, 30)...),
		"bad-lfanew": func() []byte {
			b := make([]byte, 256)
			b[0], b[1] = 'M', 'Z'
			b[0x3C], b[0x3D], b[0x3E], b[0x3F] = 0xFF, 0xFF, 0xFF, 0x7F // e_lfanew far past the end of the file
			return b
		}(),
	}
	for name, content := range cases {
		exe := filepath.Join(dir, name, "GoDownloader.exe")
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, content, 0o755); err != nil {
			t.Fatal(err)
		}
		mf := filepath.Join(dir, name, "manifest.json")
		out, err := runManifest(t, dir, mf, nil, exe)
		if err == nil || !strings.Contains(out, "is not a valid PE executable") {
			t.Errorf("%s: invalid executable accepted: err=%v\n%s", name, err, out)
		}
		if _, statErr := os.Stat(mf); !os.IsNotExist(statErr) {
			t.Errorf("%s: a manifest was written", name)
		}
	}
}

// A GUI-subsystem executable's --print-build-info output is still captured by the manifest probe (redirected
// standard output), parsed, and checked against HEAD. Runs without NSIS.
func TestPackaging_ManifestCapturesBuildInfoFromGUIExecutable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
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
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "README")
	git("commit", "-q", "-m", "fixture")
	head := git("rev-parse", "HEAD")

	dir := filepath.Join(t.TempDir(), "candidate out dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "GoDownloader.exe")
	buildHelper(t, exe, "-w -s -H windowsgui")
	if got := peSubsystem(t, exe); got != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatalf("fixture is subsystem %d, want GUI (2)", got)
	}
	mf := filepath.Join(dir, "manifest.json")
	if out, err := runManifest(t, root, mf, []string{"FAKE_COMMIT=" + head}, exe); err != nil {
		t.Fatalf("manifest for a GUI-subsystem executable: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ReleaseCandidate bool `json:"releaseCandidate"`
		Source           struct {
			EmbeddedBuildInfo struct {
				Version       string `json:"version"`
				Commit        string `json:"commit"`
				Dirty         bool   `json:"dirty"`
				Configuration string `json:"configuration"`
			} `json:"embeddedBuildInfo"`
			EmbeddedCommitMatchesHead bool `json:"embeddedCommitMatchesHead"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest JSON: %v\n%s", err, raw)
	}
	bi := m.Source.EmbeddedBuildInfo
	if !m.ReleaseCandidate || !m.Source.EmbeddedCommitMatchesHead || bi.Version != "0.9.1" || bi.Commit != head || bi.Dirty || bi.Configuration != "production" {
		t.Fatalf("build info not captured from the GUI-subsystem executable: %s", raw)
	}
}

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
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(custom, "GoDownloader.exe")
	buildHelper(t, exe, "-H windowsgui") // GUI subsystem, like the production desktop executable
	if out, err := runPackage(t, root, nsi, custom); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}

	manifest := func(commit string) (string, error) {
		mf := filepath.Join(custom, "manifest.json")
		_ = os.Remove(mf)
		return runManifest(t, root, mf, []string{"FAKE_COMMIT=" + commit}, exe, filepath.Join(custom, installerName))
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
