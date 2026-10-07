package api_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestPersonRenameKeepsHistorySeatsAndRetiredHandle(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	board, agent, _ := s.pair("recommended")
	before, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, before, err, 200)
	posted := say(s, s.owner, board, nil, "Written by @alex, unchanged text")
	mustStatus(t, posted, nil, 201)
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var oldActor, oldData, oldHash string
	if err := db.QueryRowContext(ctx, "SELECT actor_json,data_json,hash FROM events WHERE type='message.posted' ORDER BY seq LIMIT 1").Scan(&oldActor, &oldData, &oldHash); err != nil {
		t.Fatal(err)
	}
	r, err := s.client(s.owner).RenamePersonWithResponse(ctx, "alex", nil, api.PersonRename{Handle: "leo"})
	mustStatus(t, r, err, 200)
	if r.JSON200.Person.Id != before.JSON200.Id || r.JSON200.Person.Handle != "leo" || !r.JSON200.Changed {
		t.Fatalf("identity changed: %+v", r.JSON200)
	}
	var actor, data, hash string
	if err := db.QueryRowContext(ctx, "SELECT actor_json,data_json,hash FROM events WHERE type='message.posted' ORDER BY seq LIMIT 1").Scan(&actor, &data, &hash); err != nil {
		t.Fatal(err)
	}
	if actor != oldActor || data != oldData || hash != oldHash {
		t.Fatal("rename rewrote hashed history")
	}
	var changes int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE type='person.renamed'").Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("rename events: %d", changes)
	}
	members, err := s.client(agent).ListMembersWithResponse(ctx, board, nil)
	mustStatus(t, members, err, 200)
	for _, m := range members.JSON200.Members {
		if m.Kind == api.MemberKindAgent && (m.Owner == nil || *m.Owner != "leo") {
			t.Fatalf("old owner: %+v", m)
		}
	}
	timeline, err := s.client(agent).ListMessagesWithResponse(ctx, board, nil)
	mustStatus(t, timeline, err, 200)
	found := false
	for _, m := range timeline.JSON200.Messages {
		if m.Body == "Written by @alex, unchanged text" {
			found = true
			if m.From.Name != "leo" {
				t.Fatalf("historical sender: %+v", m.From)
			}
		}
	}
	if !found {
		t.Fatal("historical message disappeared")
	}
	inv := s.invite(s.owner, 0)
	attempt := s.connect(inv.JSON201.Invite, "alex")
	wantCode(t, attempt, 409, "handle_taken")
	again, err := s.client(s.owner).RenamePersonWithResponse(ctx, "leo", nil, api.PersonRename{Handle: "alex"})
	mustStatus(t, again, err, 200)
	if again.JSON200.Person.Id != before.JSON200.Id {
		t.Fatal("rename back changed identity")
	}
}

