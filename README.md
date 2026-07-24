# GoDownloader V0.4 — Torrent, Media & Direct Download Manager

A high-performance, local-first download manager built with **Go**, **React (TypeScript + Vite)**, **SQLite**, **aria2c**, **yt-dlp**, **FFmpeg**, and **qBittorrent-nox**.

V0.4 extends GoDownloader with first-class **Torrent & Magnet** download capabilities powered by qBittorrent-nox Web API v2.

---

## 🚀 Key Features

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
- **Subprocess Security & Lifecycle**: Safe subprocess execution (`exec.CommandContext`) without shell invocation. Context cancellation cleans up orphan processes.

### ⚡ Direct Downloads & Core System (V0.1 / V0.2)
- **Engine Registry & Resolver**: Auto-routes inputs: `magnet:` / `.torrent` → `qbittorrent`, media URLs → `ytdlp`, direct HTTP/HTTPS → `aria2c`.
- **Unified State Machine**: Centralized state validation (`queued`, `analyzing`, `awaiting_selection`, `downloading`, `processing`, `seeding`, `paused`, `completed`, `failed`, `cancelled`).
- **Pause, Resume, Cancel & Retry**: Universal controls for all download engines.
- **Real-time SSE Streaming**: Server-Sent Events stream live progress, speeds, file sizes, and ETAs directly to the UI without client polling.

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
                                  │ State Machine    │
                                  │ Engine Registry  │
                                  │ Progress Monitor │
                                  │ Restart Recovery │
                                  └───────┬──┬───────┘
                                          │  │
             ┌────────────────────────────┘  └────────────────────────────┐
             ▼                                                            ▼
        SQLite Store                                                   Event Bus
             │                                                            │
             │                                                            ▼
             │                                                         SSE Stream
             ▼
      Engine Registry
             │
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
| `POST` | `/api/v1/jobs` | Create a new HTTP or magnet download job (`{"source": "..."}`) |
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
| `LISTEN_ADDR` | `:8080` | Server listen address |
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
