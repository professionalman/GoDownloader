# GoDownloader

A self-hosted download manager that handles direct files, media streams, and torrents — all from one clean interface.

Current release: **V0.7 — Network & Protocol Power Controls**

Built with **Go**, **React**, **SQLite**, and powered by **aria2**, **yt-dlp**, and **qBittorrent** under the hood.

---

## What It Does

Paste a link. GoDownloader figures out the rest.

- **Direct files** (HTTP/HTTPS) — handed off to aria2 for fast, resumable downloads.
- **Media links** (YouTube, Vimeo, Twitch, etc.) — analyzed by yt-dlp so you can pick a format and resolution before downloading.
- **Torrents & magnets** — managed through qBittorrent with full file selection, priority control, and seeding lifecycle.

Everything runs locally on your machine. No cloud. No accounts. Your downloads, your storage.

---

## Features

### Smart Queue & Scheduler
Downloads don't all fire at once. A built-in scheduler manages concurrency with configurable limits (default: 3 simultaneous downloads). Jobs are organized into **priority lanes** (high, normal, low) and processed in FIFO order within each lane. Higher priority jobs move ahead in the queue but never interrupt downloads already in progress.

### Batch & Bulk Operations
Submit up to 100 links at once. Select multiple jobs in the UI and pause, resume, cancel, or retry them in one click.

### Torrent Support
- Accepts magnet links and `.torrent` file uploads
- Shows the full file list before you start — pick which files to download and set per-file priorities
- Tracks seeding progress (upload speed, ratio, peers) with a one-click stop

### Media Downloads
- Auto-detects supported media platforms via yt-dlp
- Presents available formats (1080p, 720p, audio-only, etc.) with estimated file sizes
- Merges video + audio streams automatically using FFmpeg

### Storage, Categories & File Lifecycle
- **Per-Job Destinations**: Target specific download directories per job, snapshotting destination path at creation time.
- **Download Categories**: Organize downloads with category folder mappings (relative to default download dir or absolute).
- **Disk-Space Preflight**: Automatic free disk space validation before start/resume to prevent out-of-disk failures.
- **Filename Conflict Policies**: Choose how collisions are handled for direct and media downloads (`rename`, `overwrite`, `fail`).
- **Isolated Media Workspace**: Media downloads (yt-dlp/FFmpeg) process in an isolated temporary directory before safe finalization to destination.

### Real-Time Progress
All job updates (speed, ETA, progress percentage, state changes) stream to the browser in real time via Server-Sent Events. No polling, no page refreshes.

### Restart Recovery
Active downloads are reattached after a restart. Queued jobs are preserved. Torrent jobs reconnect to the qBittorrent daemon automatically.

### Settings
Configure max concurrent downloads from the UI or via environment variable. The setting is persisted in the database and takes effect immediately.

### V0.7 Network and Protocol Controls

- Normalized global and per-job bandwidth limits. `0` means unlimited; positive values are bytes per second.
- Capability-driven proxy, User-Agent, HTTP header, retry, timeout, aria2 connection, torrent tracker, and seeding controls.
- Five torrent seeding modes: `none`, `unlimited`, `ratio`, `duration`, and `ratio_or_duration`.
- HTTP(S) tracker subscriptions with conditional refresh, 2 MiB/10,000-line bounds, four-worker concurrency, and transactional last-good entries.
- AES-256-GCM encryption for proxy passwords and sensitive headers. APIs, SSE, logs, and UI responses expose only configured markers.
- qBittorrent changes are restricted to hashes persisted for GoDownloader jobs. Managed daemon-global proxy settings are off by default and fail closed when the daemon contains unowned torrents.

The global bandwidth setting follows truthful engine scope: aria2 applies an aggregate daemon limit, yt-dlp applies the effective limit to future processes, and qBittorrent projects it only to GoDownloader-owned torrents. It is not a strict cross-engine aggregate.

| Control | Direct (aria2) | Media (yt-dlp) | Torrent (qBittorrent) |
|---|---|---|---|
| Pause/resume | Live | Unsupported | Live |
| Download limit | Live | Startup-only | Live |
| Upload limit | Unsupported | Unsupported | Live |
| Proxy | Snapshot HTTP/system/disabled | Snapshot HTTP/HTTPS/SOCKS5 | Managed global HTTP/SOCKS5 opt-in |
| Headers/retry/timeouts | Snapshot | Snapshot | Unsupported |
| Trackers/seeding | Unsupported | Unsupported | Owned public torrents only |

Tracker source URLs may intentionally target HTTP(S) loopback or private-network services. Userinfo, unsafe schemes, scheme-changing redirects, and more than five redirects are rejected.

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

**Engine Router** automatically selects the right backend:
- `http://` / `https://` → aria2
- YouTube, Vimeo, media URLs → yt-dlp (+ FFmpeg for merging)
- `magnet:` links / `.torrent` files → qBittorrent

---

## Prerequisites

