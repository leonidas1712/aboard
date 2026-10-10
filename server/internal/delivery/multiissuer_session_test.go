package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

func TestMultiIssuerEqualSeatsSurviveRestartAndSiblingRevocation(t *testing.T) {
	r := newRig(t)
	r.status()
	r.stop()
	other := deliverytest.NewFakeServer()
	const otherURL = "https://other.example"
	r.configure = func(c *delivery.Config) {
		c.Connect = func(issuer string) delivery.Server {
			switch issuer {
			case serverURL:
				return r.server
			case otherURL:
				return other
			default:
				t.Errorf("unexpected issuer %s", issuer)
				return other
			}
		}
	}
	r.start()
	a, b := reviewer, reviewer
	b.Server = otherURL
	r.bind("codex", "issuers", a)
	r.bind("codex", "issuers", b)
	refs := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "issuers"}).Agents
	if len(refs) != 2 {
		t.Fatalf("equal board/member displaced sibling: %+v", refs)
	}
	post := func(s *deliverytest.FakeServer, a delivery.AgentRef, text string) int {
		return s.Post(a, delivery.Message{Body: text, FromHuman: true, FromName: "alex", Sender: "owner"})
	}
	first, second := post(r.server, a, "local decision"), post(other, b, "remote decision")
	if first != second {
		t.Fatalf("fixture seqs %d/%d", first, second)
	}
	r.eventually("both issuer acknowledgments", 500*time.Millisecond, func() bool { return r.server.Cursor(a) == first && other.Cursor(b) == second })
	texts := r.codex.Handed("issuers")
	if len(texts) != 1 {
		t.Fatalf("expected combined handoff: %+v", texts)
	}
	for _, want := range []string{`server="` + serverURL + `"`, `server="` + otherURL + `"`, "--server " + serverURL, "--server " + otherURL, "--board docs", "local decision", "remote decision"} {
		if !strings.Contains(texts[0], want) {
			t.Errorf("missing %q in %s", want, texts[0])
		}
	}
	r.restart()
	refs = r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "issuers"}).Agents
	if len(refs) != 2 {
		t.Fatalf("restart lost sibling: %+v", refs)
	}
	r.server.Revoke(a)
	post(r.server, a, "revocation head")
	r.eventually("local issuer terminal", 0, func() bool {
		for _, s := range r.status().Agents {
			if s.Agent.Key() == a.Key() && s.Reason == delivery.ReasonUnauthorized {
				return true
			}
		}
		return false
	})
	next := post(other, b, "remote after revocation")
	r.eventually("remote sibling continues", 500*time.Millisecond, func() bool { return other.Cursor(b) == next })
	if len(r.codex.Handed("issuers")) != 2 {
		t.Fatalf("sibling repeated or stopped: %+v", r.codex.Handed("issuers"))
	}
}
