//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvitedSetupVerifiesTwoPeopleExactSessions(t *testing.T) {
	invitedSetupProof(t, "InvitedSetupVerifiesTwoPeopleExactSessions", false, false)
}

func TestBrowserApprovedInviteReachesItsOriginalSession(t *testing.T) {
	invitedSetupProof(t, "BrowserApprovedInviteReachesItsOriginalSession", true, false)
}

func TestApprovedInviteWakesIdleOrigin(t *testing.T) {
	invitedSetupProof(t, "ApprovedInviteWakesIdleOrigin", true, true)
}

func invitedSetupProof(t *testing.T, name string, held, wakeOnly bool) {
	t.Helper()
	eachHarness(t, name, func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		inviter := newLab(t)
		d.setUp(inviter)
		board := inviter.pairCLI()
		if !held {
			inviter.run("allowance", "on")
			inviter.run("allowance", "set", "invite-people", "on")
		}
		writerDir := inviter.project("setup-inviter", d.p.Harness)
		writer := d.start(inviter, "writer", writerDir)
		writer.bind("writer")
		invitePath := filepath.Join(writerDir, "invite.json")
		writer.submit(fmt.Sprintf("Run umask 077; aboard invite --person --server http://%s --board %s --json > %q, then end your turn. Keep the invitation only in that private file, never in board messages. If a later approval notice says this request executed, run its aboard approvals show command once and save that JSON outcome to the same private file. When a setup hello arrives, reply to that message with aboard say --reply and the body SETUP-HELLO-REPLY, then end your turn. Do no other work.", inviter.addr, board, invitePath))
		if held {
			var pending struct {
				State    string `json:"state"`
				Approval struct {
					ID string `json:"id"`
				} `json:"approval"`
			}
			inviter.waitFor(3*time.Minute, "an invite held for browser approval", func() bool {
				data, err := os.ReadFile(filepath.Clean(invitePath))
				return err == nil && json.Unmarshal(data, &pending) == nil && pending.State == "pending" && pending.Approval.ID != ""
			})
			writer.waitIdle(2 * time.Minute)
			inviter.approveInviteInBrowser(pending.Approval.ID)
			// No invitation, outcome or approval id is relayed from the browser.
			if !wakeOnly {
				writer.submit(fmt.Sprintf("Continue with any Aboard next-turn notice. If it names an executed approval, run its aboard approvals show command with --json and save that JSON to %q, then end your turn. Do not request another invite or select a pairing endpoint manually.", invitePath))
			}
		}
		var invitation struct {
			Invite struct {
				Secret string `json:"invite"`
				ID     string `json:"pairing_request_id"`
			} `json:"invite"`
		}
		inviter.waitFor(3*time.Minute, "a bundled invitation from the exact initiating session", func() bool {
			data, err := os.ReadFile(filepath.Clean(invitePath))
			return err == nil && json.Unmarshal(data, &invitation) == nil && invitation.Invite.Secret != "" && invitation.Invite.ID != ""
		})
		link := "http://" + inviter.addr + "/join#" + invitation.Invite.Secret
		writer.waitIdle(2 * time.Minute)
		if wakeOnly {
			return
		}
		newcomer := newLab(t)
		d.setUp(newcomer)
		// Start with the global hooks setup will maintain already loaded. Installing
		// another scope after startup correctly requires a harness restart instead.
		newcomer.run("init", "--yes", "--scope", "global", "--harness", d.p.Harness, "--allow-commands")
		reviewerDir := filepath.Join(newcomer.dir, "setup-newcomer")
		if err := os.MkdirAll(reviewerDir, 0o750); err != nil {
			t.Fatal(err)
		}
		start := d.start
		if d.p.Harness == "codex" {
			// Global hooks are already installed; project instrumentation would
			// add a second scope that setup would correctly require restarting.
			start = d.startPlain
		}
		reviewer := start(newcomer, "reviewer", reviewerDir)
		resultPath := filepath.Join(reviewerDir, "setup.json")
		waitingPath := filepath.Join(reviewerDir, "waiting.json")
		reviewer.submit(fmt.Sprintf("Run aboard setup %q --json > %q, then end your turn without choosing a handle or continuing setup. Keep the invitation out of board messages.", link, waitingPath))
		var waiting struct {
			State  string          `json:"state"`
			Person json.RawMessage `json:"person"`
			Next   struct {
				Command string `json:"command"`
			} `json:"next"`
		}
		newcomer.waitFor(2*time.Minute, "a secret-free saved setup continuation", func() bool {
			data, err := os.ReadFile(filepath.Clean(waitingPath))
			return err == nil && json.Unmarshal(data, &waiting) == nil && waiting.State == "pending"
		})
		if len(waiting.Person) != 0 && string(waiting.Person) != "null" || !strings.HasPrefix(waiting.Next.Command, "aboard setup --continue --handle ") || strings.Contains(waiting.Next.Command, invitation.Invite.Secret) {
			t.Fatal("setup spent the invite or omitted its secret-free continuation")
		}
		reviewer.waitIdle(2 * time.Minute)
		prompt := fmt.Sprintf("Your person chose newcomer as the visible handle. Run aboard setup --continue --handle newcomer --json > %q, then end your turn. When a setup hello arrives, reply to that message with aboard say --reply and the body SETUP-HELLO-REPLY, then end your turn. Keep the invitation out of board messages. Do no other work.", resultPath)
		reviewer.submit(prompt)
		newcomer.waitFor(2*time.Minute, "setup joined the invited board", func() bool {
			data, err := os.ReadFile(filepath.Clean(resultPath))
			var result struct {
				Steps []struct{ Step, State string } `json:"steps"`
			}
			if err != nil || json.Unmarshal(data, &result) != nil {
				return false
			}
			for _, step := range result.Steps {
				if step.Step == "pairing" && step.State == "complete" {
					return true
				}
			}
			return false
		})
		writer.waitIdle(2 * time.Minute)
		writer.submit("Run aboard inbox, reply to the setup hello with aboard say --reply and the body SETUP-HELLO-REPLY, then end your turn. Do not create a pairing request or select an endpoint.")
		inviter.waitFor(2*time.Minute, "an ordinary reply to the setup hello", func() bool {
			var messages struct {
				Messages []struct {
					Body  string `json:"body"`
					Reply *int   `json:"reply_to_seq"`
				} `json:"messages"`
			}
			inviter.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board+"/messages?limit=100", nil, &messages)
			for _, message := range messages.Messages {
				if message.Body == "SETUP-HELLO-REPLY" && message.Reply != nil {
					return true
				}
			}
			return false
		})
		writer.waitIdle(2 * time.Minute)
		reviewer.waitIdle(2 * time.Minute)
		reviewer.submit(fmt.Sprintf("Run aboard setup --continue --handle newcomer --json > %q once more to read the delivery result, then end your turn. Do not create another account or invitation.", resultPath))
		var output struct {
			State string `json:"state"`
			Steps []struct {
				Step  string `json:"step"`
				State string `json:"state"`
			} `json:"steps"`
		}
		newcomer.waitFor(2*time.Minute, "setup's hello/reply result", func() bool {
			data, err := os.ReadFile(filepath.Clean(resultPath))
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

func (l *lab) approveInviteInBrowser(id string) {
	l.t.Helper()
	key, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		l.t.Fatal("read the isolated browser sign-in key")
	}
	base := "http://" + l.addr
	raw, err := json.Marshal(map[string]string{"key": strings.TrimSpace(string(key))})
	if err != nil {
		l.t.Fatal("encode isolated browser sign-in")
	}
	req, err := http.NewRequestWithContext(l.t.Context(), http.MethodPost, base+"/v1/browser-sessions", bytes.NewReader(raw))
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		l.t.Fatal("sign in the isolated browser")
	}
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	err = json.NewDecoder(response.Body).Decode(&login)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusCreated || len(response.Cookies()) != 1 || login.CSRF == "" {
		l.t.Fatal("browser sign-in did not return its cookie and CSRF proof")
	}
	req, err = http.NewRequestWithContext(l.t.Context(), http.MethodPost, base+"/v1/me/approvals/"+id+"/allow", strings.NewReader("{}"))
	if err != nil {
		l.t.Fatal(err)
	}
	req.AddCookie(response.Cookies()[0])
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Aboard-CSRF", login.CSRF)
	response, err = client.Do(req)
	if err != nil {
		l.t.Fatal("approve the invitation through the browser API")
	}
	// The approving browser's one-time link is never read or passed to either agent.
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		l.t.Fatalf("browser approval returned status %d", response.StatusCode)
	}
}
