package deliverytest

import (
	"net"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// The fakes the daemon's tests use must behave like the real adapters.
func TestFakeIdleHarnessPassesTheAdapterContract(t *testing.T) {
	RunAdapter(t, FakeAdapterFixture(true))
}

func TestFakeQueueingHarnessPassesTheAdapterContract(t *testing.T) {
	RunAdapter(t, FakeAdapterFixture(false))
}

func TestPipeControlPassesTheControlContract(t *testing.T) {
	RunControl(t, ControlFixture{New: func(*testing.T) (delivery.Control, func() (net.Conn, error)) {
		p := NewPipeControl()
		return p, p.Dial
	}})
}

func TestFakeServerPassesTheServerContract(t *testing.T) {
	RunServer(t, ServerFixture{New: func(*testing.T) (delivery.Server, delivery.AgentRef, func(string, bool) int) {
		s := NewFakeServer()
		to := delivery.AgentRef{Server: "http://fake", Board: "docs", Name: "reviewer"}
		return s, to, func(body string, urgent bool) int {
			return s.Post(to, delivery.Message{Body: body, Urgent: urgent})
		}
	}})
}
