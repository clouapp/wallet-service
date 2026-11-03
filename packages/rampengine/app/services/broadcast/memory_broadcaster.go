package broadcast

import (
	"sync"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// hub is one fan-out: a per-user set of buffered channels guarded by one
// RWMutex. Both streams need identical machinery and differ only in payload,
// so it is written once and instantiated per event type — a second hand-rolled
// copy is where the two would drift on the next fix.
type hub[T any] struct {
	mu         sync.RWMutex
	subs       map[uint]map[chan T]struct{}
	bufferSize int
}

func newHub[T any](bufferSize int) hub[T] {
	return hub[T]{subs: make(map[uint]map[chan T]struct{}), bufferSize: bufferSize}
}

// publish delivers to every subscriber of userID, dropping rather than
// blocking. subject names the dropped event in the log — never the event
// itself, which may carry fields that do not belong in a log line.
func (h *hub[T]) publish(userID uint, event T, subject string) {
	// unsubscribe removes a channel from the map under the write lock and only
	// then closes it, so a publisher holding the read lock either sends before
	// the removal or never sees the channel.
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[userID] {
		select {
		case ch <- event:
		default:
			facades.Log().Warningf(
				"broadcast: dropping event for user %d (%s): subscriber buffer full",
				userID, subject,
			)
		}
	}
}

func (h *hub[T]) subscribe(userID uint) (<-chan T, func()) {
	ch := make(chan T, h.bufferSize)

	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = make(map[chan T]struct{})
	}
	h.subs[userID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[userID], ch)
			if len(h.subs[userID]) == 0 {
				delete(h.subs, userID)
			}
			h.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsubscribe
}

// MemoryBroadcaster is a single-process Broadcaster. Valid here because the
// Goravel queue worker (which drives webhook and KYC-submit publishes) runs
// inside the same process as the HTTP server. For horizontal scaling, replace
// with a Redis pub/sub implementation behind the same interface.
type MemoryBroadcaster struct {
	transactions hub[TransactionEvent]
	kyc          hub[KycEvent]
}

// NewMemoryBroadcaster builds the hub. bufferSize is the per-subscriber
// channel capacity; non-positive values fall back to 16.
func NewMemoryBroadcaster(bufferSize int) *MemoryBroadcaster {
	if bufferSize <= 0 {
		bufferSize = 16
	}
	return &MemoryBroadcaster{
		transactions: newHub[TransactionEvent](bufferSize),
		kyc:          newHub[KycEvent](bufferSize),
	}
}

func (b *MemoryBroadcaster) Publish(userID uint, event TransactionEvent) {
	b.transactions.publish(userID, event, "tx "+event.ID)
}

func (b *MemoryBroadcaster) Subscribe(userID uint) (<-chan TransactionEvent, func()) {
	return b.transactions.subscribe(userID)
}

func (b *MemoryBroadcaster) PublishKyc(userID uint, event KycEvent) {
	b.kyc.publish(userID, event, "kyc "+event.Provider)
}

func (b *MemoryBroadcaster) SubscribeKyc(userID uint) (<-chan KycEvent, func()) {
	return b.kyc.subscribe(userID)
}

var _ Broadcaster = (*MemoryBroadcaster)(nil)
