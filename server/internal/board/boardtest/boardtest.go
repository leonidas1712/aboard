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
		{"AccessKeysRoundTripAndGetNamed", accessKeysRoundTripAndGetNamed},
		{"KeysListRevokeAndRecordUse", keysListRevokeAndRecordUse},
		{"ServerInvitesAreUsedOnce", serverInvitesAreUsedOnce},
		{"BrowserLoginsRoundTripAndEnd", browserLoginsRoundTripAndEnd},
		{"BrowserLoginsListAndEndOneByID", browserLoginsListAndEndOneByID},
		{"MachineRequestsAreDecidedAndCollectedOnce", machineRequestsAreDecidedAndCollectedOnce},
		{"BoardRoundTripsEveryField", boardRoundTripsEveryField},
		{"BoardNameTaken", boardNameTaken},
		{"SetBoardPolicyReplacesPolicy", setBoardPolicyReplacesPolicy},
		{"SetBoardTitleReplacesTitle", setBoardTitleReplacesTitle},
		{"BoardsOfHumanListsOnlyHumanMemberships", boardsOfHumanListsOnlyHumanMemberships},
		{"AppendEventMovesHead", appendEventMovesHead},
		{"AppendEventRefusesGapsAndRepeats", appendEventRefusesGapsAndRepeats},
		{"EventsPageByAfterAndLimit", eventsPageByAfterAndLimit},
		{"WriteRollsBackWhenFnFails", writeRollsBackWhenFnFails},
		{"ConcurrentWritesGetGaplessSeqs", concurrentWritesGetGaplessSeqs},
		{"MembersInJoinOrder", membersInJoinOrder},
		{"MemberLookups", memberLookups},
		{"SetCursorOnlyMovesForward", setCursorOnlyMovesForward},
		{"SetPresenceReplacesIt", setPresenceReplacesIt},
		{"SetDeliveryReplacesItAndKeepsPresence", setDeliveryReplacesItAndKeepsPresence},
		{"JoinCodesByDigestAndID", joinCodesByDigestAndID},
		{"RevokeJoinCodeKeepsFirstTime", revokeJoinCodeKeepsFirstTime},
		{"TimelineReadAllReturnsEveryMessage", timelineReadAllReturnsEveryMessage},
		{"TimelineAddressedReturnsOnlyVisibleMessages", timelineAddressedReturnsOnlyVisibleMessages},
		{"TimelineFiltersAndWindows", timelineFiltersAndWindows},
		{"InboxSkipsOwnAndAlreadyReadMessages", inboxSkipsOwnAndAlreadyReadMessages},
		{"InboxHoldsMessagesThatMentionTheReader", inboxHoldsMessagesThatMentionTheReader},
		{"CountUnreadCountsWhatInboxOrTheTimelineHasLeft", countUnreadCountsWhatInboxOrTheTimelineHasLeft},
		{"MessageRecipientsRoundTrip", messageRecipientsRoundTrip},
		{"MessageByIDFillsSenderAndReply", messageByIDFillsSenderAndReply},
		{"MessagesBySeq", messagesBySeq},
		{"InsertMessageCountsItOnItsBoard", insertMessageCountsItOnItsBoard},
		{"ThreadsReadAndCountOnlyVisibleReplies", threadsReadAndCountOnlyVisibleReplies},
		{"ReactionsReadBackPerMessageOldestFirst", reactionsReadBackPerMessageOldestFirst},
		{"ThreadsListNewestActivityFirst", threadsListNewestActivityFirst},
		{"ReadSeesCommittedWritesOnly", readSeesCommittedWritesOnly},
		{"BoardVisibilityDecidesWhoSeesIt", boardVisibilityDecidesWhoSeesIt},
		{"PeopleWhoLeftAreNotOnTheBoard", peopleWhoLeftAreNotOnTheBoard},
		{"WorkingJoinCodesSkipRevokedAndExpired", workingJoinCodesSkipRevokedAndExpired},
		{"BoardCreationDefaultsToMembers", boardCreationDefaultsToMembers},
		{"RemovedPeopleFreeTheirHandles", removedPeopleFreeTheirHandles},
		{"ServerRolesAndAdminsCounted", serverRolesAndAdminsCounted},
		{"GuestCodesAreUsedOnce", guestCodesAreUsedOnce},
		{"MembersCarryTheirPersonsRole", membersCarryTheirPersonsRole},
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
	return board.Human{ID: id, Name: id, Role: board.ServerMember, CreatedAt: at}
}

// creatorID is the human who creates every board in these tests.
const creatorID = "hum_alex"

// newBoard stores the creating human (once), a board named name and the human's
// membership of it. The member's id is "mem_" + name.
func newBoard(tx board.Tx, name string) (board.Board, board.Member, error) {
	humanID := creatorID
	if _, err := tx.HumanByID(humanID); errors.Is(err, board.ErrNotFound) {
		if err := tx.InsertHuman(human(humanID)); err != nil {
			return board.Board{}, board.Member{}, err
		}
	} else if err != nil {
		return board.Board{}, board.Member{}, err
	}
	b := board.Board{
		ID: "brd_" + name, Name: name, Charter: "", Roles: map[string]rules.Role{rules.MemberRole: rules.DefaultMemberRole()},
		Policy: mustPreset(rules.Starter), HeadHash: events.GenesisHash, CreatedAt: at, CreatedBy: "mem_" + name, Visibility: board.BoardOpen,
	}
	if err := tx.InsertBoard(b); err != nil {
		return board.Board{}, board.Member{}, err
	}
	m := board.Member{
		ID: "mem_" + name, BoardID: b.ID, Name: humanID, Kind: "human", HumanID: humanID,
		Access: rules.AccessAdmin, Status: "active", JoinedAt: at, PersonRole: board.ServerMember,
	}
	if err := tx.InsertMember(m); err != nil {
		return board.Board{}, board.Member{}, err
	}
	return b, m, nil
}

