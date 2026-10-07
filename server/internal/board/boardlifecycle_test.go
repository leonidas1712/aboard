package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func lifecycleBoard(t *testing.T, w *teamWorld) board.Board {
	t.Helper()
	v, err := w.svc.GetBoard(context.Background(), w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	return v.Board
}

func archiveLifecycleBoard(t *testing.T, w *teamWorld) {
	t.Helper()
	if _, err := w.svc.ArchiveBoard(context.Background(), w.maya, w.board); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveFreezesContentAndGrantsWithoutChangingTheRecord(t *testing.T) {
	for _, which := range []string{"person post", "agent post", "reply", "reaction add", "reaction remove", "add person", "pair code", "guest code", "person join", "delegated join", "guest redeem", "title", "policy", "make owner", "make open"} {
		t.Run(which, func(t *testing.T) {
			ctx := context.Background()
			w := newTeamWorld(t)
			m, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "record before archive"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.svc.React(ctx, w.sam, m.ID, "thumbsup", true); err != nil {
				t.Fatal(err)
			}
			guest, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Guest: "visitor", Role: "member"})
			if err != nil {
				t.Fatal(err)
			}
			d, err := w.svc.CreateDelegation(ctx, w.sam, "lifecycle-test")
			if err != nil {
				t.Fatal(err)
			}
			delegate := w.auth(ctx, t, d.Token)
			archiveLifecycleBoard(t, w)
			before := lifecycleBoard(t, w)
			switch which {
			case "person post":
				_, err = w.svc.PostMessage(ctx, w.sam, w.board, board.NewMessage{Body: "must not appear"})
			case "agent post":
				_, err = w.svc.PostMessage(ctx, w.samAgent, w.board, board.NewMessage{Body: "must not appear"})
			case "reply":
				_, err = w.svc.PostMessage(ctx, w.sam, w.board, board.NewMessage{Body: "must not appear", ReplyTo: &m.ID})
			case "reaction add":
				_, err = w.svc.React(ctx, w.sam, m.ID, "heart", true)
			case "reaction remove":
				_, err = w.svc.React(ctx, w.sam, m.ID, "thumbsup", false)
			case "add person":
				_, err = w.svc.AddPerson(ctx, w.maya, w.board, "alex")
			case "pair code":
				_, err = w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member"})
			case "guest code":
				_, err = w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Guest: "second-visitor", Role: "member"})
			case "person join":
				_, err = w.svc.Join(ctx, w.sam, board.JoinInput{Board: w.board, Role: "member"})
			case "delegated join":
				_, err = w.svc.Join(ctx, delegate, board.JoinInput{Board: w.board, Session: "codex:lifecycle-test", Role: "member"})
			case "guest redeem":
				_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: guest.Code, KeyName: "visitor-laptop"})
			case "title":
				title := "must not appear"
				_, err = w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{Title: &title})
			case "policy":
				policy := rules.PolicyChange{Preset: before.Policy.Preset}
				_, err = w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{Policy: &policy})
			case "make owner":
				_, _, err = w.svc.MakeOwner(ctx, w.maya, w.board, "sam")
			case "make open":
				_, err = w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardOpen, false)
			}
			wantCode(t, which, err, "board_archived")
			after := lifecycleBoard(t, w)
			if before.HeadSeq != after.HeadSeq || before.HeadHash != after.HeadHash {
				t.Fatal("refused archived mutation changed event chain")
			}
			if _, err = w.svc.RestoreBoard(ctx, w.maya, w.board); err != nil {
				t.Fatal(err)
			}
			if _, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: guest.Code, KeyName: "visitor-laptop"}); err != nil {
				t.Fatalf("refusal consumed or revoked existing guest code: %v", err)
			}
		})
	}
}

