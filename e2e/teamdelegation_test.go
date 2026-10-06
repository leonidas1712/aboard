//go:build e2e

package e2e

import (
	"net/http"
	"testing"
)

func delegationToken(t *testing.T, tm *team, key string) string {
	t.Helper()
	status, body := tm.call("POST", "/v1/delegations", key, map[string]any{"name": "acceptance-machine"})
	if status != http.StatusCreated {
		t.Fatalf("create delegation: status %d, error %v", status, body["error"])
	}
	return field(t, body, "token").(string)
}

func delegatedSeat(t *testing.T, tm *team, token, board string, want int) map[string]any {
	t.Helper()
	status, body := tm.call("POST", "/v1/join", token, map[string]any{"board": board, "session": "codex:same-session", "harness": "codex"})
	if status != want {
		t.Fatalf("delegated join %s: status %d, want %d, error %v", board, status, want, body["error"])
	}
	return body
}

type delegationBoardSnapshot struct {
	head    any
	members int
}

func delegationSnapshot(t *testing.T, tm *team, board string) delegationBoardSnapshot {
	t.Helper()
	status, b := tm.call("GET", "/v1/boards/"+board, tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("board snapshot %s: status %d, error %v", board, status, b["error"])
	}
	status, m := tm.call("GET", "/v1/boards/"+board+"/members", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("member snapshot %s: status %d, error %v", board, status, m["error"])
	}
	return delegationBoardSnapshot{head: b["head_seq"], members: len(m["members"].([]any))}
}

