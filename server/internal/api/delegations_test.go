package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// answer is a finished request: its status, headers and decoded body.
type answer struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (a answer) code() string {
	e, _ := a.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (a answer) str(path ...string) string {
	v, _ := jsonAt(a.body, path...).(string)
	return v
}

// call makes a request with token, and an Idempotency-Key when key isn't empty, through
// the conformance checker.
func (s *testServer) call(method, path, token string, body any, key string) answer {
	s.t.Helper()
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.url+path, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	out := answer{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (s *testServer) want(a answer, status int, code string) {
	s.t.Helper()
	if a.status != status || a.code() != code {
		s.t.Fatalf("got %d %s, want %d %s", a.status, a.raw, status, code)
	}
}

// delegation makes a machine's delegation called name with a person's key.
func (s *testServer) delegation(key, name string) answer {
	s.t.Helper()
	a := s.call("POST", "/v1/delegations", key, map[string]any{"name": name}, "")
	if a.status != 201 {
		s.t.Fatalf("make delegation %s: %d %s", name, a.status, a.raw)
	}
	return a
}

// delegate makes a delegation and returns its token.
func (s *testServer) delegate(key string) string {
	s.t.Helper()
	return s.delegation(key, "laptop").str("token")
}

// joinSession joins session to board through a delegation.
func (s *testServer) joinSession(dlg, board, session string, extra map[string]any) answer {
	s.t.Helper()
	body := map[string]any{"board": board, "session": session}
	for k, v := range extra {
		body[k] = v
	}
	return s.call("POST", "/v1/join", dlg, body, "")
}

// openBoard has token create an open board from the general template, and returns its
// name.
func (s *testServer) openBoard(token string) string {
	s.t.Helper()
	a := s.call("POST", "/v1/boards", token, map[string]any{"template": "general"}, "")
	if a.status != 201 {
		s.t.Fatalf("create board: %d %s", a.status, a.raw)
	}
	return a.str("name")
}

// eventTypes returns the types of a board's events after seq, as token reads them.
func (s *testServer) eventsAfter(token, board string, after int) []map[string]any {
	s.t.Helper()
	a := s.call("GET", fmt.Sprintf("/v1/boards/%s/events?after=%d", board, after), token, nil, "")
	if a.status != 200 {
		s.t.Fatalf("events of %s: %d %s", board, a.status, a.raw)
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(a.raw), &out); err != nil {
		s.t.Fatal(err)
	}
	return out.Events
}

func (s *testServer) head(token, board string) int {
	s.t.Helper()
	a := s.call("GET", "/v1/boards/"+board, token, nil, "")
	if a.status != 200 {
		s.t.Fatalf("board %s: %d %s", board, a.status, a.raw)
	}
	seq, _ := a.body["head_seq"].(float64)
	return int(seq)
}

func types(evs []map[string]any) []string {
	var out []string
	for _, e := range evs {
		typ, _ := e["type"].(string)
		out = append(out, typ)
	}
	return out
}

// newJoinServer is a test server whose join limit the tests don't reach.
func newJoinServer(t *testing.T) *testServer {
	return newTestServer(t, func(o *api.Options) { o.JoinsPerMinute = 1000 })
}

func noStore(t *testing.T, a answer) {
	t.Helper()
	if got := a.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q on %d %s, want no-store", got, a.status, a.raw)
	}
}

// A delegation is made only with a person's own key, answers without being cached, and
// its token starts abd_.
func TestADelegationIsMadeOnlyWithAPersonsKey(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	a := s.delegation(maya, "maya-laptop")
	noStore(t, a)
	if !strings.HasPrefix(a.str("token"), "abd_") || !strings.HasPrefix(a.str("id"), "dlg_") || a.str("name") != "maya-laptop" {
		t.Fatalf("new delegation: %s", a.raw)
	}
	_, agent, _ := s.pair("starter")
	s.want(s.call("POST", "/v1/delegations", agent, map[string]any{"name": "x"}, ""), 403, "human_token_required")
	s.want(s.call("POST", "/v1/delegations", a.str("token"), map[string]any{"name": "x"}, ""), 403, "human_token_required")
	s.want(s.call("POST", "/v1/delegations", s.browserToken(maya), map[string]any{"name": "x"}, ""), 403, "human_token_required")
	// The key lists how many working delegations it has.
	if k := keyNamed(t, s.keys(maya, ""), "laptop"); k.Delegations == nil || *k.Delegations != 1 {
		t.Fatalf("maya's key's delegations: %+v", k)
	}
}

// A delegation does exactly two things; everything else is forbidden, whatever the
// person behind it may do.
func TestADelegationOnlyListsBoardsAndJoins(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	board, agent, _ := s.pair("starter")
	dlg := s.delegate(s.owner)
	msg := say(s, agent, board, nil, "hi")
	mustStatus(t, msg, nil, 201)
	requests := []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/me", nil},
		{"GET", "/v1/boards/" + board, nil},
		{"GET", "/v1/boards/" + board + "/messages", nil},
		{"GET", "/v1/boards/" + board + "/members", nil},
		{"GET", "/v1/boards/" + board + "/people", nil},
		{"GET", "/v1/boards/" + board + "/events", nil},
		{"POST", "/v1/boards/" + board + "/messages", map[string]any{"body": "hi"}},
		{"POST", "/v1/boards", map[string]any{"template": "general"}},
		{"POST", "/v1/boards/" + board + "/join-codes", map[string]any{"role": "writer"}},
		{"POST", "/v1/boards/" + board + "/people", map[string]any{"handle": "alex"}},
		{"GET", "/v1/keys", nil},
		{"POST", "/v1/keys", map[string]any{"name": "more"}},
		{"GET", "/v1/people", nil},
		{"POST", "/v1/invites", map[string]any{}},
		{"GET", "/v1/me/inbox", nil},
		{"GET", "/v1/stream", nil},
		{"PUT", "/v1/messages/" + msg.JSON201.Id + "/reactions/eyes", nil},
	}
	for _, r := range requests {
		a := s.call(r.method, r.path, dlg, r.body, "")
		if a.status != 403 || a.code() != "forbidden" {
			t.Errorf("%s %s with a delegation: %d %s, want 403 forbidden", r.method, r.path, a.status, a.raw)
		}
	}
	if a := s.call("GET", "/v1/boards", dlg, nil, ""); a.status != 200 {
		t.Errorf("list boards: %d %s", a.status, a.raw)
	}
}

// A delegation ends with its key, revoked or expired, with its person's removal from the
// server, and when the same key makes another with the same name.
func TestADelegationEndsWithItsKeyItsPersonAndItsReplacement(t *testing.T) {
	t.Parallel()
	t.Run("key revoked", func(t *testing.T) {
		s := newTestServer(t)
		maya := s.addHuman("maya")
		id, key := s.newKey(maya, "spare")
		dlg := s.delegate(key)
		mustStatus(t, s.revokeKey(maya, id), nil, 200)
		s.want(s.call("GET", "/v1/boards", dlg, nil, ""), 401, "delegation_revoked")
		s.want(s.joinSession(dlg, "anything", "claude-code:1", nil), 401, "delegation_revoked")
	})
	t.Run("key expired", func(t *testing.T) {
		s := newTestServer(t)
		maya := s.addHuman("maya")
		r := s.createKey(maya, "short", 3600)
		mustStatus(t, r, nil, 201)
		dlg := s.delegate(r.JSON201.Token)
		s.clock.Advance(time.Hour + time.Second)
		s.want(s.call("GET", "/v1/boards", dlg, nil, ""), 401, "delegation_revoked")
	})
	t.Run("person removed", func(t *testing.T) {
		s := newTestServer(t)
		maya := s.addHuman("maya")
		dlg := s.delegate(maya)
		if a := s.call("DELETE", "/v1/people/maya", s.owner, nil, ""); a.status != 200 {
			t.Fatalf("remove maya: %d %s", a.status, a.raw)
		}
		s.want(s.call("GET", "/v1/boards", dlg, nil, ""), 401, "delegation_revoked")
	})
	t.Run("replaced by the same key and name", func(t *testing.T) {
		s := newTestServer(t)
		maya := s.addHuman("maya")
		first := s.delegation(maya, "laptop").str("token")
		other := s.delegation(maya, "desktop").str("token")
		second := s.delegation(maya, "laptop").str("token")
		s.want(s.call("GET", "/v1/boards", first, nil, ""), 401, "delegation_revoked")
		for _, dlg := range []string{other, second} {
			if a := s.call("GET", "/v1/boards", dlg, nil, ""); a.status != 200 {
				t.Errorf("a working delegation: %d %s", a.status, a.raw)
			}
		}
		// An Idempotency-Key is ignored: a repeat makes another delegation.
		a := s.call("POST", "/v1/delegations", maya, map[string]any{"name": "laptop"}, "same-key")
		b := s.call("POST", "/v1/delegations", maya, map[string]any{"name": "laptop"}, "same-key")
		if a.status != 201 || b.status != 201 || a.str("token") == b.str("token") || b.header.Get("Idempotent-Replayed") != "" {
			t.Fatalf("a repeated key: %d %s / %d %s", a.status, a.raw, b.status, b.raw)
		}
		s.want(s.call("GET", "/v1/boards", a.str("token"), nil, ""), 401, "delegation_revoked")
	})
	t.Run("an unknown token", func(t *testing.T) {
		s := newTestServer(t)
		s.want(s.call("GET", "/v1/boards", "abd_"+strings.Repeat("x", 40), nil, ""), 401, "unauthorized")
	})
}

// guestOn brings guest onto board with a guest code token makes, and returns the
// guest's new key.
func (s *testServer) guestOn(token, board, guest string) string {
	s.t.Helper()
	gc := s.call("POST", "/v1/boards/"+board+"/join-codes", token, map[string]any{"role": "member", "guest": guest}, "")
	if gc.status != 201 {
		s.t.Fatalf("guest code: %d %s", gc.status, gc.raw)
	}
	g := s.guestJoin(gc.str("code"))
	mustStatus(s.t, g, nil, 201)
	return g.JSON201.Key.Token
}

func boardNames(a answer) map[string]map[string]any {
	var list struct {
		Boards []map[string]any `json:"boards"`
	}
	_ = json.Unmarshal([]byte(a.raw), &list)
	out := map[string]map[string]any{}
	for _, b := range list.Boards {
		name, _ := b["name"].(string)
		out[name] = b
	}
	return out
}

// A delegation lists every board its person can see, with counts, and nothing that is
// the person's own or hidden from them: an admin's lists no hidden boards, and a
// guest's lists only the guest's boards.
func TestADelegationListsTheBoardsItsPersonCanSee(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	open := s.openBoard(maya)
	mine := s.privateBoard(maya)
	hidden := s.privateBoard(sam)
	s.joinSession(s.delegate(maya), mine, "claude-code:1", nil)

	for who, key := range map[string]string{"maya": maya, "the admin": s.owner} {
		a := s.call("GET", "/v1/boards?all=true", s.delegate(key), nil, "")
		if a.status != 200 {
			t.Fatalf("%s: %d %s", who, a.status, a.raw)
		}
		if _, ok := a.body["hidden_boards"]; ok {
			t.Errorf("%s's delegation got hidden boards: %s", who, a.raw)
		}
		boards := boardNames(a)
		if boards[hidden] != nil {
			t.Errorf("%s's delegation lists %s, which they can't see", who, hidden)
		}
		for _, b := range boards {
			for _, own := range []string{"read_up_to", "unread", "needs_reply"} {
				if _, ok := b[own]; ok {
					t.Errorf("%s's delegation got %s on %s", who, own, b["name"])
				}
			}
		}
		if boards[open] == nil {
			t.Fatalf("%s's delegation misses the open board: %s", who, a.raw)
		}
	}
	boards := boardNames(s.call("GET", "/v1/boards", s.delegate(maya), nil, ""))
	if b := boards[mine]; b == nil || b["on_board"] != true || b["people_count"] != 1.0 || b["agent_count"] != 1.0 {
		t.Errorf("maya's private board: %v", b)
	}
	alexs := boardNames(s.call("GET", "/v1/boards", s.delegate(s.owner), nil, ""))
	if b := alexs[open]; b == nil || b["on_board"] != false || b["people_count"] != 1.0 || b["agent_count"] != nil {
		t.Errorf("an open board alex isn't on: %v", b)
	}

	// A guest's delegation lists only the guest's own board.
	guests := boardNames(s.call("GET", "/v1/boards", s.delegate(s.guestOn(maya, open, "kim")), nil, ""))
	if len(guests) != 1 || guests[open] == nil {
		t.Errorf("the guest's delegation lists %v", guests)
	}
}

// A delegated join gives the session a seat recorded as made through the delegation,
// with the person as actor; a repeat from the same session reuses that seat with a new
// token, stops the earlier one and records nothing.
func TestADelegatedJoinMakesASeatAndReusesIt(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya := s.addHuman("maya")
	board := s.openBoard(maya)
	d := s.delegation(maya, "maya-laptop")
	dlg := d.str("token")
	before := s.head(maya, board)

	first := s.joinSession(dlg, board, "claude-code:5f1c", map[string]any{"harness": "claude-code"})
	if first.status != 201 || first.body["reused"] != nil {
		t.Fatalf("first join: %d %s", first.status, first.raw)
	}
	noStore(t, first)
	evs := s.eventsAfter(maya, board, before)
	if len(evs) != 1 || evs[0]["type"] != "member.joined" {
		t.Fatalf("events: %v", types(evs))
	}
	data, _ := evs[0]["data"].(map[string]any)
	actor, _ := evs[0]["actor"].(map[string]any)
	if data["via"] != "delegation" || data["delegation_id"] != d.str("id") || data["join_code_id"] != nil || actor["name"] != "maya" || data["role"] != "member" {
		t.Errorf("member.joined: %v by %v", data, actor)
	}
	if strings.Contains(fmt.Sprint(evs), "5f1c") {
		t.Errorf("the session string reached the record: %v", evs)
	}
	agent := first.str("agent", "id")
	oldToken := first.str("token")
	s.works(oldToken, true)

	head := s.head(maya, board)
	again := s.joinSession(dlg, board, "claude-code:5f1c", nil)
	if again.status != 200 || again.body["reused"] != true || again.str("agent", "id") != agent || again.str("token") == oldToken {
		t.Fatalf("second join: %d %s", again.status, again.raw)
	}
	noStore(t, again)
	s.works(oldToken, false)
	s.works(again.str("token"), true)
	if s.head(maya, board) != head {
		t.Errorf("a reuse recorded an event")
	}

	// The new token's parent is the key behind the delegation: revoking it ends the seat.
	keys := s.keys(maya, "")
	mustStatus(t, s.revokeKey(maya, keyNamed(t, keys, "laptop").Id), nil, 200)
	s.works(again.str("token"), false)
}

// The seat lookup is keyed by the person: two people whose harnesses give the same
// session string each get their own seat.
func TestTwoPeopleWithTheSameSessionStringNeverShareASeat(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	board := s.openBoard(maya)
	a := s.joinSession(s.delegate(maya), board, "codex:same", nil)
	b := s.joinSession(s.delegate(sam), board, "codex:same", nil)
	if a.status != 201 || b.status != 201 || a.str("agent", "id") == b.str("agent", "id") {
		t.Fatalf("maya %d %s, sam %d %s", a.status, a.raw, b.status, b.raw)
	}
	s.works(a.str("token"), true)
	again := s.joinSession(s.delegate(sam), board, "codex:same", nil)
	if again.str("agent", "id") != b.str("agent", "id") {
		t.Fatalf("sam's repeat found %s", again.raw)
	}
	s.works(a.str("token"), true)
}

// A delegated join refuses, writing nothing, a board the person can't see, a guest's
// own board, a code, and an incomplete request.
func TestADelegatedJoinRefusesWhatThePersonCantDo(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	open := s.openBoard(maya)
	private := s.privateBoard(sam)
	dlg := s.delegate(maya)
	before := s.head(sam, private)
	hidden := s.joinSession(dlg, private, "codex:1", nil)
	missing := s.joinSession(dlg, "no-such-board", "codex:1", nil)
	s.want(hidden, 404, "board_not_found")
	if strings.ReplaceAll(hidden.raw, private, "no-such-board") != missing.raw {
		t.Errorf("a hidden board answers unlike a missing one: %s / %s", hidden.raw, missing.raw)
	}
	if s.head(sam, private) != before {
		t.Errorf("a refused join wrote on the hidden board")
	}
	// An admin's delegation sees no hidden board either.
	s.want(s.joinSession(s.delegate(s.owner), private, "codex:1", nil), 404, "board_not_found")

	code := s.call("POST", "/v1/boards/"+open+"/join-codes", maya, map[string]any{"role": "member"}, "")
	s.want(s.call("POST", "/v1/join", dlg, map[string]any{"code": code.str("code"), "board": open, "session": "codex:1"}, ""), 403, "forbidden")
	s.want(s.call("POST", "/v1/join", dlg, map[string]any{"board": open}, ""), 422, "invalid_request")

	// A guest's agents come only from guest codes.
	guest := s.delegate(s.guestOn(maya, open, "kim"))
	s.want(s.joinSession(guest, open, "codex:1", nil), 403, "guest_not_allowed")
	other := s.openBoard(sam)
	s.want(s.joinSession(guest, other, "codex:1", nil), 404, "board_not_found")
}

// On an open board the person isn't on, a delegated join adds them first, as a member;
// a refusal after that undoes the addition too.
func TestADelegatedJoinAddsThePersonToAnOpenBoardOrNothing(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	board := s.openBoard(maya)
	dlg := s.delegate(sam)
	head := s.head(maya, board)

	for _, refusal := range []struct {
		extra       map[string]any
		status      int
		code, label string
	}{
		{map[string]any{"role": "nope"}, 422, "role_not_found", "an unknown role"},
		{map[string]any{"name": "maya"}, 409, "name_taken", "a taken name"},
	} {
		s.want(s.joinSession(dlg, board, "codex:1", refusal.extra), refusal.status, refusal.code)
		if s.head(maya, board) != head {
			t.Errorf("%s: the refused join left events: %v", refusal.label, types(s.eventsAfter(maya, board, head)))
		}
		people := s.call("GET", "/v1/boards/"+board+"/people", maya, nil, "")
		if strings.Contains(people.raw, "\"sam\"") {
			t.Errorf("%s: sam is on the board: %s", refusal.label, people.raw)
		}
	}

	ok := s.joinSession(dlg, board, "codex:1", nil)
	if ok.status != 201 {
		t.Fatalf("join: %d %s", ok.status, ok.raw)
	}
	evs := s.eventsAfter(maya, board, head)
	if got := types(evs); len(got) != 2 || got[0] != "person.added" || got[1] != "member.joined" {
		t.Fatalf("events: %v", got)
	}
	added, _ := evs[0]["data"].(map[string]any)
	if added["via"] != "delegation" || added["delegation_id"] == nil || added["access"] != "member" || jsonAt(evs[0], "actor", "name") != "sam" {
		t.Errorf("person.added: %v", evs[0])
	}
}

// A removed seat is never replaced by a delegated join: the session gets agent_removed
// with who removed it and when, and nothing is written.
func TestARemovedSeatIsNeverReplaced(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	board := s.openBoard(maya)
	dlg := s.delegate(sam)
	if a := s.joinSession(dlg, board, "codex:1", nil); a.status != 201 {
		t.Fatalf("join: %d %s", a.status, a.raw)
	}
	if a := s.call("DELETE", "/v1/boards/"+board+"/people/sam", maya, nil, ""); a.status != 200 {
		t.Fatalf("remove sam: %d %s", a.status, a.raw)
	}
	head := s.head(maya, board)
	a := s.joinSession(dlg, board, "codex:1", nil)
	s.want(a, 403, "agent_removed")
	details, _ := jsonAt(a.body, "error", "details").(map[string]any)
	if details["agent"] != "member" || details["removed_by"] != "board_owner" || details["removed_at"] == nil {
		t.Errorf("details: %s", a.raw)
	}
	if s.head(maya, board) != head {
		t.Errorf("the refused join wrote: %v", types(s.eventsAfter(maya, board, head)))
	}
	// Its person leaving removes it too, by the person.
	other := s.openBoard(maya)
	if a := s.joinSession(dlg, other, "codex:2", nil); a.status != 201 {
		t.Fatalf("join: %d %s", a.status, a.raw)
	}
	if a := s.call("POST", "/v1/boards/"+other+"/leave", sam, nil, ""); a.status != 200 {
		t.Fatalf("leave: %d %s", a.status, a.raw)
	}
	a = s.joinSession(dlg, other, "codex:2", nil)
	s.want(a, 403, "agent_removed")
	if jsonAt(a.body, "error", "details", "removed_by") != "person" {
		t.Errorf("details: %s", a.raw)
	}
}

// A delegated join's answer is never kept for an Idempotency-Key: a repeat is a new
// call, which finds the same seat with another new token.
func TestADelegatedJoinIsNeverReplayed(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya := s.addHuman("maya")
	board := s.openBoard(maya)
	dlg := s.delegate(maya)
	body := map[string]any{"board": board, "session": "codex:1"}
	a := s.call("POST", "/v1/join", dlg, body, "k1")
	b := s.call("POST", "/v1/join", dlg, body, "k1")
	if a.status != 201 || b.status != 200 || b.header.Get("Idempotent-Replayed") != "" || a.str("token") == b.str("token") ||
		a.str("agent", "id") != b.str("agent", "id") {
		t.Fatalf("repeat: %d %s / %d %s", a.status, a.raw, b.status, b.raw)
	}
	s.works(a.str("token"), false)
	s.works(b.str("token"), true)
}

// A join with a person's key keeps its replay, rechecked: it returns the stored token
// while it works, seat_token_replaced once a delegated join from the same session gave
// the seat a new one, and the error a new call would get once the person lost the board.
func TestAPersonsJoinReplayIsRechecked(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya := s.addHuman("maya")
	board := s.openBoard(maya)
	body := map[string]any{"board": board, "role": "member", "session": "claude-code:abc"}
	first := s.call("POST", "/v1/join", maya, body, "k1")
	if first.status != 201 {
		t.Fatalf("join: %d %s", first.status, first.raw)
	}
	noStore(t, first)
	again := s.call("POST", "/v1/join", maya, body, "k1")
	if again.status != 201 || again.header.Get("Idempotent-Replayed") != "true" || again.str("token") != first.str("token") {
		t.Fatalf("replay: %d %s", again.status, again.raw)
	}
	noStore(t, again)

	// The delegation finds the seat the person's key recorded for the session.
	reused := s.joinSession(s.delegate(maya), board, "claude-code:abc", nil)
	if reused.status != 200 || reused.str("agent", "id") != first.str("agent", "id") {
		t.Fatalf("delegated join: %d %s", reused.status, reused.raw)
	}
	s.want(s.call("POST", "/v1/join", maya, body, "k1"), 409, "seat_token_replaced")

	// An open board the person then leaves: it is still visible to them and their seat
	// was removed with them, so the replay says the seat was removed.
	other := s.openBoard(s.owner)
	if a := s.call("POST", "/v1/boards/"+other+"/people", maya, map[string]any{"handle": "maya"}, ""); a.status != 201 {
		t.Fatalf("join %s: %d %s", other, a.status, a.raw)
	}
	body2 := map[string]any{"board": other, "role": "member"}
	if a := s.call("POST", "/v1/join", maya, body2, "k2"); a.status != 201 {
		t.Fatalf("join: %d %s", a.status, a.raw)
	}
	if a := s.call("POST", "/v1/boards/"+other+"/leave", maya, nil, ""); a.status != 200 {
		t.Fatalf("leave: %d %s", a.status, a.raw)
	}
	removed := s.call("POST", "/v1/join", maya, body2, "k2")
	s.want(removed, 403, "agent_removed")
	if jsonAt(removed.body, "error", "details", "removed_by") != "person" {
		t.Errorf("details: %s", removed.raw)
	}
}

// A replay never says anything about a board the caller can no longer see: removed
// from a private board, the replay gets board_not_found, exactly as a new join would.
func TestAReplayOnAHiddenBoardSaysNothingAboutTheSeat(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya := s.addHuman("maya")
	private := s.privateBoard(s.owner)
	if a := s.call("POST", "/v1/boards/"+private+"/people", s.owner, map[string]any{"handle": "maya"}, ""); a.status != 201 {
		t.Fatalf("add maya: %d %s", a.status, a.raw)
	}
	body := map[string]any{"board": private, "role": "member"}
	if a := s.call("POST", "/v1/join", maya, body, "k1"); a.status != 201 {
		t.Fatalf("join: %d %s", a.status, a.raw)
	}
	if a := s.call("DELETE", "/v1/boards/"+private+"/people/maya", s.owner, nil, ""); a.status != 200 {
		t.Fatalf("remove maya: %d %s", a.status, a.raw)
	}
	replay := s.call("POST", "/v1/join", maya, body, "k1")
	fresh := s.call("POST", "/v1/join", maya, body, "k2")
	s.want(replay, 404, "board_not_found")
	if replay.raw != fresh.raw {
		t.Errorf("the replay differs from a new call: %s / %s", replay.raw, fresh.raw)
	}
}

// A guest code the first call used up still counts for its own repeat, as long as its
// maker's authority holds.
func TestAUsedGuestCodeStillCountsForItsOwnRepeat(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	first, second := s.openBoard(maya), s.openBoard(maya)
	if a := s.call("POST", "/v1/boards/"+second+"/people", maya, map[string]any{"handle": "sam"}, ""); a.status != 201 {
		t.Fatalf("add sam: %d %s", a.status, a.raw)
	}
	// kim is a guest already, with a key, who joins a second board with sam's guest code.
	kim := s.guestOn(maya, first, "kim")
	gc := s.call("POST", "/v1/boards/"+second+"/join-codes", sam, map[string]any{"role": "member", "guest": "kim"}, "")
	if gc.status != 201 {
		t.Fatalf("second guest code: %d %s", gc.status, gc.raw)
	}
	body := map[string]any{"code": gc.str("code")}
	a := s.call("POST", "/v1/join", kim, body, "k1")
	if a.status != 201 {
		t.Fatalf("redeem: %d %s", a.status, a.raw)
	}
	b := s.call("POST", "/v1/join", kim, body, "k1")
	if b.status != 201 || b.str("token") != a.str("token") || b.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("repeat with the used code: %d %s", b.status, b.raw)
	}
	// A new call with the used code is refused.
	s.want(s.call("POST", "/v1/join", kim, body, "k2"), 404, "join_code_invalid")
	// Once its maker leaves the board, the code's authority is gone for its repeat too.
	if r := s.call("POST", "/v1/boards/"+second+"/leave", sam, nil, ""); r.status != 200 {
		t.Fatalf("sam leaves: %d %s", r.status, r.raw)
	}
	s.want(s.call("POST", "/v1/join", kim, body, "k1"), 404, "join_code_invalid")
}

