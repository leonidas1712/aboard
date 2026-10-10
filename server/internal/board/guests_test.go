package board_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

// guestCode makes a guest code on the team's board for handle, as maker.
func (w *teamWorld) guestCode(ctx context.Context, t *testing.T, maker board.Principal, handle string) string {
	t.Helper()
	jc, err := w.svc.CreateJoinCode(ctx, maker, w.board, board.JoinCodeInput{Role: "member", Guest: handle})
	if err != nil {
		t.Fatal(err)
	}
	if jc.JoinCode.Kind != board.CodeGuest || jc.Line != "Join Aboard board "+w.board+" on localhost as guest with code "+jc.Code {
		t.Fatalf("the guest code: %+v", jc)
	}
	return jc.Code
}

// A guest code brings one person from outside the server onto its board, once, as a
// guest with an agent of their own; the guest's agent reads and posts there and reaches
// nothing else, and the server's people list them as a guest.
func TestAGuestCodeLetsOneGuestOntoOneBoard(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	code := w.guestCode(ctx, t, w.sam, "kim")
	joined, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: code, Harness: "claude-code", KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if joined.Agent.Name != "claude" || joined.Agent.Owner == nil || *joined.Agent.Owner != "kim" ||
		joined.Agent.KeyID == nil || *joined.Agent.KeyID != joined.Key.ID || joined.Person.Role != board.ServerGuest {
		t.Fatalf("the guest's agent: %+v", joined.Agent)
	}
	kim := w.auth(ctx, t, joined.Token)
	if _, err := w.svc.PostMessage(ctx, kim, w.board, board.NewMessage{Body: "hello from a guest"}); err != nil {
		t.Fatalf("the guest's agent posting: %v", err)
	}
	if _, err := w.svc.Timeline(ctx, kim, w.board, board.TimelineFilter{Limit: 10}); err != nil {
		t.Fatalf("the guest's agent reading: %v", err)
	}
	_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: code, KeyName: "laptop"})
	wantCode(t, "the guest code used again", err, "join_code_invalid")

	people, err := w.svc.ListServerPeople(ctx, w.maya)
	if err != nil {
		t.Fatal(err)
	}
	if last := people[len(people)-1]; last.Name != "kim" || last.Role != board.ServerGuest {
		t.Fatalf("the server's people: %+v", people)
	}
	onBoard, err := w.svc.People(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	if last := onBoard.People[len(onBoard.People)-1]; last.Person.Name != "kim" || last.IsOwner() {
		t.Fatalf("the board's people: %+v", onBoard.People)
	}
}

