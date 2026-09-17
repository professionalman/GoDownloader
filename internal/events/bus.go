package events

import (
	"sync"

	"downloader/internal/job"
)

var _ job.IEventBus = (*InMemoryBus)(nil)

// ReplayBuffer is a thread-safe circular/bounded buffer for sequenced events.
type ReplayBuffer struct {
	mu       sync.RWMutex
	capacity int
	events   []job.Event
}

// NewReplayBuffer creates a new replay buffer with the given capacity.
func NewReplayBuffer(capacity int) *ReplayBuffer {
	if capacity <= 0 {
		capacity = 256
	}
	return &ReplayBuffer{
		capacity: capacity,
		events:   make([]job.Event, 0, capacity),
	}
}

// Push adds a sequenced event to the buffer. Events with Sequence <= 0 are ignored.
func (rb *ReplayBuffer) Push(event job.Event) {
	if event.Sequence <= 0 {
		return
	}
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.events) >= rb.capacity {
		copy(rb.events, rb.events[1:])
		rb.events[len(rb.events)-1] = event
	} else {
		rb.events = append(rb.events, event)
	}
}

// GetEventsAfter returns all contiguous events with sequence > cursor up to the latest sequence.
// Returns (events, true) on success.
// If cursor == newestSeq, returns ([], true).
// If cursor cannot be contiguously bridged (older than buffer or gap or ahead), returns (nil, false).
func (rb *ReplayBuffer) GetEventsAfter(cursor int64) ([]job.Event, bool) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if len(rb.events) == 0 {
		return nil, false
	}

	oldestSeq := rb.events[0].Sequence
	newestSeq := rb.events[len(rb.events)-1].Sequence

	if cursor == newestSeq {
		return []job.Event{}, true
	}

	if cursor > newestSeq {
		return nil, false
	}

	if cursor < oldestSeq-1 {
		return nil, false
	}

	startIdx := -1
	for i, ev := range rb.events {
		if ev.Sequence == cursor+1 {
			startIdx = i
			break
		}
	}

	if startIdx == -1 {
		return nil, false
	}

	// Verify contiguous sequencing
	for i := startIdx; i < len(rb.events)-1; i++ {
		if rb.events[i+1].Sequence != rb.events[i].Sequence+1 {
			return nil, false
		}
	}

	result := make([]job.Event, len(rb.events)-startIdx)
	copy(result, rb.events[startIdx:])
	return result, true
}

// LatestSequence returns the sequence number of the most recent event in the buffer, or 0 if empty.
func (rb *ReplayBuffer) LatestSequence() int64 {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if len(rb.events) == 0 {
		return 0
	}
	return rb.events[len(rb.events)-1].Sequence
}

// OldestSequence returns the sequence number of the oldest event retained in the buffer, or 0 if empty.
func (rb *ReplayBuffer) OldestSequence() int64 {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if len(rb.events) == 0 {
		return 0
	}
	return rb.events[0].Sequence
}

// Len returns the current number of events stored in the buffer.
func (rb *ReplayBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return len(rb.events)
}

// Capacity returns the maximum capacity of the buffer.
func (rb *ReplayBuffer) Capacity() int {
	return rb.capacity
}

// InMemoryBus is an in-memory event bus with fan-out to subscribers and bounded replay support.
// It implements the job.IEventBus interface.
type InMemoryBus struct {
	mu          sync.RWMutex
	subscribers map[<-chan job.Event]chan job.Event
	replay      *ReplayBuffer
}

// NewInMemoryBus creates a new in-memory event bus with default replay capacity (256).
func NewInMemoryBus() *InMemoryBus {
	return NewInMemoryBusWithCapacity(256)
}

// NewInMemoryBusWithCapacity creates a new in-memory event bus with specified replay buffer capacity.
func NewInMemoryBusWithCapacity(capacity int) *InMemoryBus {
	return &InMemoryBus{
		subscribers: make(map[<-chan job.Event]chan job.Event),
		replay:      NewReplayBuffer(capacity),
	}
}

// Publish sends an event to all subscribers and records sequenced events into the replay buffer.
func (b *InMemoryBus) Publish(event job.Event) {
	if event.Sequence > 0 && b.replay != nil {
		b.replay.Push(event)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// subscriber too slow, skip
		}
	}
}

// Subscribe returns a channel that receives events.
func (b *InMemoryBus) Subscribe() <-chan job.Event {
	ch := make(chan job.Event, 64)
	b.mu.Lock()
	b.subscribers[ch] = ch
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *InMemoryBus) Unsubscribe(ch <-chan job.Event) {
	b.mu.Lock()
	if writeCh, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(writeCh)
	}
	b.mu.Unlock()
}

// GetEventsAfter returns all contiguous buffered events with sequence > cursor.
func (b *InMemoryBus) GetEventsAfter(cursor int64) ([]job.Event, bool) {
	if b.replay == nil {
		return nil, false
	}
	return b.replay.GetEventsAfter(cursor)
}

// LatestSequence returns the highest sequence recorded in the replay buffer.
func (b *InMemoryBus) LatestSequence() int64 {
	if b.replay == nil {
		return 0
	}
	return b.replay.LatestSequence()
}

// ReplayBuffer returns the underlying replay buffer.
func (b *InMemoryBus) ReplayBuffer() *ReplayBuffer {
	return b.replay
}