// agent returns an agent owned by humanID, a member of the server, on board b.
func agent(b board.Board, humanID, name, role string) board.Member {
	return board.Member{
		ID: "mem_" + b.Name + "_" + name, BoardID: b.ID, Name: name, Kind: "agent", Role: ptr(role), HumanID: humanID,
		Owner: ptr(humanID), Harness: ptr("claude-code"), TokenDigest: ptr("digest-" + b.Name + "-" + name), Status: "active", JoinedAt: at,
		PersonRole: board.ServerMember,
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
		"AccessKeyByDigest":    func(tx board.ReadTx) error { _, err := tx.AccessKeyByDigest("nope"); return err },
		"AccessKeyByID":        func(tx board.ReadTx) error { _, err := tx.AccessKeyByID("key_nope"); return err },
		"HumanByID":            func(tx board.ReadTx) error { _, err := tx.HumanByID("hum_nope"); return err },
		"HumanByName":          func(tx board.ReadTx) error { _, err := tx.HumanByName("nope"); return err },
		"ServerInviteByDigest": func(tx board.ReadTx) error { _, err := tx.ServerInviteByDigest("nope"); return err },
		"BrowserLoginByDigest": func(tx board.ReadTx) error { _, err := tx.BrowserLoginByDigest("nope"); return err },
		"BrowserLoginByID":     func(tx board.ReadTx) error { _, err := tx.BrowserLoginByID("ses_nope"); return err },
		"MachineRequestByCode": func(tx board.ReadTx) error { _, err := tx.MachineRequestByCode("nope"); return err },
		"MachineRequestBySecret": func(tx board.ReadTx) error {
			_, err := tx.MachineRequestBySecret("nope")
			return err
		},
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
	want.Role, want.DisplayName = board.ServerAdmin, ptr("Alex Example")
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(want); err != nil {
			return err
		}
		return tx.InsertHuman(human("hum_blair"))
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.HumanByName(want.Name)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("HumanByName = %+v, want %+v", got, want)
		}
		if n, err := tx.HumanCount(); err != nil || n != 2 {
			t.Errorf("HumanCount = %d, %v; want 2", n, err)
		}
		byID, err := tx.HumanByID(want.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byID, want) {
			t.Errorf("HumanByID = %+v, want %+v", byID, want)
		}
		return nil
	})
}

func accessKeysRoundTripAndGetNamed(t *testing.T, st board.Store) {
	named := board.AccessKey{ID: "key_a", HumanID: "hum_alex", Name: "laptop", Digest: "k-a", CreatedAt: at, ExpiresAt: ptr("2026-12-01T00:00:00.000Z")}
	unnamed := board.AccessKey{ID: "key_b", HumanID: "hum_alex", Digest: "k-b", CreatedAt: at, RevokedAt: ptr(at)}
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(human("hum_alex")); err != nil {
			return err
		}
		if err := tx.InsertAccessKey(named); err != nil {
			return err
		}
		if err := tx.InsertAccessKey(unnamed); err != nil {
			return err
		}
		return tx.NameUnnamedKeys("desktop")
	})
	unnamed.Name = "desktop"
	read(t, st, func(tx board.ReadTx) error {
		for _, want := range []board.AccessKey{named, unnamed} {
			byDigest, err := tx.AccessKeyByDigest(want.Digest)
			if err != nil {
				return err
			}
			byID, err := tx.AccessKeyByID(want.ID)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(byDigest, want) || !reflect.DeepEqual(byID, want) {
				t.Errorf("access key %s: by digest %+v, by id %+v; want %+v", want.ID, byDigest, byID, want)
			}
		}
		return nil
	})
}