// A guest, with their own key or through their agent, sees only their board: every
// other board, open or private, answers exactly as a board that doesn't exist; they list
// only their own board; and they can't make join codes, change the title or list the
// server's people. With their key they also can't create boards, add people or agents,
// cancel codes, or manage keys.
func TestAGuestSeesOnlyTheirBoard(t *testing.T) {
	for _, who := range []string{"guest", "agent"} {
		t.Run(who, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			open := w.openBoard(ctx, t)
			private, err := w.svc.CreateBoard(ctx, w.alex, board.NewBoard{Template: "general", Visibility: board.BoardPrivate})
			if err != nil {
				t.Fatal(err)
			}
			joined, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
			if err != nil {
				t.Fatal(err)
			}
			kim := w.auth(ctx, t, joined.KeyToken)
			if who == "agent" {
				kim = w.auth(ctx, t, joined.Token)
			}
			for _, name := range []string{open, private.Board.Name} {
				_, err := w.svc.GetBoard(ctx, kim, name)
				e, ok := apierr.As(err)
				m := apierr.BoardNotFound(name)
				if !ok || e.Code != "board_not_found" || e.Status != m.Status || e.Message != m.Message || e.Hint != m.Hint {
					t.Fatalf("the guest looking at %s: %v, a missing board: %v", name, err, m)
				}
				_, err = w.svc.People(ctx, kim, name)
				wantCode(t, "the guest listing "+name+"'s people", err, "board_not_found")
				_, err = w.svc.AddPerson(ctx, kim, name, "kim")
				wantCode(t, "the guest joining "+name, err, "board_not_found")
			}
			list, err := w.svc.ListBoards(ctx, kim, true)
			if err != nil || len(list.Boards) != 1 || list.Boards[0].Board.Name != w.board || len(list.Hidden) != 0 {
				t.Fatalf("the guest listing boards: %+v %v", list, err)
			}
			if _, err := w.svc.PostMessage(ctx, kim, w.board, board.NewMessage{Body: "hello from " + who}); err != nil {
				t.Fatalf("the guest posting: %v", err)
			}
			title := "A guest's title"
			_, err = w.svc.UpdateBoard(ctx, kim, w.board, board.Change{Title: &title})
			wantCode(t, "the guest changing the title", err, "guest_not_allowed")
			_, err = w.svc.CreateJoinCode(ctx, kim, w.board, board.JoinCodeInput{Role: "member"})
			wantCode(t, "the guest making a pairing code", err, "guest_not_allowed")
			_, err = w.svc.CreateJoinCode(ctx, kim, w.board, board.JoinCodeInput{Role: "member", Guest: "lee"})
			wantCode(t, "the guest making a guest code", err, map[string]string{"guest": "guest_not_allowed", "agent": "human_token_required"}[who])
			listed, err := w.svc.ListServerPeople(ctx, kim)
			wantCode(t, "the guest listing the server's people", err, "guest_not_allowed")
			if len(listed) != 0 {
				t.Fatalf("the guest received the server directory: %+v", listed)
			}
			if who == "agent" {
				return
			}
			_, err = w.svc.CreateBoard(ctx, kim, board.NewBoard{Template: "general"})
			wantCode(t, "the guest creating a board", err, "guest_not_allowed")
			_, err = w.svc.AddPerson(ctx, kim, w.board, "alex")
			wantCode(t, "the guest adding alex", err, "guest_not_allowed")
			_, err = w.svc.Join(ctx, kim, board.JoinInput{Board: w.board, Role: "member"})
			wantCode(t, "the guest adding an agent", err, "guest_not_allowed")
			pairing, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = w.svc.Join(ctx, kim, board.JoinInput{Code: pairing.Code})
			wantCode(t, "the guest redeeming maya's pairing code", err, "guest_not_allowed")
			_, _, err = w.svc.RevokeJoinCode(ctx, kim, w.board, pairing.JoinCode.ID)
			wantCode(t, "the guest canceling maya's code", err, "guest_not_allowed")
			_, err = w.svc.ListKeys(ctx, kim, "")
			wantCode(t, "the guest listing keys", err, "guest_not_allowed")
			_, err = w.svc.CreateKey(ctx, kim, "phone", 0)
			wantCode(t, "the guest making a key", err, "guest_not_allowed")
			_, err = w.svc.RevokeKey(ctx, kim, joined.Key.ID)
			wantCode(t, "the guest revoking a key", err, "guest_not_allowed")
			_, err = w.svc.CreateServerInvite(ctx, kim, 0)
			wantCode(t, "the guest inviting someone", err, "server_admin_required")
			_, _, err = w.svc.MakeOwner(ctx, kim, w.board, "kim")
			wantCode(t, "the guest making themselves an owner", err, "owner_required")
			_, err = w.svc.SetVisibility(ctx, kim, w.board, board.BoardOpen, true)
			wantCode(t, "the guest turning the board open", err, "owner_required")
			if _, err := w.svc.CreateLoginCode(ctx, kim); err != nil {
				t.Fatalf("the guest signing a browser in: %v", err)
			}
		})
	}
}

