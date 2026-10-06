package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// guestCode makes a guest code for handle on board with token.
func (s *testServer) guestCode(token, board, handle string) *api.CreateJoinCodeResponse {
	s.t.Helper()
	r, err := s.client(token).CreateJoinCodeWithResponse(context.Background(), board, nil, api.CreateJoinCodeRequest{Role: "reviewer", Guest: &handle})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// guestJoin redeems code with no token.
func (s *testServer) guestJoin(code string) *api.GuestJoinResponse {
	s.t.Helper()
	r, err := s.client("").GuestJoinWithResponse(context.Background(), nil, api.GuestJoinRequest{Code: code, KeyName: "laptop"})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// A guest code is redeemed with no token at all and gives the guest's agent a token for
// that one board; the guest's agent can't reach any other board, and a probe of a board
// it isn't on answers byte for byte as a board that doesn't exist.
func TestAGuestJoinsWithNoTokenAndSeesOneBoard(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	code := s.guestCode(s.owner, boardName, "kim")
	mustStatus(t, code, nil, 201)
	if code.JSON201.Kind != api.JoinCodeKindGuest || code.JSON201.Guest == nil || *code.JSON201.Guest != "kim" ||
		*code.JSON201.JoinLine != "Join Aboard board "+boardName+" on localhost as guest with code "+*code.JSON201.Code {
		t.Fatalf("the guest code: %s", bodyOf(code))
	}
	joined := s.guestJoin(*code.JSON201.Code)
	mustStatus(t, joined, nil, 201)
	kim := joined.JSON201.Token
	if joined.JSON201.Agent.Owner == nil || *joined.JSON201.Agent.Owner != "kim" || joined.JSON201.Board.Name != boardName {
		t.Fatalf("the guest's agent: %s", bodyOf(joined))
	}
	wantCode(t, s.guestJoin(*code.JSON201.Code), 404, "join_code_invalid")

	private := "secret-plans"
	visibility := api.BoardVisibilityPrivate
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &private, Visibility: &visibility})
	mustStatus(t, b, err, 201)
	open := "open-plans"
	b, err = s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &open})
	mustStatus(t, b, err, 201)
	kimKey := joined.JSON201.Key.Token
	me, err := s.client(kimKey).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	if me.JSON200.ServerRole == nil || *me.JSON200.ServerRole != api.ServerRoleGuest {
		t.Fatalf("the guest's key: %s", bodyOf(me))
	}
	for _, token := range []string{kim, kimKey} {
		for _, path := range []string{"/v1/boards/%s", "/v1/boards/%s/messages", "/v1/boards/%s/people", "/v1/boards/%s/events", "/v1/boards/%s/members"} {
			_, missing := s.raw(http.MethodGet, fmt.Sprintf(path, "no-such-board"), token, nil)
			for _, name := range []string{private, open} {
				status, got := s.raw(http.MethodGet, fmt.Sprintf(path, name), token, nil)
				if status != 404 || got != strings.ReplaceAll(missing, "no-such-board", name) {
					t.Fatalf("the guest at %s: %d %s; a missing board: %s", fmt.Sprintf(path, name), status, got, missing)
				}
			}
		}
		status, got := s.raw(http.MethodPost, "/v1/boards/"+open+"/people", token, map[string]string{"handle": "kim"})
		if status != 404 && status != 403 {
			t.Fatalf("the guest joining an open board: %d %s", status, got)
		}
	}
	created, err := s.client(kimKey).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{})
	if c := errorCode(t, created, err, 403); c != "guest_not_allowed" {
		t.Fatalf("the guest creating a board: %s", c)
	}
	list, err := s.client(kim).ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Boards) != 1 || list.JSON200.Boards[0].Name != boardName {
		t.Fatalf("the guest's agent listing boards: %s", bodyOf(list))
	}
	title := "Mine now"
	u, err := s.client(kim).UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Title: &title})
	if c := errorCode(t, u, err, 403); c != "guest_not_allowed" {
		t.Fatalf("the guest's agent changing the title: %s", c)
	}
	jc, err := s.client(kim).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	if c := errorCode(t, jc, err, 403); c != "guest_not_allowed" {
		t.Fatalf("the guest's agent making a pairing code: %s", c)
	}
	people, err := s.client(kim).ListServerPeopleWithResponse(ctx)
	if c := errorCode(t, people, err, 403); c != "human_token_required" {
		t.Fatalf("the guest's agent listing the server's people: %s", c)
	}
	onBoard, err := s.client(s.owner).ListPeopleWithResponse(ctx, boardName)
	mustStatus(t, onBoard, err, 200)
	last := onBoard.JSON200.People[len(onBoard.JSON200.People)-1]
	if last.Handle != "kim" || last.ServerRole != api.ServerRoleGuest || last.BoardRole != api.BoardRoleMember {
		t.Fatalf("the board's people: %s", bodyOf(onBoard))
	}
	members, err := s.client(s.owner).ListMembersWithResponse(ctx, boardName)
	mustStatus(t, members, err, 200)
	for _, m := range members.JSON200.Members {
		switch {
		case m.Name == "kim" && (m.ServerRole == nil || *m.ServerRole != api.ServerRoleGuest):
			t.Fatalf("kim among the members: %+v", m)
		case m.Kind == api.MemberKindAgent && m.ServerRole != nil:
			t.Fatalf("an agent with a server role: %+v", m)
		}
	}
}

