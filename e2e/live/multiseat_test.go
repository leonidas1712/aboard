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

// Equal board sequence numbers must remain separate through the real harness's
// combined handoff, replies and acknowledgements.
func TestMultiSeatEqualSequences(t *testing.T) {
	eachHarness(t, "MultiSeatEqualSequences", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.run("up")
		boards := []string{"multi-a", "multi-b"}
		for _, board := range boards {
			var created struct {
				Name string `json:"name"`
			}
			l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards", map[string]any{
				"name": board, "template": "general",
			}, &created)
			if created.Name != board {
				t.Fatalf("created board %q, want %q", created.Name, board)
			}
		}
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.submit("Run exactly these two commands in order: `aboard join --board multi-a --name writer`; " +
			"`aboard join --board multi-b --name writer`. Do not pair, create another seat, or post any message. " +
			"Afterwards, whenever an Aboard message arrives, run exactly the command it requests using its named board, then stop. Reply only OK now.")
		writer.waitIdle(3 * time.Minute)
		d.afterBind(writer)
		for _, board := range boards {
			l.waitFor(30*time.Second, "writer to join "+board, func() bool {
				names := l.agents(board)
				return len(names) == 1 && names[0] == "writer"
			})
		}

		seats := l.multiSeatCredentials(boards)
		if seats[0].MemberID == "" || seats[0].MemberID == seats[1].MemberID {
			t.Fatal("the boards did not receive distinct immutable seats")
		}

		var sent []message
		for i, board := range boards {
			var state struct {
				HeadSeq int `json:"head_seq"`
			}
			l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board, nil, &state)
			body := fmt.Sprintf("Run exactly `aboard say --board %s --reply %d \"MULTI-%d-ACK\"`. Run no other Aboard command.", board, state.HeadSeq+1, i)
			sent = append(sent, l.postAsOwner(board, "@writer", body))
		}
		if sent[0].Seq != sent[1].Seq {
			t.Fatalf("fixture messages have different sequences: %d and %d", sent[0].Seq, sent[1].Seq)
		}
		for i, board := range boards {
			marker := fmt.Sprintf("MULTI-%d-ACK", i)
			l.waitFor(3*time.Minute, "writer's reply on "+board, func() bool {
				for _, m := range l.multiSeatMessages(board) {
					if m.Body == marker && m.From.Name == "writer" && m.ReplyToSeq != nil && *m.ReplyToSeq == sent[i].Seq {
						return true
					}
				}
				return false
			})
		}
		writer.waitIdle(2 * time.Minute)
		for i, board := range boards {
			count := 0
			for _, m := range l.multiSeatMessages(board) {
				if m.Body == fmt.Sprintf("MULTI-%d-ACK", 1-i) {
					t.Errorf("reply for the other board appeared on %s", board)
				}
				if m.Body == fmt.Sprintf("MULTI-%d-ACK", i) {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%s has %d replies; want exactly one", board, count)
			}
			l.waitFor(30*time.Second, "independent acknowledgement on "+board, func() bool {
				var inbox struct {
					Messages []message `json:"messages"`
					Cursor   int       `json:"cursor"`
				}
				l.multiSeatRequest(seats[i].Token, http.MethodGet, "/v1/me/inbox", nil, &inbox)
				return inbox.Cursor >= sent[i].Seq && len(inbox.Messages) == 0
			})
		}
	})
}

func (l *lab) multiSeatMessages(board string) []message {
	l.t.Helper()
	var page struct {
		Messages []message `json:"messages"`
	}
	l.decode(l.human, &page, "read", "--board", board, "--as", "writer", "--limit", "200")
	return page.Messages
}

// Requests use only the lab's credentials and omit response bodies from failures.
func (l *lab) multiSeatOwnerRequest(method, path string, input, output any) {
	l.t.Helper()
	key, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		l.t.Fatal(err)
	}
	l.multiSeatRequest(strings.TrimSpace(string(key)), method, path, input, output)
}

func (l *lab) multiSeatRequest(key, method, path string, input, output any) {
	l.t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		l.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(l.t.Context(), method, "http://"+l.addr+path, bytes.NewReader(body))
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		l.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		l.t.Fatalf("%s %s: status %d", method, path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		l.t.Fatalf("decode %s %s: %v", method, path, err)
	}
}

type multiSeatCredential struct {
	Board    string `json:"board"`
	Name     string `json:"name"`
	MemberID string `json:"member_id"`
	Token    string `json:"token"`
}

func (l *lab) multiSeatCredentials(boards []string) []multiSeatCredential {
	l.t.Helper()
	raw, err := os.ReadFile(filepath.Join(l.configDir(), "credentials.json"))
	if err != nil {
		l.t.Fatal(err)
	}
	var saved struct {
		Agents []multiSeatCredential `json:"agents"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		l.t.Fatal(err)
	}
	var seats []multiSeatCredential
	for _, board := range boards {
		var found []multiSeatCredential
		for _, seat := range saved.Agents {
			if seat.Board == board && seat.Name == "writer" {
				found = append(found, seat)
			}
		}
		if len(found) != 1 {
			l.t.Fatalf("%s has %d saved writer seats; want one", board, len(found))
		}
		seats = append(seats, found[0])
	}
	return seats
}