func keysListRevokeAndRecordUse(t *testing.T, st board.Store) {
	const later = "2026-10-31T16:00:00.000Z"
	laptop := board.AccessKey{ID: "key_a", HumanID: creatorID, Name: "laptop", Digest: "k-a", CreatedAt: at, IdleSeconds: ptr(int64(60)), ExpiresAt: ptr(later)}
	phone := board.AccessKey{ID: "key_b", HumanID: creatorID, Name: "phone", Digest: "k-b", CreatedAt: "2026-10-02T16:00:00.000Z", ExpiresAt: ptr(later)}
	other := board.AccessKey{ID: "key_c", HumanID: "hum_blair", Name: "laptop", Digest: "k-c", CreatedAt: at}
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "keys")
		if err != nil {
			return err
		}
		if err := tx.InsertHuman(human("hum_blair")); err != nil {
			return err
		}
		for _, k := range []board.AccessKey{phone, laptop, other} {
			if err := tx.InsertAccessKey(k); err != nil {
				return err
			}
		}
		for i, name := range []string{"writer", "reviewer"} {
			a := agent(b, creatorID, name, rules.MemberRole)
			a.KeyID = ptr([]string{laptop.ID, other.ID}[i])
			if err := tx.InsertMember(a); err != nil {
				return err
			}
		}
		for _, l := range []board.BrowserLogin{
			{ID: "ses_b1", TokenDigest: "b-1", HumanID: creatorID, KeyID: laptop.ID, CreatedAt: at, ExpiresAt: later},
			{ID: "ses_b2", TokenDigest: "b-2", HumanID: creatorID, KeyID: laptop.ID, CreatedAt: at, ExpiresAt: at},
			{ID: "ses_b3", TokenDigest: "b-3", HumanID: creatorID, KeyID: phone.ID, CreatedAt: at, ExpiresAt: later},
		} {
			if err := tx.InsertBrowserLogin(l); err != nil {
				return err
			}
		}
		return nil
	})
	write(t, st, func(tx board.Tx) error {
		if err := tx.RevokeAccessKey(phone.ID, "2026-10-03T16:00:00.000Z"); err != nil {
			return err
		}
		if err := tx.RevokeAccessKey(phone.ID, "2026-10-04T16:00:00.000Z"); err != nil {
			return err
		}
		if err := tx.UseAccessKey(laptop.ID, "2026-10-05T16:00:00.000Z", ptr("2026-11-05T16:00:00.000Z")); err != nil {
			return err
		}
		return tx.UseAccessKey(other.ID, "2026-10-05T16:00:00.000Z", nil)
	})
	laptop.LastUsedAt, laptop.ExpiresAt = ptr("2026-10-05T16:00:00.000Z"), ptr("2026-11-05T16:00:00.000Z")
	phone.RevokedAt = ptr("2026-10-03T16:00:00.000Z")
	other.LastUsedAt = ptr("2026-10-05T16:00:00.000Z")
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.KeysOf(creatorID, "2026-10-05T16:00:00.000Z")
		if err != nil {
			return err
		}
		want := []board.KeyUsage{{AccessKey: laptop, BrowserSessions: 1, AgentSeats: 1}, {AccessKey: phone, BrowserSessions: 1}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("KeysOf = %+v, want %+v", got, want)
		}
		o, err := tx.AccessKeyByID(other.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(o, other) {
			t.Errorf("a key used without moving its expiry = %+v, want %+v", o, other)
		}
		return nil
	})
}

func serverInvitesAreUsedOnce(t *testing.T, st board.Store) {
	inv := board.ServerInvite{ID: "inv_a", Digest: "i-a", CreatedBy: "hum_alex", CreatedAt: at, ExpiresAt: "2026-10-08T16:00:00.000Z"}
	write(t, st, func(tx board.Tx) error {
		for _, h := range []string{"hum_alex", "hum_blair", "hum_casey"} {
			if err := tx.InsertHuman(human(h)); err != nil {
				return err
			}
		}
		return tx.InsertServerInvite(inv)
	})
	write(t, st, func(tx board.Tx) error {
		first, err := tx.UseServerInvite(inv.ID, at, "hum_blair")
		if err != nil {
			return err
		}
		again, err := tx.UseServerInvite(inv.ID, at, "hum_casey")
		if err != nil {
			return err
		}
		if !first || again {
			t.Errorf("UseServerInvite twice = %v, %v; want true, false", first, again)
		}
		return nil
	})
	inv.UsedAt, inv.UsedBy = ptr(at), ptr("hum_blair")
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.ServerInviteByDigest(inv.Digest)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, inv) {
			t.Errorf("ServerInviteByDigest = %+v, want %+v", got, inv)
		}
		return nil
	})
}

// A machine request is found by either of its digests, decided once, counted, collected
// once, and deleted once it has ended.
func machineRequestsAreDecidedAndCollectedOnce(t *testing.T, st board.Store) {
	req := board.MachineRequest{
		ID: "mrq_a", CodeDigest: "c-a", SecretDigest: "s-a", Label: "maya-desktop", Handle: "maya", RequestedFrom: "203.0.113.7",
		CreatedAt: at, ExpiresAt: "2026-10-01T16:05:00.000Z", State: board.MachinePending,
	}
	later := req
	later.ID, later.CodeDigest, later.SecretDigest, later.ExpiresAt = "mrq_b", "c-b", "s-b", "2026-10-01T16:09:00.000Z"
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(human("hum_maya")); err != nil {
			return err
		}
		for _, id := range []string{"key_laptop", "key_desktop"} {
			if err := tx.InsertAccessKey(board.AccessKey{ID: id, HumanID: "hum_maya", Name: id, Digest: "d-" + id, CreatedAt: at}); err != nil {
				return err
			}
		}
		if err := tx.InsertMachineRequest(req); err != nil {
			return err
		}
		return tx.InsertMachineRequest(later)
	})
	write(t, st, func(tx board.Tx) error {
		if collected, err := tx.CollectMachineRequest(req.ID, "key_desktop"); err != nil || collected {
			t.Errorf("CollectMachineRequest before approval = %v, %v; want false", collected, err)
		}
		if err := tx.CountMachineRequestPoll(req.ID); err != nil {
			return err
		}
		first, err := tx.DecideMachineRequest(req.ID, board.MachineApproved, "hum_maya", "key_laptop", at)
		if err != nil {
			return err
		}
		again, err := tx.DecideMachineRequest(req.ID, board.MachineRefused, "hum_maya", "key_laptop", at)
		if err != nil {
			return err
		}
		if !first || again {
			t.Errorf("DecideMachineRequest twice = %v, %v; want true, false", first, again)
		}
		first, err = tx.CollectMachineRequest(req.ID, "key_desktop")
		if err != nil {
			return err
		}
		again, err = tx.CollectMachineRequest(req.ID, "key_desktop")
		if err != nil {
			return err
		}
		if !first || again {
			t.Errorf("CollectMachineRequest twice = %v, %v; want true, false", first, again)
		}
		return nil
	})
	req.State, req.DecidedBy, req.DecidedKey, req.DecidedAt = board.MachineCollected, ptr("hum_maya"), ptr("key_laptop"), ptr(at)
	req.KeyID, req.Polls = ptr("key_desktop"), 1
	read(t, st, func(tx board.ReadTx) error {
		byCode, err := tx.MachineRequestByCode(req.CodeDigest)
		if err != nil {
			return err
		}
		bySecret, err := tx.MachineRequestBySecret(req.SecretDigest)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(byCode, req) || !reflect.DeepEqual(bySecret, req) {
			t.Errorf("the collected request = %+v and %+v, want %+v", byCode, bySecret, req)
		}
		return nil
	})
	write(t, st, func(tx board.Tx) error { return tx.DeleteEndedMachineRequests(req.ExpiresAt) })
	read(t, st, func(tx board.ReadTx) error {
		if _, err := tx.MachineRequestByCode(req.CodeDigest); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("the ended request after DeleteEndedMachineRequests: %v, want board.ErrNotFound", err)
		}
		if _, err := tx.MachineRequestByCode(later.CodeDigest); err != nil {
			t.Errorf("the live request after DeleteEndedMachineRequests: %v", err)
		}
		return nil
	})
}

