//go:build live

package live

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestPairingVerifiesBothExactHarnessSessions(t *testing.T) {
	eachHarness(t, "PairingVerifiesBothExactHarnessSessions", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		l := newLab(t)
		d.setUp(l)
		board := l.pairCLI()
		writer := d.start(l, "writer", l.project("pairing-writer", d.p.Harness))
		writer.bind("writer")
		reviewer := d.start(l, "reviewer", l.project("pairing-reviewer", d.p.Harness))
		reviewer.bind("reviewer")
		writer.submit(fmt.Sprintf("Run aboard pairing request me --board %s %q --json, then end your turn. When an ABOARD-PAIRING ping arrives, follow its PING/reply wiring protocol: reply to that exact message using aboard say --reply and its specified canonical reply marker, then end your turn. These messages test delivery only; do no other work and do not create another pairing request.", board, "Verify the two real sessions"))
		type endpoint struct {
			AgentID string `json:"agent_id"`
			Binding string `json:"session_binding"`
		}
		type request struct {
			ID         string    `json:"id"`
			State      string    `json:"state"`
			Generation int       `json:"generation"`
			Initiator  *endpoint `json:"initiator"`
			Recipient  *endpoint `json:"recipient"`
		}
		var page struct {
			Requests []request `json:"requests"`
		}
		l.waitFor(3*time.Minute, "the initiating session's selected pairing endpoint", func() bool {
			l.multiSeatOwnerRequest(http.MethodGet, "/v1/pairing-requests", nil, &page)
			return len(page.Requests) == 1 && page.Requests[0].Initiator != nil
		})
		initial := page.Requests[0]
		if initial.State == "ready" || initial.Recipient != nil {
			t.Fatal("one selected session claimed verified pairing before the recipient accepted")
		}
		writer.waitIdle(2 * time.Minute)
		reviewer.submit(fmt.Sprintf("Run aboard pairing accept %s --here --json, then end your turn. When an ABOARD-PAIRING ping arrives, follow its PING/reply wiring protocol: reply to that exact message using aboard say --reply and its specified canonical reply marker, then end your turn. These messages test delivery only; do no other work and do not create another pairing request.", initial.ID))
		var final request
		l.waitFor(4*time.Minute, "both confirmed native pairing round trips", func() bool {
			l.multiSeatOwnerRequest(http.MethodGet, "/v1/pairing-requests/"+initial.ID, nil, &final)
			return final.State == "ready"
		})
		if final.Generation != initial.Generation || final.Initiator == nil || final.Recipient == nil || final.Initiator.AgentID == final.Recipient.AgentID || final.Initiator.Binding == final.Recipient.Binding {
			t.Fatalf("pairing did not retain two distinct exact current endpoints: %+v", final)
		}
		writer.waitIdle(2 * time.Minute)
		reviewer.waitIdle(2 * time.Minute)
	})
}
