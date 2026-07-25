# GoDownloader V0.5 — Smart Queue, Batch Jobs, Concurrency & Settings Foundation

A high-performance, local-first download manager built with **Go**, **React (TypeScript + Vite)**, **SQLite**, **aria2c**, **yt-dlp**, **FFmpeg**, and **qBittorrent-nox**.

V0.5 extends GoDownloader with a central **Smart Queue & Concurrency Scheduler**, **Priority Lanes**, **Batch Job Submissions**, **Bulk Control Actions**, and **Settings Persistence**.

---

## 🚀 Key Features

### ⏳ Smart Queue & Concurrency Scheduler (V0.5)
- **Central Scheduler**: The Manager/Scheduler owns execution policy (`Manager` -> `Scheduler` -> `Queue` -> `IEngine`). Engines never decide queue policy.
- **Strict Capacity Accounting**: Downloads occupy 1 active slot during `DOWNLOADING`. Transitions to `PROCESSING` (yt-dlp FFmpeg merge) and `SEEDING` (qBittorrent seeding) immediately release download capacity slots.
- **Priority Lanes**: Supports `high`, `normal` (default), and `low` priority lanes.
- **Non-Preemptive Scheduling**: Higher priority downloads move ahead in queue but never interrupt active downloads.
- **Interactive Reordering**: Dynamic drag/move reordering within priority lanes via API and React UI.
- **Startup Recovery & Cleanup**: Stale or completed queue entries are automatically cleaned up on restart.

### 📦 Batch & Bulk Operations (V0.5)
- **Batch Submission**: Submit up to 100 links at once via multiline text input or `POST /api/v1/jobs/batch`.
- **Bulk Lifecycle Control**: Perform `pause`, `resume`, `cancel`, or `retry` best-effort on up to 100 job IDs at once.
- **Selection Toolbar**: Multi-select job cards in the UI for one-click bulk operations.

### ⚙️ App Settings Foundation (V0.5)
- **Max Concurrent Downloads**: Configurable in DB (`app_settings` table, validated 1–20, default 3).
- **Environment Override**: Optional `MAX_CONCURRENT_DOWNLOADS` env var override with UI indicator.
- **Settings UI**: Modal panel to view and modify queue concurrency settings live.

### 🧲 Torrent & Magnet Downloads (V0.4)
- **Magnet URIs & .torrent Uploads**: Accepts `magnet:` links or `.torrent` file uploads.
- **Async Metadata Acquisition**: Automatically fetches torrent metainfo in the background (`analyzing` state).
- **Interactive Torrent File Selector**: View file lists, individual file sizes, set per-file priorities (`High`, `Normal`, `Skip`), and configure seed-after-download preferences before starting.
- **Seeding Lifecycle Management**: Conditional seeding (`seeding` state) with real-time upload speed, total uploaded bytes, ratio, and seeders/leechers counts, with a 1-click **Stop Seeding** action.
- **qBittorrent Daemon Reattachment**: Daemon-aware restart recovery reattaches active and seeding torrent jobs across Go backend restarts without losing state.

### 🎬 Media Downloads (V0.3)
- **Media URL Detection & Analysis**: Auto-detects media links (YouTube, Vimeo, Twitch, etc.) and extracts available formats via `yt-dlp`.
- **Interactive Format Selector**: Select resolution (1080p, 720p, etc.), codec, or audio-only streams with estimated file sizes before downloading.
- **FFmpeg Stream Merging**: Merges separate video and audio streams using `ffmpeg` with live status reporting (`processing` state).

### ⚡ Direct Downloads & Core System (V0.1 / V0.2)
- **Engine Registry & Resolver**: Auto-routes inputs: `magnet:` / `.torrent` → `qbittorrent`, media URLs → `ytdlp`, direct HTTP/HTTPS → `aria2c`.
- **Unified State Machine**: Centralized state validation (`queued`, `analyzing`, `awaiting_selection`, `downloading`, `processing`, `seeding`, `paused`, `completed`, `failed`, `cancelled`).
- **Real-time SSE Streaming**: Server-Sent Events stream live progress, speeds, file sizes, and ETAs directly to the UI.

