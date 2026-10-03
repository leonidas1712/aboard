package idlehook

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

func TestIdleHookAdapterContract(t *testing.T) {
	deliverytest.RunAdapter(t, deliverytest.AdapterFixture{
		New:  func(*testing.T) delivery.Adapter { return Adapter{Name: "claude-code"} },
		Root: func(*testing.T) string { return "5f1c2d3e-0000-4000-8000-000000000001" },
		// Sub-agents run inside their parent's session, and only the session-start hook
		// reports a session, so neither kind can be named here.
		SubAgent: func(*testing.T) string { return "" },
		Absent:   func(*testing.T) string { return "" },
	})
}
