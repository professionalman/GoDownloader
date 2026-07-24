package events

import (
	"sync"

	"downloader/internal/job"
)

var _ job.IEventBus = (*InMemoryBus)(nil)

// InMemoryBus is a simple in-memory event bus with fan-out to subscribers.
// It implements the job.IEventBus interface.
type InMemoryBus struct {
	mu          sync.RWMutex
	subscribers map[<-chan job.Event]chan job.Event
}

// NewInMemoryBus creates a new in-memory event bus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{
		subscribers: make(map[<-chan job.Event]chan job.Event),
	}
}

// Publish sends an event to all subscribers.
func (b *InMemoryBus) Publish(event job.Event) {
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
