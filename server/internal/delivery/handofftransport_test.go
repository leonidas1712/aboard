package delivery

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestCombinedWireUsesItsExactHandoffConfirmation(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	c := &extConn{conn: server, took: map[int64]bool{}, handoffs: map[string]bool{}, supportsHandoffs: true, signal: make(chan struct{}, 1), gone: make(chan struct{})}
	id := "hnd_0123456789abcdef0123456789abcdef"
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		done <- c.DeliverHandoff(ctx, Handover{ID: 42, HandoffID: id, Class: ClassMixed, Bundle: "two boards"})
	}()
	var frame Response
	if err := ReadFrame(bufio.NewReader(client), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.ID != 0 || frame.HandoffID != id || frame.DeliveryClass != ClassMixed {
		t.Fatalf("combined frame: %+v", frame)
	}
	c.received(42)
	c.receivedHandoff("hnd_ffffffffffffffffffffffffffffffff")
	if c.hasHandoff(id) {
		t.Fatal("legacy or unknown confirmation confirmed combined payload")
	}
	c.receivedHandoff(id)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("exact confirmation did not complete")
	}
}

func TestCombinedTransportRequiresCurrentNegotiatedCapability(t *testing.T) {
	for _, closed := range []bool{false, true} {
		c := &extConn{supportsHandoffs: closed, gone: make(chan struct{})}
		if closed {
			close(c.gone)
		}
		if c.SupportsHandoffs() {
			t.Fatal("unknown or disconnected capability accepted")
		}
		if err := c.DeliverHandoff(context.Background(), Handover{HandoffID: "hnd_0123456789abcdef0123456789abcdef"}); !errors.Is(err, ErrExtensionOutdated) {
			t.Fatalf("old extension: %v", err)
		}
	}
}

func TestOldExtensionHelloKeepsExistingSeatsAndConnection(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	old := &extConn{supportsHandoffs: true, gone: make(chan struct{})}
	c := &extConn{conn: server, registered: make(chan bool, 1), gone: make(chan struct{})}
	a := AgentRef{Server: "server", Board: "a", MemberID: "mem_a"}
	b := AgentRef{Server: "server", Board: "b", MemberID: "mem_b"}
	s := &session{ext: old, agents: map[AgentKey]*agentState{a.Key(): newAgentState(a, false), b.Key(): newAgentState(b, false)}}
	done := make(chan struct{})
	go func() { s.onHello(context.Background(), Request{}, c); close(done) }()
	var frame Response
	if err := ReadFrame(bufio.NewReader(client), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Error == nil || frame.Error.Code != "extension_outdated" {
		t.Fatalf("old hello: %+v", frame)
	}
	<-done
	if <-c.registered {
		t.Fatal("old extension registered")
	}
	if s.ext != old || len(s.agents) != 2 {
		t.Fatal("refused hello changed bindings or connection")
	}
}
