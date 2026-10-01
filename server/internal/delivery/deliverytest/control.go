package deliverytest

import (
	"bufio"
	"errors"
	"net"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// ControlFixture is one control socket, prepared for the control contract suite.
type ControlFixture struct {
	// New returns an open control socket and a way to connect to it as this OS user.
	New func(t *testing.T) (delivery.Control, func() (net.Conn, error))
}

// RunControl runs the Control contract suite.
func RunControl(t *testing.T, f ControlFixture) {
	t.Run("AcceptsAConnectionFromThisUser", func(t *testing.T) {
		ctl, dial := f.New(t)
		t.Cleanup(func() { _ = ctl.Close() })
		accepted := make(chan net.Conn, 1)
		errs := make(chan error, 1)
		go func() {
			c, err := ctl.Accept()
			if err != nil {
				errs <- err
				return
			}
			accepted <- c
		}()
		client, err := dial()
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = client.Close() }()
		var server net.Conn
		select {
		case server = <-accepted:
		case err := <-errs:
			t.Fatalf("Accept refused this user's connection: %v", err)
		}
		defer func() { _ = server.Close() }()
		go func() {
			_ = delivery.WriteFrame(client, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpStatus})
		}()
		var req delivery.Request
		if err := delivery.ReadFrame(bufio.NewReader(server), &req); err != nil || req.Op != delivery.OpStatus {
			t.Fatalf("read %+v, %v", req, err)
		}
	})

	t.Run("AcceptFailsOnceClosed", func(t *testing.T) {
		ctl, _ := f.New(t)
		if err := ctl.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := ctl.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
		}
	})
}
