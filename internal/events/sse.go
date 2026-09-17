package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"downloader/internal/job"
)

// CursorProvider provides the current authoritative cursor watermark.
type CursorProvider interface {
	GetCurrentCursor(ctx context.Context) int64
}

// ReplayProvider provides bounded event replay functionality.
type ReplayProvider interface {
	GetEventsAfter(cursor int64) ([]job.Event, bool)
	LatestSequence() int64
}

// SyncRequiredPayload is emitted when event continuity cannot be proven.
type SyncRequiredPayload struct {
	Cursor int64  `json:"cursor"`
	Reason string `json:"reason"`
}

// SSEHandler serves Server-Sent Events to connected clients.
// It subscribes to the IEventBus and forwards events with replay support.
type SSEHandler struct {
	bus            job.IEventBus
	cursorProvider CursorProvider
}

// NewSSEHandler creates a new SSE handler connected to the event bus.
func NewSSEHandler(bus job.IEventBus) *SSEHandler {
	return &SSEHandler{bus: bus}
}

// SetCursorProvider wires the authoritative cursor provider.
func (h *SSEHandler) SetCursorProvider(cp CursorProvider) {
	h.cursorProvider = cp
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

	ctx := r.Context()

	// Parse client cursor from Last-Event-ID header or cursor query parameter.
	var clientCursor int64 = -1
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if seq, err := strconv.ParseInt(strings.TrimSpace(lastID), 10, 64); err == nil {
			clientCursor = seq
		}
	}
	if qCursor := r.URL.Query().Get("cursor"); qCursor != "" {
		if seq, err := strconv.ParseInt(strings.TrimSpace(qCursor), 10, 64); err == nil {
			clientCursor = seq
		}
	}

	// Subscribe to event bus FIRST so we don't miss live events while replaying
	ch := h.bus.Subscribe()
	defer h.bus.Unsubscribe(ch)

	// Send initial heartbeat
	fmt.Fprintf(w, ": heartbeat\n\n")
	flusher.Flush()

	var lastSentSeq int64 = 0

	// Handle replay if client requested a cursor >= 0
	if clientCursor >= 0 {
		var currentCursor int64 = 0
		if h.cursorProvider != nil {
			currentCursor = h.cursorProvider.GetCurrentCursor(ctx)
		} else if rp, ok := h.bus.(ReplayProvider); ok {
			currentCursor = rp.LatestSequence()
		}

		if clientCursor == currentCursor {
			// Client is up to date
			lastSentSeq = currentCursor
		} else if clientCursor > currentCursor {
			// Client cursor ahead of server - terminate stream immediately
			h.emitSyncRequired(w, flusher, currentCursor, "cursor_ahead_of_server")
			return
		} else {
			// clientCursor < currentCursor -> try replay from buffer
			replayed := false
			if rp, ok := h.bus.(ReplayProvider); ok {
				events, ok := rp.GetEventsAfter(clientCursor)
				if ok && (len(events) > 0 && events[len(events)-1].Sequence >= currentCursor) {
					for _, ev := range events {
						h.writeEvent(w, ev)
						if ev.Sequence > lastSentSeq {
							lastSentSeq = ev.Sequence
						}
					}
					flusher.Flush()
					replayed = true
				}
			}

			if !replayed {
				// Replay gap, buffer overflow, or restart - terminate stream immediately
				h.emitSyncRequired(w, flusher, currentCursor, "replay_gap_or_buffer_overflow")
				return
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.Sequence > 0 {
				// Detect slow subscriber sequence drops - terminate stream immediately
				if lastSentSeq > 0 && event.Sequence > lastSentSeq+1 {
					h.emitSyncRequired(w, flusher, event.Sequence, "slow_subscriber_gap_detected")
					return
				}
				if event.Sequence <= lastSentSeq {
					continue // Skip already replayed event
				}
				lastSentSeq = event.Sequence
			}
			h.writeEvent(w, event)
			flusher.Flush()
		}
	}
}

func (h *SSEHandler) writeEvent(w io.Writer, event job.Event) {
	payload := any(event.Job)
	if event.Data != nil {
		payload = event.Data
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("SSE: failed to marshal event: %v", err)
		return
	}
	if event.Sequence > 0 {
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	} else {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
	}
}

func (h *SSEHandler) emitSyncRequired(w io.Writer, flusher http.Flusher, cursor int64, reason string) {
	payload := SyncRequiredPayload{
		Cursor: cursor,
		Reason: reason,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(`{}`)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", job.EventSyncRequired, data)
	if flusher != nil {
		flusher.Flush()
	}
}