func TestTeamDelegationListsOnlyVisibleBoardsAndCannotReadContent(t *testing.T) {
	tm := newTeam(t)
	open := tm.newBoard(tm.admin, "open")
	maya := tm.person("maya")
	hidden := tm.newBoard(maya, "private")
	delegation := delegationToken(t, tm, tm.key(tm.admin))
	status, body := tm.call("GET", "/v1/boards?all=true", delegation, nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %v", status, body)
	}
	if _, present := body["hidden_boards"]; present {
		t.Fatalf("admin delegation exposes hidden boards: %v", body)
	}
	found := false
	for _, raw := range body["boards"].([]any) {
		b := raw.(map[string]any)
		if b["name"] == hidden {
			t.Fatalf("hidden board exposed: %v", b)
		}
		if b["name"] == open {
			found = true
		}
		for _, name := range []string{"unread", "read_up_to", "needs_reply"} {
			if _, present := b[name]; present {
				t.Fatalf("person's %s exposed: %v", name, b)
			}
		}
	}
	if !found {
		t.Fatalf("open board missing: %v", body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/boards/" + open + "/messages", nil},
		{"GET", "/v1/stream", nil},
		{"POST", "/v1/keys", map[string]any{"name": "extra"}},
		{"POST", "/v1/join", map[string]any{"code": "ZZZ-ZZZ", "session": "codex:same-session"}},
	} {
		status, body := tm.call(tc.method, tc.path, delegation, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("restricted %s %s: status %d, error %v", tc.method, tc.path, status, body["error"])
		}
	}
}

func TestTeamDelegationKeepsTwoOwnersWithTheSameSessionApart(t *testing.T) {
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	maya := tm.person("maya")
	adminID, mayaID := tm.me(tm.key(tm.admin))["id"], tm.me(tm.key(maya))["id"]
	a := delegationToken(t, tm, tm.key(tm.admin))
	b := delegationToken(t, tm, tm.key(maya))
	first := delegatedSeat(t, tm, a, board, http.StatusCreated)
	second := delegatedSeat(t, tm, b, board, http.StatusCreated)
	if field(t, first, "agent.id") == field(t, second, "agent.id") || field(t, first, "agent.owner_id") != adminID || field(t, second, "agent.owner_id") != mayaID {
		t.Fatalf("owners not isolated: first seat %v owner %v; second seat %v owner %v", field(t, first, "agent.id"), field(t, first, "agent.owner_id"), field(t, second, "agent.id"), field(t, second, "agent.owner_id"))
	}
	before := delegationSnapshot(t, tm, board)
	reused := delegatedSeat(t, tm, a, board, http.StatusOK)
	if reused["reused"] != true || field(t, reused, "agent.id") != field(t, first, "agent.id") || reused["token"] == first["token"] {
		t.Fatalf("seat reuse did not rotate its token: seat %v, reused %v", field(t, reused, "agent.id"), reused["reused"])
	}
	if after := delegationSnapshot(t, tm, board); before != after {
		t.Fatalf("reuse changed board: before %+v, after %+v", before, after)
	}
	status, body := tm.call("GET", "/v1/me", first["token"].(string), nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("old token still works: status %d, error %v", status, body["error"])
	}
	if tm.me(reused["token"].(string))["id"] != field(t, first, "agent.id") {
		t.Fatal("replacement token doesn't authenticate as the reused seat")
	}
	if tm.me(second["token"].(string))["owner"] != "maya" {
		t.Fatal("other owner's seat changed")
	}
}

func TestTeamGuestDelegationListsOnlyItsBoardAndCannotJoin(t *testing.T) {
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	other := tm.newBoard(tm.admin, "open")
	status, code := tm.call("POST", "/v1/boards/"+board+"/join-codes", tm.key(tm.admin), map[string]any{"role": "member", "guest": "sam"})
	if status != http.StatusCreated {
		t.Fatalf("guest code: status %d, error %v", status, code["error"])
	}
	status, guest := tm.call("POST", "/v1/guest-join", "", map[string]any{"code": code["code"], "key_name": "laptop"})
	if status != http.StatusCreated {
		t.Fatalf("guest join: status %d, error %v", status, guest["error"])
	}
	d := delegationToken(t, tm, field(t, guest, "key.token").(string))
	status, listed := tm.call("GET", "/v1/boards?all=true", d, nil)
	if status != http.StatusOK {
		t.Fatalf("guest list: %d %v", status, listed)
	}
	boards := listed["boards"].([]any)
	if len(boards) != 1 || boards[0].(map[string]any)["name"] != board {
		t.Fatalf("guest list escaped scope: %v", listed)
	}
	for _, tc := range []struct {
		board  string
		status int
		code   string
	}{{board, 403, "guest_not_allowed"}, {other, 404, "board_not_found"}} {
		r := delegatedSeat(t, tm, d, tc.board, tc.status)
		if errorCode(t, r) != tc.code {
			t.Fatalf("guest refusal: %v", r)
		}
	}
}

func TestTeamDelegationCannotReplaceARemovedSeat(t *testing.T) {
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	maya := tm.person("maya")
	d := delegationToken(t, tm, tm.key(maya))
	joined := delegatedSeat(t, tm, d, board, http.StatusCreated)
	status, body := tm.call("DELETE", "/v1/boards/"+board+"/people/maya", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("remove: %d %v", status, body)
	}
	before := delegationSnapshot(t, tm, board)
	refused := delegatedSeat(t, tm, d, board, http.StatusForbidden)
	if errorCode(t, refused) != "agent_removed" {
		t.Fatalf("removed seat refusal: %v", refused)
	}
	if after := delegationSnapshot(t, tm, board); before != after {
		t.Fatalf("refusal changed board: before %+v, after %+v", before, after)
	}
	assertRemovedSeatHasNoAccess(t, tm, board, joined["token"].(string))
	status, body = tm.call("POST", "/v1/boards/"+board+"/people", tm.key(tm.admin), map[string]any{"handle": "maya"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("re-add person: %d %v", status, body)
	}
	before = delegationSnapshot(t, tm, board)
	refused = delegatedSeat(t, tm, d, board, http.StatusForbidden)
	if errorCode(t, refused) != "agent_removed" {
		t.Fatalf("re-added person revived old seat: %v", refused)
	}
	if after := delegationSnapshot(t, tm, board); before != after {
		t.Fatalf("re-added person's refusal changed board: before %+v, after %+v", before, after)
	}
	assertRemovedSeatHasNoAccess(t, tm, board, joined["token"].(string))
}

func assertRemovedSeatHasNoAccess(t *testing.T, tm *team, board, token string) {
	t.Helper()
	for _, method := range []string{"GET", "POST"} {
		var input any
		if method == "POST" {
			input = map[string]any{"body": "removed seat must not post"}
		}
		status, body := tm.call(method, "/v1/boards/"+board+"/messages", token, input)
		if status != http.StatusForbidden || errorCode(t, body) != "agent_removed" {
			t.Fatalf("removed seat %s access: status %d, error %v", method, status, body["error"])
		}
	}
}

func TestTeamDelegationReplacementAndParentRevocationWriteNothing(t *testing.T) {
	for _, ending := range []string{"replacement", "parent revoked"} {
		t.Run(ending, func(t *testing.T) {
			tm := newTeam(t)
			board := tm.newBoard(tm.admin, "open")
			maya := tm.person("maya")
			status, key := tm.call("POST", "/v1/keys", tm.key(maya), map[string]any{"name": "delegation-parent"})
			if status != http.StatusCreated {
				t.Fatalf("create parent key: status %d, error %v", status, key["error"])
			}
			d := delegationToken(t, tm, key["token"].(string))
			if ending == "replacement" {
				delegationToken(t, tm, key["token"].(string))
			} else {
				status, body := tm.call("DELETE", "/v1/keys/"+key["id"].(string), tm.key(maya), nil)
				if status != http.StatusOK {
					t.Fatalf("revoke parent: status %d, error %v", status, body["error"])
				}
			}
			before := delegationSnapshot(t, tm, board)
			status, list := tm.call("GET", "/v1/boards", d, nil)
			if status != http.StatusUnauthorized || errorCode(t, list) != "delegation_revoked" {
				t.Fatalf("ended delegation listed boards: status %d, error %v", status, list["error"])
			}
			refused := delegatedSeat(t, tm, d, board, http.StatusUnauthorized)
			if errorCode(t, refused) != "delegation_revoked" {
				t.Fatalf("ended delegation join: error %v", refused["error"])
			}
			if after := delegationSnapshot(t, tm, board); before != after {
				t.Fatalf("refusal changed board: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestTeamDelegationDiscoveryDoesNotAuthorizeAJoinAfterAccessLoss(t *testing.T) {
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	maya := tm.person("maya")
	status, added := tm.call("POST", "/v1/boards/"+board+"/people", tm.key(tm.admin), map[string]any{"handle": "maya"})
	if status != http.StatusCreated {
		t.Fatalf("add person: status %d, error %v", status, added["error"])
	}
	d := delegationToken(t, tm, tm.key(maya))
	status, list := tm.call("GET", "/v1/boards", d, nil)
	if status != http.StatusOK {
		t.Fatalf("discover board: status %d, error %v", status, list["error"])
	}
	found := false
	for _, raw := range list["boards"].([]any) {
		if raw.(map[string]any)["name"] == board {
			found = true
		}
	}
	if !found {
		t.Fatalf("private membership missing from discovery: board %s", board)
	}
	status, removed := tm.call("DELETE", "/v1/boards/"+board+"/people/maya", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("remove person: status %d, error %v", status, removed["error"])
	}
	before := delegationSnapshot(t, tm, board)
	refused := delegatedSeat(t, tm, d, board, http.StatusNotFound)
	if errorCode(t, refused) != "board_not_found" {
		t.Fatalf("lost access join: error %v", refused["error"])
	}
	if after := delegationSnapshot(t, tm, board); before != after {
		t.Fatalf("refusal changed board: before %+v, after %+v", before, after)
	}
}
