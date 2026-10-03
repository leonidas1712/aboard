package extension

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

func TestExtensionAdapterContract(t *testing.T) {
	deliverytest.RunAdapter(t, deliverytest.AdapterFixture{
		New:  func(*testing.T) delivery.Adapter { return Adapter{Name: "omp"} },
		Root: func(*testing.T) string { return "0199a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d" },
		// Subagents never connect, and only the extension reports a session, so neither
		// kind can be named here.
		SubAgent:       func(*testing.T) string { return "" },
		Absent:         func(*testing.T) string { return "" },
		ConfirmsOnHand: true,
	})
}
