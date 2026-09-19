# GoDownloader — SPK-SYN3-01: Wails v3 Desktop Viability Spike
# Windows Desktop Host Evaluation & Architecture Verification Report

- **Document:** `docs/implementation/SPK-SYN3-01_WAILS_V3_VIABILITY.md`
- **Spike ID:** `SPK-SYN3-01`
- **Milestone:** V0.9.0 Windows Desktop Technical Preview — Architecture Viability Spike
- **Starting Baseline / HEAD:** `8e5f63dafb393a5a08edb90626d63899e93854c6` (`docs(v0.9): record desktop entry and Wails spike plan`)
- **Spike Directory:** `spikes/spk-syn3-01-wails-v3/`
- **Spike Branch:** `spike/spk-syn3-01-wails-v3`
- **Pinned Wails v3 Version:** `v3.0.0-beta.23` (GitHub release 2026-09-16)
- **Tauri v2:** NOT EVALUATED / FALLBACK ONLY
- **Status:** **FINAL EVIDENCE GATE COMPLETE — ALL CRITERIA VERIFIED**
- **Outcome Standard:** **WAILS ACCEPTED WITH NON-BLOCKING LIMITATIONS**
- **SPK-SYN3-01 Status:** **COMPLETE / LOCKED**

---

## 1. Executive Summary & Verdict

### Final Verdict: WAILS ACCEPTED WITH NON-BLOCKING LIMITATIONS

- **Desktop Framework:** Wails v3
- **Evaluated Pinned Version:** `v3.0.0-beta.23`
- **Tauri v2:** NOT EVALUATED / FALLBACK ONLY
- **SPK-SYN3-01:** COMPLETE / LOCKED

Architecture spike `SPK-SYN3-01` has evaluated **Wails v3** (`v3.0.0-beta.23`) as the Windows desktop application host for GoDownloader across all **22 criteria (A through V)** defined in the architectural specification.

All architecture-critical criteria have **PASSED** with direct machine-readable evidence:
- Direct in-process IPC bindings with auto-generated TypeScript interfaces.
- Zero listening localhost TCP ports (REST and SSE eliminated for desktop UI).
- Single-instance mutual exclusion: secondary process exits cleanly with code 0 inside `application.New()`, forwards arguments via IPC, and initializes zero duplicate backend core.
- Descendant child process containment via Win32 Job Objects (`KILL_ON_JOB_CLOSE`) supervised by `internal/process.ProcessSupervisor`.
- Self-terminating explicit Quit lifecycle: desktop host terminates its own process and its supervised process tree without external intervention (`Stop-Process` was not used).
- OS-level secret management via `internal/securestore.WindowsCredentialManagerProvider` under standard user credentials.
- Deterministic data root (`%APPDATA%\GoDownloader`) independent of invocation working directory.

Only two non-blocking limitations were noted (Criterion J: default system tray styling, and Criterion Q: development host WebView2 packaging considerations). No hard architectural blockers exist. The fallback framework (Tauri v2) is **not required** and has not been installed.

---

## 2. Complete Acceptance Matrix (Criteria A through V)

The original spike specification defined 22 criteria (A through V). The table below records the verified status and evidence for every criterion:

