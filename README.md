# GoDownloader V0.3 — Media & Direct Download Manager

A high-performance, local-first download manager built with **Go**, **React (TypeScript + Vite)**, **SQLite**, **aria2c**, **yt-dlp**, and **FFmpeg**.

V0.3 extends GoDownloader from direct HTTP downloads into a complete media download system capable of extracting video metadata, format selection, video/audio stream merging, and persistent job management.

---

## 🚀 Key Features

### 🎬 Media Downloads (V0.3)
- **Media URL Detection & Analysis**: Auto-detects media site links (YouTube, Vimeo, etc.) and extracts formats via `yt-dlp --dump-json`.
- **Interactive Format Selector**: Select resolution (1080p, 720p, etc.), codec, or audio-only options with estimated file sizes before downloading.
- **FFmpeg Post-Processing**: Automatically merges separate video and audio streams using `ffmpeg` with live status reporting (`processing` state).
- **Subprocess Security & Lifecycle**: Safe subprocess execution (`exec.CommandContext`) without shell invocation. Context cancellation cleans up orphan processes.
- **Rich Media UI**: Displays video thumbnails, titles, duration, and progress.

### ⚡ Direct Downloads & Core System (V0.1 / V0.2)
- **Engine Registry**: Auto-routes direct HTTP files to `aria2c` and media URLs to `yt-dlp`.
- **Unified State Machine**: Centralized state validation (`queued`, `analyzing`, `downloading`, `processing`, `paused`, `completed`, `failed`, `cancelled`).
- **Pause & Resume**: Pause and resume direct `aria2c` downloads seamlessly.
- **Retry Mechanism**: Retry failed downloads with fresh engine executions while preserving job history.
- **Backend Restart Reconciliation**: Restarting the Go backend re-attaches active `aria2c` downloads and gracefully fails interrupted media subprocesses with a 1-click retry option.
- **Real-time SSE Streaming**: Server-Sent Events stream live progress, speed, completed bytes, size, and ETA directly to the UI.

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
   ┌─────────┴─────────┐
   ▼                   ▼
aria2 Adapter     yt-dlp Engine
   │                   │
   ▼                   ▼
aria2c daemon       yt-dlp subprocess ──→ FFmpeg / ffprobe
   │                   │
   └─────────┬─────────┘
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

---

## 🚦 Quick Start

### 1. Start the aria2c Daemon

```bash
aria2c --enable-rpc --rpc-listen-all=false --rpc-listen-port=6800 --rpc-allow-origin-all
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
# Terminal 1: aria2 daemon
aria2c --enable-rpc --rpc-listen-all=false --rpc-listen-port=6800 --rpc-allow-origin-all

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
| `POST` | `/api/v1/jobs` | Create a new download job (`{"source": "https://..."}`) |
| `POST` | `/api/v1/analyze` | Analyze a media URL and return formats (`{"source": "https://..."}`) |
| `POST` | `/api/v1/jobs/{id}/format` | Select format for media job (`{"formatId": "..."}`) |
| `GET`  | `/api/v1/jobs` | List all historical and active jobs |
| `GET`  | `/api/v1/jobs/{id}` | Get details of a specific job |
| `POST` | `/api/v1/jobs/{id}/pause` | Pause an active direct download |
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
| `DOWNLOAD_DIR` | `./downloads` | Destination folder for downloads |
| `ARIA2_RPC_URL` | `http://localhost:6800/jsonrpc` | aria2 RPC endpoint |
| `ARIA2_SECRET` | *(empty)* | RPC secret token |
| `YTDLP_PATH` | `yt-dlp` | Executable path for yt-dlp |
| `FFMPEG_PATH` | `ffmpeg` | Executable path for FFmpeg |
| `WEB_DIR` | `./web/dist` | Static web files directory |

---

## 🧪 Testing

Run backend test suite and race detector checks:

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
│   └── server/main.go          # Application entry point & graceful shutdown
├── internal/
│   ├── api/                    # HTTP router & handlers (/jobs, /analyze)
│   ├── config/                 # App configuration & env vars
│   ├── database/               # SQLite storage & schema migrations (v1-v3)
│   ├── engine/                 # Engine Registry, Resolver, & Adapters
│   │   ├── aria2/              # aria2 RPC client
│   │   └── ytdlp/              # yt-dlp runner, analyzer, & progress parser
│   ├── events/                 # Pub/Sub EventBus & SSE HTTP handler
│   └── job/                    # State machine, Manager, Monitor, & Recovery
├── web/                        # React + TypeScript + Vite frontend
│   └── src/
│       ├── components/         # DownloadForm, FormatSelector, JobCard, JobList
│       ├── api.ts              # REST client bindings
│       └── types.ts            # TypeScript interfaces
├── downloads/                  # Default download folder
├── go.mod
└── README.md
```
