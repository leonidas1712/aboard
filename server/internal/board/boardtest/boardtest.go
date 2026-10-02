// Package boardtest is the contract every storage adapter for board must pass. It
// checks, through board's Store port alone, the behavior the domain relies on: lookups,
// round-trips, the gapless event log, transactions and message visibility. An adapter's
// own tests call Run with a function that opens a fresh, empty store.
package boardtest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Run checks that a store adapter does everything board needs from a Store.
func Run(t *testing.T, open func(t *testing.T) board.Store) {
	tests := []struct {
		name string
		test func(t *testing.T, st board.Store)
	}{
		{"MissingRecordsAreNotFound", missingRecordsAreNotFound},
		{"HumansRoundTrip", humansRoundTrip},
		{"BoardRoundTripsEveryField", boardRoundTripsEveryField},
		{"BoardNameTaken", boardNameTaken},
		{"SetBoardPolicyReplacesPolicy", setBoardPolicyReplacesPolicy},
		{"BoardsOfHumanListsOnlyHumanMemberships", boardsOfHumanListsOnlyHumanMemberships},
		{"AppendEventMovesHead", appendEventMovesHead},
		{"AppendEventRefusesGapsAndRepeats", appendEventRefusesGapsAndRepeats},
		{"EventsPageByAfterAndLimit", eventsPageByAfterAndLimit},
		{"WriteRollsBackWhenFnFails", writeRollsBackWhenFnFails},
		{"ConcurrentWritesGetGaplessSeqs", concurrentWritesGetGaplessSeqs},
		{"MembersInJoinOrder", membersInJoinOrder},
		{"MemberLookups", memberLookups},
		{"SetCursorOnlyMovesForward", setCursorOnlyMovesForward},
		{"JoinCodesByDigestAndID", joinCodesByDigestAndID},
		{"RevokeJoinCodeKeepsFirstTime", revokeJoinCodeKeepsFirstTime},
		{"TimelineReadAllReturnsEveryMessage", timelineReadAllReturnsEveryMessage},
		{"TimelineAddressedReturnsOnlyVisibleMessages", timelineAddressedReturnsOnlyVisibleMessages},
		{"TimelineFiltersAndWindows", timelineFiltersAndWindows},
		{"InboxSkipsOwnAndAlreadyReadMessages", inboxSkipsOwnAndAlreadyReadMessages},
		{"MessageByIDFillsSenderAndReply", messageByIDFillsSenderAndReply},
		{"MessagesBySeq", messagesBySeq},
		{"ReadSeesCommittedWritesOnly", readSeesCommittedWritesOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.test(t, open(t))
		})
	}
}

const at = "2026-10-01T16:00:00.000Z"

