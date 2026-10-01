// Package notify wakes readers waiting for changes on a board, within one server
// process. It only signals that something changed; readers then re-read the store, so
// nothing is delivered from memory and a missed signal loses no data.
package notify

import "sync"

// InProcess is a board.Notifier for a single server process.
type InProcess struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

// NewInProcess returns a notifier with no watchers.
func NewInProcess() *InProcess { return &InProcess{chans: map[string]chan struct{}{}} }

// Watch returns a channel that is closed at the next change on the board. Call it before
// reading, so a change between the read and the wait isn't missed.
func (n *InProcess) Watch(boardID string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[boardID]
	if !ok {
		ch = make(chan struct{})
		n.chans[boardID] = ch
	}
	return ch
}

// Changed wakes everyone watching the board.
func (n *InProcess) Changed(boardID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[boardID]; ok {
		close(ch)
		delete(n.chans, boardID)
	}
}
