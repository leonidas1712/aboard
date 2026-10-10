package api_test

import (
	"context"
	"database/sql"
	"net/http"
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

func TestPersonRenameRejectsOtherTargetsAndOtherPeople(t *testing.T) {
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	r, err := s.client(agent).RenamePersonWithResponse(context.Background(), "missing", nil, api.PersonRename{Handle: "leo"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, r, 403, "server_admin_required")
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
	mustStatus(t, refused, nil, 200)
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

func TestAgentRenamesOnlyItsOwnPerson(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	b, agent, _ := s.pair("starter")
	maya := s.addHuman("maya")
	_ = maya
	wrong, err := s.client(agent).RenamePersonWithResponse(ctx, "maya", nil, api.PersonRename{Handle: "sam"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, wrong, 403, "server_admin_required")
	renamed, err := s.client(agent).RenamePersonWithResponse(ctx, "alex", nil, api.PersonRename{Handle: "leo"})
	mustStatus(t, renamed, err, 200)
	members, err := s.client(agent).ListMembersWithResponse(ctx, b, nil)
	mustStatus(t, members, err, 200)
	for _, m := range members.JSON200.Members {
		if m.Kind == api.MemberKindAgent && (m.Owner == nil || *m.Owner != "leo") {
			t.Fatalf("owner did not follow identity: %+v", m)
		}
	}
	inv := s.invite(s.owner, 0)
	wantCode(t, s.connect(inv.JSON201.Invite, "alex"), 409, "handle_taken")
}

func TestInviteHandleEditIsMetadataOnly(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	_, agent, _ := s.pair("starter")
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{SuggestedHandle: stringPointer("maya")})
	mustStatus(t, inv, err, 201)
	if inv.JSON201.SuggestedHandle == nil || *inv.JSON201.SuggestedHandle != "maya" {
		t.Fatal("issuance omitted suggestion")
	}
	edited, err := s.client(agent).EditServerInviteWithResponse(ctx, inv.JSON201.Id, nil, api.EditServerInvite{SuggestedHandle: "sam"})
	mustStatus(t, edited, err, 200)
	if edited.JSON200.SuggestedHandle == nil || *edited.JSON200.SuggestedHandle != "sam" {
		t.Fatal("edit omitted suggestion")
	}
	preview, err := s.client("").PreviewServerInviteWithResponse(ctx, api.PreviewServerInviteJSONRequestBody{Invite: inv.JSON201.Invite})
	mustStatus(t, preview, err, 200)
	if preview.JSON200.SuggestedHandle == nil || *preview.JSON200.SuggestedHandle != "sam" {
		t.Fatal("preview omitted suggestion")
	}
	connected := s.connect(inv.JSON201.Invite, "chosen")
	mustStatus(t, connected, nil, 201)
	unavailable, err := s.client(agent).EditServerInviteWithResponse(ctx, inv.JSON201.Id, nil, api.EditServerInvite{SuggestedHandle: "other"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, unavailable, 409, "invite_unavailable")
}

func TestInviteEditRefusesOtherOwnerAndReplaysAfterUse(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	inv := s.invite(s.owner, 0)
	other := s.addHuman("maya")
	refused, err := s.client(other).EditServerInviteWithResponse(ctx, inv.JSON201.Id, nil, api.EditServerInvite{SuggestedHandle: "sam"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, refused, 404, "invite_not_found")
	key := "edit-once"
	params := &api.EditServerInviteParams{IdempotencyKey: &key}
	changed, err := s.client(s.owner).EditServerInviteWithResponse(ctx, inv.JSON201.Id, params, api.EditServerInvite{SuggestedHandle: "sam"})
	mustStatus(t, changed, err, 200)
	mustStatus(t, s.connect(inv.JSON201.Invite, "chosen"), nil, 201)
	replay, err := s.client(s.owner).EditServerInviteWithResponse(ctx, inv.JSON201.Id, params, api.EditServerInvite{SuggestedHandle: "sam"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, replay, 409, "invite_unavailable")
}

func TestBrowserOwnRenameAndInviteEditNeedCSRF(t *testing.T) {
	s := newTestServer(t)
	b := s.cookieBrowser(s.owner)
	inv := s.invite(s.owner, 0)
	refused := s.send("PATCH", "/v1/invites/"+inv.JSON201.Id, map[string]any{"suggested_handle": "maya"}, func(r *http.Request) { r.AddCookie(b.cookie); s.fromPage(r) })
	if refused.status != 403 || refused.code() != "csrf_token_invalid" {
		t.Fatalf("unguarded edit: %+v", refused)
	}
	rejectedRename := s.send("POST", "/v1/people/alex/rename", map[string]any{"handle": "leo"}, func(r *http.Request) { r.AddCookie(b.cookie); s.fromPage(r) })
	if rejectedRename.status != 403 || rejectedRename.code() != "csrf_token_invalid" {
		t.Fatalf("unguarded rename: %+v", rejectedRename)
	}
	edited := b.do("PATCH", "/v1/invites/"+inv.JSON201.Id, map[string]any{"suggested_handle": "maya"})
	if edited.status != 200 {
		t.Fatalf("browser edit: %+v", edited)
	}
	renamed := b.do("POST", "/v1/people/alex/rename", map[string]any{"handle": "leo"})
	if renamed.status != 200 {
		t.Fatalf("browser rename: %+v", renamed)
	}
}

func TestInviteEditDoesNotRevealOtherBoardsToAgent(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	_, agent, _ := s.pair("starter")
	hidden, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: stringPointer("hidden"), Template: stringPointer("writer-reviewer")})
	mustStatus(t, hidden, err, 201)
	ids := []string{hidden.JSON201.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &ids})
	mustStatus(t, inv, err, 201)
	edited, err := s.client(agent).EditServerInviteWithResponse(ctx, inv.JSON201.Id, nil, api.EditServerInvite{SuggestedHandle: "maya"})
	mustStatus(t, edited, err, 200)
	if len(edited.JSON200.Boards) != 0 {
		t.Fatalf("agent learned another board: %v", edited.JSON200.Boards)
	}
}
