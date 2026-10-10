package cli

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSetupReadsInviterWithoutSelectingEndpoint(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	e.env["ABOARD_SESSION"] = "claude-code:setup-current"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	var selects atomic.Int32
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		resp := delivery.Response{V: delivery.ProtocolVersion}
		if req.Op == delivery.OpPairing {
			if req.PairingAction != "get" {
				selects.Add(1)
			}
			resp.Server = "https://issuer.example"
			resp.PairingBoard = "work"
			resp.Pairing = &delivery.PairingRequest{ID: "prq_original", ServerID: "srv_issuer", BoardID: "brd_work", InviterID: "per_inviter", State: "verifying", Generation: 1, CreatedAt: "2026-10-10T00:00:00Z", ExpiresAt: "2026-10-11T00:00:00Z"}
		}
		return resp
	})
	out := newSetupOutput(serverRef{URL: "https://issuer.example"})
	out.continueCommand = "aboard setup --continue"
	out.confirmSetupHarness(out.continueCommand)
	if err := a.continueSetupPairing(context.Background(), &out, "prq_original"); err != nil {
		t.Fatal(err)
	}
	if selects.Load() != 0 || out.Steps[5].State != "pending" || !strings.Contains(out.Steps[5].Message, "Waiting for a reply") {
		t.Fatalf("setup selected an endpoint instead of greeting: selects=%d result=%+v", selects.Load(), out)
	}
}
