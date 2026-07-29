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
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/tracker"
)

// NewRouter sets up the HTTP router with API routes, SSE, and static file serving.
func NewRouter(cfg *config.Config, manager *job.Manager, sseHandler *events.SSEHandler, settingsService *settings.SettingsService, dependencies ...any) http.Handler {
	r := mux.NewRouter()
	h := NewHandler(manager, settingsService)
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case storage.ICategoryRepository:
			h.SetCategoryRepository(value)
		case *tracker.Service:
			h.SetTrackerService(value)
		}
	}

	// API routes
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/jobs/batch", h.CreateBatchJobs).Methods("POST")
	api.HandleFunc("/jobs/bulk", h.BulkAction).Methods("POST")
	api.HandleFunc("/jobs", h.CreateJob).Methods("POST")
	api.HandleFunc("/jobs", h.GetJobs).Methods("GET")
	api.HandleFunc("/jobs/torrent", h.CreateTorrentJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/torrent/files", h.GetTorrentFiles).Methods("GET")
	api.HandleFunc("/jobs/{id}/torrent/start", h.StartTorrent).Methods("POST")
	api.HandleFunc("/jobs/{id}/torrent/trackers", h.AddTorrentTrackers).Methods("POST")
	api.HandleFunc("/jobs/{id}/torrent/seeding-policy", h.UpdateSeedingPolicy).Methods("PUT")
	api.HandleFunc("/jobs/{id}/stop-seeding", h.StopSeeding).Methods("POST")
	api.HandleFunc("/jobs/{id}/network", h.UpdateJobNetwork).Methods("PUT")
	api.HandleFunc("/jobs/{id}/capabilities", h.GetJobCapabilities).Methods("GET")
	api.HandleFunc("/jobs/{id}/priority", h.SetJobPriority).Methods("PUT")
	api.HandleFunc("/jobs/{id}", h.GetJob).Methods("GET")
	api.HandleFunc("/jobs/{id}/pause", h.PauseJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/resume", h.ResumeJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/retry", h.RetryJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/cancel", h.CancelJob).Methods("POST")
	api.HandleFunc("/jobs/{id}/format", h.SelectFormat).Methods("POST")

	api.HandleFunc("/categories", h.GetCategories).Methods("GET")
	api.HandleFunc("/categories", h.CreateCategory).Methods("POST")
	api.HandleFunc("/categories/{id}", h.UpdateCategory).Methods("PUT")
	api.HandleFunc("/categories/{id}", h.DeleteCategory).Methods("DELETE")

	api.HandleFunc("/queue", h.GetQueueSnapshot).Methods("GET")
	api.HandleFunc("/queue/reorder", h.ReorderQueue).Methods("PUT")
	api.HandleFunc("/settings", h.GetSettings).Methods("GET")
	api.HandleFunc("/settings", h.UpdateSettings).Methods("PUT")
	api.HandleFunc("/capabilities", h.GetCapabilities).Methods("GET")
	api.HandleFunc("/capabilities/resolve", h.ResolveCapabilities).Methods("POST")
	api.HandleFunc("/tracker-sources", h.GetTrackerSources).Methods("GET")
	api.HandleFunc("/tracker-sources", h.CreateTrackerSource).Methods("POST")
	api.HandleFunc("/tracker-sources/refresh", h.RefreshAllTrackerSources).Methods("POST")
	api.HandleFunc("/tracker-sources/{id}", h.UpdateTrackerSource).Methods("PUT")
	api.HandleFunc("/tracker-sources/{id}", h.DeleteTrackerSource).Methods("DELETE")
	api.HandleFunc("/tracker-sources/{id}/refresh", h.RefreshTrackerSource).Methods("POST")

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

	// CORS - local same-origin & local dev origins only
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "http://127.0.0.1:5173", "http://localhost:8080", "http://127.0.0.1:8080"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
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