func write(t *testing.T, st board.Store, fn func(board.Tx) error) {
	t.Helper()
	if err := st.Write(context.Background(), fn); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func read(t *testing.T, st board.Store, fn func(board.ReadTx) error) {
	t.Helper()
	if err := st.Read(context.Background(), fn); err != nil {
		t.Fatalf("read: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func human(id string) board.Human {
	return board.Human{ID: id, Name: id, TokenDigest: "digest-" + id, CreatedAt: at}
}

// creatorID is the human who creates every board in these tests.
const creatorID = "hum_alex"

// newBoard stores the creating human (once), a board named name and the human's
// membership of it. The member's id is "mem_" + name.
func newBoard(tx board.Tx, name string) (board.Board, board.Member, error) {
	humanID := creatorID
	if _, err := tx.HumanByTokenDigest("digest-" + humanID); errors.Is(err, board.ErrNotFound) {
		if err := tx.InsertHuman(human(humanID)); err != nil {
			return board.Board{}, board.Member{}, err
		}
	} else if err != nil {
		return board.Board{}, board.Member{}, err
	}
	b := board.Board{
		ID: "brd_" + name, Name: name, Charter: "", Roles: map[string]rules.Role{rules.MemberRole: rules.DefaultMemberRole()},
		Policy: mustPreset(rules.Starter), HeadHash: events.GenesisHash, CreatedAt: at, CreatedBy: "mem_" + name,
	}
	if err := tx.InsertBoard(b); err != nil {
		return board.Board{}, board.Member{}, err
	}
	m := board.Member{
		ID: "mem_" + name, BoardID: b.ID, Name: humanID, Kind: "human", HumanID: humanID,
		Access: rules.AccessAdmin, Status: "active", JoinedAt: at,
	}
	if err := tx.InsertMember(m); err != nil {
		return board.Board{}, board.Member{}, err
	}
	return b, m, nil
}

// agent returns an agent owned by humanID on board b.
func agent(b board.Board, humanID, name, role string) board.Member {
	return board.Member{
		ID: "mem_" + b.Name + "_" + name, BoardID: b.ID, Name: name, Kind: "agent", Role: ptr(role), HumanID: humanID,
		Owner: ptr(humanID), Harness: ptr("claude-code"), TokenDigest: ptr("digest-" + b.Name + "-" + name), Status: "active", JoinedAt: at,
	}
}

func mustPreset(name string) rules.Policy {
	p, err := rules.Preset(name)
	if err != nil {
		panic(err) // the presets used here always exist
	}
	return p
}

// nextEvent returns the sealed event that follows b's head.
func nextEvent(b board.Board) (events.Event, error) {
	seq := b.HeadSeq + 1
	e := events.Event{
		ID: fmt.Sprintf("evt_%s_%d", b.ID, seq), BoardID: b.ID, Seq: seq, Type: events.MessagePosted, At: at,
		Actor: events.Actor{Kind: "human"}, PrevHash: b.HeadHash,
	}
	err := e.Seal(map[string]any{"n": seq})
	return e, err
}

// appendNext reads b's head inside tx and appends the next event.
func appendNext(tx board.Tx, boardID string) (events.Event, error) {
	b, err := tx.BoardByID(boardID)
	if err != nil {
		return events.Event{}, err
	}
	e, err := nextEvent(b)
	if err != nil {
		return events.Event{}, err
	}
	return e, tx.AppendEvent(e)
}

func seqsOf(evs []events.Event) []int64 {
	out := []int64{}
	for _, e := range evs {
		out = append(out, e.Seq)
	}
	return out
}

func missingRecordsAreNotFound(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		_, _, err := newBoard(tx, "docs")
		return err
	})
	lookups := map[string]func(board.ReadTx) error{
		"HumanByTokenDigest":  func(tx board.ReadTx) error { _, err := tx.HumanByTokenDigest("nope"); return err },
		"BoardByName":         func(tx board.ReadTx) error { _, err := tx.BoardByName("nope"); return err },
		"BoardByID":           func(tx board.ReadTx) error { _, err := tx.BoardByID("nope"); return err },
		"MemberByTokenDigest": func(tx board.ReadTx) error { _, err := tx.MemberByTokenDigest("nope"); return err },
		"HumanMember":         func(tx board.ReadTx) error { _, err := tx.HumanMember("brd_docs", "hum_nope"); return err },
		"MemberByName":        func(tx board.ReadTx) error { _, err := tx.MemberByName("brd_docs", "nope"); return err },
		"JoinCodeByDigest":    func(tx board.ReadTx) error { _, err := tx.JoinCodeByDigest("nope"); return err },
		"JoinCodeByID":        func(tx board.ReadTx) error { _, err := tx.JoinCodeByID("nope"); return err },
		"MessageByID":         func(tx board.ReadTx) error { _, err := tx.MessageByID("nope"); return err },
	}
	for name, lookup := range lookups {
		err := st.Read(context.Background(), lookup)
		if !errors.Is(err, board.ErrNotFound) {
			t.Errorf("%s of a missing record: got error %v, want board.ErrNotFound", name, err)
		}
	}
}

func humansRoundTrip(t *testing.T, st board.Store) {
	read(t, st, func(tx board.ReadTx) error {
		if n, err := tx.HumanCount(); err != nil || n != 0 {
			t.Errorf("HumanCount of an empty store = %d, %v; want 0", n, err)
		}
		return nil
	})
	want := human("hum_alex")
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(want); err != nil {
			return err
		}
		return tx.InsertHuman(human("hum_blair"))
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.HumanByTokenDigest(want.TokenDigest)
		if err != nil {
			return err
		}
		if got != want {
			t.Errorf("HumanByTokenDigest = %+v, want %+v", got, want)
		}
		if n, err := tx.HumanCount(); err != nil || n != 2 {
			t.Errorf("HumanCount = %d, %v; want 2", n, err)
		}
		return nil
	})
}

