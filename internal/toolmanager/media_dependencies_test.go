package toolmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeTools creates placeholder executables and makes probes answer per file base name: "ok" answers with a valid
// version line, "wrong" with another program's banner, "fail" exits non-zero, "hang" outlives the probe timeout.
type fakeTools struct {
	dir     string
	answers map[string]string // base name -> behaviour
	onPath  map[string]string // tool -> file path returned by lookPath
}

func newFakeTools(t *testing.T) *fakeTools {
	return &fakeTools{dir: t.TempDir(), answers: map[string]string{}, onPath: map[string]string{}}
}

func (f *fakeTools) file(t *testing.T, sub, name, behaviour string) string {
	t.Helper()
	dir := filepath.Join(f.dir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, executableName(name))
	if err := os.WriteFile(p, []byte("placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.answers[p] = behaviour
	return p
}

func (f *fakeTools) install(m *Manager) {
	m.lookPathFn = func(file string) (string, error) {
		if p, ok := f.onPath[file]; ok {
			return p, nil
		}
		return "", fmt.Errorf("%s not on PATH", file)
	}
	m.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		tool := strings.TrimSuffix(filepath.Base(name), ".exe")
		switch f.answers[name] {
		case "ok":
			return exec.CommandContext(ctx, "cmd", "/c", "echo "+tool+" version 7.1.1-essentials Copyright (c) the FFmpeg developers")
		case "wrong":
			return exec.CommandContext(ctx, "cmd", "/c", "echo Microsoft Windows [Version 10.0]")
		case "hang":
			return exec.CommandContext(ctx, "cmd", "/c", "ping -n 6 127.0.0.1 >NUL")
		default:
			return exec.CommandContext(ctx, "cmd", "/c", "exit 3")
		}
	}
}

func mediaTestManager(t *testing.T) *Manager {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("probe fakes use cmd.exe")
	}
	return New(filepath.Join(t.TempDir(), "data"), newMockExecutionRepo())
}

func TestMediaDependencies_ConfiguredFFmpegWithSiblingFFprobe(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	ff := f.file(t, "ffmpeg-bin", "ffmpeg", "ok")
	f.file(t, "ffmpeg-bin", "ffprobe", "ok")
	f.install(m)
	m.RegisterConfig(ToolFFmpeg, ff)
	_, _ = m.Resolve(context.Background(), ToolFFmpeg) // startup resolution

	st := m.MediaDependencies(context.Background())
	if st.FFmpeg.State != DependencyAvailable || st.FFmpeg.Source != DependencySourceUserConfigured || st.FFmpeg.Version != "7.1.1-essentials" {
		t.Fatalf("ffmpeg: %+v", st.FFmpeg)
	}
	if st.FFprobe.State != DependencyAvailable || st.FFprobe.Source != DependencySourceAlongsideFFmpeg {
		t.Fatalf("ffprobe: %+v", st.FFprobe)
	}
	if st.FFmpeg.RestartRequired || st.FFmpeg.Ownership != OwnershipExternalUser {
		t.Fatalf("unexpected ffmpeg flags: %+v", st.FFmpeg)
	}
}

func TestMediaDependencies_MissingEverywhere(t *testing.T) {
	m := mediaTestManager(t)
	newFakeTools(t).install(m)
	st := m.MediaDependencies(context.Background())
	if st.FFmpeg.State != DependencyMissing || st.FFprobe.State != DependencyMissing {
		t.Fatalf("states: ffmpeg=%s ffprobe=%s", st.FFmpeg.State, st.FFprobe.State)
	}
	if !strings.Contains(st.FFmpeg.Diagnostic, "Install FFmpeg") || !strings.Contains(st.FFprobe.Diagnostic, "ships with FFmpeg") {
		t.Fatalf("diagnostics not actionable: %q / %q", st.FFmpeg.Diagnostic, st.FFprobe.Diagnostic)
	}
	for _, w := range st.Workflows {
		if w.ID == "merge_video_audio" && (w.Ready || !strings.Contains(w.Diagnostic, "FFmpeg")) {
			t.Fatalf("merge workflow must name FFmpeg as missing: %+v", w)
		}
	}
}

