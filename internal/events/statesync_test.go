package events

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/job"
)

type staticCursorProvider struct {
	cursor int64
}

func (p *staticCursorProvider) GetCurrentCursor(ctx context.Context) int64 {
	return atomic.LoadInt64(&p.cursor)
}

func (p *staticCursorProvider) SetCursor(c int64) {
	atomic.StoreInt64(&p.cursor, c)
}

func TestReplayBuffer_Basic(t *testing.T) {
	rb := NewReplayBuffer(10)

	// Ignored if Sequence <= 0
	rb.Push(job.Event{Sequence: 0, Type: "job.updated"})
	if rb.Len() != 0 {
		t.Fatalf("expected 0 events, got %d", rb.Len())
	}

	rb.Push(job.Event{Sequence: 1, Type: "job.created"})
	rb.Push(job.Event{Sequence: 2, Type: "job.updated"})
	rb.Push(job.Event{Sequence: 3, Type: "job.completed"})

	if rb.Len() != 3 {
		t.Fatalf("expected 3 events, got %d", rb.Len())
	}
	if rb.OldestSequence() != 1 {
		t.Fatalf("expected oldest 1, got %d", rb.OldestSequence())
	}
	if rb.LatestSequence() != 3 {
		t.Fatalf("expected latest 3, got %d", rb.LatestSequence())
	}

	// Query exactly at latest sequence -> returns empty slice, true
	events, ok := rb.GetEventsAfter(3)
	if !ok || len(events) != 0 {
		t.Fatalf("expected empty slice with ok=true, got ok=%v len=%d", ok, len(events))
	}

	// Query from beginning (cursor = 0) -> returns events 1, 2, 3
	events, ok = rb.GetEventsAfter(0)
	if !ok || len(events) != 3 {
		t.Fatalf("expected 3 events, got ok=%v len=%d", ok, len(events))
	}
	if events[0].Sequence != 1 || events[1].Sequence != 2 || events[2].Sequence != 3 {
		t.Fatalf("unexpected sequences: %v, %v, %v", events[0].Sequence, events[1].Sequence, events[2].Sequence)
	}

	// Query cursor 1 -> returns events 2, 3
	events, ok = rb.GetEventsAfter(1)
	if !ok || len(events) != 2 {
		t.Fatalf("expected 2 events, got ok=%v len=%d", ok, len(events))
	}
	if events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("unexpected sequences: %v, %v", events[0].Sequence, events[1].Sequence)
	}

	// Query future cursor (cursor = 10) -> returns nil, false
	events, ok = rb.GetEventsAfter(10)
	if ok || events != nil {
		t.Fatalf("expected ok=false for future cursor, got ok=%v", ok)
	}
}

func TestReplayBuffer_EvictionAndOverflow(t *testing.T) {
	// Small buffer of capacity 3
	rb := NewReplayBuffer(3)

	for i := int64(1); i <= 6; i++ {
		rb.Push(job.Event{Sequence: i, Type: "job.updated"})
	}

	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}
	// Buffer should contain sequences 4, 5, 6
	if rb.OldestSequence() != 4 {
		t.Fatalf("expected oldest 4, got %d", rb.OldestSequence())
	}
	if rb.LatestSequence() != 6 {
		t.Fatalf("expected latest 6, got %d", rb.LatestSequence())
	}

	// Cursor 3: boundary condition (3 == 4 - 1). Can replay 4, 5, 6
	events, ok := rb.GetEventsAfter(3)
	if !ok || len(events) != 3 {
		t.Fatalf("expected 3 events for cursor 3, got ok=%v len=%d", ok, len(events))
	}

	// Cursor 2: evicted! Oldest is 4, 2 < 4-1. Should return false
	events, ok = rb.GetEventsAfter(2)
	if ok || events != nil {
		t.Fatalf("expected ok=false for evicted cursor 2, got ok=%v", ok)
	}

	// Cursor 0: evicted! Should return false
	events, ok = rb.GetEventsAfter(0)
	if ok || events != nil {
		t.Fatalf("expected ok=false for evicted cursor 0, got ok=%v", ok)
	}
}

