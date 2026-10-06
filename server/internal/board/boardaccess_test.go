package board_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// shortKey gives sam a second key that works for an hour, and an agent made with it on
// the board, and returns both principals and the key's id.
func (w *teamWorld) shortKey(ctx context.Context, t *testing.T) (person, agent board.Principal, keyID string) {
	t.Helper()
	k, err := w.svc.CreateKey(ctx, w.sam, "short", board.MinKeyTTL)
	if err != nil {
		t.Fatal(err)
	}
	person = w.auth(ctx, t, k.Token)
	joined, err := w.svc.Join(ctx, person, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	return person, w.auth(ctx, t, joined.Token), k.Key.ID
}

// readsWithList are the board reads, and listing boards.
func readsWithList() map[string]func(ctx context.Context, w *teamWorld, p board.Principal) error {
	out := map[string]func(ctx context.Context, w *teamWorld, p board.Principal) error{}
	for k, v := range reads {
		out[k] = v
	}
	out["list"] = func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.ListBoards(ctx, p, true)
		return err
	}
	return out
}

// A key revoked, or expiring, while a read waits for its transaction reads nothing: every
// read checks the credential, with the time, inside its own transaction.
func TestAKeyEndingWhileABoardReadWaitsEndsTheRead(t *testing.T) {
	for name, read := range readsWithList() {
		for _, who := range []string{"person", "agent"} {
			for _, how := range []string{"revoked", "expired"} {
				t.Run(name+" by the "+who+", key "+how, func(t *testing.T) {
					w := newTeamWorld(t)
					ctx := context.Background()
					person, agent, keyID := w.shortKey(ctx, t)
					p := person
					if who == "agent" {
						p = agent
					}
					if err := read(ctx, w, p); err != nil {
						t.Fatalf("before the key ended: %v", err)
					}
					err := w.gatedRead(t, func() error { return read(ctx, w, p) }, func() {
						if how == "expired" {
							w.clk.Advance(board.MinKeyTTL + time.Minute)
							return
						}
						if _, err := w.svc.RevokeKey(ctx, w.sam, keyID); err != nil {
							t.Error(err)
						}
					})
					wantCode(t, name, err, "unauthorized")
				})
			}
		}
	}
}

// Removing a person, or their leaving, ends their agents on the board for good: added
// back, the person returns, but their old agent's token is refused; a new agent works,
// and the record names the agents that ended.
func TestRemovedPeoplesAgentsStayRemoved(t *testing.T) {
	for _, how := range []string{"removed", "left"} {
		t.Run(how, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			if how == "removed" {
				if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := w.svc.Leave(ctx, w.sam, w.board); err != nil {
				t.Fatal(err)
			}
			if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "sam"); err != nil {
				t.Fatal(err)
			}
			_, err := w.svc.PostMessage(ctx, w.samAgent, w.board, board.NewMessage{Body: "back again"})
			wantCode(t, "the old agent posting", err, "agent_removed")
			_, _, err = w.svc.Inbox(ctx, w.samAgent, 0, 0, 10)
			wantCode(t, "the old agent's inbox", err, "agent_removed")
			joined, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: w.board, Role: "member"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.svc.PostMessage(ctx, w.auth(ctx, t, joined.Token), w.board, board.NewMessage{Body: "new agent"}); err != nil {
				t.Fatalf("the new agent posting: %v", err)
			}
			log, err := w.svc.Events(ctx, w.maya, w.board, 0, 200)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range log.Events {
				if e.Type == "person."+how && strings.Contains(string(e.Data), w.samAgent.Agent.ID) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no person.%s event names the agent %s", how, w.samAgent.Agent.ID)
			}
		})
	}
}
