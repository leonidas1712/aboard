package board

import "sync"

// notifier wakes readers waiting for new events on a board. It only signals that
// something changed; readers then re-read the store, so nothing is delivered from memory.
type notifier struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

func newNotifier() *notifier { return &notifier{chans: map[string]chan struct{}{}} }

// watch returns a channel that is closed at the next change on the board. Call it before
// reading, so a change between the read and the wait isn't missed.
func (n *notifier) watch(boardID string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[boardID]
	if !ok {
		ch = make(chan struct{})
		n.chans[boardID] = ch
	}
	return ch
}

// changed wakes everyone watching the board.
func (n *notifier) changed(boardID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[boardID]; ok {
		close(ch)
		delete(n.chans, boardID)
	}
}