func boardRoundTripsEveryField(t *testing.T, st board.Store) {
	policy := mustPreset(rules.Recommended)
	policy.Urgent = rules.Everyone
	policy.Overrides = []string{"urgent"}
	want := board.Board{
		ID: "brd_review", Name: "review", Template: ptr("code-review"), Charter: "Review every change.",
		Roles: map[string]rules.Role{
			rules.MemberRole: rules.DefaultMemberRole(),
			"reviewer": {Charter: "Read diffs.", Can: []rules.Grant{
				{Permission: rules.Post},
				{Permission: rules.ClaimTasks, TaskTypes: []string{"review", "triage"}},
			}},
		},
		Policy: policy, HeadSeq: 0, HeadHash: events.GenesisHash, CreatedAt: at, CreatedBy: "mem_review",
	}
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(human("hum_alex")); err != nil {
			return err
		}
		return tx.InsertBoard(want)
	})
	read(t, st, func(tx board.ReadTx) error {
		byName, err := tx.BoardByName("review")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byName, want) {
			t.Errorf("BoardByName = %+v,\nwant %+v", byName, want)
		}
		byID, err := tx.BoardByID("brd_review")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byID, want) {
			t.Errorf("BoardByID = %+v,\nwant %+v", byID, want)
		}
		return nil
	})
}

func boardNameTaken(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		_, _, err := newBoard(tx, "docs")
		return err
	})
	read(t, st, func(tx board.ReadTx) error {
		for name, want := range map[string]bool{"docs": true, "docs-2": false} {
			got, err := tx.BoardNameTaken(name)
			if err != nil {
				return err
			}
			if got != want {
				t.Errorf("BoardNameTaken(%q) = %v, want %v", name, got, want)
			}
		}
		return nil
	})
}

func setBoardPolicyReplacesPolicy(t *testing.T, st board.Store) {
	want := mustPreset(rules.Recommended)
	want.Overrides = []string{"broadcast"}
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		return tx.SetBoardPolicy(b.ID, want)
	})
	read(t, st, func(tx board.ReadTx) error {
		b, err := tx.BoardByName("docs")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(b.Policy, want) {
			t.Errorf("policy after SetBoardPolicy = %+v, want %+v", b.Policy, want)
		}
		return nil
	})
}

func boardsOfHumanListsOnlyHumanMemberships(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		for _, name := range []string{"zeta", "alpha"} {
			if _, _, err := newBoard(tx, name); err != nil {
				return err
			}
		}
		if err := tx.InsertHuman(human("hum_blair")); err != nil {
			return err
		}
		// blair has an agent on zeta but is not a human member of it.
		zeta, err := tx.BoardByName("zeta")
		if err != nil {
			return err
		}
		if err := tx.InsertMember(agent(zeta, "hum_blair", "writer", "member")); err != nil {
			return err
		}
		// blair is a human member of alpha.
		alpha, err := tx.BoardByName("alpha")
		if err != nil {
			return err
		}
		return tx.InsertMember(board.Member{
			ID: "mem_alpha_blair", BoardID: alpha.ID, Name: "blair", Kind: "human", HumanID: "hum_blair", Status: "active", JoinedAt: at,
		})
	})
	read(t, st, func(tx board.ReadTx) error {
		for humanID, want := range map[string][]string{"hum_alex": {"alpha", "zeta"}, "hum_blair": {"alpha"}, "hum_nobody": {}} {
			bs, err := tx.BoardsOfHuman(humanID)
			if err != nil {
				return err
			}
			got := []string{}
			for _, b := range bs {
				got = append(got, b.Name)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("BoardsOfHuman(%s) = %v, want %v", humanID, got, want)
			}
		}
		return nil
	})
}