func TestMediaDependencies_ConfiguredMissingFallsBackToPath(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	f.onPath[ToolFFmpeg] = f.file(t, "path", "ffmpeg", "ok")
	f.file(t, "path", "ffprobe", "ok")
	f.install(m)
	m.RegisterConfig(ToolFFmpeg, filepath.Join(f.dir, "nowhere", executableName("ffmpeg")))
	_, _ = m.Resolve(context.Background(), ToolFFmpeg) // startup: the engine falls back to the PATH FFmpeg

	st := m.MediaDependencies(context.Background())
	if st.FFmpeg.State != DependencyAvailable || st.FFmpeg.Source != DependencySourceSystemPath || st.FFmpeg.RestartRequired {
		t.Fatalf("ffmpeg: %+v", st.FFmpeg)
	}
	if !strings.Contains(st.FFmpeg.Diagnostic, "configured FFmpeg could not be used") {
		t.Fatalf("configured-path problem not reported: %q", st.FFmpeg.Diagnostic)
	}
	if st.FFprobe.Source != DependencySourceAlongsideFFmpeg || !st.FFprobe.Available {
		t.Fatalf("ffprobe: %+v", st.FFprobe)
	}
}

// Finding #3: with --ffmpeg-location, yt-dlp looks for ffprobe only next to that FFmpeg. An FFprobe elsewhere on PATH
// does not help, so it must not be reported as available (and workflows needing it are not ready).
func TestMediaDependencies_FFprobeOnlyNextToEngineFFmpeg(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	ff := f.file(t, "ffmpeg-only", "ffmpeg", "ok")
	f.onPath[ToolFFprobe] = f.file(t, "elsewhere", "ffprobe", "ok")
	f.install(m)
	m.RegisterConfig(ToolFFmpeg, ff)
	m.SetEngineFFmpegPath(ff)

	st := m.MediaDependencies(context.Background())
	if st.FFprobe.Available || st.FFprobe.State != DependencyMissing || !strings.Contains(st.FFprobe.Diagnostic, "only in that folder") {
		t.Fatalf("ffprobe on PATH must not count when the engine pins FFmpeg's folder: %+v", st.FFprobe)
	}
	for _, w := range st.Workflows {
		if w.ID == "merge_video_audio" && w.Ready {
			t.Fatal("merge workflow ready although yt-dlp cannot find FFprobe")
		}
	}

	// Without --ffmpeg-location (no FFmpeg at startup), yt-dlp searches PATH, so the PATH FFprobe counts.
	m2 := mediaTestManager(t)
	f.install(m2)
	m2.SetEngineFFmpegPath("")
	if st := m2.MediaDependencies(context.Background()); !st.FFprobe.Available || st.FFprobe.Source != DependencySourceSystemPath {
		t.Fatalf("PATH ffprobe must count when yt-dlp searches PATH: %+v", st.FFprobe)
	}
}

// Finding #4: the engine is pinned to FFmpeg A; A disappears and PATH now has B. A restart is required (the running
// engine still passes A), and workflows are not ready until then.
func TestMediaDependencies_EnginePinnedToGoneFFmpegRequiresRestart(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	gone := filepath.Join(f.dir, "removed", executableName("ffmpeg"))
	f.onPath[ToolFFmpeg] = f.file(t, "b", "ffmpeg", "ok")
	f.file(t, "b", "ffprobe", "ok")
	f.install(m)
	m.SetEngineFFmpegPath(gone)

	st := m.MediaDependencies(context.Background())
	if !st.FFmpeg.Available || !st.FFmpeg.RestartRequired || !strings.Contains(st.FFmpeg.Diagnostic, "restart GoDownloader") {
		t.Fatalf("restart requirement not reported: %+v", st.FFmpeg)
	}
	for _, w := range st.Workflows {
		if (w.ID == "merge_video_audio" || w.ID == "subtitle_postprocessing") && w.Ready {
			t.Fatalf("%s ready although the running engine's FFmpeg is gone", w.ID)
		}
	}
}

