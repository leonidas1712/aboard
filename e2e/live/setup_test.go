//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInvitedSetupVerifiesTwoPeopleExactSessions(t *testing.T) {
	eachHarness(t, "InvitedSetupVerifiesTwoPeopleExactSessions", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		inviter := newLab(t)
		d.setUp(inviter)
		board := inviter.pairCLI()
		inviter.run("allowance", "on")
		writerDir := inviter.project("setup-inviter", d.p.Harness)
		writer := d.start(inviter, "writer", writerDir)
		writer.bind("writer")
		invitePath := filepath.Join(writerDir, "invite.json")
		writer.submit(fmt.Sprintf("Run umask 077; aboard invite --server http://%s --board %s --pairing %q --json > %q, then end your turn. Keep the invitation only in that private file, never in board messages. When an ABOARD-PAIRING ping arrives, reply to that exact message with aboard say --reply and its specified canonical reply marker, then end your turn. Do no other work.", inviter.addr, board, "Verify newcomer setup", invitePath))
		var invitation struct {
			Link string `json:"link"`
			ID   string `json:"pairing_request_id"`
		}
		inviter.waitFor(3*time.Minute, "a bundled invitation from the exact initiating session", func() bool {
			data, err := os.ReadFile(invitePath)
			return err == nil && json.Unmarshal(data, &invitation) == nil && invitation.Link != "" && invitation.ID != ""
		})
		writer.waitIdle(2 * time.Minute)
		newcomer := newLab(t)
		d.setUp(newcomer)
		reviewerDir := newcomer.project("setup-newcomer", d.p.Harness)
		reviewer := d.start(newcomer, "reviewer", reviewerDir)
		resultPath := filepath.Join(reviewerDir, "setup.json")
		prompt := fmt.Sprintf("Run aboard setup %q --handle newcomer --json > %q, then end your turn. When an ABOARD-PAIRING ping arrives, reply to that exact message with aboard say --reply and its specified canonical reply marker, then end your turn. Keep the invitation out of board messages. Do no other work.", invitation.Link, resultPath)
		reviewer.submit(prompt)
		var paired struct {
			State string `json:"state"`
		}
		inviter.waitFor(4*time.Minute, "both newcomer setup round trips", func() bool {
			inviter.multiSeatOwnerRequest(http.MethodGet, "/v1/pairing-requests/"+invitation.ID, nil, &paired)
			return paired.State == "ready"
		})
		writer.waitIdle(2 * time.Minute)
		reviewer.waitIdle(2 * time.Minute)
		reviewer.submit(fmt.Sprintf("Run aboard setup %q --handle newcomer --json > %q once more to read the verified result, then end your turn. Do not create another account or invitation.", invitation.Link, resultPath))
		var output struct {
			State string `json:"state"`
			Steps []struct {
				Step  string `json:"step"`
				State string `json:"state"`
			} `json:"steps"`
		}
		newcomer.waitFor(2*time.Minute, "setup's verified delivery result", func() bool {
			data, err := os.ReadFile(resultPath)
			return err == nil && json.Unmarshal(data, &output) == nil && output.State == "complete"
		})
		if len(output.Steps) != 6 {
			t.Fatal("setup omitted a required verification step")
		}
		for _, step := range output.Steps {
			if step.State != "complete" {
				t.Fatalf("setup claims complete with %s still %s", step.Step, step.State)
			}
		}
		reviewer.waitIdle(2 * time.Minute)
	})
}