func appendEventMovesHead(t *testing.T, st board.Store) {
	var want []events.Event
	write(t, st, func(tx board.Tx) error {
		if _, _, err := newBoard(tx, "docs"); err != nil {
			return err
		}
		for range 3 {
			e, err := appendNext(tx, "brd_docs")
			if err != nil {
				return err
			}
			want = append(want, e)
		}
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		b, err := tx.BoardByID("brd_docs")
		if err != nil {
			return err
		}
		if b.HeadSeq != 3 || b.HeadHash != want[2].Hash {
			t.Errorf("head = %d %s, want 3 %s", b.HeadSeq, b.HeadHash, want[2].Hash)
		}
		got, err := tx.Events("brd_docs", 0, 10)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Events = %+v,\nwant %+v", got, want)
		}
		return nil
	})
}

func appendEventRefusesGapsAndRepeats(t *testing.T, st board.Store) {
	var first events.Event
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		gap, err := nextEvent(board.Board{ID: b.ID, HeadSeq: 1, HeadHash: b.HeadHash})
		if err != nil {
			return err
		}
		if err := tx.AppendEvent(gap); err == nil {
			t.Error("AppendEvent with seq 2 on an empty log succeeded, want an error")
		}
		if first, err = appendNext(tx, b.ID); err != nil {
			return err
		}
		repeat, err := nextEvent(board.Board{ID: b.ID, HeadSeq: 0, HeadHash: events.GenesisHash})
		if err != nil {
			return err
		}
		repeat.ID = "evt_repeat"
		if err := tx.AppendEvent(repeat); err == nil {
			t.Error("AppendEvent with seq 1 after seq 1 succeeded, want an error")
		}
		// Commit: the refused appends must have stored nothing on their own.
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		b, err := tx.BoardByID("brd_docs")
		if err != nil {
			return err
		}
		if b.HeadSeq != 1 || b.HeadHash != first.Hash {
			t.Errorf("head = %d %s, want 1 %s", b.HeadSeq, b.HeadHash, first.Hash)
		}
		evs, err := tx.Events("brd_docs", 0, 10)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(evs, []events.Event{first}) {
			t.Errorf("Events = %+v, want only %+v", evs, first)
		}
		return nil
	})
}

func eventsPageByAfterAndLimit(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		for _, name := range []string{"docs", "other"} {
			if _, _, err := newBoard(tx, name); err != nil {
				return err
			}
		}
		for range 5 {
			if _, err := appendNext(tx, "brd_docs"); err != nil {
				return err
			}
			if _, err := appendNext(tx, "brd_other"); err != nil {
				return err
			}
		}
		return nil
	})
	tests := []struct {
		after int64
		limit int
		want  []int64
	}{
		{0, 10, []int64{1, 2, 3, 4, 5}},
		{1, 2, []int64{2, 3}},
		{3, 10, []int64{4, 5}},
		{5, 10, []int64{}},
	}
	read(t, st, func(tx board.ReadTx) error {
		for _, tt := range tests {
			evs, err := tx.Events("brd_docs", tt.after, tt.limit)
			if err != nil {
				return err
			}
			if got := seqsOf(evs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Events(after %d, limit %d) seqs = %v, want %v", tt.after, tt.limit, got, tt.want)
			}
			for _, e := range evs {
				if e.BoardID != "brd_docs" {
					t.Errorf("Events of brd_docs returned an event of %s", e.BoardID)
				}
			}
		}
		return nil
	})
}

