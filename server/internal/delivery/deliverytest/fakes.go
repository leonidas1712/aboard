package deliverytest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// FakeAdapter is a harness for tests. It records every bundle handed to it and can be
// told to fail, to be busy, or to run a function right after handing a bundle over (to
// stop the daemon there, as a crash would).
type FakeAdapter struct {
	harness string
	idle    bool

	mu       sync.Mutex
	handed   map[string][]string
	attempts int
	fail     error
	busy     bool
	absent   map[string]bool
	subAgent map[string]bool
	onHand   func()
	notify   chan struct{}
}

var _ delivery.Adapter = (*FakeAdapter)(nil)

// NewFakeAdapter returns a fake harness. With waitsForIdle it hands bundles only to a
// waiting hook, like Claude Code; otherwise it queues them, like Codex.
func NewFakeAdapter(harness string, waitsForIdle bool) *FakeAdapter {
	return &FakeAdapter{
		harness: harness, idle: waitsForIdle, handed: map[string][]string{},
		absent: map[string]bool{}, subAgent: map[string]bool{}, notify: make(chan struct{}, 1),
	}
}

// Harness returns the harness name the fake was made with.
func (f *FakeAdapter) Harness() string { return f.harness }

// WaitsForIdle reports the mode the fake was made with.
func (f *FakeAdapter) WaitsForIdle() bool { return f.idle }

// Validate refuses sessions marked absent or sub-agent.
func (f *FakeAdapter) Validate(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.absent[id]:
		return delivery.ErrTargetAbsent
	case f.subAgent[id]:
		return delivery.ErrSubAgent
	}
	return nil
}

// Hand records the bundle, or fails as told.
func (f *FakeAdapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	f.mu.Lock()
	f.attempts++
	fail, busy, absent, onHand := f.fail, f.busy, f.absent[h.SessionID], f.onHand
	f.mu.Unlock()
	switch {
	case busy:
		return false, delivery.ErrBusy
	case fail != nil:
		f.signal()
		return false, fail
	case absent && !f.idle:
		f.signal()
		return false, delivery.ErrTargetAbsent
	}
	if f.idle {
		if h.Waiter == nil {
			return false, delivery.ErrBusy
		}
		if err := h.Waiter.Deliver(ctx, h.ID, h.Bundle); err != nil {
			return false, err
		}
	}
	f.mu.Lock()
	f.handed[h.SessionID] = append(f.handed[h.SessionID], h.Bundle)
	f.mu.Unlock()
	if onHand != nil {
		onHand()
	}
	f.signal()
	return !f.idle, nil
}

func (f *FakeAdapter) signal() {
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

// Handed returns the bundles handed to a session so far.
func (f *FakeAdapter) Handed(sessionID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.handed[sessionID])
}

// Attempts is how many times Hand was called.
func (f *FakeAdapter) Attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// Notify fires after each hand-over attempt that wasn't busy.
func (f *FakeAdapter) Notify() <-chan struct{} { return f.notify }

// FailWith makes every later hand-over fail with err; nil makes them succeed again.
func (f *FakeAdapter) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// SetBusy makes every later hand-over return ErrBusy.
func (f *FakeAdapter) SetBusy(busy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = busy
}

// MarkAbsent makes a session one that doesn't exist.
func (f *FakeAdapter) MarkAbsent(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.absent[id] = true
}

// MarkSubAgent makes a session a sub-agent.
func (f *FakeAdapter) MarkSubAgent(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subAgent[id] = true
}

// OnHand runs fn after each bundle handed over.
func (f *FakeAdapter) OnHand(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onHand = fn
}

// FakeAdapterFixture prepares a FakeAdapter for the adapter contract suite.
func FakeAdapterFixture(waitsForIdle bool) AdapterFixture {
	var last *FakeAdapter
	return AdapterFixture{
		New: func(*testing.T) delivery.Adapter {
			last = NewFakeAdapter("fake", waitsForIdle)
			last.MarkSubAgent("sub")
			last.MarkAbsent("gone")
			return last
		},
		Root:     func(*testing.T) string { return "root" },
		SubAgent: func(*testing.T) string { return "sub" },
		Absent:   func(*testing.T) string { return "gone" },
		Received: func(_ *testing.T, id string) []string { return last.Handed(id) },
	}
}

// FakeServer is an Aboard server for tests: agents' inboxes with read positions, and a
// stream of board heads.
type FakeServer struct {
	mu        sync.Mutex
	heads     map[string]int
	inboxes   map[delivery.AgentRef][]delivery.Message
	cursors   map[delivery.AgentRef]int
	revoked   map[delivery.AgentRef]bool
	followers map[chan delivery.Head]bool
	acks      []int
	failAcks  bool
	presence  map[delivery.AgentRef][]delivery.Presence
	modes     map[delivery.AgentRef]delivery.Mode
}

var _ delivery.Server = (*FakeServer)(nil)

// NewFakeServer returns an empty server.
func NewFakeServer() *FakeServer {
	return &FakeServer{
		heads: map[string]int{}, inboxes: map[delivery.AgentRef][]delivery.Message{},
		cursors: map[delivery.AgentRef]int{}, revoked: map[delivery.AgentRef]bool{}, followers: map[chan delivery.Head]bool{},
		presence: map[delivery.AgentRef][]delivery.Presence{}, modes: map[delivery.AgentRef]delivery.Mode{},
	}
}