---

## 🏗️ Architecture

```text
                                  React UI (Vite + TS)
                                           │
                                      REST + SSE
                                           │
                                           ▼
                                  ┌──────────────────┐
                                  │   Go API Server  │
                                  └────────┬─────────┘
                                           │
                                           ▼
                                  ┌──────────────────┐
                                  │   Job Manager    │
                                  │                  │
                                  │   Scheduler ─────┼──┐
                                  │ State Machine    │  │
                                  │ Engine Registry  │  │
                                  │ Progress Monitor │  │
                                  └───────┬──┬───────┘  │
                                          │  │          ▼
             ┌────────────────────────────┘  └──── Persistent Queue
             ▼                                      (job_queue)
        SQLite Store                                    │
             │                                          │
             │                                          ▼
             │                                       Event Bus
             ▼                                          │
      Engine Registry                                   ▼
             │                                       SSE Stream
   ┌─────────┼──────────────────┐
   ▼         ▼                  ▼
aria2     yt-dlp           qBittorrent
   │         │                  │
   ▼         ▼                  ▼
aria2c     yt-dlp ──→ FFmpeg  qBittorrent-nox ──→ BitTorrent
   │         │                  │
   └─────────┼──────────────────┘
             ▼
        Local File
```

---

## 🛠️ Prerequisites