func TestSSEHandler_ClientUpToDate(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 5}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Publish events 1..5
	for i := int64(1); i <= 5; i++ {
		bus.Publish(job.Event{Sequence: i, Type: job.EventJobUpdated, Job: job.Job{ID: fmt.Sprintf("job-%d", i)}})
	}

	// Client connects with cursor=5 (already up to date)
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=5", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	// Allow initial flush
	time.Sleep(50 * time.Millisecond)

	// Publish live event 6
	bus.Publish(job.Event{Sequence: 6, Type: job.EventJobUpdated, Job: job.Job{ID: "job-6"}})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	// Should NOT contain sync.required
	if strings.Contains(body, "sync.required") {
		t.Fatalf("unexpected sync.required in body: %s", body)
	}
	// Should NOT contain replayed event 5
	if strings.Contains(body, "id: 5\n") {
		t.Fatalf("unexpected replayed event 5: %s", body)
	}
	// Should contain live event 6 with id: 6
	if !strings.Contains(body, "id: 6\n") || !strings.Contains(body, "job-6") {
		t.Fatalf("expected live event 6 in body: %s", body)
	}
}

func TestSSEHandler_ReplayWithinWindow(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 5}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Publish events 1..5
	for i := int64(1); i <= 5; i++ {
		bus.Publish(job.Event{Sequence: i, Type: job.EventJobUpdated, Job: job.Job{ID: fmt.Sprintf("job-%d", i)}})
	}

	// Client connects with Last-Event-ID: 3
	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", "3")
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Publish live event 6
	cursorProv.SetCursor(6)
	bus.Publish(job.Event{Sequence: 6, Type: job.EventJobUpdated, Job: job.Job{ID: "job-6"}})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	// Should replay 4 and 5
	if !strings.Contains(body, "id: 4\n") || !strings.Contains(body, "job-4") {
		t.Fatalf("missing replayed event 4 in body: %s", body)
	}
	if !strings.Contains(body, "id: 5\n") || !strings.Contains(body, "job-5") {
		t.Fatalf("missing replayed event 5 in body: %s", body)
	}
	// Should stream 6
	if !strings.Contains(body, "id: 6\n") || !strings.Contains(body, "job-6") {
		t.Fatalf("missing live event 6 in body: %s", body)
	}
	// Should NOT contain 3
	if strings.Contains(body, "id: 3\n") {
		t.Fatalf("unexpected event 3 in body: %s", body)
	}
}

func TestSSEHandler_BufferOverflow_EmitsSyncRequired(t *testing.T) {
	bus := NewInMemoryBusWithCapacity(3)
	cursorProv := &staticCursorProvider{cursor: 10}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Push events 8, 9, 10 into small buffer (capacity 3)
	for i := int64(8); i <= 10; i++ {
		bus.Publish(job.Event{Sequence: i, Type: job.EventJobUpdated, Job: job.Job{ID: fmt.Sprintf("job-%d", i)}})
	}

	// Client requests cursor 2 (long evicted from buffer)
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=2", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: sync.required") {
		t.Fatalf("expected sync.required event, got: %s", body)
	}
	if !strings.Contains(body, `"cursor":10`) {
		t.Fatalf("expected cursor 10 in sync.required payload, got: %s", body)
	}
}

func TestSSEHandler_ServerRestart_EmptyBuffer_EmitsSyncRequired(t *testing.T) {
	bus := NewInMemoryBus()
	// Server restarted: DB cursor is at 25, but in-memory buffer has 0 events!
	cursorProv := &staticCursorProvider{cursor: 25}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Client connects with cursor 20
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=20", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: sync.required") {
		t.Fatalf("expected sync.required event on restart with empty buffer, got: %s", body)
	}
}

func TestSSEHandler_CursorAhead_EmitsSyncRequired(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 5}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Client has cursor 999 (ahead of server 5)
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=999", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: sync.required") {
		t.Fatalf("expected sync.required for future cursor, got: %s", body)
	}
	if !strings.Contains(body, "cursor_ahead_of_server") {
		t.Fatalf("expected cursor_ahead_of_server reason, got: %s", body)
	}
}