// A guest's key redeems a later guest code for them: they come onto the second board
// with a new agent; a guest code for someone else is refused.
func TestAGuestWithAKeyRedeemsTheirNextCode(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	first, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	kim := w.auth(ctx, t, first.KeyToken)
	other := w.openBoard(ctx, t)
	for handle, want := range map[string]string{"lee": "guest_code_not_for_members", "kim": ""} {
		jc, err := w.svc.CreateJoinCode(ctx, w.alex, other, board.JoinCodeInput{Role: "member", Guest: handle})
		if err != nil {
			t.Fatal(err)
		}
		joined, err := w.svc.Join(ctx, kim, board.JoinInput{Code: jc.Code})
		if want != "" {
			wantCode(t, "kim redeeming lee's guest code", err, want)
			continue
		}
		if err != nil || joined.Agent.HumanID != first.Person.ID || joined.Agent.KeyID == nil || *joined.Agent.KeyID != first.Key.ID {
			t.Fatalf("kim redeeming her second guest code: %+v %v", joined.Agent, err)
		}
	}
	list, err := w.svc.ListBoards(ctx, kim, true)
	if err != nil || len(list.Boards) != 2 {
		t.Fatalf("kim's boards: %+v %v", list, err)
	}
}

// Each kind of code works only for its own purpose, and only people make guest codes,
// for handles that are free or a guest's; a guest never becomes an owner or a teammate
// some other way.
func TestCodesWorkOnlyForTheirPurpose(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	guest := w.guestCode(ctx, t, w.maya, "kim")
	_, err := w.svc.Join(ctx, w.sam, board.JoinInput{Code: guest})
	wantCode(t, "a member redeeming a guest code", err, "guest_code_not_for_members")
	pairing, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: pairing.Code, KeyName: "laptop"})
	wantCode(t, "a pairing code at the guest door", err, "join_code_invalid")
	_, err = w.svc.Join(ctx, w.sam, board.JoinInput{Code: pairing.Code})
	wantCode(t, "sam redeeming maya's pairing code", err, "join_code_not_yours")
	_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: "ZZZ-ZZZ", KeyName: "laptop"})
	wantCode(t, "a guessed guest code", err, "join_code_invalid")

	_, err = w.svc.CreateJoinCode(ctx, w.samAgent, w.board, board.JoinCodeInput{Role: "member", Guest: "lee"})
	wantCode(t, "an agent making a guest code", err, "human_token_required")
	_, err = w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member", Guest: "alex"})
	wantCode(t, "a guest code for a member's handle", err, "handle_taken")

	if _, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: guest, KeyName: "laptop"}); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member", Guest: "kim"})
	wantCode(t, "a guest code for a guest already on the board", err, "already_on_board")
	_, _, err = w.svc.MakeOwner(ctx, w.maya, w.board, "kim")
	wantCode(t, "making a guest an owner", err, "person_is_guest")
	_, err = w.svc.AddPerson(ctx, w.alex, w.openBoard(ctx, t), "kim")
	wantCode(t, "adding a guest to another board", err, "person_is_guest")
	_, err = w.svc.SetServerRole(ctx, w.alex, "kim", board.ServerMember)
	wantCode(t, "making a guest a member", err, "person_is_guest")
}