| Criterion | Category | Status | Evidence & Verification Summary |
|:---:|---|:---:|---|
| **A** | Pinned Wails Build | **PASS** | Built via `wails3 task build` on Windows 11 with Go 1.26.3. Produced stripped desktop binary `bin/spk-syn3-01-wails-v3.exe` (10.69 MB, `CGO_ENABLED=0`, exit code 0). |
| **B** | React/Vite Assets | **PASS** | Spike React frontend compiled with `tsc && vite build` in 136ms; embedded into Go binary via `embed.FS` and served internally via custom URI schemes without HTTP static server. |
| **C** | No Required REST/SSE | **PASS** | Process inspection via `Get-NetTCPConnection -OwningProcess <PID> -State Listen` confirmed **zero listening TCP ports** owned by desktop host. Localhost port 8080 eliminated. |
| **D** | Typed Bindings | **PASS** | `wails3 generate bindings -ts -i` scanned packages and generated strongly typed TypeScript interfaces in 3.68s with zero manual IPC glue code. |
| **E** | Native Events | **PASS** | Real-time in-process event streaming via `app.Event.Emit` and `@wailsio/runtime` `Events.On`. Dispatched 100 rapid events in under 15ms. |
| **F** | StateSync Semantics | **PASS** | Monotonic sequence cursor progression (`cursor: 100 -> 101`), authoritative snapshot hydration, and typed state modification verified in-process. |
| **G** | WebView Reload / One Core | **PASS** | Backend boot instance ID (`core-1789844282088-e5c9`) persists across webview reloads (`window.location.reload()`). Core initialization log confirms exactly 1 core construction. |
| **H** | Native Single Instance | **PASS** | `application.SingleInstanceOptions` enforces mutual exclusion. Secondary process exits with code 0 in 66ms. Core initialization log confirmed zero duplicate core construction. |
| **I** | Second-Launch Args | **PASS** | Forwarded CLI args (`--second-instance-proof=0c6d2191-761a-474e-bf30-e70303727159`, magnet link) and CWD received by primary instance via `OnSecondInstanceLaunch`. Window restored and focused. |
| **J** | System Tray | **PASS WITH NON-BLOCKING LIMITATION** | System tray created via `app.SystemTray.New()` with dark/light icons from `wails/v3/pkg/icons`, tooltip, click-to-restore, and minimal menu (`Show`, `Quit`). Advanced custom tray features deferred to V1.0. |
| **K** | Intercepted Close / Background | **PASS** | Window close intercepted via `events.Common.WindowClosing` hook; calls `e.Cancel()` and `mainWindow.Hide()`. Backend and supervised child processes continue running while window is hidden. |
| **L** | Explicit Quit Lifecycle | **PASS** | Explicit Quit action initiates orderly shutdown coordinator: shuts down `ProcessSupervisor`, waits for completion, calls `app.Quit()`. Desktop host terminates itself cleanly with exit code 0. |
| **M** | ProcessSupervisor Desktop Compatibility | **PASS** | Real `internal/process.ProcessSupervisor` executed in desktop host. Supervised `bin/helper.exe` root process (PID 8588) and child process (PID 11440) assigned to Win32 Job Object (`KILL_ON_JOB_CLOSE`). |
| **N** | Quit Terminates Process Tree | **PASS** | Upon explicit Quit, `ProcessSupervisor.Shutdown(ctx)` closed Job Object handles. Root PID 8588 and child PID 11440 terminated cleanly with zero orphan leakage. `Stop-Process` was not used. |
| **O** | Credential Manager | **PASS** | Real `internal/securestore.WindowsCredentialManagerProvider` executed inside desktop host. 32-byte key write (`CredWriteW`), read-back bit-for-bit equality, and idempotent deletion (`CredDeleteW`) confirmed. |
| **P** | Deterministic Data Root | **PASS** | Decoupled from CWD. Resolves deterministically to `%APPDATA%\GoDownloader` when launched from repository root and when launched from `$env:TEMP`. |
| **Q** | WebView2 Runtime | **PASS WITH NON-BLOCKING LIMITATION** | Evergreen Runtime 153.0.4234.32 verified on development host. NSIS bootstrapper generation supported by `wails3 generate webview2bootstrapper` (documented as future packaging milestone DSK-4). |
| **R** | Existing Frontend Compatibility | **PASS** | Existing GoDownloader frontend under `/web` passed all unit tests (`npm test`: 19/19 files, 186/186 passed) and built cleanly (`npm run build` in 1.14s). Generated static assets (`web/dist`) are compatible with Wails AssetServer. |
| **S** | Representative DTO Bindings | **PASS** | TypeScript interfaces generated for string IDs, `time.Time` (string), nullable pointers (`*time.Time`, `*string`), numeric types (`int64`, `float64`), and string slices without domain model modifications. |
| **T** | Go Error Propagation | **PASS** | `TriggerError()` returning Go `error` translates to rejected `CancellablePromise<void>` in TypeScript; error message string is readable directly in `.catch((err) => ...)`. |
| **U** | Event Listener Reload Safety | **PASS** | WebView2 reloads discard the previous JavaScript VM context, tearing down old listeners. React `useEffect` cleanup unregisters listeners. Event delivery verified at exactly 1 delivery per event across reloads. |
| **V** | No Production Source Changes | **PASS** | Zero lines of production code in root `internal/` or `cmd/` were modified. Root `go.mod`, `go.sum`, and `package.json` remain untouched. |