func browserLoginsRoundTripAndEnd(t *testing.T, st board.Store) {
	const before, now, later = "2026-10-01T15:00:00.000Z", "2026-10-01T16:00:00.000Z", "2026-10-31T16:00:00.000Z"
	login := func(digest, humanID, expires string) board.BrowserLogin {
		return board.BrowserLogin{
			ID: "ses_" + digest, TokenDigest: digest, HumanID: humanID, StartedWith: board.SessionFromLoginCode,
			CreatedAt: before, ExpiresAt: expires,
		}
	}
	alexA, alexB, alexOld := login("b-alex-a", "hum_alex", later), login("b-alex-b", "hum_alex", later), login("b-alex-old", "hum_alex", now)
	blair := login("b-blair", "hum_blair", later)
	write(t, st, func(tx board.Tx) error {
		for _, h := range []string{"hum_alex", "hum_blair"} {
			if err := tx.InsertHuman(human(h)); err != nil {
				return err
			}
		}
		for _, l := range []board.BrowserLogin{alexA, alexB, alexOld, blair} {
			if err := tx.InsertBrowserLogin(l); err != nil {
				return err
			}
		}
		return nil
	})
	found := func(digest string) bool {
		var ok bool
		read(t, st, func(tx board.ReadTx) error {
			_, err := tx.BrowserLoginByDigest(digest)
			if errors.Is(err, board.ErrNotFound) {
				return nil
			}
			ok = err == nil
			return err
		})
		return ok
	}
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.BrowserLoginByDigest(alexA.TokenDigest)
		if err != nil {
			return err
		}
		if got != alexA {
			t.Errorf("BrowserLoginByDigest = %+v, want %+v", got, alexA)
		}
		return nil
	})

	// A login whose ExpiresAt is now has ended; the rest stay.
	write(t, st, func(tx board.Tx) error { return tx.DeleteExpiredBrowserLogins(now) })
	if found(alexOld.TokenDigest) {
		t.Error("DeleteExpiredBrowserLogins kept a login that ended at now")
	}
	if !found(alexA.TokenDigest) || !found(blair.TokenDigest) {
		t.Error("DeleteExpiredBrowserLogins removed a login that hasn't ended")
	}

	var n int
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertBrowserLogin(alexOld); err != nil {
			return err
		}
		var err error
		n, err = tx.DeleteBrowserLogins("hum_alex", now)
		return err
	})
	if n != 2 {
		t.Errorf("DeleteBrowserLogins counted %d logins, want 2: the expired one isn't counted", n)
	}
	for _, l := range []board.BrowserLogin{alexA, alexB, alexOld} {
		if found(l.TokenDigest) {
			t.Errorf("DeleteBrowserLogins kept %s", l.TokenDigest)
		}
	}
	if !found(blair.TokenDigest) {
		t.Error("DeleteBrowserLogins removed another human's login")
	}
}

func browserLoginsListAndEndOneByID(t *testing.T, st board.Store) {
	const now = "2026-10-01T16:00:00.000Z"
	login := func(id, humanID, created, expires, startedWith string) board.BrowserLogin {
		return board.BrowserLogin{
			ID: id, TokenDigest: "d-" + id, HumanID: humanID, StartedWith: startedWith, CreatedAt: created, ExpiresAt: expires,
		}
	}
	older := login("ses_older", "hum_alex", "2026-10-01T10:00:00.000Z", "2026-10-31T10:00:00.000Z", board.SessionFromLoginCode)
	newer := login("ses_newer", "hum_alex", "2026-10-01T12:00:00.000Z", "2026-10-31T12:00:00.000Z", board.SessionFromKey)
	ended := login("ses_ended", "hum_alex", "2026-09-01T12:00:00.000Z", now, board.SessionFromLoginCode)
	blair := login("ses_blair", "hum_blair", "2026-10-01T11:00:00.000Z", "2026-10-31T11:00:00.000Z", board.SessionFromKey)
	write(t, st, func(tx board.Tx) error {
		for _, h := range []string{"hum_alex", "hum_blair"} {
			if err := tx.InsertHuman(human(h)); err != nil {
				return err
			}
		}
		for _, l := range []board.BrowserLogin{older, newer, ended, blair} {
			if err := tx.InsertBrowserLogin(l); err != nil {
				return err
			}
		}
		return nil
	})
	list := func() []board.BrowserLogin {
		var got []board.BrowserLogin
		read(t, st, func(tx board.ReadTx) error {
			var err error
			got, err = tx.BrowserLoginsOf("hum_alex", now)
			return err
		})
		return got
	}
	if got := list(); !reflect.DeepEqual(got, []board.BrowserLogin{newer, older}) {
		t.Errorf("BrowserLoginsOf = %+v, want the two that haven't ended, newest first", got)
	}
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.BrowserLoginByID(newer.ID)
		if err != nil {
			return err
		}
		if got != newer {
			t.Errorf("BrowserLoginByID = %+v, want %+v", got, newer)
		}
		return nil
	})
	write(t, st, func(tx board.Tx) error { return tx.DeleteBrowserLogin(newer.ID) })
	if got := list(); !reflect.DeepEqual(got, []board.BrowserLogin{older}) {
		t.Errorf("after DeleteBrowserLogin, BrowserLoginsOf = %+v, want only %s", got, older.ID)
	}
	read(t, st, func(tx board.ReadTx) error {
		if _, err := tx.BrowserLoginByID(blair.ID); err != nil {
			t.Errorf("DeleteBrowserLogin removed another login: %v", err)
		}
		return nil
	})
}