func TestSSEHandler_ProgressTicksUnsequenced(t *testing.T) {
	bus := NewInMemoryBus()
	handler := NewSSEHandler(bus)

	req := httptest.NewRequest("GET", "/api/v1/events", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Progress tick: Sequence == 0
	bus.Publish(job.Event{
		Sequence: 0,
		Type:     job.EventJobUpdated,
		Job: job.Job{
			ID:       "job-1",
			Progress: 42.5,
		},
	})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	// Should contain event: job.updated
	if !strings.Contains(body, "event: job.updated\n") {
		t.Fatalf("expected job.updated event in body: %s", body)
	}
	// Must NOT contain id:
	if strings.Contains(body, "id:") {
		t.Fatalf("expected NO id: for unsequenced progress tick, got: %s", body)
	}
}

func TestSSEHandler_SlowSubscriber_GapDetected(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 1}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Client starts at cursor 1
	bus.Publish(job.Event{Sequence: 1, Type: job.EventJobCreated, Job: job.Job{ID: "job-1"}})

	req := httptest.NewRequest("GET", "/api/v1/events?cursor=1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Publish sequence 5 directly (simulating dropped 2, 3, 4 due to slow subscriber channel buffer)
	cursorProv.SetCursor(5)
	bus.Publish(job.Event{Sequence: 5, Type: job.EventJobUpdated, Job: job.Job{ID: "job-5"}})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	// Must detect gap (5 > 1 + 1) and emit sync.required
	if !strings.Contains(body, "event: sync.required") {
		t.Fatalf("expected sync.required on sequence gap, got: %s", body)
	}
	if !strings.Contains(body, "slow_subscriber_gap_detected") {
		t.Fatalf("expected slow_subscriber_gap_detected reason, got: %s", body)
	}
}

func parseSSEEvents(raw string) []map[string]string {
	var events []map[string]string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	current := make(map[string]string)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(current) > 0 {
				events = append(events, current)
				current = make(map[string]string)
			}
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			current[parts[0]] = parts[1]
		}
	}
	if len(current) > 0 {
		events = append(events, current)
	}
	return events
}

func TestSSEHandler_TerminatesAndUnsubscribesOnSyncRequired(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 5}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Publish sequences 1..5
	for i := int64(1); i <= 5; i++ {
		bus.Publish(job.Event{Sequence: i, Type: job.EventJobUpdated, Job: job.Job{ID: fmt.Sprintf("job-%d", i)}})
	}

	// 1. Test future cursor terminates stream and cleans up subscription immediately
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=99", nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		// Handler terminated on its own without context cancel
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not terminate immediately after emitting sync.required")
	}

	bus.mu.RLock()
	subCount := len(bus.subscribers)
	bus.mu.RUnlock()
	if subCount != 0 {
		t.Fatalf("expected 0 active subscribers after stream termination, got %d", subCount)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: sync.required") {
		t.Fatalf("expected sync.required in response body: %s", body)
	}
}

func TestSSEHandler_ReplaySubscriptionRace_NoMissingEvents(t *testing.T) {
	bus := NewInMemoryBus()
	cursorProv := &staticCursorProvider{cursor: 2}
	handler := NewSSEHandler(bus)
	handler.SetCursorProvider(cursorProv)

	// Initial buffer: seq 1, 2
	bus.Publish(job.Event{Sequence: 1, Type: job.EventJobCreated, Job: job.Job{ID: "job-1"}})
	bus.Publish(job.Event{Sequence: 2, Type: job.EventJobUpdated, Job: job.Job{ID: "job-2"}})

	// Connect with cursor 1
	req := httptest.NewRequest("GET", "/api/v1/events?cursor=1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	// Concurrently publish seq 3 right around replay inspection
	cursorProv.SetCursor(3)
	bus.Publish(job.Event{Sequence: 3, Type: job.EventJobUpdated, Job: job.Job{ID: "job-3"}})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	events := parseSSEEvents(rec.Body.String())
	seenIDs := make(map[string]int)
	for _, ev := range events {
		if id, ok := ev["id"]; ok {
			seenIDs[id]++
		}
	}

	// Must have received seq 2 and seq 3
	if seenIDs["2"] == 0 {
		t.Fatalf("expected seq 2 in delivered events: %v", seenIDs)
	}
	if seenIDs["3"] == 0 {
		t.Fatalf("expected seq 3 in delivered events: %v", seenIDs)
	}
	// Duplicates are filtered by SSEHandler (lastSentSeq) so each sequenced event appears exactly once
	if seenIDs["2"] > 1 {
		t.Fatalf("expected at most 1 occurrence of seq 2, got %d", seenIDs["2"])
	}
	if seenIDs["3"] > 1 {
		t.Fatalf("expected at most 1 occurrence of seq 3, got %d", seenIDs["3"])
	}
}
