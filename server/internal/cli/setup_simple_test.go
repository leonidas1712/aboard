package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
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

func TestSetupDisplayLookupCannotBlockOrAdvanceDelivery(t *testing.T) {
	for _, ready := range []bool{false, true} {
		for _, failure := range []string{"unavailable", "foreign-issuer", "foreign-request", "missing-inviter", "foreign-board"} {
			t.Run(failure+fmt.Sprint(ready), func(t *testing.T) {
				e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
				e.env["ABOARD_SESSION"] = "claude-code:setup-current"
				a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
				fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
					if req.Op != delivery.OpPairing || req.PairingAction != "get" {
						t.Errorf("display lookup attempted delivery work: %+v", req)
					}
					resp := delivery.Response{V: delivery.ProtocolVersion, Server: "https://issuer.example", Pairing: &delivery.PairingRequest{ID: "prq_original", BoardID: "brd_work", InviterID: "per_inviter", Display: json.RawMessage(`{"person_handle":"foreign"}`)}}
					switch failure {
					case "unavailable":
						resp.Error = &delivery.WireError{Code: "server_unreachable", Message: "Unavailable"}
					case "foreign-issuer":
						resp.Server = "https://foreign.example"
					case "foreign-request":
						resp.Pairing.ID = "prq_other"
					case "missing-inviter":
						resp.Pairing.InviterID = ""
					case "foreign-board":
						resp.Pairing.BoardID = "brd_other"
					}
					return resp
				})
				out := newSetupOutput(serverRef{URL: "https://issuer.example"})
				out.Next = &api.NextStep{Command: "aboard init", Resume: "Keep the harness action"}
				if ready {
					out.Steps[3].State = "complete"
				}
				pending := &setupPending{Receipt: &api.OnboardingReceipt{Boards: []string{"brd_work"}}}
				if err := a.continueSetupGreeting(context.Background(), &out, "prq_original", pending); err != nil {
					t.Fatal(err)
				}
				if out.Steps[5].State != "pending" || out.Next.Command != "aboard init" || out.Steps[5].Message != "Waiting for a reply from the inviting person's agents." {
					t.Fatalf("display failure changed setup: %+v", out)
				}
			})
		}
	}
}
