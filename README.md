<p align="center">
  <strong>GoDownloader</strong>
</p>

<p align="center">
  A self-hosted download manager for direct files, media streams, and torrents — unified in one clean interface.
</p>

<p align="center">
  <a href="https://github.com/professionalman/GoDownloader/actions/workflows/ci.yml"><img src="https://github.com/professionalman/GoDownloader/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25">
  <img src="https://img.shields.io/badge/react-19-61DAFB?logo=react&logoColor=white" alt="React 19">
  <img src="https://img.shields.io/badge/sqlite-3-003B57?logo=sqlite&logoColor=white" alt="SQLite">
  <img src="https://img.shields.io/badge/license-personal_use-gray" alt="License">
</p>

---

## Overview

GoDownloader is a unified download management system that orchestrates three specialized engines behind a single API and UI. Paste a link — GoDownloader routes it to the right backend, manages the lifecycle, and streams progress to your browser in real time.

| Engine | Protocols | Capabilities |
|---|---|---|
| **aria2** | HTTP / HTTPS / FTP | Multi-connection, resumable, segmented downloads |
| **yt-dlp** | 1800+ media sites | Format selection, audio/video merge via FFmpeg |
| **qBittorrent** | BitTorrent / Magnet | File selection, priority control, seeding lifecycle |

Everything runs locally. No cloud services, no accounts, no telemetry.

---

## Key Features

### 🎯 Intelligent Routing
Paste any URL, magnet link, or upload a `.torrent` file. The engine router analyzes the source and dispatches to the optimal backend automatically.

### 📊 Priority Queue & Scheduler
A built-in scheduler manages download concurrency with configurable limits. Jobs are organized into **priority lanes** (high, normal, low) and processed in FIFO order within each lane. Higher priority jobs advance in the queue without interrupting active downloads.

### 🔄 Real-Time Progress
All job updates — speed, ETA, progress, state changes — stream to the browser via Server-Sent Events. No polling, no page refreshes.

### 📦 Batch & Bulk Operations
Submit up to 100 links at once. Select multiple jobs and pause, resume, cancel, retry, or delete them in a single action.

### 🎬 Media Downloads
- Auto-detects 1800+ supported platforms via yt-dlp
- Presents available formats (4K, 1080p, 720p, audio-only) with codec info and estimated file sizes
- Merges video + audio streams automatically using FFmpeg
- Isolated temporary workspace with safe finalization to destination

### 🌊 Torrent Support
- Accepts magnet links and `.torrent` file uploads
- Full file tree with per-file selection and priority control before starting
- Live seeding statistics (upload speed, ratio, connected peers)
- Five seeding policies: `none`, `unlimited`, `ratio`, `duration`, `ratio_or_duration`

### 🗂️ Storage & File Lifecycle
- **Per-job destinations** with path snapshotting at creation time
- **Download categories** with folder mappings (relative or absolute)
- **Disk-space preflight** validation before start/resume
- **Filename conflict policies**: `rename`, `overwrite`, or `fail`
- **Safe deletion** with ownership verification — only files GoDownloader created are touched

### 🔒 Network & Protocol Controls (v0.7)
- Global and per-job bandwidth limits (download + upload)
- Proxy support (HTTP, HTTPS, SOCKS5) with per-engine capability awareness
- Custom User-Agent, HTTP headers, retry/timeout controls
- AES-256-GCM encryption for proxy passwords and sensitive headers
- HTTP(S) tracker subscriptions with bounded refresh and transactional persistence
- qBittorrent operations scoped exclusively to GoDownloader-owned hashes

### 🛡️ Restart Recovery
Active downloads reattach after server restart. Queued jobs are preserved. Torrent jobs automatically reconnect to the qBittorrent daemon.

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   React UI (Vite + TS)              │
└──────────────────────┬──────────────────────────────┘
                       │ REST API + SSE
                       ▼