func writeRollsBackWhenFnFails(t *testing.T, st board.Store) {
	errStop := errors.New("stop")
	err := st.Write(context.Background(), func(tx board.Tx) error {
		if _, _, err := newBoard(tx, "docs"); err != nil {
			return err
		}
		if _, err := appendNext(tx, "brd_docs"); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("Write returned %v, want the error fn returned", err)
	}
	read(t, st, func(tx board.ReadTx) error {
		if n, err := tx.HumanCount(); err != nil || n != 0 {
			t.Errorf("HumanCount after a failed write = %d, %v; want 0", n, err)
		}
		if _, err := tx.BoardByID("brd_docs"); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("BoardByID after a failed write: got error %v, want board.ErrNotFound", err)
		}
		evs, err := tx.Events("brd_docs", 0, 10)
		if err != nil {
			return err
		}
		if len(evs) != 0 {
			t.Errorf("Events after a failed write = %d events, want none", len(evs))
		}
		return nil
	})
}

func concurrentWritesGetGaplessSeqs(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		_, _, err := newBoard(tx, "docs")
		return err
	})
	const writers = 20
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			errs <- st.Write(context.Background(), func(tx board.Tx) error {
				_, err := appendNext(tx, "brd_docs")
				return err
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	read(t, st, func(tx board.ReadTx) error {
		evs, err := tx.Events("brd_docs", 0, writers+10)
		if err != nil {
			return err
		}
		want := []int64{}
		for i := range int64(writers) {
			want = append(want, i+1)
		}
		if got := seqsOf(evs); !reflect.DeepEqual(got, want) {
			t.Errorf("seqs after %d concurrent writes = %v, want %v", writers, got, want)
		}
		return nil
	})
}

func membersInJoinOrder(t *testing.T, st board.Store) {
	var want []board.Member
	write(t, st, func(tx board.Tx) error {
		b, creator, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		want = append(want, creator)
		// Same joined_at, names out of alphabetical order: only join order can sort them.
		for _, name := range []string{"zed", "amy", "mo"} {
			m := agent(b, "hum_alex", name, rules.MemberRole)
			if err := tx.InsertMember(m); err != nil {
				return err
			}
			want = append(want, m)
		}
		// A second person, who is a member rather than an admin.
		if err := tx.InsertHuman(human("hum_blair")); err != nil {
			return err
		}
		blair := board.Member{
			ID: "mem_docs_blair", BoardID: b.ID, Name: "blair", Kind: "human", HumanID: "hum_blair",
			Access: rules.AccessMember, Status: "active", JoinedAt: at,
		}
		if err := tx.InsertMember(blair); err != nil {
			return err
		}
		want = append(want, blair)
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.Members("brd_docs")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Members = %+v,\nwant %+v", got, want)
		}
		return nil
	})
}

func memberLookups(t *testing.T, st board.Store) {
	var owner, writer board.Member
	write(t, st, func(tx board.Tx) error {
		b, m, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		owner, writer = m, agent(b, "hum_alex", "writer", "writer")
		writer.Cursor = 4
		return tx.InsertMember(writer)
	})
	read(t, st, func(tx board.ReadTx) error {
		byName, err := tx.MemberByName("brd_docs", "writer")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byName, writer) {
			t.Errorf("MemberByName = %+v, want %+v", byName, writer)
		}
		byToken, err := tx.MemberByTokenDigest(*writer.TokenDigest)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byToken, writer) {
			t.Errorf("MemberByTokenDigest = %+v, want %+v", byToken, writer)
		}
		// The agent shares its owner's human id; HumanMember must still pick the human.
		hm, err := tx.HumanMember("brd_docs", "hum_alex")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(hm, owner) {
			t.Errorf("HumanMember = %+v, want %+v", hm, owner)
		}
		return nil
	})
}

func setCursorOnlyMovesForward(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		return tx.InsertMember(agent(b, "hum_alex", "writer", "member"))
	})
	for _, step := range []struct{ set, want int64 }{{5, 5}, {3, 5}, {7, 7}} {
		write(t, st, func(tx board.Tx) error { return tx.SetCursor("mem_docs_writer", step.set) })
		read(t, st, func(tx board.ReadTx) error {
			m, err := tx.MemberByName("brd_docs", "writer")
			if err != nil {
				return err
			}
			if m.Cursor != step.want {
				t.Errorf("cursor after SetCursor(%d) = %d, want %d", step.set, m.Cursor, step.want)
			}
			return nil
		})
	}
}

