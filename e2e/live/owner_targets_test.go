//go:build live

package live

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestOwnerTargetsWakeEachCurrentAgent(t *testing.T) {
	eachHarness(t, "OwnerTargetsWakeEachCurrentAgent", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("writer-project", d.p.Harness))
		reviewer := d.start(l, "reviewer", l.project("reviewer-project", d.p.Harness))
		writer.bind("writer")
		reviewer.bind("reviewer")
		l.waitQuiet(4*time.Minute, "reviewer", writer, reviewer)
		var person struct {
			Name string `json:"name"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/me", nil, &person)
		var state struct {
			HeadSeq int `json:"head_seq"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/writer-reviewer", nil, &state)
		const marker = "OWNER-TARGET-ACK-6317"
		posted := l.postAsOwner("writer-reviewer", "owner:"+person.Name, fmt.Sprintf("Run exactly `aboard say --board writer-reviewer --reply %d %q`. Run no other Aboard command, then end your turn.", state.HeadSeq+1, marker))
		for _, name := range []string{"writer", "reviewer"} {
			l.waitFor(3*time.Minute, name+" to answer the owner target", func() bool {
				for _, m := range l.multiSeatMessages("writer-reviewer") {
					if m.From.Name == name && m.Body == marker && m.ReplyToSeq != nil && *m.ReplyToSeq == posted.Seq {
						return true
					}
				}
				return false
			})
		}
		writer.waitIdle(2 * time.Minute)
		reviewer.waitIdle(2 * time.Minute)
		var receipts struct {
			Recipients []struct {
				Member struct {
					Name string `json:"name"`
				} `json:"member"`
				State string `json:"state"`
			} `json:"recipients"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, fmt.Sprintf("/v1/boards/writer-reviewer/messages/%d/receipts", posted.Seq), nil, &receipts)
		if len(receipts.Recipients) != 2 {
			t.Fatalf("owner target recipients: %+v", receipts.Recipients)
		}
		for _, recipient := range receipts.Recipients {
			if recipient.State != "received" {
				t.Fatalf("owner target receipt: %+v", recipient)
			}
		}
	})
}