// SetPresence records the agent's presence.
func (s *FakeServer) SetPresence(_ context.Context, agent delivery.AgentRef, p delivery.Presence, mode delivery.Mode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked[agent] {
		return delivery.ErrUnauthorized
	}
	s.presence[agent] = append(s.presence[agent], p)
	if mode != "" {
		s.modes[agent] = mode
	}
	return nil
}

// Mode is the agent's delivery mode as last reported with its presence, or "".
func (s *FakeServer) Mode(agent delivery.AgentRef) delivery.Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes[agent]
}

// Presence is the agent's latest reported presence, or "" if none was reported.
func (s *FakeServer) Presence(agent delivery.AgentRef) delivery.Presence {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.presence[agent]; len(r) > 0 {
		return r[len(r)-1]
	}
	return ""
}

// PresenceReports is how many times the agent's presence was reported.
func (s *FakeServer) PresenceReports(agent delivery.AgentRef) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.presence[agent])
}

// Post adds a message for an agent on its board and moves the board's head. It returns
// the message's sequence number.
func (s *FakeServer) Post(to delivery.AgentRef, m delivery.Message) int {
	s.mu.Lock()
	s.heads[to.Board]++
	seq := s.heads[to.Board]
	m.Seq, m.Board = seq, to.Board
	if m.FromName == "" {
		m.FromName, m.Role, m.Sender = "writer", "writer", "owner_agent"
	}
	s.inboxes[to] = append(s.inboxes[to], m)
	followers := make([]chan delivery.Head, 0, len(s.followers))
	for ch := range s.followers {
		followers = append(followers, ch)
	}
	s.mu.Unlock()
	for _, ch := range followers {
		ch <- delivery.Head{Board: to.Board, Seq: seq}
	}
	return seq
}

// Follow sends every head change until ctx ends.
func (s *FakeServer) Follow(ctx context.Context, connected func(), head func(delivery.Head)) error {
	ch := make(chan delivery.Head, 64)
	s.mu.Lock()
	s.followers[ch] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.followers, ch)
		s.mu.Unlock()
	}()
	connected()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("follow: %w", ctx.Err())
		case h := <-ch:
			head(h)
		}
	}
}

// Inbox returns the agent's messages after its read position.
func (s *FakeServer) Inbox(_ context.Context, agent delivery.AgentRef) (msgs []delivery.Message, cursor int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked[agent] {
		return nil, 0, delivery.ErrUnauthorized
	}
	for _, m := range s.inboxes[agent] {
		if m.Seq > s.cursors[agent] {
			msgs = append(msgs, m)
		}
	}
	return msgs, s.cursors[agent], nil
}

// Ack moves the agent's read position forward.
func (s *FakeServer) Ack(_ context.Context, agent delivery.AgentRef, upTo int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked[agent] {
		return delivery.ErrUnauthorized
	}
	if s.failAcks {
		return errors.New("server unreachable")
	}
	if upTo > s.heads[agent.Board] {
		return errors.New("ack_out_of_range")
	}
	s.acks = append(s.acks, upTo)
	s.cursors[agent] = max(s.cursors[agent], upTo)
	return nil
}

// Cursor is the agent's read position.
func (s *FakeServer) Cursor(agent delivery.AgentRef) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[agent]
}

// FailAcks makes acknowledgements fail, as if the server were unreachable.
func (s *FakeServer) FailAcks(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAcks = fail
}

// Acks returns every acknowledgement the server accepted, in order.
func (s *FakeServer) Acks() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.acks)
}

// Revoke makes the agent's token rejected from now on.
func (s *FakeServer) Revoke(agent delivery.AgentRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[agent] = true
}

// PipeControl is an in-memory control socket: Dial returns one end of a pipe whose other
// end Accept returns.
type PipeControl struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

var _ delivery.Control = (*PipeControl)(nil)

// NewPipeControl returns an open in-memory control socket.
func NewPipeControl() *PipeControl {
	return &PipeControl{conns: make(chan net.Conn), closed: make(chan struct{})}
}

// Accept returns the next dialed connection.
func (p *PipeControl) Accept() (net.Conn, error) {
	select {
	case c := <-p.conns:
		return c, nil
	case <-p.closed:
		return nil, net.ErrClosed
	}
}

// Close stops accepting.
func (p *PipeControl) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

// Dial connects to the daemon behind the socket.
func (p *PipeControl) Dial() (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case p.conns <- server:
		return client, nil
	case <-p.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

// FakeProcesses is a process table for tests: every process is alive until killed.
type FakeProcesses struct {
	mu   sync.Mutex
	dead map[delivery.Process]bool
}

var _ delivery.Processes = (*FakeProcesses)(nil)

// NewFakeProcesses returns a process table where everything runs.
func NewFakeProcesses() *FakeProcesses {
	return &FakeProcesses{dead: map[delivery.Process]bool{}}
}

// Alive reports whether p was not killed.
func (f *FakeProcesses) Alive(p delivery.Process) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[p]
}

// StartTime returns the start time a fake process with any id has: 1.
func (f *FakeProcesses) StartTime(int) (int64, bool) { return 1, true }

// Kill makes p dead.
func (f *FakeProcesses) Kill(p delivery.Process) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dead[p] = true
}