func boardRoundTripsEveryField(t *testing.T, st board.Store) {
	policy := mustPreset(rules.Recommended)
	policy.Urgent = rules.Everyone
	policy.Overrides = []string{"urgent"}
	want := board.Board{
		ID: "brd_review", Name: "review", Title: ptr("Review every change"), Template: ptr("code-review"), Charter: "Review every change.",
		Roles: map[string]rules.Role{
			rules.MemberRole: rules.DefaultMemberRole(),
			"reviewer": {Charter: "Read diffs.", Can: []rules.Grant{
				{Permission: rules.Post},
				{Permission: rules.ClaimTasks, TaskTypes: []string{"review", "triage"}},
			}},
		},
		Policy: policy, HeadSeq: 0, HeadHash: events.GenesisHash, CreatedAt: at, CreatedBy: "mem_review", Visibility: board.BoardPrivate,
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

func setBoardTitleReplacesTitle(t *testing.T, st board.Store) {
	var id string
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		id = b.ID
		if err != nil || b.Title != nil {
			return fmt.Errorf("new board title %v, err %w", b.Title, err)
		}
		return tx.SetBoardTitle(b.ID, ptr("Docs review"))
	})
	title := func() *string {
		var got *string
		read(t, st, func(tx board.ReadTx) error {
			b, err := tx.BoardByID(id)
			got = b.Title
			return err
		})
		return got
	}
	if got := title(); got == nil || *got != "Docs review" {
		t.Errorf("title after SetBoardTitle = %v, want Docs review", got)
	}
	write(t, st, func(tx board.Tx) error { return tx.SetBoardTitle(id, nil) })
	if got := title(); got != nil {
		t.Errorf("title after removing = %q, want none", *got)
	}
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
			Access: rules.AccessMember, Status: "active", JoinedAt: at, PersonRole: board.ServerMember,
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

func setPresenceReplacesIt(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		return tx.InsertMember(agent(b, "hum_alex", "writer", "member"))
	})
	read(t, st, func(tx board.ReadTx) error {
		m, err := tx.MemberByName("brd_docs", "writer")
		if err == nil && m.Presence != (board.Presence{}) {
			t.Errorf("presence of a new agent = %+v, want none", m.Presence)
		}
		return err
	})
	for _, p := range []board.Presence{
		{State: board.PresenceWorking, Since: at, At: at},
		{State: board.PresenceIdle, Since: "2026-10-01T16:01:00.000Z", At: "2026-10-01T16:02:00.000Z"},
	} {
		write(t, st, func(tx board.Tx) error { return tx.SetPresence("mem_docs_writer", p) })
		read(t, st, func(tx board.ReadTx) error {
			ms, err := tx.Members("brd_docs")
			if err != nil {
				return err
			}
			for _, m := range ms {
				if m.Name == "writer" && m.Presence != p {
					t.Errorf("presence after SetPresence(%+v) = %+v", p, m.Presence)
				}
			}
			return nil
		})
	}
}

// An agent's delivery mode as its person set it starts unset, and SetDelivery replaces it
// without touching the presence its daemon reported, and the other way round.
func setDeliveryReplacesItAndKeepsPresence(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		return tx.InsertMember(agent(b, "hum_alex", "writer", "member"))
	})
	memberIs := func(want board.DeliverySetting, presence board.Presence) {
		t.Helper()
		read(t, st, func(tx board.ReadTx) error {
			m, err := tx.MemberByName("brd_docs", "writer")
			if err == nil && (m.Delivery != want || m.Presence != presence) {
				t.Errorf("writer: delivery %+v presence %+v, want %+v and %+v", m.Delivery, m.Presence, want, presence)
			}
			return err
		})
	}
	memberIs(board.DeliverySetting{}, board.Presence{})
	reported := board.Presence{State: board.PresenceIdle, Since: at, At: at, Delivery: "all"}
	write(t, st, func(tx board.Tx) error { return tx.SetPresence("mem_docs_writer", reported) })
	for _, d := range []board.DeliverySetting{{Mode: "off", Seq: 7}, {Mode: "humans", Seq: 9}} {
		write(t, st, func(tx board.Tx) error { return tx.SetDelivery("mem_docs_writer", d) })
		memberIs(d, reported)
	}
	again := board.Presence{State: board.PresenceWorking, Since: at, At: at, Delivery: "humans"}
	write(t, st, func(tx board.Tx) error { return tx.SetPresence("mem_docs_writer", again) })
	memberIs(board.DeliverySetting{Mode: "humans", Seq: 9}, again)
}

