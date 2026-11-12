package dispatch

import (
	"sync"
	"time"
)

// Event is one message on the live bus. The UI consumes these over SSE.
type Event struct {
	Type string
	Data any
	At   time.Time
}

// Bus is a fan-out pub/sub with drop-on-slow-consumer semantics: dispatch must
// never block because one browser tab stopped reading. Each subscriber gets a
// buffered channel; if it is full the event is dropped for that subscriber
// only, and the next fleet tick re-synchronises the UI anyway.
type Bus struct {
	mu     sync.Mutex
	subs   map[chan Event]struct{}
	drops  uint64
	subSeq uint64
}

// NewBus creates an empty bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[chan Event]struct{})}
}

// Subscribe returns a channel and a cancel function.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.subSeq++
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

// Publish delivers an event to every subscriber without blocking.
func (b *Bus) Publish(typ string, data any) {
	ev := Event{Type: typ, Data: data, At: time.Now()}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.drops++
		}
	}
}

// Stats reports subscriber count and dropped deliveries, useful when debugging
// a lagging browser tab.
func (b *Bus) Stats() (subs int, drops uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs), b.drops
}