// A guest invited onto a second board is the same person there, with a new agent.
func TestAGuestOnTwoBoardsIsOnePerson(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	first, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	other := w.openBoard(ctx, t)
	jc, err := w.svc.CreateJoinCode(ctx, w.alex, other, board.JoinCodeInput{Role: "member", Guest: "kim"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.svc.Join(ctx, w.auth(ctx, t, first.KeyToken), board.JoinInput{Code: jc.Code})
	if err != nil {
		t.Fatal(err)
	}
	if first.Agent.HumanID != second.Agent.HumanID {
		t.Fatalf("kim is two people: %s and %s", first.Agent.HumanID, second.Agent.HumanID)
	}
	// Each agent reaches only its own board.
	_, err = w.svc.Timeline(ctx, w.auth(ctx, t, first.Token), other, board.TimelineFilter{Limit: 1})
	wantCode(t, "kim's first agent on the second board", err, "board_not_found")
}

// A guest code stops working when its maker, or the maker's standing, ends while the
// redemption waits for its transaction; and a handle a member took meanwhile is refused.
func TestAGuestCodeIsCheckedWhenItIsUsed(t *testing.T) {
	changes := map[string]struct {
		change func(ctx context.Context, w *teamWorld, t *testing.T)
		code   string
	}{
		"maker removed from the board": {func(ctx context.Context, w *teamWorld, t *testing.T) {
			if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
				t.Error(err)
			}
		}, "join_code_invalid"},
		"maker removed from the server": {func(ctx context.Context, w *teamWorld, t *testing.T) {
			if _, err := w.svc.RemoveFromServer(ctx, w.alex, "sam", false); err != nil {
				t.Error(err)
			}
		}, "join_code_invalid"},
		"board made private and back": {func(ctx context.Context, w *teamWorld, t *testing.T) {
			if _, err := w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardOpen, false); err != nil {
				t.Error(err)
			}
			if _, err := w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardPrivate, false); err != nil {
				t.Error(err)
			}
		}, "join_code_invalid"},
		"handle taken by a member": {func(ctx context.Context, w *teamWorld, t *testing.T) {
			w.person(ctx, t, "kim")
		}, "join_code_invalid"},
	}
	for name, c := range changes {
		t.Run(name, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			code := w.guestCode(ctx, t, w.sam, "kim")
			err := w.gatedWrite(t, func() error {
				_, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: code, KeyName: "laptop"})
				return err
			}, func() { c.change(ctx, w, t) })
			wantCode(t, name, err, c.code)
			people, err := w.svc.People(ctx, w.maya, w.board)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range people.People {
				if p.Person.Role == board.ServerGuest {
					t.Fatalf("a guest came on after %s: %+v", name, p)
				}
			}
		})
	}
}

// A guest removed from the server loses their agents on every board at once.
func TestRemovingAGuestEndsTheirAgents(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	joined, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	kim := w.auth(ctx, t, joined.Token)
	err = w.gatedWrite(t, func() error {
		_, err := w.svc.PostMessage(ctx, kim, w.board, board.NewMessage{Body: "late"})
		return err
	}, func() {
		if _, err := w.svc.RemoveFromServer(ctx, w.alex, "kim", false); err != nil {
			t.Error(err)
		}
	})
	wantCode(t, "the removed guest's agent posting", err, "unauthorized")
	if _, err := w.svc.Authenticate(ctx, joined.Token); err == nil {
		t.Fatal("the removed guest's agent token still authenticates")
	}
}

func TestRacingGuestCodesCreateOnlyOneIdentity(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	codes := []string{w.guestCode(ctx, t, w.maya, "lee"), w.guestCode(ctx, t, w.maya, "lee")}
	ready := make(chan struct{})
	results := make(chan error, len(codes))
	for _, code := range codes {
		go func() {
			<-ready
			_, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: code, KeyName: "laptop"})
			results <- err
		}()
	}
	close(ready)
	success := 0
	for range codes {
		err := <-results
		if err == nil {
			success++
		} else {
			wantCode(t, "another code for the same new guest", err, "join_code_invalid")
		}
	}
	if success != 1 {
		t.Fatalf("%d guest identities created, want exactly one", success)
	}
}

func TestAGuestCodeDoesNotFollowAReusedHandle(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	first, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	other := w.openBoard(ctx, t)
	oldCode, err := w.svc.CreateJoinCode(ctx, w.alex, other, board.JoinCodeInput{Role: "member", Guest: "kim"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.RemoveFromServer(ctx, w.alex, "kim", false); err != nil {
		t.Fatal(err)
	}
	replacement, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Person.ID == replacement.Person.ID {
		t.Fatal("removed identity was restored")
	}
	_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: oldCode.Code, KeyName: "phone"})
	wantCode(t, "anonymous redemption of an identity-bound code", err, "join_code_invalid")
	_, err = w.svc.Join(ctx, w.auth(ctx, t, replacement.KeyToken), board.JoinInput{Code: oldCode.Code})
	wantCode(t, "replacement person redeeming the old person's code", err, "guest_code_not_for_members")
}