// Each code works only at its own door: a guest code with a person's key is refused, a
// pairing code with no key looks like a wrong code, and an agent can't make a guest code.
func TestCodesAreNeverTakenForTheOtherKind(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	guest := s.guestCode(s.owner, boardName, "kim")
	mustStatus(t, guest, nil, 201)
	maya := s.addHuman("maya")
	j, err := s.client(maya).JoinWithResponse(ctx, nil, api.JoinRequest{Code: guest.JSON201.Code})
	if c := errorCode(t, j, err, 403); c != "guest_code_not_for_members" {
		t.Fatalf("a guest code with maya's key: %s", c)
	}
	pairing, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, pairing, err, 201)
	wantCode(t, s.guestJoin(*pairing.JSON201.Code), 404, "join_code_invalid")
	wantCode(t, s.guestCode(writer, boardName, "lee"), 403, "human_token_required")
	wantCode(t, s.guestCode(s.owner, boardName, "maya"), 409, "handle_taken")
	wantCode(t, s.guestCode(maya, boardName, "lee"), 403, "not_on_board")
	// The guest code still works for kim after the refusals.
	mustStatus(t, s.guestJoin(*guest.JSON201.Code), nil, 201)
}

// Redeeming guest codes needs no token, so it is limited per address and across the
// server, together with redeeming invites.
func TestGuestJoinAttemptsAreLimited(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, func(o *api.Options) { o.ConnectsPerMinute, o.ConnectsPerMinuteServer = 3, 100 })
	wantCode(t, s.connect("abi_guess", "maya"), 404, "invite_invalid")
	for range 2 {
		wantCode(t, s.guestJoin("ZZZ-ZZZ"), 404, "join_code_invalid")
	}
	r := s.guestJoin("ZZZ-ZZZ")
	wantCode(t, r, 429, "rate_limited")
	if r.HTTPResponse.Header.Get("Retry-After") == "" {
		t.Fatal("a refused attempt has no Retry-After")
	}
	s.clock.Advance(time.Minute)
	wantCode(t, s.guestJoin("ZZZ-ZZZ"), 404, "join_code_invalid")
}

// The server's people are listed with their roles to a person; only an admin's own key
// changes a role or removes someone, and a removed person's key, browser and agent all
// stop at once.
func TestServerPeopleRolesAndRemoval(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	maya := s.addHuman("maya")
	j := s.joinBoard(maya, boardName, "writer", nil)
	mayaAgent, mayaBrowser := j.JSON201.Token, s.browserToken(maya)

	people, err := s.client(maya).ListServerPeopleWithResponse(ctx)
	mustStatus(t, people, err, 200)
	if len(people.JSON200.People) != 2 || people.JSON200.People[0].ServerRole != api.ServerRoleAdmin || people.JSON200.People[1].ServerRole != api.ServerRoleMember {
		t.Fatalf("the server's people: %s", bodyOf(people))
	}
	admin := api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRoleAdmin}
	r, err := s.client(maya).SetServerRoleWithResponse(ctx, "maya", nil, admin)
	if c := errorCode(t, r, err, 403); c != "server_admin_required" {
		t.Fatalf("a member making herself an admin: %s", c)
	}
	r, err = s.client(s.browserToken(s.owner)).SetServerRoleWithResponse(ctx, "maya", nil, admin)
	if c := errorCode(t, r, err, 403); c != "human_token_required" {
		t.Fatalf("an admin's browser changing a role: %s", c)
	}
	r, err = s.client(s.owner).SetServerRoleWithResponse(ctx, "alex", nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRoleMember})
	if c := errorCode(t, r, err, 409); c != "last_admin" {
		t.Fatalf("the last admin made a member: %s", c)
	}
	r, err = s.client(s.owner).SetServerRoleWithResponse(ctx, "maya", nil, admin)
	mustStatus(t, r, err, 200)
	if !r.JSON200.Changed || r.JSON200.Person.ServerRole != api.ServerRoleAdmin {
		t.Fatalf("maya made an admin: %s", bodyOf(r))
	}

	dry := true
	preview, err := s.client(s.owner).RemoveFromServerWithResponse(ctx, "maya", &api.RemoveFromServerParams{DryRun: &dry})
	mustStatus(t, preview, err, 200)
	if !preview.JSON200.DryRun || preview.JSON200.KeysRevoked != 1 || preview.JSON200.AgentsRemoved != 1 || preview.JSON200.BoardsLeft != 1 {
		t.Fatalf("the preview: %s", bodyOf(preview))
	}
	gone, err := s.client(s.browserToken(s.owner)).RemoveFromServerWithResponse(ctx, "maya", nil)
	if c := errorCode(t, gone, err, 403); c != "human_token_required" {
		t.Fatalf("an admin's browser removing someone: %s", c)
	}
	gone, err = s.client(s.owner).RemoveFromServerWithResponse(ctx, "maya", nil)
	mustStatus(t, gone, err, 200)
	for name, token := range map[string]string{"key": maya, "browser": mayaBrowser, "agent": mayaAgent} {
		me, err := s.client(token).GetMeWithResponse(ctx)
		if c := errorCode(t, me, err, 401); c != "unauthorized" {
			t.Fatalf("maya's %s after removal: %s", name, c)
		}
	}
	again, err := s.client(s.owner).RemoveFromServerWithResponse(ctx, "maya", nil)
	if c := errorCode(t, again, err, 404); c != "person_not_found" {
		t.Fatalf("removing maya again: %s", c)
	}
}
