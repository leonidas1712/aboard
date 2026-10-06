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
	defer server.Close()
	defer client.Close()
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
