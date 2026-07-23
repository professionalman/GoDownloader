package api

import (
	"net/http"
	"os/exec"
	"runtime"

	"github.com/gorilla/mux"
	"github.com/rs/cors"

	"downloader/internal/config"
	"downloader/internal/events"
	"downloader/internal/job"
)

// NewRouter sets up the HTTP router with API routes, SSE, and static file serving.
func NewRouter(cfg *config.Config, manager *job.Manager, sseHandler *events.SSEHandler) http.Handler {
	r := mux.NewRouter()
	h := NewHandler(manager)

	// API routes
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/jobs", h.CreateJob).Methods("POST")
	api.HandleFunc("/jobs", h.GetJobs).Methods("GET")
	api.HandleFunc("/jobs/{id}", h.GetJob).Methods("GET")
	api.HandleFunc("/jobs/{id}/pause", h.PauseJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/resume", h.ResumeJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/retry", h.RetryJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/cancel", h.CancelJob).Methods("POST")
	api.Handle("/events", sseHandler).Methods("GET")

	// Config endpoint
	api.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"downloadDir": cfg.DownloadDir,
		})
	}).Methods("GET")

	// Open downloads folder
	api.HandleFunc("/open-folder", func(w http.ResponseWriter, r *http.Request) {
		openFolder(cfg.DownloadDir)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}).Methods("POST")

	// Serve static frontend files
	spa := spaHandler{staticPath: cfg.WebDir, indexPath: "index.html"}
	r.PathPrefix("/").Handler(spa)

	// CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
	})

	return c.Handler(r)
}

// spaHandler serves the React SPA.
type spaHandler struct {
	staticPath string
	indexPath  string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

// openFolder opens the given directory in the system file explorer.
func openFolder(dir string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	cmd.Start()
}