func TestPersonRenameRejectsAgentAndOtherPerson(t *testing.T) {
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	r, err := s.client(agent).RenamePersonWithResponse(context.Background(), "alex", nil, api.PersonRename{Handle: "leo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, r, 403, "human_token_required")
	maya := s.addHuman("maya")
	r, err = s.client(maya).RenamePersonWithResponse(context.Background(), "alex", nil, api.PersonRename{Handle: "leo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, r, 403, "server_admin_required")
}

func TestPersonRenameReplayRechecksAuthorityByID(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	maya := s.addHuman("maya")
	key := "rename-once"
	params := &api.RenamePersonParams{IdempotencyKey: &key}
	renamed, err := s.client(s.owner).RenamePersonWithResponse(ctx, "maya", params, api.PersonRename{Handle: "sam"})
	mustStatus(t, renamed, err, 200)
	// Promote maya's same identity, then demote the actor behind the cached receipt.
	promoted, err := s.client(s.owner).SetServerRoleWithResponse(ctx, "sam", nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRoleAdmin})
	mustStatus(t, promoted, err, 200)
	demoted, err := s.client(maya).SetServerRoleWithResponse(ctx, "alex", nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRoleMember})
	mustStatus(t, demoted, err, 200)
	again, err := s.client(s.owner).RenamePersonWithResponse(ctx, "maya", params, api.PersonRename{Handle: "sam"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, again, 403, "server_admin_required")
	selfKey := "self-rename"
	selfParams := &api.RenamePersonParams{IdempotencyKey: &selfKey}
	self, err := s.client(maya).RenamePersonWithResponse(ctx, "sam", selfParams, api.PersonRename{Handle: "maya"})
	mustStatus(t, self, err, 200)
	repeated, err := s.client(maya).RenamePersonWithResponse(ctx, "sam", selfParams, api.PersonRename{Handle: "maya"})
	mustStatus(t, repeated, err, 200)
	if repeated.JSON200.Person.Id != self.JSON200.Person.Id {
		t.Fatal("replay changed identity")
	}
}

func TestPersonRenameKeepsBoundGuestCodeAndRefusesRetiredGuestHandle(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	b, _, _ := s.pair("starter")
	first := s.guestCode(s.owner, b, "kim")
	mustStatus(t, first, nil, 201)
	unboundBoard := "unbound-guest"
	tpl := "writer-reviewer"
	unboundCreated, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &unboundBoard, Template: &tpl})
	mustStatus(t, unboundCreated, err, 201)
	unbound := s.guestCode(s.owner, unboundBoard, "kim")
	mustStatus(t, unbound, nil, 201)
	guest := s.guestJoin(*first.JSON201.Code)
	mustStatus(t, guest, nil, 201)
	// A code for a new identity issued before kim existed must not become identity proof.
	// Existing-person guest codes already record GuestID.
	secondBoard := "guest-second"
	made, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &secondBoard, Template: stringPointer("writer-reviewer")})
	mustStatus(t, made, err, 201)
	bound := s.guestCode(s.owner, secondBoard, "kim")
	mustStatus(t, bound, nil, 201)
	r, err := s.client(guest.JSON201.Key.Token).RenamePersonWithResponse(ctx, "kim", nil, api.PersonRename{Handle: "sam"})
	mustStatus(t, r, err, 200)
	joined, err := s.client(guest.JSON201.Key.Token).JoinWithResponse(ctx, nil, api.JoinRequest{Code: bound.JSON201.Code})
	mustStatus(t, joined, err, 201)
	oldCode := s.guestJoin(*unbound.JSON201.Code)
	wantCode(t, oldCode, 404, "join_code_invalid")
	reserved := s.guestCode(s.owner, b, "kim")
	wantCode(t, reserved, 409, "handle_taken")
	browser := s.browserToken(s.owner)
	refused, err := s.client(browser).RenamePersonWithResponse(ctx, "alex", nil, api.PersonRename{Handle: "leo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, refused, 403, "human_token_required")
}

func stringPointer(s string) *string { return &s }

func TestPersonRenameLeavesOldMachineApprovalInvalid(t *testing.T) {
	s := newTestServer(t)
	code, _ := s.startMachine("alex", "new-machine")
	renamed, err := s.client(s.owner).RenamePersonWithResponse(context.Background(), "alex", nil, api.PersonRename{Handle: "leo"})
	mustStatus(t, renamed, err, 200)
	refused := s.approveMachine(s.owner, code)
	wantCode(t, refused, 404, "machine_request_invalid")
}

func TestMemberListingCarriesDisplayNameOutsideSenderFields(t *testing.T) {
	s := newTestServer(t)
	b, agent, _ := s.pair("starter")
	inv := s.invite(s.owner, 0)
	display := "Leo Raghav"
	connected, err := s.client("").ConnectWithResponse(context.Background(), nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "leo", KeyName: "laptop", DisplayName: &display})
	mustStatus(t, connected, err, 201)
	added, err := s.client(s.owner).AddPersonWithResponse(context.Background(), b, nil, api.AddPersonRequest{Handle: "leo"})
	mustStatus(t, added, err, 201)
	members, err := s.client(agent).ListMembersWithResponse(context.Background(), b, nil)
	mustStatus(t, members, err, 200)
	found := false
	for _, m := range members.JSON200.Members {
		if m.Name == "leo" {
			found = m.DisplayName != nil && *m.DisplayName == display
		}
	}
	if !found {
		t.Fatal("human display name absent from agent-facing member list")
	}
	posted := say(s, connected.JSON201.Key.Token, b, nil, "Display name is not identity")
	mustStatus(t, posted, nil, 201)
	if posted.JSON201.From.Name != "leo" {
		t.Fatal("display name entered sender identity")
	}
}