func joinCode(b board.Board, creator board.Member) board.JoinCode {
	return board.JoinCode{
		ID: "jc_1", BoardID: b.ID, CodeDigest: "digest-code", Role: "reviewer",
		ExpiresAt: "2026-10-02T16:00:00.000Z", CreatedAt: at, CreatedBy: creator.ID,
	}
}

func joinCodesByDigestAndID(t *testing.T, st board.Store) {
	var want board.JoinCode
	write(t, st, func(tx board.Tx) error {
		b, m, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		want = joinCode(b, m)
		return tx.InsertJoinCode(want)
	})
	read(t, st, func(tx board.ReadTx) error {
		byDigest, err := tx.JoinCodeByDigest(want.CodeDigest)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byDigest, want) {
			t.Errorf("JoinCodeByDigest = %+v, want %+v", byDigest, want)
		}
		byID, err := tx.JoinCodeByID(want.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byID, want) {
			t.Errorf("JoinCodeByID = %+v, want %+v", byID, want)
		}
		return nil
	})
}

func revokeJoinCodeKeepsFirstTime(t *testing.T, st board.Store) {
	const first, second = "2026-10-01T17:00:00.000Z", "2026-10-01T18:00:00.000Z"
	write(t, st, func(tx board.Tx) error {
		b, m, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		if err := tx.InsertJoinCode(joinCode(b, m)); err != nil {
			return err
		}
		return tx.RevokeJoinCode("jc_1", first)
	})
	write(t, st, func(tx board.Tx) error { return tx.RevokeJoinCode("jc_1", second) })
	read(t, st, func(tx board.ReadTx) error {
		jc, err := tx.JoinCodeByID("jc_1")
		if err != nil {
			return err
		}
		if jc.RevokedAt == nil || *jc.RevokedAt != first {
			t.Errorf("revoked_at after revoking twice = %v, want %s", jc.RevokedAt, first)
		}
		return nil
	})
}

// conversation is a board with a human, three agents and five messages between them:
//
//	1 alex     -> all
//	2 writer   -> @reviewer
//	3 other    -> @writer
//	4 alex     -> role:reviewer
//	5 reviewer -> @other
type conversation struct {
	alex, writer, reviewer, other board.Member
	messages                      []board.Message
}

func newConversation(t *testing.T, st board.Store) conversation {
	t.Helper()
	var c conversation
	write(t, st, func(tx board.Tx) error {
		b, alex, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		c.alex = alex
		c.writer = agent(b, "hum_alex", "writer", "writer")
		c.reviewer = agent(b, "hum_alex", "reviewer", "reviewer")
		c.other = agent(b, "hum_alex", "other", "member")
		for _, m := range []board.Member{c.writer, c.reviewer, c.other} {
			if err := tx.InsertMember(m); err != nil {
				return err
			}
		}
		posts := []struct {
			from board.Member
			to   string
		}{
			{c.alex, "all"}, {c.writer, "@reviewer"}, {c.other, "@writer"}, {c.alex, "role:reviewer"}, {c.reviewer, "@other"},
		}
		for i, p := range posts {
			seq := int64(i + 1)
			m := board.Message{
				ID: fmt.Sprintf("msg_%d", seq), BoardID: b.ID, Seq: seq, At: at, SenderID: p.from.ID, To: []string{p.to},
				Body: fmt.Sprintf("message %d", seq), Redactions: []board.Redaction{},
				SenderName: p.from.Name, SenderKind: p.from.Kind, SenderRole: p.from.Role, SenderOwner: p.from.Owner, SenderHuman: p.from.HumanID,
			}
			if err := tx.InsertMessage(m); err != nil {
				return err
			}
			c.messages = append(c.messages, m)
		}
		return nil
	})
	return c
}

func messageSeqs(ms []board.Message) []int64 {
	out := []int64{}
	for _, m := range ms {
		out = append(out, m.Seq)
	}
	return out
}

