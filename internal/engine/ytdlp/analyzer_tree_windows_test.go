//go:build windows

package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"downloader/internal/job"
	"downloader/internal/process"
)

// H-2: media analysis must run as a supervised process tree. A PyInstaller onefile yt-dlp.exe re-executes itself as
// a child; killing only the root (the previous exec.CommandContext behaviour) orphaned that child, which kept the
// tool binary in use after the usage gate was released.

// treeHelperSource mimics the PyInstaller layout: the root spawns a child copy of itself that inherits stdout and
// stays alive. Modes: "hang" (root waits forever), "exit-json" (root prints analysis JSON and exits, leaving the
// child running), "fail" (root exits 1 leaving the child running).
const treeHelperSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	dir := os.Getenv("TREE_HELPER_DIR")
	if os.Getenv("TREE_HELPER_ROLE") == "child" {
		_ = os.WriteFile(filepath.Join(dir, "child.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(10 * time.Minute)
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "root.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), "TREE_HELPER_ROLE=child")
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: spawn child:", err)
		os.Exit(3)
	}
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	switch os.Getenv("TREE_HELPER_MODE") {
	case "exit-json":
		fmt.Println(` + "`" + `{"title":"Tree Helper","formats":[{"format_id":"18","ext":"mp4","height":360,"vcodec":"avc1","acodec":"mp4a"}]}` + "`" + `)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "ERROR: simulated extractor failure")
		os.Exit(1)
	default:
		time.Sleep(10 * time.Minute)
	}
}
`

func buildTreeHelper(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(treeHelperSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(src, "tree-helper.exe")
	cmd := exec.Command("go", "build", "-o", out, "main.go")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build tree helper: %v %s", err, b)
	}
	return out
}

type treeHarness struct {
	eng *Engine
	sup *process.Supervisor
	dir string
}

func newTreeHarness(t *testing.T, mode string) *treeHarness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TREE_HELPER_DIR", dir)
	t.Setenv("TREE_HELPER_MODE", mode)
	sup := process.NewSupervisor()
	t.Cleanup(func() { _ = sup.Shutdown(context.Background()) })
	helper := buildTreeHelper(t)
	eng := NewEngine(helper, "")
	eng.SetProcessSupervisor(sup)
	t.Cleanup(func() { reapOwnHelpers(dir, helper) })
	return &treeHarness{eng: eng, sup: sup, dir: dir}
}

// reapOwnHelpers is failure hygiene: if a regression leaves this test's own helper processes running, terminate
// exactly the PIDs the helper recorded, and only after confirming each still runs this test's helper binary.
func reapOwnHelpers(dir, helper string) {
	for _, name := range []string{"child.pid", "root.pid"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil &&
			strings.EqualFold(filepath.Clean(windows.UTF16ToString(buf[:size])), filepath.Clean(helper)) {
			_ = windows.TerminateProcess(h, 1)
		}
		_ = windows.CloseHandle(h)
	}
}

// awaitAnalysis bounds the wait for Analyze to return. An unsupervised analysis whose orphaned child keeps the
// output pipe open never returns; report that as a failure instead of hanging the test binary.
func awaitAnalysis(t *testing.T, done <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Analyze did not return within %s: the analysis process tree was not torn down", within)
		return nil
	}
}

func (h *treeHarness) waitPID(t *testing.T, name string) uint32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(h.dir, name)); err == nil && len(b) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil {
				return uint32(pid)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s not written", name)
	return 0
}

// pidAlive reports whether pid is a running process (read-only query of one specific PID).
func pidAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

func (h *treeHarness) assertTreeGone(t *testing.T, pids ...uint32) {
	t.Helper()
	for _, pid := range pids {
		if pidAlive(pid) {
			t.Fatalf("process %d of the analysis tree is still alive after Analyze returned", pid)
		}
	}
	if n := h.sup.ActiveCount(); n != 0 {
		t.Fatalf("supervisor still tracks %d process(es)", n)
	}
	if h.eng.InUse() {
		t.Fatal("usage gate still held after Analyze returned")
	}
}

func TestAnalyzeTree_CancelKillsChildBeforeGateRelease(t *testing.T) {
	h := newTreeHarness(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.eng.Analyze(ctx, "https://example.invalid/v"); done <- err }()

	root, child := h.waitPID(t, "root.pid"), h.waitPID(t, "child.pid")
	if !h.eng.InUse() {
		t.Fatal("usage gate not held during analysis")
	}
	// Observe the moment the gate is released: the child must already be dead then.
	childAliveAtRelease := make(chan bool, 1)
	go func() {
		for h.eng.InUse() {
			time.Sleep(time.Millisecond)
		}
		childAliveAtRelease <- pidAlive(child)
	}()
	cancel()
	cancel() // repeated cancellation is harmless
	err := awaitAnalysis(t, done, 30*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if <-childAliveAtRelease {
		t.Fatal("usage gate released while the analysis child process was still alive")
	}
	h.assertTreeGone(t, root, child)
}

func TestAnalyzeTree_DeadlineKillsTree(t *testing.T) {
	h := newTreeHarness(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.eng.Analyze(ctx, "https://example.invalid/v"); done <- err }()
	root, child := h.waitPID(t, "root.pid"), h.waitPID(t, "child.pid")
	err := awaitAnalysis(t, done, 30*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	h.assertTreeGone(t, root, child)
}

func TestAnalyzeTree_GracefulExitReapsLingeringChild(t *testing.T) {
	h := newTreeHarness(t, "exit-json")
	type result struct {
		info *job.MediaInfo
		err  error
	}
	res := make(chan result, 1)
	go func() { i, e := h.eng.Analyze(context.Background(), "https://example.invalid/v"); res <- result{i, e} }()
	var r result
	select {
	case r = <-res:
	case <-time.After(30 * time.Second):
		t.Fatal("Analyze did not return after the root exited: the lingering child was not torn down")
	}
	info, err := r.info, r.err
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if info.Title != "Tree Helper" || len(info.Formats) == 0 {
		t.Fatalf("unexpected media info: %+v", info)
	}
	h.assertTreeGone(t, h.waitPID(t, "root.pid"), h.waitPID(t, "child.pid"))
}

func TestAnalyzeTree_FailedExitReportsStderrAndReapsChild(t *testing.T) {
	h := newTreeHarness(t, "fail")
	done := make(chan error, 1)
	go func() { _, e := h.eng.Analyze(context.Background(), "https://example.invalid/v"); done <- e }()
	err := awaitAnalysis(t, done, 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "simulated extractor failure") {
		t.Fatalf("expected extractor failure with stderr detail, got %v", err)
	}
	h.assertTreeGone(t, h.waitPID(t, "root.pid"), h.waitPID(t, "child.pid"))
}

func TestAnalyzeTree_FailedProcessCreationReleasesGate(t *testing.T) {
	sup := process.NewSupervisor()
	defer sup.Shutdown(context.Background())
	eng := NewEngine(filepath.Join(t.TempDir(), "missing-yt-dlp.exe"), "")
	eng.SetProcessSupervisor(sup)
	if _, err := eng.Analyze(context.Background(), "https://example.invalid/v"); err == nil {
		t.Fatal("expected start failure")
	}
	if eng.InUse() || sup.ActiveCount() != 0 {
		t.Fatalf("gate in use=%v active=%d after failed start", eng.InUse(), sup.ActiveCount())
	}
}

func TestAnalyzeTree_SupervisorShutdownKillsTree(t *testing.T) {
	h := newTreeHarness(t, "hang")
	done := make(chan error, 1)
	go func() { _, err := h.eng.Analyze(context.Background(), "https://example.invalid/v"); done <- err }()
	root, child := h.waitPID(t, "root.pid"), h.waitPID(t, "child.pid")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.sup.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("supervisor shutdown: %v", err)
	}
	if err := awaitAnalysis(t, done, 30*time.Second); err == nil {
		t.Fatal("expected analysis to fail after supervisor shutdown")
	}
	h.assertTreeGone(t, root, child)
	if _, err := h.eng.Analyze(context.Background(), "https://example.invalid/v"); !errors.Is(err, process.ErrSupervisorClosed) {
		t.Fatalf("analysis after shutdown must be refused, got %v", err)
	}
}

// --- Real PyInstaller yt-dlp.exe (opt-in) -----------------------------------------------------------------------

// realYtdlpSHA256 pins the official yt-dlp 2026.10.26 Windows release asset the audit reproduced H-2 with.
const realYtdlpSHA256 = "f5389d3698ecde4ef06f4e6b1ce0294fe9ac1396228c8990b8d3416d6d177a7d"

// realYtdlp returns the opt-in official binary (GODOWNLOADER_REAL_YTDLP_EXE), verified against the pinned digest
// before anything executes it.
func realYtdlp(t *testing.T) string {
	t.Helper()
	path := os.Getenv("GODOWNLOADER_REAL_YTDLP_EXE")
	if path == "" {
		t.Skip("set GODOWNLOADER_REAL_YTDLP_EXE to the official yt-dlp 2026.10.26 yt-dlp.exe to run")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open real yt-dlp: %v", err)
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != realYtdlpSHA256 {
		t.Fatalf("real yt-dlp digest %s does not match pinned %s; refusing to execute", got, realYtdlpSHA256)
	}
	return path
}

// childrenOf lists processes whose parent is pid. Test-only, read-only observation scoped to this test's own
// analysis root; nothing is terminated by it.
func childrenOf(pid uint32) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var out []uint32
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ParentProcessID == pid && e.ProcessID != pid {
			out = append(out, e.ProcessID)
		}
	}
	return out
}

// observeRealTree records the analysis root and every descendant seen while the analysis runs.
type realTreeObserver struct {
	mu   sync.Mutex
	pids map[uint32]bool
	root uint32
}

func (o *realTreeObserver) run(sup *process.Supervisor, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		for _, info := range sup.List() {
			if info.Purpose != "analysis" || info.PID == 0 {
				continue
			}
			o.mu.Lock()
			o.root = uint32(info.PID)
			o.pids[o.root] = true
			frontier := []uint32{o.root}
			for len(frontier) > 0 {
				p := frontier[0]
				frontier = frontier[1:]
				for _, c := range childrenOf(p) {
					if !o.pids[c] {
						o.pids[c] = true
						frontier = append(frontier, c)
					}
				}
			}
			o.mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (o *realTreeObserver) snapshot() (uint32, []uint32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var all []uint32
	for p := range o.pids {
		all = append(all, p)
	}
	return o.root, all
}

func runRealAnalysis(t *testing.T, ctxFn func() (context.Context, context.CancelFunc), url string, during func(sup *process.Supervisor, obs *realTreeObserver)) (error, []uint32, *Engine, *process.Supervisor) {
	t.Helper()
	sup := process.NewSupervisor()
	t.Cleanup(func() { _ = sup.Shutdown(context.Background()) })
	eng := NewEngine(realYtdlp(t), "")
	eng.SetProcessSupervisor(sup)
	obs := &realTreeObserver{pids: map[uint32]bool{}}
	stop := make(chan struct{})
	go obs.run(sup, stop)

	ctx, cancel := ctxFn()
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := eng.Analyze(ctx, url); done <- err }()
	if during != nil {
		during(sup, obs)
	}
	err := awaitAnalysis(t, done, 30*time.Second)
	close(stop)
	_, pids := obs.snapshot()
	return err, pids, eng, sup
}

// waitRealDescendant waits until the PyInstaller root has spawned at least one descendant.
func waitRealDescendant(t *testing.T, obs *realTreeObserver) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, pids := obs.snapshot(); len(pids) >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("real yt-dlp.exe did not spawn its child process")
}

func assertRealTreeGone(t *testing.T, pids []uint32, eng *Engine, sup *process.Supervisor) {
	t.Helper()
	if len(pids) < 2 {
		t.Fatalf("expected the PyInstaller root and at least one descendant to be observed, got %v", pids)
	}
	for _, pid := range pids {
		if pidAlive(pid) {
			t.Fatalf("real yt-dlp process %d survived analysis teardown (observed tree %v)", pid, pids)
		}
	}
	if eng.InUse() || sup.ActiveCount() != 0 {
		t.Fatalf("gate in use=%v supervisor active=%d", eng.InUse(), sup.ActiveCount())
	}
}

// A blackhole address keeps the real extractor waiting on connect, without reaching any external service.
const realBlackholeURL = "http://10.255.255.1/video"

func TestRealYtdlpAnalysisTree_Cancel(t *testing.T) {
	var cancelFn context.CancelFunc
	err, pids, eng, sup := runRealAnalysis(t, func() (context.Context, context.CancelFunc) {
		ctx, c := context.WithCancel(context.Background())
		cancelFn = c
		return ctx, c
	}, realBlackholeURL, func(sup *process.Supervisor, obs *realTreeObserver) {
		waitRealDescendant(t, obs)
		cancelFn()
		cancelFn()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	assertRealTreeGone(t, pids, eng, sup)
}

func TestRealYtdlpAnalysisTree_Deadline(t *testing.T) {
	err, pids, eng, sup := runRealAnalysis(t, func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 8*time.Second)
	}, realBlackholeURL, func(sup *process.Supervisor, obs *realTreeObserver) { waitRealDescendant(t, obs) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	assertRealTreeGone(t, pids, eng, sup)
}

func TestRealYtdlpAnalysisTree_NaturalExit(t *testing.T) {
	// A refused loopback connection makes the real extractor exit on its own with an error.
	err, pids, eng, sup := runRealAnalysis(t, func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 60*time.Second)
	}, "http://127.0.0.1:1/video", nil)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("expected the process to exit with an extractor error, got %v", err)
	}
	assertRealTreeGone(t, pids, eng, sup)
}

func TestRealYtdlpAnalysisTree_SupervisorShutdown(t *testing.T) {
	err, pids, eng, sup := runRealAnalysis(t, func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}, realBlackholeURL, func(sup *process.Supervisor, obs *realTreeObserver) {
		waitRealDescendant(t, obs)
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := sup.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	if err == nil {
		t.Fatal("expected analysis to fail after supervisor shutdown")
	}
	assertRealTreeGone(t, pids, eng, sup)
}