func joinCode(b board.Board, creator board.Member) board.JoinCode {
	return board.JoinCode{
		ID: "jc_1", BoardID: b.ID, CodeDigest: "digest-code", Role: "reviewer",
		ExpiresAt: "2026-10-02T16:00:00.000Z", CreatedAt: at, CreatedBy: creator.ID, Kind: board.CodePairing,
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
				Body: fmt.Sprintf("message %d", seq), Redactions: []board.Redaction{}, Mentions: []board.Mention{},
				SenderName: p.from.Name, SenderKind: p.from.Kind, SenderRole: p.from.Role, SenderOwner: p.from.Owner, SenderHuman: p.from.HumanID,
				SenderHarness: p.from.Harness, AgentOwners: 1, // every agent here is alex's
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
		got, err := tx.Inbox(reviewer, true, 10)
		if err != nil {
			return err
		}
		if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, []int64{2, 4}) {
			t.Errorf("Inbox seqs = %v, want [2 4]", seqs)
		}
		limited, err := tx.Inbox(reviewer, true, 1)
		if err != nil {
			return err
		}
		if seqs := messageSeqs(limited); !reflect.DeepEqual(seqs, []int64{2}) {
			t.Errorf("Inbox with limit 1 seqs = %v, want [2]", seqs)
		}
		return nil
	})
}