func timelineReadAllReturnsEveryMessage(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.Timeline("brd_docs", c.reviewer, true, board.TimelineQuery{Limit: 10})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, c.messages) {
			t.Errorf("Timeline(readAll) = %+v,\nwant %+v", got, c.messages)
		}
		page, err := tx.Timeline("brd_docs", c.reviewer, true, board.TimelineQuery{After: 1, Limit: 2})
		if err != nil {
			return err
		}
		if got := messageSeqs(page); !reflect.DeepEqual(got, []int64{2, 3}) {
			t.Errorf("Timeline(readAll, after 1, limit 2) seqs = %v, want [2 3]", got)
		}
		return nil
	})
}

func timelineAddressedReturnsOnlyVisibleMessages(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	tests := []struct {
		reader board.Member
		want   []int64
	}{
		// to all, to @reviewer, to role:reviewer, and its own message to @other.
		{c.reviewer, []int64{1, 2, 4, 5}},
		// to all, its own message to @reviewer, and to @writer.
		{c.writer, []int64{1, 2, 3}},
		{c.other, []int64{1, 3, 5}},
	}
	read(t, st, func(tx board.ReadTx) error {
		for _, tt := range tests {
			got, err := tx.Timeline("brd_docs", tt.reader, false, board.TimelineQuery{Limit: 10})
			if err != nil {
				return err
			}
			if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, tt.want) {
				t.Errorf("Timeline for %s seqs = %v, want %v", tt.reader.Name, seqs, tt.want)
			}
		}
		return nil
	})
}

func timelineFiltersAndWindows(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	tests := []struct {
		name    string
		reader  board.Member
		readAll bool
		q       board.TimelineQuery
		want    []int64
	}{
		{"before", c.reviewer, true, board.TimelineQuery{Before: 4, Limit: 10}, []int64{1, 2, 3}},
		{"after and before", c.reviewer, true, board.TimelineQuery{After: 1, Before: 5, Limit: 10}, []int64{2, 3, 4}},
		{"oldest first by default", c.reviewer, true, board.TimelineQuery{Limit: 2}, []int64{1, 2}},
		{"newest, still oldest first", c.reviewer, true, board.TimelineQuery{Newest: true, Limit: 2}, []int64{4, 5}},
		{"newest before", c.reviewer, true, board.TimelineQuery{Newest: true, Before: 5, Limit: 2}, []int64{3, 4}},
		{"from a member", c.reviewer, true, board.TimelineQuery{FromID: c.alex.ID, Limit: 10}, []int64{1, 4}},
		{"from a role", c.reviewer, true, board.TimelineQuery{SenderRole: "reviewer", Limit: 10}, []int64{5}},
		// to all, to @reviewer, to role:reviewer; not its own message.
		{"to me", c.reviewer, true, board.TimelineQuery{ToMe: true, Limit: 10}, []int64{1, 2, 4}},
		{"to me, another reader", c.writer, true, board.TimelineQuery{ToMe: true, Limit: 10}, []int64{1, 3}},
		{"to me under addressed visibility", c.other, false, board.TimelineQuery{ToMe: true, Limit: 10}, []int64{1, 5}},
		// Filters never widen what the reader may see: writer's message 2 is to @reviewer.
		{"from, visible", c.reviewer, false, board.TimelineQuery{FromID: c.writer.ID, Limit: 10}, []int64{2}},
		{"from, hidden", c.other, false, board.TimelineQuery{FromID: c.writer.ID, Limit: 10}, []int64{}},
		{"filters combine", c.reviewer, true, board.TimelineQuery{FromID: c.alex.ID, After: 1, Limit: 10}, []int64{4}},
	}
	read(t, st, func(tx board.ReadTx) error {
		for _, tt := range tests {
			got, err := tx.Timeline("brd_docs", tt.reader, tt.readAll, tt.q)
			if err != nil {
				return err
			}
			if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, tt.want) {
				t.Errorf("%s: Timeline for %s seqs = %v, want %v", tt.name, tt.reader.Name, seqs, tt.want)
			}
		}
		return nil
	})
}

