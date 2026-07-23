package events

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"downloader/internal/job"
)

// SSEHandler serves Server-Sent Events to connected clients.
// It subscribes to the EventBus and forwards events.
type SSEHandler struct {
	bus job.EventBus
}

// NewSSEHandler creates a new SSE handler connected to the event bus.
func NewSSEHandler(bus job.EventBus) *SSEHandler {
	return &SSEHandler{bus: bus}
}

// ServeHTTP implements http.Handler for SSE connections.
func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Subscribe to events
	ch := h.bus.Subscribe()
	defer h.bus.Unsubscribe(ch)

	// Send initial heartbeat
	fmt.Fprintf(w, ": heartbeat\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Job)
			if err != nil {
				log.Printf("SSE: failed to marshal event: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}
