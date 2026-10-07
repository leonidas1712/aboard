//go:build live

package live

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRenamedPersonKeepsAgentDelivery(t *testing.T) {
	eachHarness(t, "RenamedPersonKeepsAgentDelivery", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")
		writer.submit("Reply only READY and end this turn. Wait for your person's Aboard instruction.")
		writer.waitIdle(2 * time.Minute)
		var before struct {
			Name string `json:"name"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/me", nil, &before)
		var renamed map[string]any
		l.multiSeatOwnerRequest(http.MethodPost, "/v1/people/"+before.Name+"/rename", map[string]string{"handle": "renamed-owner"}, &renamed)
		var state struct {
			HeadSeq int `json:"head_seq"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/writer-reviewer", nil, &state)
		const marker = "RENAMED-OWNER-ACK-951"
		posted := l.postAsOwner("writer-reviewer", "@writer", fmt.Sprintf("Run exactly `aboard say --board writer-reviewer --reply %d %q`. Run no other Aboard command, then end your turn.", state.HeadSeq+1, marker))
		l.waitMessage("writer", posted.At, marker, 3*time.Minute)
		var members struct {
			Members []struct {
				Name  string  `json:"name"`
				Owner *string `json:"owner"`
			} `json:"members"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/writer-reviewer/members", nil, &members)
		found := false
		for _, member := range members.Members {
			if member.Name == "writer" {
				found = member.Owner != nil && *member.Owner == "renamed-owner"
			}
		}
		if !found {
			t.Fatal("current agent owner projection did not update")
		}
		writer.waitIdle(2 * time.Minute)
	})
}