func inboxSkipsOwnAndAlreadyReadMessages(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	write(t, st, func(tx board.Tx) error { return tx.SetCursor(c.reviewer.ID, 1) })
	read(t, st, func(tx board.ReadTx) error {
		reviewer, err := tx.MemberByName("brd_docs", "reviewer")
		if err != nil {
			return err
		}
		// Not 1 (at the cursor), not 3 (to @writer), not 5 (its own).
		got, err := tx.Inbox(reviewer, 10)
		if err != nil {
			return err
		}
		if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, []int64{2, 4}) {
			t.Errorf("Inbox seqs = %v, want [2 4]", seqs)
		}
		limited, err := tx.Inbox(reviewer, 1)
		if err != nil {
			return err
		}
		if seqs := messageSeqs(limited); !reflect.DeepEqual(seqs, []int64{2}) {
			t.Errorf("Inbox with limit 1 seqs = %v, want [2]", seqs)
		}
		return nil
	})
}

func messageByIDFillsSenderAndReply(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	want := board.Message{
		ID: "msg_6", BoardID: "brd_docs", Seq: 6, At: at, SenderID: c.writer.ID, To: []string{"@reviewer", "role:reviewer"},
		Body: "Fixed, see notes.", ReplyTo: ptr("msg_2"), Urgent: true, ExpectsReply: true,
		Redactions: []board.Redaction{{Kind: "github_token", Count: 2}},
	}
	plain := board.Message{ID: "msg_7", BoardID: "brd_docs", Seq: 7, At: at, SenderID: c.alex.ID, To: []string{"all"}, Body: "ok"}
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertMessage(want); err != nil {
			return err
		}
		return tx.InsertMessage(plain)
	})
	want.ReplyToSeq = ptr(int64(2))
	want.SenderName, want.SenderKind, want.SenderRole = c.writer.Name, c.writer.Kind, c.writer.Role
	want.SenderOwner, want.SenderHuman = c.writer.Owner, c.writer.HumanID
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.MessageByID("msg_6")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MessageByID = %+v,\nwant %+v", got, want)
		}
		got, err = tx.MessageByID("msg_7")
		if err != nil {
			return err
		}
		// A message stored without redactions reads back with an empty list, not nil, so
		// it is shown as [] rather than null.
		if got.Redactions == nil || len(got.Redactions) != 0 {
			t.Errorf("redactions of a message stored without any = %#v, want an empty list", got.Redactions)
		}
		if got.ReplyToSeq != nil || got.SenderRole != nil || got.SenderKind != "human" {
			t.Errorf("MessageByID of a human's plain message = %+v", got)
		}
		return nil
	})
}

func messagesBySeq(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.MessagesBySeq("brd_docs", []int64{1, 3, 99})
		if err != nil {
			return err
		}
		want := map[int64]board.Message{1: c.messages[0], 3: c.messages[2]}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MessagesBySeq(1, 3, 99) = %+v,\nwant %+v", got, want)
		}
		none, err := tx.MessagesBySeq("brd_docs", nil)
		if err != nil {
			return err
		}
		if none == nil || len(none) != 0 {
			t.Errorf("MessagesBySeq of no seqs = %#v, want an empty map", none)
		}
		return nil
	})
}

func readSeesCommittedWritesOnly(t *testing.T, st board.Store) {
	count := func() int {
		t.Helper()
		var n int
		read(t, st, func(tx board.ReadTx) error {
			var err error
			n, err = tx.HumanCount()
			return err
		})
		return n
	}
	write(t, st, func(tx board.Tx) error { return tx.InsertHuman(human("hum_alex")) })
	if n := count(); n != 1 {
		t.Fatalf("HumanCount after a committed write = %d, want 1", n)
	}
	err := st.Write(context.Background(), func(tx board.Tx) error {
		if err := tx.InsertHuman(human("hum_blair")); err != nil {
			return err
		}
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("Write whose fn failed returned nil")
	}
	if n := count(); n != 1 {
		t.Errorf("HumanCount after a failed write = %d, want 1", n)
	}
}