┌─────────────────────────────────────────────────────┐
│                  Go HTTP Server                     │
│                                                     │
│  ┌─────────────┐  ┌────────────┐  ┌──────────────┐ │
│  │ Job Manager  │─▶│ Scheduler  │─▶│   Queue DB   │ │
│  │             │  │            │  │  (SQLite)    │ │
│  │ State Machine│  │ Priority   │  └──────────────┘ │
│  │ Recovery    │  │ Lanes      │                    │
│  └──────┬──────┘  └────────────┘                    │
│         │                                           │
│  ┌──────▼──────┐         ┌───────────┐              │
│  │Engine Router│         │ Event Bus │──▶ SSE Stream │
│  └──┬───┬───┬──┘         └───────────┘              │
└─────┼───┼───┼───────────────────────────────────────┘
      │   │   │
      ▼   ▼   ▼
   aria2  yt-dlp  qBittorrent
```

---

## Prerequisites

| Dependency | Version | Install |
|---|---|---|
| **Go** | 1.25+ | [go.dev/dl](https://go.dev/dl/) |
| **Node.js** | 18+ | [nodejs.org](https://nodejs.org/) |
| **aria2** | any | `winget install aria2` · `brew install aria2` · `apt install aria2` |
| **yt-dlp** | any | `winget install yt-dlp` · `brew install yt-dlp` · `pip install yt-dlp` |
| **FFmpeg** | any | `winget install ffmpeg` · `brew install ffmpeg` · `apt install ffmpeg` |
| **qBittorrent** | 5.0+ | `apt install qbittorrent-nox` · [Docker](https://hub.docker.com/r/linuxserver/qbittorrent) · Desktop with Web UI |

> **Note:** aria2 and qBittorrent run as separate daemon processes. GoDownloader communicates with them over their local APIs — it does not bundle or manage these processes.

---

## Quick Start

### 1. Start the external engines

```bash
# aria2 RPC daemon
aria2c --enable-rpc --rpc-listen-all=false --rpc-listen-port=6800 --rpc-allow-origin-all

# qBittorrent Web API (separate terminal)
qbittorrent-nox --webui-port=8081
```

### 2. Build and run

```bash
# Build the frontend
cd web && npm install && npm run build && cd ..

# Start the server
go run ./cmd/server
```

### 3. Open the UI

Navigate to **http://localhost:8080** in your browser.

---

## Development

### Local Development Setup

```bash
# Terminal 1 — Go backend
go run ./cmd/server

# Terminal 2 — React dev server with hot reload (proxies API to :8080)
cd web && npm run dev
```

Dev UI available at **http://localhost:5173**.

### Running Tests

```bash
# Backend — all unit tests
go test ./...

# Backend — with race condition detection
go test -race ./...

# Frontend — full verification suite
cd web && npm run typecheck && npm test -- --run && npm run lint && npm run build
```

### CI Pipeline

Automated CI runs on every push and pull request against `main`:

| Job | Checks |
|---|---|
| **Go Backend Verification** | `gofmt`, `go vet`, unit tests, race detector |
| **Web Frontend Verification** | TypeScript typecheck, Vitest, linting, production build |

---

## API Reference

### Jobs

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/jobs` | Create a download job |
| `POST` | `/api/v1/jobs/batch` | Submit multiple jobs |
| `POST` | `/api/v1/jobs/bulk` | Bulk pause / resume / cancel / retry |
| `GET` | `/api/v1/jobs` | List all jobs |
| `GET` | `/api/v1/jobs/{id}` | Get job details |
| `POST` | `/api/v1/jobs/{id}/pause` | Pause a job |
| `POST` | `/api/v1/jobs/{id}/resume` | Resume a job |
| `POST` | `/api/v1/jobs/{id}/retry` | Retry a failed job |
| `POST` | `/api/v1/jobs/{id}/cancel` | Cancel a job |
| `DELETE` | `/api/v1/jobs/{id}` | Delete a job (with optional file removal) |
| `PUT` | `/api/v1/jobs/{id}/priority` | Change priority lane |