- **Go** 1.23+
- **Node.js** 18+ & npm
- **aria2** installed and on PATH:
  - **Windows**: `winget install aria2` or from [aria2.github.io](https://aria2.github.io/)
  - **macOS**: `brew install aria2`
  - **Linux**: `sudo apt install aria2`
- **yt-dlp** installed and on PATH:
  - `winget install yt-dlp` / `brew install yt-dlp` / `pip install yt-dlp`
- **FFmpeg & ffprobe** installed and on PATH:
  - `winget install ffmpeg` / `brew install ffmpeg` / `sudo apt install ffmpeg`
- **qBittorrent-nox** (5.0+) running with Web UI enabled (default port `8081`):
  - `qbittorrent-nox` / docker / desktop app with Web UI enabled

---

## 🚦 Quick Start

### 1. Start the Daemons (aria2 & qBittorrent)

```bash
# Terminal 1: aria2c daemon
aria2c --enable-rpc --rpc-listen-all=false --rpc-listen-port=6800 --rpc-allow-origin-all

# Terminal 2: qBittorrent-nox daemon (or launch qBittorrent with Web UI on 8081)
qbittorrent-nox --webui-port=8081
```

### 2. Build Frontend & Start Go Backend

```bash
# Build frontend
cd web
npm install
npm run build
cd ..

# Run backend server
go run ./cmd/server
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

---

## 💻 Local Development

For frontend hot reloading:

```bash
# Terminal 1: aria2 & qBittorrent daemons

# Terminal 2: Go API server
go run ./cmd/server

# Terminal 3: React dev server
cd web
npm run dev
```

Dev UI accessible at [http://localhost:5173](http://localhost:5173).

---

## 📡 API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/jobs` | Create a download job (`{"source": "...", "priority": "high"}`) |
| `POST` | `/api/v1/jobs/batch` | Batch submission (`{"inputs": [{"source": "..."}, ...]}`) |
| `POST` | `/api/v1/jobs/bulk` | Bulk operation (`{"action": "pause", "jobIds": [...]}`) |
| `PUT`  | `/api/v1/jobs/{id}/priority` | Update job priority lane (`{"priority": "high"}`) |
| `GET`  | `/api/v1/queue` | Get queue snapshot, positions, and capacity stats |
| `PUT`  | `/api/v1/queue/reorder` | Reorder job positions in priority lane |
| `GET`  | `/api/v1/settings` | Get application & queue settings |
| `PUT`  | `/api/v1/settings` | Update max concurrent downloads |
| `POST` | `/api/v1/jobs/torrent` | Upload a `.torrent` file (`multipart/form-data`) |
| `GET`  | `/api/v1/jobs/{id}/torrent/files` | Get file list and priorities for a torrent job |
| `POST` | `/api/v1/jobs/{id}/torrent/start` | Apply file priorities and start torrent download |
| `POST` | `/api/v1/jobs/{id}/stop-seeding` | Stop seeding a completed torrent |
| `POST` | `/api/v1/jobs/{id}/format` | Select format for a media job (`{"formatId": "..."}`) |
| `GET`  | `/api/v1/jobs` | List all historical and active jobs |
| `GET`  | `/api/v1/jobs/{id}` | Get details of a specific job |
| `POST` | `/api/v1/jobs/{id}/pause` | Pause an active download |
| `POST` | `/api/v1/jobs/{id}/resume` | Resume a paused download |
| `POST` | `/api/v1/jobs/{id}/retry` | Retry a failed download |
| `POST` | `/api/v1/jobs/{id}/cancel` | Cancel a download |
| `GET`  | `/api/v1/events` | SSE stream for real-time progress updates |

---

## ⚙️ Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `127.0.0.1:8080` | Server listen address (loopback only) |
| `MAX_CONCURRENT_DOWNLOADS` | *(empty)* | Optional override for max concurrent downloads |
| `DOWNLOAD_DIR` | `./downloads` | Destination folder for completed downloads |
| `DATA_DIR` | `./data` | Application data folder for stored `.torrent` files |
| `ARIA2_RPC_URL` | `http://localhost:6800/jsonrpc` | aria2 RPC endpoint |
| `ARIA2_SECRET` | *(empty)* | aria2 RPC secret token |
| `QBIT_URL` | `http://127.0.0.1:8081` | qBittorrent Web API URL |
| `QBIT_USERNAME` | `admin` | qBittorrent Web API username |
| `QBIT_PASSWORD` | *(empty)* | qBittorrent Web API password |
| `QBIT_TIMEOUT` | `30` | qBittorrent request timeout in seconds |
| `YTDLP_PATH` | `yt-dlp` | Executable path for yt-dlp |
| `FFMPEG_PATH` | `ffmpeg` | Executable path for FFmpeg |
| `WEB_DIR` | `./web/dist` | Static web files directory |

---

## 🧪 Testing

Run unit tests, race detector checks, and frontend verification:

```bash
# Run unit & integration tests
go test -v ./...

# Run race condition checks
go test -race ./...

# Frontend build & lint
cd web && npm run build && npm run lint
```

---

## 📁 Project Structure

```text
GoDownloader/
├── cmd/
│   └── server/main.go          # Application entry point & engine registration
├── internal/
│   ├── api/                    # HTTP router & handlers (/jobs, /jobs/torrent, SSE)
│   ├── config/                 # App configuration & env vars
│   ├── database/               # SQLite storage, migrations (v1-v4) & torrent repository
│   ├── engine/                 # Engine Registry, Resolver, & Adapters
│   │   ├── aria2/              # aria2 RPC client
│   │   ├── ytdlp/              # yt-dlp runner, analyzer, & progress parser
│   │   └── qbittorrent/        # qBittorrent Web API v2 client, mapper & engine
│   ├── events/                 # Pub/Sub EventBus & SSE HTTP handler
│   └── job/                    # State machine, Manager, Monitor, & Recovery
├── web/                        # React + TypeScript + Vite frontend
│   └── src/
│       ├── components/         # DownloadForm, FormatSelector, TorrentFileSelector, JobCard, JobList
│       ├── api.ts              # REST client bindings
│       └── types.ts            # TypeScript interfaces
├── downloads/                  # Default download folder
├── go.mod
└── README.md
```
