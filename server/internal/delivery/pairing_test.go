package delivery_test

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestPairingWithoutTrustedRuntimeRefusesBeforeParentLookup(t *testing.T) {
	r, _ := seatsRig(t)
	got := r.call(delivery.Request{V: 1, Op: "pairing", Harness: "unknown", Session: "forged", Server: "https://example.invalid"})
	if got.Error == nil || got.Error.Code != "agent_session_required" {
		t.Fatalf("unvouched session: %+v", got)
	}
}
