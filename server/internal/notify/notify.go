// Package notify wakes readers waiting for changes on a board, or on another key the
// board package names (such as a human's list of boards), within one server process.
// It only signals that something changed; readers then re-read the store, so nothing
// is delivered from memory and a missed signal loses no data.
package notify

import "sync"

// InProcess is a board.Notifier for a single server process.
type InProcess struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

// NewInProcess returns a notifier with no watchers.
func NewInProcess() *InProcess { return &InProcess{chans: map[string]chan struct{}{}} }

// Watch returns a channel that is closed at the next change on the key. Call it before
// reading, so a change between the read and the wait isn't missed.
func (n *InProcess) Watch(key string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[key]
	if !ok {
		ch = make(chan struct{})
		n.chans[key] = ch
	}
	return ch
}

// Changed wakes everyone watching the key.
func (n *InProcess) Changed(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[key]; ok {
		close(ch)
		delete(n.chans, key)
	}
}