// A message that mentions the reader with Wakes set is in its inbox, whatever it is
// addressed to, but only when the caller asks for mentions; one whose mention doesn't
// wake isn't.
func inboxHoldsMessagesThatMentionTheReader(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	mentionOf := func(m board.Member, wakes bool) board.Mention {
		mn := board.Mention{MemberID: m.ID, Kind: m.Kind, Name: m.Name, Text: "@" + m.Name, Wakes: wakes}
		if !wakes {
			mn.Reason = ptr(board.MentionLimit)
		}
		return mn
	}
	write(t, st, func(tx board.Tx) error {
		for i, mentions := range [][]board.Mention{
			{mentionOf(c.other, true), mentionOf(c.reviewer, true)},
			{mentionOf(c.reviewer, false)},
		} {
			seq := int64(6 + i)
			if err := tx.InsertMessage(board.Message{
				ID: fmt.Sprintf("msg_%d", seq), BoardID: "brd_docs", Seq: seq, At: at, SenderID: c.writer.ID,
				To: []string{"@other"}, Body: "message", Mentions: mentions,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		for _, tt := range []struct {
			mentions bool
			want     []int64
		}{{true, []int64{1, 2, 4, 6}}, {false, []int64{1, 2, 4}}} {
			got, err := tx.Inbox(c.reviewer, tt.mentions, 10)
			if err != nil {
				return err
			}
			if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, tt.want) {
				t.Errorf("Inbox with mentions %v: seqs = %v, want %v", tt.mentions, seqs, tt.want)
			}
			if n, err := tx.CountUnread(c.reviewer, true, tt.mentions); err != nil || n != int64(len(tt.want)) {
				t.Errorf("CountUnread with mentions %v = %d, %v, want %d", tt.mentions, n, err, len(tt.want))
			}
		}
		return nil
	})
}

func messageByIDFillsSenderAndReply(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	want := board.Message{
		ID: "msg_6", BoardID: "brd_docs", Seq: 6, At: at, SenderID: c.writer.ID, To: []string{"@reviewer", "role:reviewer"},
		Body: "Fixed, see notes.", ReplyTo: ptr("msg_2"), Urgent: true, ExpectsReply: true,
		Redactions: []board.Redaction{{Kind: "github_token", Count: 2}}, Recipients: []string{c.reviewer.ID},
		Mentions: []board.Mention{
			{MemberID: c.reviewer.ID, Kind: "agent", Name: "reviewer", Text: "@reviewer", Wakes: true},
			{MemberID: c.other.ID, Kind: "agent", Name: "other", Text: "@role:member", Reason: ptr(board.MentionLimit)},
		},
	}
	plain := board.Message{ID: "msg_7", BoardID: "brd_docs", Seq: 7, At: at, SenderID: c.alex.ID, To: []string{"all"}, Body: "ok"}
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertMessage(want); err != nil {
			return err
		}
		return tx.InsertMessage(plain)
	})
	want.ReplyToSeq, want.ReplyToFrom = ptr(int64(2)), ptr(c.writer.Name)
	want.SenderName, want.SenderKind, want.SenderRole = c.writer.Name, c.writer.Kind, c.writer.Role
	want.SenderOwner, want.SenderHuman, want.SenderHarness, want.AgentOwners = c.writer.Owner, c.writer.HumanID, c.writer.Harness, 1
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
		if got.Mentions == nil || len(got.Mentions) != 0 {
			t.Errorf("mentions of a message stored without any = %#v, want an empty list", got.Mentions)
		}
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

func insertMessageCountsItOnItsBoard(t *testing.T, st board.Store) {
	var empty board.Board
	write(t, st, func(tx board.Tx) error {
		var err error
		empty, _, err = newBoard(tx, "quiet")
		return err
	})
	c := newConversation(t, st)
	read(t, st, func(tx board.ReadTx) error {
		b, err := tx.BoardByName("docs")
		if err != nil {
			return err
		}
		if b.MessageCount != int64(len(c.messages)) || b.LastMessageAt == nil || *b.LastMessageAt != at {
			t.Errorf("docs: %d messages, last %v; want %d, %s", b.MessageCount, b.LastMessageAt, len(c.messages), at)
		}
		q, err := tx.BoardByID(empty.ID)
		if err != nil {
			return err
		}
		if q.MessageCount != 0 || q.LastMessageAt != nil {
			t.Errorf("a board without messages: %d messages, last %v", q.MessageCount, q.LastMessageAt)
		}
		return nil
	})
}

// threadsReadAndCountOnlyVisibleReplies adds replies to the conversation:
//
//	6 writer   -> @reviewer  replies to 1
//	7 other    -> @writer    replies to 6, in 1's thread
//	8 reviewer -> all        replies to 2
func threadsReadAndCountOnlyVisibleReplies(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	later := "2026-10-01T16:05:00.000Z"
	replies := []struct {
		from        board.Member
		to, replyTo string
		at          string
		root        string
	}{
		{c.writer, "@reviewer", "msg_1", at, "msg_1"},
		{c.other, "@writer", "msg_6", later, "msg_1"},
		{c.reviewer, "all", "msg_2", at, "msg_2"},
	}
	write(t, st, func(tx board.Tx) error {
		for i, r := range replies {
			seq := int64(6 + i)
			err := tx.InsertMessage(board.Message{
				ID: fmt.Sprintf("msg_%d", seq), BoardID: "brd_docs", Seq: seq, At: r.at, SenderID: r.from.ID, To: []string{r.to},
				Body: "reply", ReplyTo: ptr(r.replyTo), ThreadRoot: ptr(r.root),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		m, err := tx.MessageByID("msg_7")
		if err != nil {
			return err
		}
		if m.ThreadRoot == nil || *m.ThreadRoot != "msg_1" || m.ThreadRootSeq == nil || *m.ThreadRootSeq != 1 {
			t.Errorf("MessageByID(msg_7) thread root %v (#%v), want msg_1 (#1)", m.ThreadRoot, m.ThreadRootSeq)
		}

		threads := []struct {
			name    string
			readAll bool
			after   int64
			limit   int
			want    []int64
		}{
			{"every reply, oldest first", true, 0, 10, []int64{6, 7}},
			{"after a reply", true, 6, 10, []int64{7}},
			{"limited", true, 0, 1, []int64{6}},
			// 7 is addressed to @writer.
			{"only what the reader may see", false, 0, 10, []int64{6}},
		}
		for _, tt := range threads {
			got, err := tx.Thread("msg_1", c.reviewer, tt.readAll, tt.after, tt.limit)
			if err != nil {
				return err
			}
			if seqs := messageSeqs(got); !reflect.DeepEqual(seqs, tt.want) {
				t.Errorf("%s: Thread(msg_1) seqs = %v, want %v", tt.name, seqs, tt.want)
			}
		}

		all, err := tx.ThreadCounts(c.reviewer, true, []string{"msg_1", "msg_2", "msg_3"})
		if err != nil {
			return err
		}
		want := map[string]board.ThreadCount{"msg_1": {Replies: 2, LastAt: later}, "msg_2": {Replies: 1, LastAt: at}}
		if !reflect.DeepEqual(all, want) {
			t.Errorf("ThreadCounts(readAll) = %+v, want %+v", all, want)
		}
		visible, err := tx.ThreadCounts(c.reviewer, false, []string{"msg_1", "msg_2"})
		if err != nil {
			return err
		}
		want = map[string]board.ThreadCount{"msg_1": {Replies: 1, LastAt: at}, "msg_2": {Replies: 1, LastAt: at}}
		if !reflect.DeepEqual(visible, want) {
			t.Errorf("ThreadCounts(addressed) = %+v, want %+v", visible, want)
		}
		none, err := tx.ThreadCounts(c.reviewer, true, nil)
		if err != nil {
			return err
		}
		if none == nil || len(none) != 0 {
			t.Errorf("ThreadCounts of no threads = %#v, want an empty map", none)
		}
		return nil
	})
}

// reactionsReadBackPerMessageOldestFirst reacts to the conversation's messages and takes
// one reaction back.
func reactionsReadBackPerMessageOldestFirst(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	later := "2026-10-01T16:05:00.000Z"
	write(t, st, func(tx board.Tx) error {
		for _, r := range []board.Reaction{
			{MessageID: "msg_1", MemberID: c.writer.ID, Name: "thumbsup", At: at},
			{MessageID: "msg_1", MemberID: c.alex.ID, Name: "thumbsup", At: later},
			{MessageID: "msg_1", MemberID: c.writer.ID, Name: "eyes", At: later},
			{MessageID: "msg_2", MemberID: c.reviewer.ID, Name: "check", At: at},
			{MessageID: "msg_2", MemberID: c.other.ID, Name: "check", At: at},
		} {
			if err := tx.InsertReaction(r); err != nil {
				return err
			}
		}
		if err := tx.DeleteReaction("msg_2", c.reviewer.ID, "check"); err != nil {
			return err
		}
		// Taking back a reaction that isn't there changes nothing.
		return tx.DeleteReaction("msg_3", c.reviewer.ID, "check")
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.Reactions([]string{"msg_1", "msg_2", "msg_3"})
		if err != nil {
			return err
		}
		want := map[string][]board.Reaction{
			"msg_1": {
				{MessageID: "msg_1", MemberID: c.writer.ID, MemberName: "writer", Name: "thumbsup", At: at},
				{MessageID: "msg_1", MemberID: c.alex.ID, MemberName: "hum_alex", Name: "thumbsup", At: later},
				{MessageID: "msg_1", MemberID: c.writer.ID, MemberName: "writer", Name: "eyes", At: later},
			},
			"msg_2": {{MessageID: "msg_2", MemberID: c.other.ID, MemberName: "other", Name: "check", At: at}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Reactions = %+v,\nwant %+v", got, want)
		}
		none, err := tx.Reactions(nil)
		if err != nil {
			return err
		}
		if none == nil || len(none) != 0 {
			t.Errorf("Reactions of no messages = %#v, want an empty map", none)
		}
		return nil
	})
}

// threadsListNewestActivityFirst adds replies to the conversation:
//
//	6 writer   -> @reviewer  replies to 1
//	7 other    -> @writer    replies to 2
//	8 reviewer -> all        replies to 6, in 1's thread
//	9 writer   -> @other     replies to 3
func threadsListNewestActivityFirst(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	replies := []struct {
		from        board.Member
		to, replyTo string
		root        string
	}{
		{c.writer, "@reviewer", "msg_1", "msg_1"},
		{c.other, "@writer", "msg_2", "msg_2"},
		{c.reviewer, "all", "msg_6", "msg_1"},
		{c.writer, "@other", "msg_3", "msg_3"},
	}
	write(t, st, func(tx board.Tx) error {
		for i, r := range replies {
			seq := int64(6 + i)
			err := tx.InsertMessage(board.Message{
				ID: fmt.Sprintf("msg_%d", seq), BoardID: "brd_docs", Seq: seq, At: at, SenderID: r.from.ID, To: []string{r.to},
				Body: "reply", ReplyTo: ptr(r.replyTo), ThreadRoot: ptr(r.root),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	tests := []struct {
		name    string
		reader  board.Member
		readAll bool
		limit   int
		want    []board.ThreadInfo
	}{
		{"every thread, newest reply first", c.reviewer, true, 10, []board.ThreadInfo{
			{RootID: "msg_3", Repliers: []string{"writer"}},
			{RootID: "msg_1", Repliers: []string{"writer", "reviewer"}},
			{RootID: "msg_2", Repliers: []string{"other"}},
		}},
		{"limited", c.reviewer, true, 2, []board.ThreadInfo{
			{RootID: "msg_3", Repliers: []string{"writer"}},
			{RootID: "msg_1", Repliers: []string{"writer", "reviewer"}},
		}},
		// The reviewer may see 1 and 2 but not 3; of the replies, 6 and 8 but not 7, to @writer.
		{"only what the reader may see", c.reviewer, false, 10, []board.ThreadInfo{
			{RootID: "msg_1", Repliers: []string{"writer", "reviewer"}},
		}},
	}
	read(t, st, func(tx board.ReadTx) error {
		for _, tt := range tests {
			got, err := tx.Threads("brd_docs", tt.reader, tt.readAll, tt.limit)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s: Threads = %+v,\nwant %+v", tt.name, got, tt.want)
			}
		}
		return nil
	})
}

func countUnreadCountsWhatInboxOrTheTimelineHasLeft(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	write(t, st, func(tx board.Tx) error { return tx.SetCursor(c.reviewer.ID, 1) })
	read(t, st, func(tx board.ReadTx) error {
		reviewer, err := tx.MemberByName("brd_docs", "reviewer")
		if err != nil {
			return err
		}
		// 2 and 4, as Inbox returns them.
		if n, err := tx.CountUnread(reviewer, true, false); err != nil || n != 2 {
			t.Errorf("CountUnread(reviewer, addressed) = %d, %v, want 2", n, err)
		}
		// Everything after 1 it didn't send: 2, 3 and 4.
		if n, err := tx.CountUnread(reviewer, false, false); err != nil || n != 3 {
			t.Errorf("CountUnread(reviewer, all) = %d, %v, want 3", n, err)
		}
		alex := c.alex
		alex.Cursor = 3
		// Only 5: 4 is alex's own.
		if n, err := tx.CountUnread(alex, false, false); err != nil || n != 1 {
			t.Errorf("CountUnread(alex after 3) = %d, %v, want 1", n, err)
		}
		alex.Cursor = 5
		if n, err := tx.CountUnread(alex, false, false); err != nil || n != 0 {
			t.Errorf("CountUnread(alex at the end) = %d, %v, want 0", n, err)
		}
		return nil
	})
}

// messageRecipientsRoundTrip: a message's recipients read back in order; an empty list
// stays empty, apart from a message with none recorded (one to all), which reads nil.
func messageRecipientsRoundTrip(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	write(t, st, func(tx board.Tx) error {
		for _, m := range []board.Message{
			{ID: "msg_6", BoardID: "brd_docs", Seq: 6, At: at, SenderID: c.alex.ID, To: []string{"@writer", "role:reviewer"}, Body: "two", Recipients: []string{c.writer.ID, c.reviewer.ID}},
			{ID: "msg_7", BoardID: "brd_docs", Seq: 7, At: at, SenderID: c.reviewer.ID, To: []string{"role:reviewer"}, Body: "none", Recipients: []string{}},
			{ID: "msg_8", BoardID: "brd_docs", Seq: 8, At: at, SenderID: c.alex.ID, To: []string{"all"}, Body: "everyone"},
		} {
			if err := tx.InsertMessage(m); err != nil {
				return err
			}
		}
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.MessagesBySeq("brd_docs", []int64{6, 7, 8})
		if err != nil {
			return err
		}
		if r := got[6].Recipients; !reflect.DeepEqual(r, []string{c.writer.ID, c.reviewer.ID}) {
			t.Errorf("recipients of #6 = %#v, want writer then reviewer", r)
		}
		if r := got[7].Recipients; r == nil || len(r) != 0 {
			t.Errorf("recipients of #7 = %#v, want an empty list", r)
		}
		if r := got[8].Recipients; r != nil {
			t.Errorf("recipients of a message to all = %#v, want nil", r)
		}
		return nil
	})
}