### Acceptance Matrix Accounting
- **Total Criteria Evaluated:** 22 (Criteria A through V)
- **PASS (20 criteria):**
  - **A** — Pinned Wails build
  - **B** — React/Vite assets
  - **C** — No required REST/SSE
  - **D** — Typed bindings
  - **E** — Native events
  - **F** — StateSync semantics
  - **G** — WebView reload / one backend core
  - **H** — Native single instance
  - **I** — Second-launch args
  - **K** — Intercepted close/background
  - **L** — Explicit Quit lifecycle
  - **M** — ProcessSupervisor desktop compatibility
  - **N** — Quit terminates process tree
  - **O** — Credential Manager
  - **P** — Deterministic data root
  - **R** — Existing frontend compatibility
  - **S** — Representative DTO bindings
  - **T** — Go error propagation
  - **U** — Event-listener multiplication/reload safety
  - **V** — No production source changes required
- **PASS WITH NON-BLOCKING LIMITATION (2 criteria):**
  - **J** — System tray (basic tray verified; animated badges / custom draw hooks deferred)
  - **Q** — WebView2 (Evergreen runtime verified; standalone NSIS installer generation deferred to DSK-4)
- **FAIL (0 criteria):** None
- **UNVERIFIED (0 criteria):** None

---

## 3. Environment & Toolchain Verification

- **Host OS:** Windows 11 Pro 64-bit (Build 26200.5601)
- **Go Version:** `go1.26.3 windows/amd64`
- **Node.js:** `v24.4.1`, npm: `11.19.0`
- **WebView2 Runtime:** `153.0.4234.32` (Installed Evergreen Runtime)
- **Pinned Wails Version:** `github.com/wailsapp/wails/v3@v3.0.0-beta.23`
- **Tooling Readiness:** `wails3 doctor` confirmed system readiness.

---

## 4. Production Build & Tooling Proof

The production build was executed using the supported Wails v3 Taskfile task rather than a plain `go build`:

```powershell
wails3 task build
```

### Build Execution Trace
1. `windows:common:go:mod:tidy`: Tidy module dependencies.
2. `windows:common:generate:icons`: Generated Windows `.ico` and macOS `.icns`.
3. `windows:common:generate:bindings`: Generated TypeScript interfaces with flags `-f '-tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui"' -clean=true -ts -i`.
4. `windows:common:install:frontend:deps:npm`: Verified npm dependencies.
5. `windows:common:frontend:run:npm`: Built frontend production assets with Vite in 136ms.
6. `windows:generate:syso`: Generated Windows resource file `wails_windows_amd64.syso` (icon, manifest, version info).
7. `windows:build:native`: Compiled production binary with `go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui" -o "bin/spk-syn3-01-wails-v3.exe"`.
8. Removed temporary `.syso` file.

### Production Artifact Details
- **Command:** `wails3 task build`
- **Output Binary:** `spikes/spk-syn3-01-wails-v3/bin/spk-syn3-01-wails-v3.exe`
- **Binary Size:** 10,691,584 bytes (10.69 MB)
- **Subsystem:** Windows GUI (`-H windowsgui`)
- **Exit Code:** 0

---

## 5. Existing Frontend (`/web`) Compatibility Proof

The spike clearly distinguishes the spike-local test frontend (`spikes/spk-syn3-01-wails-v3/frontend`) from the existing production frontend (`web/`):

1. **Existing Frontend Tests:**
   - Command: `npm test` in `web/`
   - Result: **19 test files passed (19), 186 tests passed (186), 0 failures** (Duration: 29.32s).
2. **Existing Frontend Build:**
   - Command: `npm run build` in `web/`
   - Result: `tsc -b && vite build` completed in **1.14s**.
   - Output Artifacts:
     - `web/dist/index.html` (0.48 kB)
     - `web/dist/assets/index-BA734eJ1.css` (60.35 kB)
     - `web/dist/assets/index-BpXaAFoN.js` (354.34 kB)
3. **Wails Compatibility Verification:**
   - The compiled `web/dist` bundle consists of standard static HTML/CSS/JS assets.
   - It is directly hostable by Wails `application.AssetFileServerFS` or `embed.FS`.
   - In V0.9.0 milestone `DSK-3`, `web/src/api.ts` will provide a unified native IPC transport adapter with automatic fallback to REST for headless web mode.

---

## 6. Live Process & Lifecycle Evidence

A live deterministic verification run was executed against the production-built binary (`spk-syn3-01-wails-v3.exe`). All evidence files were captured machine-readably in `evidence/` and `%APPDATA%\GoDownloader`:

### A. Process Startup & Network Isolation
- **Primary Process ID:** `11768`
- **Listening TCP Sockets:** `None (Zero TCP ports)` confirmed via `Get-NetTCPConnection -OwningProcess 11768 -State Listen`.
- **Go Host Working Set RAM:** 67.56 MB.
- **Child Processes:**
  - `msedgewebview2.exe` (PID 8676): 146.84 MB
  - `helper.exe` (PID 8588): 6.43 MB
- **Total Combined Working Set:** 220.83 MB.

### B. Machine-Readable Single-Instance Evidence
- **Log File:** `C:\Users\bkkav\AppData\Roaming\GoDownloader\core-init.log`
- **Primary Initialization Line:**
  ```text
  core-init 11768 core-1789844282088-e5c9
  ```
- **Secondary Instance Invocation:**
  - Command: `spk-syn3-01-wails-v3.exe --second-instance-proof=0c6d2191-761a-474e-bf30-e70303727159 magnet:?xt=urn:btih:wails3test`
  - Secondary PID: `16132`
  - Secondary Exit Code: `0` (exited in 66ms)
- **Mutual Exclusion Proof:**
  - `core-init.log` was inspected after secondary process termination.
  - Entry count was verified to remain **EXACTLY 1**.
  - The secondary process exited inside `application.New()`, proving that the secondary process never constructed the GoDownloader application core or repositories.
- **Argument Forwarding Proof:**
  - Machine-readable evidence written by primary instance to `evidence/second-instance.json`:
    ```json
    {
      "instanceId": "core-1789844282088-e5c9",
      "primaryPid": 11768,
      "secondLaunchCount": 2,
      "proofToken": "0c6d2191-761a-474e-bf30-e70303727159",
      "lastForwardedCwd": "C:\\Users\\bkkav\\Downloads\\downloader"
    }
    ```

### C. ProcessSupervisor & Win32 Job Object Containment
- **Supervised Helper:** Launched via `internal/process.ProcessSupervisor.StartOwned` using `spikes/spk-syn3-01-wails-v3/bin/helper.exe`.
- **Process Hierarchy:**
  - Desktop Host: PID `11768`
  - Helper Root: PID `8588`
  - Helper Child: PID `11440`
- **Containment State:** Verified via `evidence/helper-pids.json`:
  ```json
  {
    "desktopPid": 11768,
    "rootPid": 8588,
    "childPid": 11440,
    "jobObjectActive": true,
    "bothRunning": true
  }
  ```
- Both PIDs confirmed simultaneously active under the Windows Job Object.

### D. Close-to-Tray vs. Explicit Quit Distinction
- **Close-to-Tray Test:** Window close hook cancels default destruction (`e.Cancel()`) and hides the window (`mainWindow.Hide()`). The primary process (PID 11768), helper root (PID 8588), and helper child (PID 11440) remained active in the background.
- **Explicit Quit Execution:**
  - Secondary instance invoked with `--trigger-quit`.
  - Primary instance `RecordSecondLaunch` received the command and initiated orderly shutdown:
    1. Recorded `evidence/shutdown-started.log`: `shutdown-started 11768 <timestamp>`.
    2. Invoked `s.supervisor.Shutdown(ctx)`, which closed the Job Object handle with `KILL_ON_JOB_CLOSE`.
    3. Recorded `evidence/shutdown-complete.log`: `shutdown-complete 11768 <timestamp>`.
    4. Called `app.Quit()`.
  - **Self-Termination Result:** Primary process (PID 11768) exited cleanly with code `0` (`HasExited: True`).
  - **Process Tree Termination Result:**
    - Helper root (PID 8588): **TERMINATED (NOT RUNNING)**.
    - Helper child (PID 11440): **TERMINATED (NOT RUNNING)**.
    - Zero orphan processes were leaked.
    - Zero manual `Stop-Process -Force` calls were used.

### E. Windows Credential Manager Evidence
- Verified via `evidence/cred-test.json`:
  ```json
  {
    "target": "GoDownloader/SPK-SYN3-01/test-1789844282089771700",
    "writeSuccess": true,
    "readMatch": true,
    "deleteSuccess": true,
    "postDeleteCheck": true
  }
  ```
- 32-byte key written via `advapi32.dll` `CredWriteW`, read back via `CredReadW`, verified bit-for-bit equal, deleted via `CredDeleteW`, and confirmed `ERROR_NOT_FOUND` on post-delete check.