func TestArchivedReadersAndAccessReductionsStillWork(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	if _, _, err := w.svc.MakeOwner(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardOpen, false); err != nil {
		t.Fatal(err)
	}
	code, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "still readable"})
	if err != nil {
		t.Fatal(err)
	}
	archiveLifecycleBoard(t, w)
	for name, read := range reads {
		if err := read(ctx, w, w.sam); err != nil {
			t.Fatalf("archived %s: %v", name, err)
		}
	}
	if _, _, err = w.svc.Inbox(ctx, w.samAgent, 0, 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.Ack(ctx, w.samAgent, m.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.AckBoard(ctx, w.sam, w.board, m.Seq); err != nil {
		t.Fatal(err)
	}
	if _, _, err = w.svc.SetPresence(ctx, w.samAgent, "idle", "focused"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.SetDeliveryMode(ctx, w.sam, w.board, w.samAgent.Agent.Name, "off"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = w.svc.RevokeJoinCode(ctx, w.maya, w.board, code.JoinCode.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardPrivate, false); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.RestoreBoard(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	_, _, err = w.svc.Inbox(ctx, w.samAgent, 0, 0, 10)
	wantCode(t, "restore cannot revive removed seat", err, "agent_removed")
}

func TestLifecycleAuthorityUsesCreatorAndCurrentPersonRatherThanBoardOwner(t *testing.T) {
	for _, who := range []string{"creator", "creator agent", "another owner", "another owner agent", "outside admin", "admin agent"} {
		t.Run(who, func(t *testing.T) {
			ctx := context.Background()
			w := newTeamWorld(t)
			if _, _, err := w.svc.MakeOwner(ctx, w.maya, w.board, "sam"); err != nil {
				t.Fatal(err)
			}
			created := lifecycleBoard(t, w)
			p, selector := w.maya, w.board
			expected := ""
			switch who {
			case "creator agent":
				j, err := w.svc.Join(ctx, w.maya, board.JoinInput{Board: w.board, Role: "member"})
				if err != nil {
					t.Fatal(err)
				}
				p = w.auth(ctx, t, j.Token)
			case "another owner":
				p = w.sam
				expected = "board_creator_required"
			case "another owner agent":
				p = w.samAgent
				expected = "board_creator_required"
			case "outside admin":
				p = w.alex
				selector = created.ID
			case "admin agent":
				v, err := w.svc.CreateBoard(ctx, w.alex, board.NewBoard{Template: "general"})
				if err != nil {
					t.Fatal(err)
				}
				j, err := w.svc.Join(ctx, w.alex, board.JoinInput{Board: v.Board.Name, Role: "member"})
				if err != nil {
					t.Fatal(err)
				}
				p = w.auth(ctx, t, j.Token)
				selector = created.ID
				expected = "board_not_found"
			}
			_, err := w.svc.ArchiveBoard(ctx, p, selector)
			if expected != "" {
				wantCode(t, who, err, expected)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.svc.RestoreBoard(ctx, p, selector); err != nil {
				t.Fatal(err)
			}
			if who == "outside admin" {
				_, err = w.svc.GetBoard(ctx, w.alex, w.board)
				wantCode(t, "housekeeping cannot grant private content", err, "board_not_found")
			}
			if who == "creator agent" {
				_, err = w.svc.DeleteBoard(ctx, p, selector)
				wantCode(t, "agent cannot delete", err, "human_token_required")
			}
		})
	}
}

func TestDeletionPreservesHistoryAndNameButEndsOnlyThatBoardsAccess(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	sibling, err := w.svc.CreateBoard(ctx, w.sam, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: sibling.Board.Name, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	siblingAgent := w.auth(ctx, t, j.Token)
	if _, err = w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "retained"}); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.DeleteBoard(ctx, w.maya, w.board)
	wantCode(t, "delete active", err, "board_not_archived")
	archiveLifecycleBoard(t, w)
	before := lifecycleBoard(t, w)
	if _, err = w.svc.ArchiveBoard(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	unchanged := lifecycleBoard(t, w)
	if unchanged.HeadSeq != before.HeadSeq || unchanged.HeadHash != before.HeadHash {
		t.Fatal("repeat archive appended another event")
	}
	if _, err = w.svc.DeleteBoard(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	for name, read := range reads {
		wantCode(t, "deleted "+name, read(ctx, w, w.sam), "board_not_found")
	}
	_, _, err = w.svc.Inbox(ctx, w.samAgent, 0, 0, 10)
	wantCode(t, "deleted seat inbox", err, "unauthorized")
	_, err = w.svc.WhoAmI(ctx, w.samAgent)
	wantCode(t, "deleted seat metadata", err, "unauthorized")
	_, err = w.svc.RestoreBoard(ctx, w.maya, w.board)
	wantCode(t, "deleted cannot restore", err, "board_not_found")
	if _, _, err = w.svc.Inbox(ctx, siblingAgent, 0, 0, 10); err != nil {
		t.Fatalf("delete ended sibling: %v", err)
	}
	err = w.gate.Store.Read(ctx, func(tx board.ReadTx) error {
		b, e := tx.BoardByID(before.ID)
		if e != nil {
			return e
		}
		log, e := tx.Events(before.ID, 0, 100)
		if e != nil {
			return e
		}
		if b.HeadSeq != before.HeadSeq+1 || len(log) != int(b.HeadSeq) {
			t.Fatal("delete erased or changed historical chain rows")
		}
		if log[len(log)-1].Type != "board.deleted" || log[len(log)-1].PrevHash != before.HeadHash {
			t.Fatal("tombstone was not appended to retained hash chain")
		}
		taken, e := tx.BoardNameTaken(before.Name)
		if e != nil {
			return e
		}
		if !taken {
			t.Fatal("delete released reserved name")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleWriteRechecksCreatorAccessAndCredentialInsideTransaction(t *testing.T) {
	for _, how := range []string{"removed creator", "revoked key", "expired key"} {
		t.Run(how, func(t *testing.T) {
			ctx := context.Background()
			w := newTeamWorld(t)
			if _, _, err := w.svc.MakeOwner(ctx, w.maya, w.board, "sam"); err != nil {
				t.Fatal(err)
			}
			key, err := w.svc.CreateKey(ctx, w.maya, "lifecycle", board.MinKeyTTL)
			if err != nil {
				t.Fatal(err)
			}
			p := w.auth(ctx, t, key.Token)
			waiting, release := w.gate.armWrite()
			done := make(chan error, 1)
			go func() { _, e := w.svc.ArchiveBoard(ctx, p, w.board); done <- e }()
			<-waiting
			expected := "unauthorized"
			switch how {
			case "removed creator":
				_, err = w.svc.RemovePerson(ctx, w.sam, w.board, "maya")
				expected = "board_not_found"
			case "revoked key":
				_, err = w.svc.RevokeKey(ctx, w.maya, key.Key.ID)
			case "expired key":
				w.clk.Advance(board.MinKeyTTL + time.Second)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			wantCode(t, how, <-done, expected)
			if _, err = w.svc.PostMessage(ctx, w.sam, w.board, board.NewMessage{Body: "still active"}); err != nil {
				t.Fatalf("failed archive committed lifecycle: %v", err)
			}
		})
	}
}

func TestContentWriteWaitingForTransactionSeesCommittedArchive(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	waiting, release := w.gate.armWrite()
	done := make(chan error, 1)
	go func() {
		_, err := w.svc.PostMessage(ctx, w.sam, w.board, board.NewMessage{Body: "late write"})
		done <- err
	}()
	<-waiting
	archiveLifecycleBoard(t, w)
	before := lifecycleBoard(t, w)
	close(release)
	wantCode(t, "post after archive", <-done, "board_archived")
	after := lifecycleBoard(t, w)
	if after.HeadSeq != before.HeadSeq || after.HeadHash != before.HeadHash {
		t.Fatal("waiting post mutated archived record")
	}
}

func TestLifecycleListingKeepsHiddenArchiveMetadataOutOfCounts(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	archiveLifecycleBoard(t, w)
	member, err := w.svc.ListBoards(ctx, w.sam, false)
	if err != nil || len(member.Boards) != 0 || member.ArchivedCount != 1 {
		t.Fatalf("member active listing: %+v %v", member, err)
	}
	member, err = w.svc.ListBoards(ctx, w.maya, false, "archived")
	if err != nil || len(member.Boards) != 1 || !member.Boards[0].CanRestore || !member.Boards[0].CanDelete || member.Boards[0].CanArchive {
		t.Fatalf("creator archived listing: %+v %v", member, err)
	}
	admin, err := w.svc.ListBoards(ctx, w.alex, true, "archived")
	if err != nil || admin.ArchivedCount != 0 || len(admin.Hidden) != 1 || admin.Hidden[0].Lifecycle != board.LifecycleArchived || !admin.Hidden[0].CanRestore || !admin.Hidden[0].CanDelete {
		t.Fatalf("hidden housekeeping listing: %+v %v", admin, err)
	}
}

func TestFormerBoardMemberAdminLifecycleActorDoesNotClaimMembership(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "alex"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "alex"); err != nil {
		t.Fatal(err)
	}
	before := lifecycleBoard(t, w)
	if _, err := w.svc.ArchiveBoard(ctx, w.alex, before.ID); err != nil {
		t.Fatal(err)
	}
	err := w.gate.Store.Read(ctx, func(tx board.ReadTx) error {
		events, err := tx.Events(before.ID, before.HeadSeq, 1)
		if err != nil {
			return err
		}
		if len(events) != 1 || events[0].Actor.MemberID != nil || events[0].Actor.Kind != "human" {
			t.Fatalf("outside admin claimed membership: %+v", events)
		}
		member, err := tx.HumanMember(before.ID, w.alex.Human.ID)
		if err != nil {
			return err
		}
		if member.Status == board.StatusActive {
			t.Fatal("housekeeping restored membership")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeletionFeedEndsOnlyPreviouslyObservedBoard(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	before := lifecycleBoard(t, w)
	sibling, err := w.svc.CreateBoard(ctx, w.sam, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := w.svc.FollowHeads(w.sam)
	if err != nil {
		t.Fatal(err)
	}
	first, err := feed.Start(ctx)
	if err != nil || len(first.Heads) != 2 {
		t.Fatalf("first: %+v %v", first, err)
	}
	archiveLifecycleBoard(t, w)
	archived, _, err := feed.Next(ctx, make(chan time.Time))
	if err != nil || len(archived.Unavailable) != 0 || len(archived.Heads) != 1 || archived.Heads[0].BoardID != before.ID {
		t.Fatalf("archive feed: %+v %v", archived, err)
	}
	if _, err = w.svc.DeleteBoard(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	gone, _, err := feed.Next(ctx, make(chan time.Time))
	if err != nil || len(gone.Unavailable) != 1 || gone.Unavailable[0].BoardID != before.ID || gone.Unavailable[0].MemberID != nil {
		t.Fatalf("delete feed: %+v %v", gone, err)
	}
	if _, err = w.svc.PostMessage(ctx, w.sam, sibling.Board.Name, board.NewMessage{Body: "sibling survives"}); err != nil {
		t.Fatal(err)
	}
	live, _, err := feed.Next(ctx, make(chan time.Time))
	if err != nil || len(live.Unavailable) != 0 || len(live.Heads) != 1 || live.Heads[0].BoardID != sibling.Board.ID {
		t.Fatalf("sibling feed: %+v %v", live, err)
	}
}

func TestArchivedPairingCodeDoesNotGrantAnonymousLifecycleMetadata(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	code, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	archiveLifecycleBoard(t, w)
	_, err = w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: code.Code, KeyName: "visitor-laptop"})
	wantCode(t, "anonymous pairing code", err, "join_code_invalid")
	_, err = w.svc.Join(ctx, w.sam, board.JoinInput{Code: code.Code})
	wantCode(t, "someone else's pairing code", err, "join_code_not_yours")
}

func TestDeletedSeatCredentialEndsWhileSiblingAndPersonRemain(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	sibling, err := w.svc.CreateBoard(ctx, w.sam, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: sibling.Board.Name, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: w.board, Role: "member", Name: "old-token-test"})
	if err != nil {
		t.Fatal(err)
	}
	archiveLifecycleBoard(t, w)
	if _, err = w.svc.DeleteBoard(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Authenticate(ctx, old.Token)
	wantCode(t, "deleted credential", err, "unauthorized")
	if _, err = w.svc.Authenticate(ctx, j.Token); err != nil {
		t.Fatalf("sibling token: %v", err)
	}
	if _, err = w.svc.GetBoard(ctx, w.sam, sibling.Board.Name); err != nil {
		t.Fatalf("person key: %v", err)
	}
}

func TestDeletionBetweenHeadAndPresenceReadsNeverReturnsDeletedMetadata(t *testing.T) {
	for _, seen := range []bool{false, true} {
		t.Run(map[bool]string{false: "first observation", true: "already observed"}[seen], func(t *testing.T) {
			ctx := context.Background()
			w := newTeamWorld(t)
			before := lifecycleBoard(t, w)
			sibling, err := w.svc.CreateBoard(ctx, w.sam, board.NewBoard{Template: "general"})
			if err != nil {
				t.Fatal(err)
			}
			feed, err := w.svc.FollowHeads(w.sam)
			if err != nil {
				t.Fatal(err)
			}
			if seen {
				if _, err = feed.Start(ctx); err != nil {
					t.Fatal(err)
				}
			}
			archiveLifecycleBoard(t, w)
			waiting, release := w.gate.armAfter(1)
			type result struct {
				u   board.Update
				err error
			}
			done := make(chan result, 1)
			go func() { u, err := feed.Start(ctx); done <- result{u, err} }()
			<-waiting
			if _, err = w.svc.DeleteBoard(ctx, w.maya, w.board); err != nil {
				t.Fatal(err)
			}
			close(release)
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			for _, h := range got.u.Heads {
				if h.BoardID == before.ID {
					t.Fatal("returned deleted board name/head")
				}
			}
			for _, u := range got.u.Unread {
				if u.BoardID == before.ID {
					t.Fatal("returned deleted unread count")
				}
			}
			if seen && (len(got.u.Unavailable) != 1 || got.u.Unavailable[0].BoardID != before.ID) {
				t.Fatalf("prior observation hint: %+v", got.u)
			}
			if !seen && len(got.u.Unavailable) != 0 {
				t.Fatalf("new observation leaked ID: %+v", got.u)
			}
			if _, err = w.svc.GetBoard(ctx, w.sam, sibling.Board.Name); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGuestLifecycleOnlyDisclosesItsInvitedBoard(t *testing.T) {
	ctx := context.Background()
	w := newTeamWorld(t)
	guestCode, err := w.svc.CreateJoinCode(ctx, w.maya, w.board, board.JoinCodeInput{Guest: "guest-lifecycle", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: guestCode.Code, KeyName: "guest-phone"})
	if err != nil {
		t.Fatal(err)
	}
	guest := w.auth(ctx, t, joined.KeyToken)
	foreign, err := w.svc.CreateBoard(ctx, w.sam, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{foreign.Board.Name, foreign.Board.ID, "no-such-board", "brd_no_such_board"} {
		_, err = w.svc.ArchiveBoard(ctx, guest, selector)
		wantCode(t, selector, err, "board_not_found")
	}
	if _, err = w.svc.SetVisibility(ctx, w.sam, foreign.Board.Name, board.BoardPrivate, false); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{foreign.Board.Name, foreign.Board.ID} {
		_, err = w.svc.ArchiveBoard(ctx, guest, selector)
		wantCode(t, "private "+selector, err, "board_not_found")
	}
	_, err = w.svc.ArchiveBoard(ctx, guest, w.board)
	wantCode(t, "invited board", err, "guest_not_allowed")
}
