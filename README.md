# GoDownloader V0.2 — Stable Job System

A high-performance, local-first download manager built with **Go**, **React (TypeScript + Vite)**, **SQLite**, and **aria2c**.

V0.2 upgrades direct downloads into a persistent, fault-tolerant job orchestration system capable of surviving backend restarts, browser refreshes, and network interruptions.

---

## 🚀 Key Features (V0.2)

- **Unified Job State Machine**: Centralized state validation (`queued`, `downloading`, `paused`, `completed`, `failed`, `cancelled`).
- **Pause & Resume**: Seamlessly pause active downloads and resume them without starting from scratch.
- **Retry Mechanism**: Retry failed downloads with fresh engine executions while preserving the original job identity and history.
- **Backend Restart Recovery**: Backend restarts automatically reconnect to active `aria2c` downloads and update SQLite job states.
- **Real-time SSE Streaming**: Server-Sent Events stream live progress, speed (B/s), completed bytes, total size, and ETA directly to the UI.
- **Decoupled Engine Architecture**: Engine interface abstracts `aria2c` JSON-RPC RPC calls away from the application domain model.
- **Normalized Error Messages**: Clear, human-readable errors for connection timeouts, DNS failures, 404s, and disk errors.

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
                        │   Job Service    │
                        └────────┬─────────┘
                                 │
                                 ▼
                        ┌──────────────────┐
                        │   Job Manager    │
                        │                  │
                        │ State Machine    │
                        │ Recovery         │
                        │ Retry            │
                        │ Progress Monitor │
                        └───────┬──┬───────┘
                                │  │
           ┌────────────────────┘  └────────────────────┐
           ▼                                            ▼
     SQLite Store                                   Event Bus
           │                                            │
           │                                            ▼
           │                                         SSE Stream
           ▼
     aria2 Adapter
           │
           ▼
     aria2c daemon
           │
           ▼
      Local File
```

---

## 🛠️ Prerequisites

- **Go** 1.23+
- **Node.js** 18+ & npm
- **aria2** installed and on PATH:
  - **Windows**: `winget install aria2` or download from [aria2.github.io](https://aria2.github.io/)
  - **macOS**: `brew install aria2`
  - **Linux**: `sudo apt install aria2`

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

# Run backend
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
| `GET` | `/api/v1/jobs` | List all historical and active jobs |
| `GET` | `/api/v1/jobs/{id}` | Get details of a specific job |
| `POST` | `/api/v1/jobs/{id}/pause` | Pause an active download |
| `POST` | `/api/v1/jobs/{id}/resume` | Resume a paused download |
| `POST` | `/api/v1/jobs/{id}/retry` | Retry a failed download |
| `POST` | `/api/v1/jobs/{id}/cancel` | Cancel a download |
| `GET` | `/api/v1/events` | SSE stream for real-time progress updates |

---

## ⚙️ Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8080` | Server listen address |
| `DOWNLOAD_DIR` | `./downloads` | Destination folder for downloads |
| `ARIA2_RPC_URL` | `http://localhost:6800/jsonrpc` | aria2 RPC endpoint |
| `ARIA2_SECRET` | *(empty)* | RPC secret token |
| `WEB_DIR` | `./web/dist` | Static web files directory |

---

## 🧪 Testing

Run backend tests and race detector checks:

```bash
# Run unit & integration tests
go test -v ./...

# Run race condition checks
go test -race ./...
```

---

## 📁 Project Structure

```text
GoDownloader/
├── cmd/
│   └── server/main.go          # Application entry point & graceful shutdown
├── internal/
│   ├── api/                    # HTTP router & handlers
│   ├── config/                 # App configuration
│   ├── database/               # SQLite storage & JobRepository
│   ├── engine/                 # Engine interface & aria2 RPC client
│   │   └── aria2/              # aria2 RPC client & status mapper
│   ├── events/                 # Pub/Sub EventBus & SSE HTTP handler
│   └── job/                    # State machine, Manager, Monitor, & Recovery
├── web/                        # React + TypeScript + Vite frontend
├── downloads/                  # Default download folder
├── go.mod
└── README.md
```