### Torrents

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/jobs/torrent` | Upload a `.torrent` file |
| `GET` | `/api/v1/jobs/{id}/torrent/files` | Get torrent file list |
| `POST` | `/api/v1/jobs/{id}/torrent/start` | Set file priorities and start |
| `POST` | `/api/v1/jobs/{id}/stop-seeding` | Stop seeding |
| `POST` | `/api/v1/jobs/{id}/torrent/trackers` | Add trackers to an owned public torrent |
| `PUT` | `/api/v1/jobs/{id}/torrent/seeding-policy` | Update seeding policy |

### Network & Capabilities

| Method | Endpoint | Description |
|---|---|---|
| `PUT` | `/api/v1/jobs/{id}/network` | Update live bandwidth limits |
| `GET` | `/api/v1/jobs/{id}/capabilities` | Get normalized controls for a job |
| `GET` | `/api/v1/capabilities` | Get capability profiles |
| `POST` | `/api/v1/capabilities/resolve` | Resolve source or batch intersection |

### Tracker Subscriptions

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/tracker-sources` | List tracker subscriptions |
| `POST` | `/api/v1/tracker-sources` | Create a tracker subscription |
| `PUT` | `/api/v1/tracker-sources/{id}` | Update a subscription |
| `DELETE` | `/api/v1/tracker-sources/{id}` | Delete a subscription |
| `POST` | `/api/v1/tracker-sources/{id}/refresh` | Refresh one subscription |
| `POST` | `/api/v1/tracker-sources/refresh` | Refresh all enabled subscriptions |

### Media, Categories & Queue

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/jobs/{id}/format` | Select media format |
| `GET` | `/api/v1/categories` | List download categories |
| `POST` | `/api/v1/categories` | Create a category |
| `PUT` | `/api/v1/categories/{id}` | Update a category |
| `DELETE` | `/api/v1/categories/{id}` | Delete a category |
| `GET` | `/api/v1/queue` | Queue snapshot and capacity |
| `PUT` | `/api/v1/queue/reorder` | Reorder jobs within a lane |
| `GET` | `/api/v1/settings` | Get current settings |
| `PUT` | `/api/v1/settings` | Update settings |
| `GET` | `/api/v1/events` | SSE stream for live updates |

---

## Configuration

All settings are optional. Defaults work out of the box for a typical local setup.

<details>
<summary><strong>Core Settings</strong></summary>

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:8080` | Server listen address |
| `MAX_CONCURRENT_DOWNLOADS` | `3` | Maximum simultaneous downloads |
| `DOWNLOAD_DIR` | `./downloads` | Default download directory |
| `DATA_DIR` | `./data` | Application data storage |
| `TEMP_DIR` | `<DATA_DIR>/tmp` | Temporary workspace for media downloads |
| `WEB_DIR` | `./web/dist` | Built frontend directory |
| `MIN_FREE_SPACE_BYTES` | `1073741824` | Minimum free disk space reserve (1 GiB) |
| `DEFAULT_CONFLICT_POLICY` | `rename` | Filename conflict policy: `rename`, `overwrite`, `fail` |

</details>

<details>
<summary><strong>Engine Connections</strong></summary>

| Variable | Default | Description |
|---|---|---|
| `ARIA2_RPC_URL` | `http://localhost:6800/jsonrpc` | aria2 JSON-RPC endpoint |
| `ARIA2_SECRET` | — | aria2 RPC secret |
| `QBIT_URL` | `http://127.0.0.1:8081` | qBittorrent Web API address |
| `QBIT_USERNAME` | `admin` | qBittorrent username |
| `QBIT_PASSWORD` | — | qBittorrent password |
| `QBIT_TIMEOUT` | `30` | qBittorrent request timeout (seconds) |
| `YTDLP_PATH` | `yt-dlp` | Path to yt-dlp binary |
| `FFMPEG_PATH` | `""` | Path to FFmpeg binary (empty = auto-detect via PATH) |

</details>

<details>
<summary><strong>Network & Security</strong></summary>