// Finding #8: the engine accepts any configured executable whose version probe runs. If that executable is not
// FFmpeg, the diagnostic reports it as the engine's (invalid) choice, not as "PATH used instead".
func TestMediaDependencies_ConfiguredNonFFmpegIsEnginesChoice(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	wrong := f.file(t, "cfg", "ffmpeg", "wrong")
	f.onPath[ToolFFmpeg] = f.file(t, "path", "ffmpeg", "ok")
	f.install(m)
	m.RegisterConfig(ToolFFmpeg, wrong)
	m.SetEngineFFmpegPath(wrong)

	st := m.MediaDependencies(context.Background())
	if st.FFmpeg.Available || st.FFmpeg.State != DependencyInvalid || st.FFmpeg.Source != DependencySourceUserConfigured {
		t.Fatalf("got %+v", st.FFmpeg)
	}
	if st.FFmpeg.RestartRequired || strings.Contains(st.FFmpeg.Diagnostic, "PATH is used instead") {
		t.Fatalf("misleading fallback/restart claim: %+v", st.FFmpeg)
	}
}

func TestMediaDependencies_InvalidAndFailedProbes(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, f *fakeTools) string // returns configured path
		wantState string
		wantDiag  string
	}{
		{"directory instead of file", func(t *testing.T, f *fakeTools) string {
			d := filepath.Join(f.dir, "ffmpeg-dir")
			_ = os.MkdirAll(d, 0o755)
			return d
		}, DependencyInvalid, "not an executable file"},
		{"other program", func(t *testing.T, f *fakeTools) string { return f.file(t, "w", "ffmpeg", "wrong") },
			DependencyInvalid, "does not identify itself as FFmpeg"},
		{"cannot run", func(t *testing.T, f *fakeTools) string { return f.file(t, "x", "ffmpeg", "fail") },
			DependencyInvalid, "could not be run"},
		{"probe timeout", func(t *testing.T, f *fakeTools) string { return f.file(t, "h", "ffmpeg", "hang") },
			DependencyProbeFailed, "did not answer a version check"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mediaTestManager(t)
			m.probeTimeout = 500 * time.Millisecond
			f := newFakeTools(t)
			cfg := c.setup(t, f)
			f.install(m)
			m.RegisterConfig(ToolFFmpeg, cfg)
			st := m.MediaDependencies(context.Background())
			if st.FFmpeg.State != c.wantState || !strings.Contains(st.FFmpeg.Diagnostic, c.wantDiag) || st.FFmpeg.Available {
				t.Fatalf("got %+v", st.FFmpeg)
			}
		})
	}
}

// FFmpeg that appears on the inherited PATH after startup needs no restart: the engine passes no --ffmpeg-location
// when it started without FFmpeg, so each yt-dlp run searches PATH itself.
func TestMediaDependencies_FFmpegOnPathAfterStartupNeedsNoRestart(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	f.install(m)
	m.SetEngineFFmpegPath("") // startup: no FFmpeg

	f.onPath[ToolFFmpeg] = f.file(t, "later", "ffmpeg", "ok")
	f.onPath[ToolFFprobe] = f.file(t, "later", "ffprobe", "ok")
	st := m.MediaDependencies(context.Background())
	if !st.FFmpeg.Available || st.FFmpeg.RestartRequired {
		t.Fatalf("PATH FFmpeg is usable without a restart: %+v", st.FFmpeg)
	}
}

// A configured FFmpeg that only works after startup does need a restart: FFMPEG_PATH is read at startup, and without
// --ffmpeg-location yt-dlp would not find it.
func TestMediaDependencies_ConfiguredFFmpegAfterStartupRequiresRestart(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	f.install(m)
	m.SetEngineFFmpegPath("") // startup: configured FFmpeg was not usable
	ff := f.file(t, "cfg", "ffmpeg", "ok")
	f.file(t, "cfg", "ffprobe", "ok")
	m.RegisterConfig(ToolFFmpeg, ff)

	st := m.MediaDependencies(context.Background())
	if !st.FFmpeg.Available || !st.FFmpeg.RestartRequired || !strings.Contains(st.FFmpeg.Diagnostic, "restart GoDownloader") {
		t.Fatalf("restart requirement not reported: %+v", st.FFmpeg)
	}
	for _, w := range st.Workflows {
		if w.ID == "merge_video_audio" && w.Ready {
			t.Fatal("merge workflow reported ready although the running engine has no FFmpeg")
		}
	}
}

func TestMediaDependencies_JavaScriptRuntime(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	f.install(m)
	if st := m.MediaDependencies(context.Background()); st.JavaScriptRuntime.Detected || !strings.Contains(st.JavaScriptRuntime.Diagnostic, "does not install one") {
		t.Fatalf("absent deno: %+v", st.JavaScriptRuntime)
	}
	f.onPath["deno"] = f.file(t, "deno", "deno", "ok")
	if st := m.MediaDependencies(context.Background()); !st.JavaScriptRuntime.Detected || st.JavaScriptRuntime.Runtime != "deno" {
		t.Fatalf("deno not detected: %+v", st.JavaScriptRuntime)
	}
}