### F. Data Root / CWD Independence Evidence
- Verified via `evidence/data-root.json` and standalone CLI execution:
  - Invocation 1 (from repository): `CWD=C:\Users\bkkav\Downloads\downloader` $\to$ `DATA_ROOT=C:\Users\bkkav\AppData\Roaming\GoDownloader`
  - Invocation 2 (from `$env:TEMP`): `CWD=C:\Users\bkkav\AppData\Local\Temp` $\to$ `DATA_ROOT=C:\Users\bkkav\AppData\Roaming\GoDownloader`
- Data root resolves deterministically to the user's roaming AppData directory regardless of working directory.

---

## 7. Non-Blocking Limitations & Mitigations

| Item | Criterion | Finding / Limitation | Impact | Mitigation for V0.9 Implementation |
|---|:---:|---|---|---|
| **1** | — | **Upstream Beta Status:** Wails v3 is currently in public beta (`v3.0.0-beta.23`). Minor API adjustments may occur prior to 3.0 GA. | Low | Pin exact version `v3.0.0-beta.23` in `go.mod` and tooling. Upgrade deliberately via dedicated check. |
| **2** | **J** | **System Tray Polish:** Native tray supports minimal menu (`Show`, `Quit`), tooltip, and icons. Animated download badges or progress overlays require custom native draw hooks. | Cosmetic | Retain clean standard tray icon and menu for V0.9.0 Technical Preview; defer advanced tray badge rendering to post-V1. |
| **3** | — | **Console Window Flashing:** Windows GUI executables (`-H windowsgui`) spawning console children (e.g. `yt-dlp.exe`) can momentarily flash a console window if spawned without creation flags. | Cosmetic | Ensure `internal/process.ProcessSupervisor` continues to enforce `CREATE_NO_WINDOW` (`0x08000000`) in `SysProcAttr` on Windows. |
| **4** | **Q** | **Installer & Bootstrapper Packaging:** Development machine has WebView2 Evergreen runtime installed. While `wails3 generate webview2bootstrapper` exists, creating a signed redistributable installer was not part of this architecture spike. | Packaging concern | Defer NSIS installer script generation and code signing to dedicated milestone `DSK-4`. |

---

## 8. Root Repository Regression Verification

To guarantee zero collateral impact on existing production functionality:

1. **Go Test Suite (`go test -count=1 ./...`):**
   - Result: **100% PASS across all 17 packages**.
   - Packages: `api`, `config`, `database`, `engine`, `engine/aria2`, `engine/qbittorrent`, `engine/ytdlp`, `events`, `job`, `mediaauth`, `networkpolicy`, `process`, `securestore`, `settings`, `storage`, `toolmanager`, `tracker`.
2. **Frontend Test Suite (`npm test` in `web/`):**
   - Result: **100% PASS** (19 test files, 186 passed).
3. **Frontend Production Build (`npm run build` in `web/`):**
   - Result: **100% PASS** (built in 1.14s).
4. **Spike Automated Tests (`go test -v -count=1 .` in `spikes/spk-syn3-01-wails-v3/`):**
   - Result: **100% PASS** (7 tests: InstanceID, StateSync, TriggerError, CredentialManager, DataRoot, ProcessSupervisor, BatchEvents).
5. **Working Tree Safety Audit:**
   - `git status --short` and `git diff` confirm zero modified tracked files. Root manifests (`go.mod`, `go.sum`, `package.json`) are untouched.

---

## 9. Final Decision Record

```text
====================================================================================================
DECISION: WAILS ACCEPTED WITH NON-BLOCKING LIMITATIONS
FRAMEWORK: Wails v3
EVALUATED PINNED VERSION: v3.0.0-beta.23
TARGET PLATFORM: Windows 11 / Windows 10 (x64)
CRITERIA EVALUATED: 22 / 22 (Criteria A through V)
CRITERIA ACCOUNTING:
  - PASS (20): A, B, C, D, E, F, G, H, I, K, L, M, N, O, P, R, S, T, U, V
  - PASS WITH NON-BLOCKING LIMITATION (2): J, Q
  - FAIL (0): None
  - UNVERIFIED (0): None
TAURI V2: NOT EVALUATED / FALLBACK ONLY (No architectural blockers found; framework switch not justified)
SPK-SYN3-01 STATUS: COMPLETE / LOCKED
AUTHORITATIVE REPORT: docs/implementation/SPK-SYN3-01_WAILS_V3_VIABILITY.md
====================================================================================================
```
