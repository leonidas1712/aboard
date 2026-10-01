package delivery

import "sync"

// mailbox is a goroutine's queue of requests. put never blocks, so two goroutines that
// send each other requests can't deadlock; the owner drains it when ready fires.
type mailbox[T any] struct {
	mu     sync.Mutex
	items  []T
	signal chan struct{}
}

func newMailbox[T any]() *mailbox[T] {
	return &mailbox[T]{signal: make(chan struct{}, 1)}
}

func (m *mailbox[T]) put(v T) {
	m.mu.Lock()
	m.items = append(m.items, v)
	m.mu.Unlock()
	select {
	case m.signal <- struct{}{}:
	default:
	}
}

// ready fires when items may be waiting.
func (m *mailbox[T]) ready() <-chan struct{} { return m.signal }

// take returns every waiting item, oldest first.
func (m *mailbox[T]) take() []T {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.items
	m.items = nil
	return items
}