func TestMediaWorkflows_Readiness(t *testing.T) {
	ready := MediaDependencyStatus{
		Ytdlp:             ManagedToolStatus{Available: true},
		FFmpeg:            ExternalDependencyStatus{Available: true},
		FFprobe:           ExternalDependencyStatus{Available: true},
		JavaScriptRuntime: JavaScriptRuntimeStatus{Detected: true},
	}
	for _, w := range mediaWorkflows(ready) {
		if !w.Ready || w.Diagnostic != "" {
			t.Fatalf("all tools ready but %s not ready: %+v", w.ID, w)
		}
	}
	noProbe := ready
	noProbe.FFprobe.Available = false
	noYtdlp := ready
	noYtdlp.Ytdlp.RestartRequired = true
	for _, w := range mediaWorkflows(noProbe) {
		if w.ID == "media_analysis" && !w.Ready {
			t.Fatal("analysis does not need FFprobe")
		}
		if w.ID == "subtitle_postprocessing" && (w.Ready || !strings.Contains(w.Diagnostic, "FFprobe")) {
			t.Fatalf("subtitle post-processing needs FFprobe: %+v", w)
		}
	}
	for _, w := range mediaWorkflows(noYtdlp) {
		if w.Ready && w.ID != "" {
			t.Fatalf("yt-dlp restart pending, %s must not be ready", w.ID)
		}
	}
}

// The status is shown in the UI and may be exported: it never contains executable paths.
func TestMediaDependencies_PathFree(t *testing.T) {
	m := mediaTestManager(t)
	f := newFakeTools(t)
	ff := f.file(t, "secret-location", "ffmpeg", "ok")
	f.file(t, "secret-location", "ffprobe", "wrong")
	f.install(m)
	m.RegisterConfig(ToolFFmpeg, ff)
	data, err := json.Marshal(m.MediaDependencies(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-location") || strings.Contains(string(data), f.dir) {
		t.Fatalf("status leaks a path: %s", data)
	}
}

// The workflow diagnostic lists every missing tool in a grammatical sentence, whatever the number missing.
func TestMissingRequirements_Wording(t *testing.T) {
	y, f, p := requirement{false, "yt-dlp"}, requirement{false, "FFmpeg"}, requirement{false, "FFprobe"}
	ready := func(r requirement) requirement { r.ok = true; return r }
	cases := []struct {
		name string
		reqs []requirement
		want string
	}{
		{"all ready", []requirement{ready(y), ready(f), ready(p)}, ""},
		{"one missing", []requirement{ready(y), f, ready(p)}, "Unavailable until FFmpeg is ready."},
		{"two missing", []requirement{y, f, ready(p)}, "Unavailable until yt-dlp and FFmpeg are ready."},
		{"three missing", []requirement{y, f, p}, "Unavailable until yt-dlp, FFmpeg, and FFprobe are ready."},
		{"single requirement missing", []requirement{y}, "Unavailable until yt-dlp is ready."},
		{"no requirements", nil, ""},
	}
	for _, c := range cases {
		if got := missingRequirements(c.reqs...); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The clean-machine case observed in Windows Sandbox acceptance (D-2): nothing installed.
func TestMediaWorkflows_NothingInstalledWording(t *testing.T) {
	var st MediaDependencyStatus
	got := map[string]string{}
	for _, w := range mediaWorkflows(st) {
		got[w.ID] = w.Diagnostic
	}
	want := map[string]string{
		"media_analysis":          "Unavailable until yt-dlp is ready.",
		"merge_video_audio":       "Unavailable until yt-dlp, FFmpeg, and FFprobe are ready.",
		"subtitle_postprocessing": "Unavailable until yt-dlp, FFmpeg, and FFprobe are ready.",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %q, want %q", id, got[id], w)
		}
	}
	for id, d := range got {
		if strings.Contains(d, " and FFmpeg and ") || strings.Contains(d, "FFprobe is ready") {
			t.Errorf("%s: ungrammatical diagnostic %q", id, d)
		}
	}
}
