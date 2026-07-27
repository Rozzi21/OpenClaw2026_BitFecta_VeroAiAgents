package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	CreatedAt time.Time   `json:"created_at"`
}

type Bus struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

func NewBus() *Bus {
	return &Bus{clients: make(map[chan Event]struct{})}
}

func (b *Bus) Subscribe() chan Event {
	ch := make(chan Event, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe melepas subscriber dari bus. Channel SENGAJA tidak ditutup:
// Publish mengirim di bawah RLock sehingga close(ch) bisa berpacu dengan send
// dan memicu "panic: send on closed channel" (BUG-2). Setelah dihapus dari map,
// bus tidak lagi mengakses channel; subscriber berhenti via context request dan
// sisa event di-buffer di-GC.
func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *Bus) Publish(eventType string, payload interface{}) {
	event := Event{
		ID:        uuid.NewString(),
		Type:      eventType,
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
		}
	}
}
