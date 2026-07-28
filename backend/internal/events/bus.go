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

// BUG-4: batas maksimal subscriber SSE aktif. Mencegah map clients tumbuh tak
// terbatas saat banyak koneksi zombie (koneksi setengah-putus yang tidak pernah
// mengirim FIN dan tidak cepat terdeteksi). Cap 100 cukup untuk operasi
// backoffice single-instance; client EventSource reconnect otomatis bila
// ditolak.
const MaxSubscribers = 100

func NewBus() *Bus {
	return &Bus{clients: make(map[chan Event]struct{})}
}

// Subscribe mendaftarkan subscriber baru. Mengembalikan ok=false bila jumlah
// subscriber sudah mencapai batas (BUG-4): tanpa cap, tiap koneksi SSE zombie
// menambah satu channel buffered ke map tanpa pernah berkurang → leak memori +
// goroutine. Caller (EventStream) menolak koneksi baru bila ok=false.
func (b *Bus) Subscribe() (ch chan Event, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.clients) >= MaxSubscribers {
		return nil, false
	}
	ch = make(chan Event, 32)
	b.clients[ch] = struct{}{}
	return ch, true
}

// SubscriberCount dipakai handler/logging untuk observasi beban subscriber.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
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