// Joins for one session at once make one seat: whatever order they commit in, exactly
// one is new, every answer names the same seat, and only the last token works.
func TestSimultaneousDelegatedJoinsMakeOneSeat(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	maya := s.addHuman("maya")
	board := s.openBoard(maya)
	dlg := s.delegate(maya)
	const n = 8
	answers := make([]answer, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = s.joinSession(dlg, board, "codex:race", nil)
		}()
	}
	wg.Wait()
	made, seats, working := 0, map[string]bool{}, 0
	for _, a := range answers {
		if a.status == 201 {
			made++
		} else if a.status != 200 {
			t.Fatalf("join: %d %s", a.status, a.raw)
		}
		seats[a.str("agent", "id")] = true
		if r := s.call("GET", "/v1/me", a.str("token"), nil, ""); r.status == 200 {
			working++
		}
	}
	if made != 1 || len(seats) != 1 || working != 1 {
		t.Fatalf("made %d seats %v, %d tokens work", made, seats, working)
	}
}

// The inbox and its acknowledgement name the seat by its member id.
func TestTheInboxNamesItsSeat(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	board, writer, _ := s.pair("starter")
	me := s.call("GET", "/v1/me", writer, nil, "")
	in := s.call("GET", "/v1/me/inbox", writer, nil, "")
	if in.str("member_id") != me.str("id") || !strings.HasPrefix(me.str("id"), "mem_") {
		t.Fatalf("inbox: %s; me: %s", in.raw, me.raw)
	}
	say(s, s.owner, board, nil, "hello")
	ack := s.call("POST", "/v1/me/inbox/ack", writer, map[string]any{"up_to": 1}, "")
	if ack.status != 200 || ack.str("member_id") != me.str("id") {
		t.Fatalf("ack: %d %s", ack.status, ack.raw)
	}
}
