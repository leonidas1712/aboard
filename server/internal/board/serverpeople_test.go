package board_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// gatedWrite runs op while it waits before its write transaction, runs change in that
// wait, and returns op's error.
func (w *teamWorld) gatedWrite(t *testing.T, op func() error, change func()) error {
	t.Helper()
	waiting, release := w.gate.armWrite()
	done := make(chan error, 1)
	go func() { done <- op() }()
	<-waiting
	change()
	close(release)
	return <-done
}

// person connects a new person with an invite from alex and returns their principal.
func (w *teamWorld) person(ctx context.Context, t *testing.T, handle string) board.Principal {
	t.Helper()
	inv, err := w.svc.CreateServerInvite(ctx, w.alex, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.svc.Connect(ctx, board.ConnectInput{Invite: inv.Secret, Handle: handle, KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	return w.auth(ctx, t, c.Token)
}

// The server always keeps an admin: its last admin can be neither made a member nor
// removed, by anyone, themselves included; once there is another, either works.
func TestTheServerKeepsItsLastAdmin(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	_, err := w.svc.SetServerRole(ctx, w.alex, "alex", board.ServerMember)
	wantCode(t, "the last admin made a member", err, "last_admin")
	_, err = w.svc.RemoveFromServer(ctx, w.alex, "alex", false)
	wantCode(t, "the last admin removed", err, "last_admin")
	_, err = w.svc.SetServerRole(ctx, w.maya, "maya", board.ServerAdmin)
	wantCode(t, "a member making herself an admin", err, "server_admin_required")

	c, err := w.svc.SetServerRole(ctx, w.alex, "maya", board.ServerAdmin)
	if err != nil || !c.Changed || c.Person.Role != board.ServerAdmin {
		t.Fatalf("maya made an admin: %+v %v", c, err)
	}
	if c, err = w.svc.SetServerRole(ctx, w.alex, "maya", board.ServerAdmin); err != nil || c.Changed {
		t.Fatalf("maya made an admin again: %+v %v", c, err)
	}
	if _, err := w.svc.SetServerRole(ctx, w.maya, "alex", board.ServerMember); err != nil {
		t.Fatalf("maya making alex a member: %v", err)
	}
	_, err = w.svc.SetServerRole(ctx, w.maya, "maya", board.ServerMember)
	wantCode(t, "maya, now the last admin, made a member", err, "last_admin")
	_, err = w.svc.SetServerRole(ctx, w.alex, "sam", board.ServerAdmin)
	wantCode(t, "alex, no longer an admin, changing a role", err, "server_admin_required")
}

// Changing roles and removing people needs an admin's own key: an agent, even an
// admin's, and a browser are refused before anything is read.
func TestOnlyAnAdminsOwnKeyManagesThePeopleOnTheServer(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	joined, err := w.svc.Join(ctx, w.alex, board.JoinInput{Board: w.openBoard(ctx, t), Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	alexAgent := w.auth(ctx, t, joined.Token)
	browser := w.alex
	browser.Browser = true
	for name, p := range map[string]board.Principal{"an admin's agent": alexAgent, "an admin's browser": browser} {
		_, err := w.svc.SetServerRole(ctx, p, "maya", board.ServerAdmin)
		wantCode(t, name+" changing a role", err, "human_token_required")
		_, err = w.svc.RemoveFromServer(ctx, p, "maya", true)
		wantCode(t, name+" removing a person", err, "human_token_required")
	}
	people, err := w.svc.ListServerPeople(ctx, alexAgent)
	if err != nil || len(people) == 0 {
		t.Fatalf("agent list: %v %v", people, err)
	}
	_, err = w.svc.RemoveFromServer(ctx, w.maya, "sam", false)
	wantCode(t, "a member removing someone", err, "server_admin_required")
	_, err = w.svc.RemoveFromServer(ctx, w.alex, "nobody", false)
	wantCode(t, "removing someone who isn't there", err, "person_not_found")
}

// openBoard is an open board of alex's.
func (w *teamWorld) openBoard(ctx context.Context, t *testing.T) string {
	t.Helper()
	v, err := w.svc.CreateBoard(ctx, w.alex, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	return v.Board.Name
}

// An admin demoted while their change waits for its transaction can't make it: the role
// is read again inside the transaction that acts on it.
func TestAnAdminDemotedWhileTheirChangeWaitsCantMakeIt(t *testing.T) {
	ops := map[string]func(ctx context.Context, w *teamWorld) error{
		"role": func(ctx context.Context, w *teamWorld) error {
			_, err := w.svc.SetServerRole(ctx, w.alex, "sam", board.ServerAdmin)
			return err
		},
		"remove": func(ctx context.Context, w *teamWorld) error {
			_, err := w.svc.RemoveFromServer(ctx, w.alex, "sam", false)
			return err
		},
		"invite": func(ctx context.Context, w *teamWorld) error {
			_, err := w.svc.CreateServerInvite(ctx, w.alex, 0)
			return err
		},
		"settings": func(ctx context.Context, w *teamWorld) error {
			_, err := w.svc.UpdateServerSettings(ctx, w.alex, board.Settings{BoardCreation: board.CreationAdmins})
			return err
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			if _, err := w.svc.SetServerRole(ctx, w.alex, "maya", board.ServerAdmin); err != nil {
				t.Fatal(err)
			}
			err := w.gatedWrite(t, func() error { return op(ctx, w) }, func() {
				if _, err := w.svc.SetServerRole(ctx, w.maya, "alex", board.ServerMember); err != nil {
					t.Error(err)
				}
			})
			wantCode(t, name, err, "server_admin_required")
			people, err := w.svc.ListServerPeople(ctx, w.maya)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range people {
				if p.Name == "sam" && p.Role != board.ServerMember {
					t.Fatalf("sam after the refused change: %+v", p)
				}
			}
		})
	}
}

// Two admins demoting each other at once leave one admin: the second change, rechecked
// inside its transaction, finds its admin demoted.
func TestTwoAdminsDemotingEachOtherKeepOne(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.SetServerRole(ctx, w.alex, "maya", board.ServerAdmin); err != nil {
		t.Fatal(err)
	}
	err := w.gatedWrite(t, func() error {
		_, err := w.svc.RemoveFromServer(ctx, w.alex, "maya", false)
		return err
	}, func() {
		if _, err := w.svc.RemoveFromServer(ctx, w.maya, "alex", false); err != nil {
			t.Error(err)
		}
	})
	wantCode(t, "alex removing maya after maya removed alex", err, "unauthorized")
	people, err := w.svc.ListServerPeople(ctx, w.maya)
	if err != nil {
		t.Fatal(err)
	}
	admins := 0
	for _, p := range people {
		if p.Role == board.ServerAdmin {
			admins++
		}
	}
	if admins != 1 {
		t.Fatalf("admins after the race: %+v", people)
	}
}

// A person removed from the server while their write, or their agent's, waits for its
// transaction writes nothing, and their reads read nothing.
func TestRemovalFromTheServerWhileARequestWaitsEndsIt(t *testing.T) {
	for _, who := range []string{"person", "agent"} {
		t.Run("post by the "+who, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			p := w.sam
			if who == "agent" {
				p = w.samAgent
			}
			err := w.gatedWrite(t, func() error {
				_, err := w.svc.PostMessage(ctx, p, w.board, board.NewMessage{Body: "after removal"})
				return err
			}, func() {
				if _, err := w.svc.RemoveFromServer(ctx, w.alex, "sam", false); err != nil {
					t.Error(err)
				}
			})
			wantCode(t, "the post", err, "unauthorized")
			r, err := w.svc.Timeline(ctx, w.maya, w.board, board.TimelineFilter{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range r.Messages {
				if m.Body == "after removal" {
					t.Fatal("the removed person's message was stored")
				}
			}
		})
		for name, read := range reads {
			t.Run(name+" by the "+who, func(t *testing.T) {
				w := newTeamWorld(t)
				ctx := context.Background()
				p := w.sam
				if who == "agent" {
					p = w.samAgent
				}
				err := w.gatedRead(t, func() error { return read(ctx, w, p) }, func() {
					if _, err := w.svc.RemoveFromServer(ctx, w.alex, "sam", false); err != nil {
						t.Error(err)
					}
				})
				wantCode(t, name, err, "unauthorized")
			})
		}
	}
}

// Removing a person from the server, in one step: their keys, browser logins and agents
// stop; they leave every board, each recording the admin who removed them; ownership of a
// board they were the last owner of passes to the person on it longest; a private board
// left with no one is reported; and their handle is free for a new person, who
// inherits nothing. A preview changes nothing.
func TestRemovingAPersonFromTheServer(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	alone, err := w.svc.CreateBoard(ctx, w.maya, board.NewBoard{Template: "general", Visibility: board.BoardPrivate})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.maya, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	mayaAgent := w.auth(ctx, t, joined.Token)
	tok, _, err := w.svc.CreateBrowserToken(ctx, mustLoginCode(ctx, t, w.svc, w.maya))
	if err != nil {
		t.Fatal(err)
	}
	mayaBrowser := w.auth(ctx, t, tok)

	preview, err := w.svc.RemoveFromServer(ctx, w.alex, "maya", true)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || preview.KeysRevoked != 1 || preview.BrowserSessions != 1 || preview.Agents != 1 || preview.Boards != 2 ||
		preview.OwnersPassed != 1 || len(preview.Unreachable) != 1 || preview.Unreachable[0] != alone.Board.ID {
		t.Fatalf("the preview: %+v", preview)
	}
	if _, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "still here after the preview"}); err != nil {
		t.Fatalf("maya after the preview: %v", err)
	}

	done, err := w.svc.RemoveFromServer(ctx, w.alex, "maya", false)
	if err != nil {
		t.Fatal(err)
	}
	if done.DryRun || done.KeysRevoked != 1 || done.Agents != 1 || done.Boards != 2 || done.OwnersPassed != 1 {
		t.Fatalf("the removal: %+v", done)
	}
	for name, p := range map[string]board.Principal{"key": w.maya, "browser": mayaBrowser, "agent": mayaAgent} {
		_, err := w.svc.PostMessage(ctx, p, w.board, board.NewMessage{Body: "after removal"})
		wantCode(t, "maya's "+name+" after removal", err, "unauthorized")
	}
	if _, err := w.svc.Authenticate(ctx, joined.Token); err == nil {
		t.Fatal("maya's agent token still authenticates")
	}

	// sam, on the board longest after maya, owns it now; the record says who removed
	// maya and why sam is an owner.
	people, err := w.svc.People(ctx, w.sam, w.board)
	if err != nil {
		t.Fatal(err)
	}
	if len(people.People) != 1 || people.People[0].Person.Name != "sam" || !people.People[0].IsOwner() {
		t.Fatalf("the board's people after maya's removal: %+v", people.People)
	}
	log, err := w.svc.Events(ctx, w.sam, w.board, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var removed, owner bool
	for _, e := range log.Events {
		var data map[string]any
		_ = json.Unmarshal(e.Data, &data)
		switch {
		case e.Type == "person.removed" && data["from_server"] == true && data["name"] == "maya":
			removed = e.Actor.Kind == "human" && e.Actor.Name != nil && *e.Actor.Name == "alex" &&
				strings.Contains(string(e.Data), mayaAgent.Agent.ID)
		case e.Type == "person.made_owner" && data["reason"] == "owner_removed_from_server" && data["name"] == "sam":
			owner = e.Actor.Kind == "system"
		}
	}
	if !removed || !owner {
		t.Fatalf("the record after maya's removal: removed %v, owner passed %v", removed, owner)
	}

	// The handle is free: a new maya is a different person, on no board.
	newMaya := w.person(ctx, t, "maya")
	if newMaya.Human.ID == w.maya.Human.ID {
		t.Fatal("the new maya has the removed maya's id")
	}
	_, err = w.svc.GetBoard(ctx, newMaya, w.board)
	wantCode(t, "the new maya looking at the old maya's private board", err, "board_not_found")
	_, err = w.svc.RemoveFromServer(ctx, w.alex, "maya", false)
	if err != nil {
		t.Fatalf("removing the new maya: %v", err)
	}
}

func mustLoginCode(ctx context.Context, t *testing.T, svc *board.Service, p board.Principal) string {
	t.Helper()
	c, err := svc.CreateLoginCode(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return c.Code
}