| Variable | Default | Description |
|---|---|---|
| `GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Global download limit (0 = unlimited) |
| `DEFAULT_TORRENT_DOWNLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Default per-torrent download limit |
| `DEFAULT_TORRENT_UPLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Default per-torrent upload limit |
| `DEFAULT_PROXY_MODE` | `disabled` | `disabled`, `system`, or `custom` |
| `DEFAULT_PROXY_PROTOCOL` | — | `http`, `https`, or `socks5` |
| `DEFAULT_PROXY_HOST` / `DEFAULT_PROXY_PORT` | — | Custom proxy endpoint |
| `DEFAULT_PROXY_USERNAME` / `DEFAULT_PROXY_PASSWORD` | — | Proxy credentials |
| `DEFAULT_NO_PROXY` | — | Comma-separated proxy bypass list |
| `DEFAULT_USER_AGENT` | — | Default User-Agent |
| `V0.7_SETTINGS_ENCRYPTION_KEY` | — | AES-256-GCM key for persisted secrets |
| `MANAGE_QBIT_GLOBAL_NETWORK_SETTINGS` | `false` | Opt-in for managed qBittorrent proxy settings |

</details>

<details>
<summary><strong>Download Tuning</strong></summary>

| Variable | Default | Description |
|---|---|---|
| `DEFAULT_MAX_ATTEMPTS` | `0` | Retry attempts (0 = engine default) |
| `DEFAULT_RETRY_WAIT_SECONDS` | `0` | Wait between retries (0–3600s) |
| `DEFAULT_CONNECT_TIMEOUT_SECONDS` | `0` | Connection timeout (0 = engine default) |
| `DEFAULT_REQUEST_TIMEOUT_SECONDS` | `0` | Request timeout (0 = engine default) |
| `DEFAULT_ARIA2_SPLIT` | `5` | aria2 split count (1–16) |
| `DEFAULT_ARIA2_MAX_CONNECTIONS_PER_SERVER` | `1` | aria2 connections per server (1–16) |
| `DEFAULT_ARIA2_MIN_SPLIT_SIZE_BYTES` | `20971520` | aria2 minimum split size (1 MiB–1 GiB) |
| `DEFAULT_SEEDING_MODE` | `none` | Seeding policy: `none`, `unlimited`, `ratio`, `duration`, `ratio_or_duration` |
| `DEFAULT_SEED_RATIO` | — | Ratio threshold for ratio-based modes |
| `DEFAULT_SEED_TIME_SECONDS` | — | Time threshold for duration-based modes |
| `TRACKER_AUTO_APPLY` | `false` | Auto-apply tracker entries to new public torrents |

</details>

---

## Engine Capability Matrix

| Control | Direct (aria2) | Media (yt-dlp) | Torrent (qBittorrent) |
|---|---|---|---|
| Pause / Resume | ✅ Live | ❌ | ✅ Live |
| Download Limit | ✅ Live | ⚡ Startup-only | ✅ Live |
| Upload Limit | ❌ | ❌ | ✅ Live |
| Delete with Files | ✅ Ownership-verified | ✅ Ownership-verified | ✅ Selected-only |
| Proxy | Snapshot HTTP | Snapshot HTTP/HTTPS/SOCKS5 | Managed global opt-in |
| Headers / Retry / Timeouts | Snapshot | Snapshot | ❌ |
| Trackers / Seeding | ❌ | ❌ | ✅ Owned torrents only |

---

## Project Structure

```
GoDownloader/
├── cmd/server/              Application entry point
├── internal/
│   ├── api/                 HTTP handlers and REST routing
│   ├── config/              Environment and configuration loading
│   ├── database/            SQLite storage, migrations, and repositories
│   ├── engine/              Engine registry and adapters
│   │   ├── aria2/             aria2 JSON-RPC client
│   │   ├── ytdlp/             yt-dlp process runner and format analyzer
│   │   └── qbittorrent/       qBittorrent Web API client
│   ├── events/              Event bus and SSE handler
│   ├── job/                 Job state machine, scheduler, queue, and recovery
│   ├── networkpolicy/       Capability profiles and policy validation
│   ├── securestore/         Field-bound AES-256-GCM secret storage
│   ├── settings/            Application settings persistence
│   ├── storage/             Storage resolution, disk preflight, and file lifecycle
│   └── tracker/             Bounded tracker subscription management
├── web/
│   └── src/
│       ├── components/      React UI components
│       ├── hooks/           Custom React hooks
│       ├── api.ts           API client
│       └── App.tsx          Application root
├── .github/workflows/       CI pipeline definitions
└── go.mod                   Go module definition
```

---

## License

This project is for personal use.