| Dependency | Version | Install |
|---|---|---|
| **Go** | 1.25+ | [go.dev](https://go.dev/dl/) |
| **Node.js** | 18+ | [nodejs.org](https://nodejs.org/) |
| **aria2** | any | `winget install aria2` · `brew install aria2` · `apt install aria2` |
| **yt-dlp** | any | `winget install yt-dlp` · `brew install yt-dlp` · `pip install yt-dlp` |
| **FFmpeg** | any | `winget install ffmpeg` · `brew install ffmpeg` · `apt install ffmpeg` |
| **qBittorrent-nox** | 5.0+ | `apt install qbittorrent-nox` · [Docker](https://hub.docker.com/r/linuxserver/qbittorrent) · Desktop app with Web UI |

> **Note:** aria2 and qBittorrent run as background daemons. GoDownloader communicates with them over their local APIs — it does not bundle or manage these processes.

---

## Quick Start

### 1. Start the daemons

```bash
# aria2 RPC daemon
aria2c --enable-rpc --rpc-listen-all=false --rpc-listen-port=6800 --rpc-allow-origin-all

# qBittorrent Web API (in a separate terminal)
qbittorrent-nox --webui-port=8081
```

### 2. Build and run

```bash
# Build the frontend
cd web && npm install && npm run build && cd ..

# Start the server
go run ./cmd/server
```

Open **http://localhost:8080** in your browser.

---

## Development

For frontend hot-reloading during development:

```bash
# Terminal 1 — Go backend
go run ./cmd/server

# Terminal 2 — React dev server (proxies API to :8080)
cd web && npm run dev
```

Dev UI is at **http://localhost:5173**.

### Running Tests

```bash
# All tests
go test ./...

# With race condition detection
go test -race ./...

# Frontend verification
cd web && npm run typecheck && npm test && npm run build && npm run lint
```

---

## API

| Method | Endpoint | Purpose |
|---|---|---|
| **Jobs** | | |
| `POST` | `/api/v1/jobs` | Create a single download job |
| `POST` | `/api/v1/jobs/batch` | Submit multiple jobs at once |
| `POST` | `/api/v1/jobs/bulk` | Bulk pause / resume / cancel / retry |
| `GET` | `/api/v1/jobs` | List all jobs |
| `GET` | `/api/v1/jobs/{id}` | Get job details |
| `POST` | `/api/v1/jobs/{id}/pause` | Pause a job |
| `POST` | `/api/v1/jobs/{id}/resume` | Resume a job |
| `POST` | `/api/v1/jobs/{id}/retry` | Retry a failed job |
| `POST` | `/api/v1/jobs/{id}/cancel` | Cancel a job |
| `PUT` | `/api/v1/jobs/{id}/priority` | Change priority lane |
| **Torrents** | | |
| `POST` | `/api/v1/jobs/torrent` | Upload a `.torrent` file |
| `GET` | `/api/v1/jobs/{id}/torrent/files` | Get torrent file list |
| `POST` | `/api/v1/jobs/{id}/torrent/start` | Set file priorities and start |
| `POST` | `/api/v1/jobs/{id}/stop-seeding` | Stop seeding |
| `PUT` | `/api/v1/jobs/{id}/network` | Update supported live bandwidth limits |
| `GET` | `/api/v1/jobs/{id}/capabilities` | Get normalized controls for a job |
| `POST` | `/api/v1/jobs/{id}/torrent/trackers` | Add trackers to an owned public torrent |
| `PUT` | `/api/v1/jobs/{id}/torrent/seeding-policy` | Update normalized seeding policy |
| **Capabilities & Trackers** | | |
| `GET` | `/api/v1/capabilities` | Get capability profiles |
| `POST` | `/api/v1/capabilities/resolve` | Resolve a source or batch intersection |
| `GET/POST` | `/api/v1/tracker-sources` | List or create tracker subscriptions |
| `PUT/DELETE` | `/api/v1/tracker-sources/{id}` | Update or delete a subscription |
| `POST` | `/api/v1/tracker-sources/{id}/refresh` | Refresh one subscription |
| `POST` | `/api/v1/tracker-sources/refresh` | Refresh all enabled subscriptions |
| **Categories** | | |
| `GET` | `/api/v1/categories` | List all download categories |
| `POST` | `/api/v1/categories` | Create a new download category |
| `PUT` | `/api/v1/categories/{id}` | Update category name and directory |
| `DELETE` | `/api/v1/categories/{id}` | Delete a category |
| **Media** | | |
| `POST` | `/api/v1/jobs/{id}/format` | Select media format |
| **Queue & Settings** | | |
| `GET` | `/api/v1/queue` | Queue snapshot and capacity |
| `PUT` | `/api/v1/queue/reorder` | Reorder jobs within a lane |
| `GET` | `/api/v1/settings` | Get current settings |
| `PUT` | `/api/v1/settings` | Update settings |
| **Events** | | |
| `GET` | `/api/v1/events` | SSE stream for live updates |

---

## Configuration

All settings are optional. Defaults work out of the box for a typical local setup.

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `127.0.0.1:8080` | Address the server listens on |
| `MAX_CONCURRENT_DOWNLOADS` | — | Override max concurrent downloads (otherwise set via UI/DB, default 3) |
| `DOWNLOAD_DIR` | `./downloads` | Where downloaded files are saved |
| `TEMP_DIR` | `<DATA_DIR>/tmp` | Temporary working directory for media downloads |
| `MIN_FREE_SPACE_BYTES` | `1073741824` (1 GiB) | Minimum free disk space reserve before download start |
| `DEFAULT_CONFLICT_POLICY` | `rename` | Default filename conflict policy (`rename`, `overwrite`, `fail`) |
| `DATA_DIR` | `./data` | Storage for `.torrent` files and app data |
| `ARIA2_RPC_URL` | `http://localhost:6800/jsonrpc` | aria2 JSON-RPC endpoint |
| `ARIA2_SECRET` | — | aria2 RPC secret (if configured) |
| `QBIT_URL` | `http://127.0.0.1:8081` | qBittorrent Web API address |
| `QBIT_USERNAME` | `admin` | qBittorrent username |
| `QBIT_PASSWORD` | — | qBittorrent password |
| `QBIT_TIMEOUT` | `30` | qBittorrent request timeout (seconds) |
| `YTDLP_PATH` | `yt-dlp` | Path to yt-dlp binary |
| `FFMPEG_PATH` | `ffmpeg` | Path to FFmpeg binary |
| `WEB_DIR` | `./web/dist` | Directory serving the built frontend |
| `V0.7_SETTINGS_ENCRYPTION_KEY` | — | Base64 32-byte or 64-character hex AES key for persisted secrets |
| `GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Engine-scoped global download limit |
| `DEFAULT_TORRENT_DOWNLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Default owned-torrent download limit |
| `DEFAULT_TORRENT_UPLOAD_LIMIT_BYTES_PER_SECOND` | `0` | Default owned-torrent upload limit |
| `DEFAULT_PROXY_MODE` | `disabled` | `disabled`, `system`, or `custom` |
| `DEFAULT_PROXY_PROTOCOL` | — | `http`, `https`, or `socks5` where supported |
| `DEFAULT_PROXY_HOST` / `DEFAULT_PROXY_PORT` | — | Custom proxy endpoint |
| `DEFAULT_PROXY_USERNAME` / `DEFAULT_PROXY_PASSWORD` | — | Proxy credentials; environment passwords are not copied to SQLite |
| `DEFAULT_NO_PROXY` | — | Comma-separated proxy bypass list |
| `DEFAULT_USER_AGENT` | — | Default normalized User-Agent |
| `DEFAULT_MAX_ATTEMPTS` | `0` | Engine default at `0`; otherwise 1–100 attempts |
| `DEFAULT_RETRY_WAIT_SECONDS` | `0` | Retry wait, 0–3600 seconds |
| `DEFAULT_CONNECT_TIMEOUT_SECONDS` | `0` | Connect timeout, `0` or 1–86400 seconds |
| `DEFAULT_REQUEST_TIMEOUT_SECONDS` | `0` | Request timeout, `0` or 1–86400 seconds |
| `DEFAULT_ARIA2_SPLIT` | `5` | aria2 split count, 1–16 |
| `DEFAULT_ARIA2_MAX_CONNECTIONS_PER_SERVER` | `1` | aria2 connections/server, 1–16 |
| `DEFAULT_ARIA2_MIN_SPLIT_SIZE_BYTES` | `20971520` | aria2 minimum split, 1 MiB–1 GiB |
| `TRACKER_AUTO_APPLY` | `false` | Snapshot enabled tracker entries into new public torrents |
| `MANAGE_QBIT_GLOBAL_NETWORK_SETTINGS` | `false` | Dedicated-daemon opt-in for verified qB proxy management |

---

## Project Structure

```
GoDownloader/
├── cmd/server/           Entry point
├── internal/
│   ├── api/              HTTP handlers and routing
│   ├── config/           Environment and configuration
│   ├── database/         SQLite storage and migrations
│   ├── engine/           Engine registry and adapters
│   │   ├── aria2/          aria2 RPC client
│   │   ├── ytdlp/          yt-dlp runner and format analyzer
│   │   └── qbittorrent/    qBittorrent Web API client
│   ├── events/           Event bus and SSE handler
│   ├── job/              Job state machine, scheduler, and recovery
│   ├── settings/         App settings persistence
│   ├── networkpolicy/    Normalized capability and policy validation
│   ├── securestore/      Field-bound AES-256-GCM secret storage
│   ├── tracker/          Bounded tracker subscription refresh
│   └── storage/          Storage resolution, disk preflight, and file lifecycle
├── web/src/              React frontend
│   ├── components/         UI components
│   ├── api.ts              API client
│   └── types.ts            TypeScript types
├── downloads/            Default download directory
└── data/                 Application data
```

---

## License

This project is for personal use.
