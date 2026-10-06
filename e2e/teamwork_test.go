//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workCall(t *testing.T, tm *team, method, path, token, idem string, body any) (int, map[string]any, http.Header) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, tm.url()+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s %s: status %d, non-JSON response", method, path, resp.StatusCode)
	}
	return resp.StatusCode, out, resp.Header
}

func workStatus(t *testing.T, got, want int, out map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("status %d, want %d; error %v", got, want, out["error"])
	}
}

func workCreate(t *testing.T, tm *team, d, name, visibility string) map[string]any {
	t.Helper()
	status, out, headers := workCall(t, tm, "POST", "/v1/delegations/boards", d, name, map[string]any{
		"session": "codex:" + name, "harness": "codex", "template": "general", "name": name, "visibility": visibility,
	})
	workStatus(t, status, http.StatusCreated, out)
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("token-bearing creation response is not no-store")
	}
	return out
}

func workRefusal(t *testing.T, tm *team, board, token, handle string, status int, code string) {
	t.Helper()
	before := delegationSnapshot(t, tm, board)
	got, out := tm.call("POST", "/v1/boards/"+board+"/people", token, map[string]any{"handle": handle})
	workStatus(t, got, status, out)
	if errorCode(t, out) != code {
		t.Fatalf("refusal code %s, want %s", errorCode(t, out), code)
	}
	if after := delegationSnapshot(t, tm, board); before != after {
		t.Fatalf("refused add changed board: before %+v after %+v", before, after)
	}
}

func workAssertOwnership(t *testing.T, tm *team, person *env, out map[string]any) {
	t.Helper()
	board := field(t, out, "board.name").(string)
	status, members := tm.call("GET", "/v1/boards/"+board+"/members", tm.key(person), nil)
	workStatus(t, status, http.StatusOK, members)
	rows := members["members"].([]any)
	if len(rows) != 2 {
		t.Fatalf("atomic creation has %d members, want person and agent only", len(rows))
	}
	personID := tm.me(tm.key(person))["id"]
	var humanID any
	for _, raw := range rows {
		m := raw.(map[string]any)
		if m["kind"] == "human" {
			humanID = m["id"]
		}
		if m["kind"] == "agent" && (m["id"] != field(t, out, "agent.id") || m["owner_id"] != personID || m["role"] != "member") {
			t.Fatal("creation did not give the person's ordinary agent its own seat")
		}
	}
	if humanID == nil || field(t, out, "board.created_by.id") != humanID {
		t.Fatal("the person is not the recorded creator")
	}
	status, people := tm.call("GET", "/v1/boards/"+board+"/people", tm.key(person), nil)
	workStatus(t, status, http.StatusOK, people)
	rows = people["people"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["board_role"] != "owner" {
		t.Fatal("creation did not make the person the sole first owner")
	}
}

func TestTeamWorkDelegatedCreationIsAtomicAndReplaysOnlyTheCurrentSeat(t *testing.T) {
	tm := newTeam(t)
	d := delegationToken(t, tm, tm.key(tm.admin))
	out := workCreate(t, tm, d, "work-created", "open")
	workAssertOwnership(t, tm, tm.admin, out)
	status, events := tm.call("GET", "/v1/boards/work-created/events", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusOK, events)
	rows := events["events"].([]any)
	if len(rows) != 3 || rows[0].(map[string]any)["type"] != "board.created" || rows[1].(map[string]any)["type"] != "member.joined" || rows[2].(map[string]any)["type"] != "member.joined" {
		t.Fatal("atomic creation did not record the board, person and agent in order")
	}
	if field(t, rows[0].(map[string]any), "actor.member_id") != field(t, out, "board.created_by.id") || field(t, rows[0].(map[string]any), "data.agent_id") != field(t, out, "agent.id") {
		t.Fatal("creation record does not name the person and its agent seat")
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(out["token"].(string))) || bytes.Contains(raw, []byte(d)) || bytes.Contains(raw, []byte("codex:work-created")) {
		t.Fatal("creation recorded a token or session identifier")
	}
	body := map[string]any{"session": "codex:work-created", "harness": "codex", "template": "general", "name": "work-created", "visibility": "open"}
	before := delegationSnapshot(t, tm, "work-created")
	status, replay, headers := workCall(t, tm, "POST", "/v1/delegations/boards", d, "work-created", body)
	workStatus(t, status, http.StatusCreated, replay)
	if headers.Get("Idempotent-Replayed") != "true" || replay["token"] != out["token"] || field(t, replay, "agent.id") != field(t, out, "agent.id") {
		t.Fatal("retry did not return the original seat credential")
	}
	if after := delegationSnapshot(t, tm, "work-created"); after != before {
		t.Fatal("retry appended events or members")
	}
	body["title"] = "different request"
	status, replay, _ = workCall(t, tm, "POST", "/v1/delegations/boards", d, "work-created", body)
	workStatus(t, status, http.StatusUnprocessableEntity, replay)
	if errorCode(t, replay) != "idempotency_conflict" {
		t.Fatal("changed retry body was not rejected")
	}
	delete(body, "title")
	status, rotated := tm.call("POST", "/v1/join", d, map[string]any{"board": "work-created", "session": "codex:work-created", "harness": "codex"})
	workStatus(t, status, http.StatusOK, rotated)
	status, replay, _ = workCall(t, tm, "POST", "/v1/delegations/boards", d, "work-created", body)
	workStatus(t, status, http.StatusConflict, replay)
	if errorCode(t, replay) != "seat_token_replaced" {
		t.Fatal("replay returned a superseded seat credential")
	}
}

func TestTeamWorkSessionCreatesBoardsWithoutReplacingItsEarlierSeat(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	tm.person("sam")
	board := tm.newBoard(tm.admin, "open")
	tm.link(maya, board)
	s := maya.claudeSession("work-session")
	first := s.run("join", "--board", board, "--server", tm.url(), "--json").json(t)
	firstID := field(t, first, "agent.id")
	for _, args := range [][]string{
		{"pair", "--new", "general", "--title", "Agent work", "--json"},
		{"board", "new", "work-second", "--title", "More agent work", "--json"},
	} {
		r := s.runExit(args...)
		if r.code != 0 {
			t.Fatalf("session creation %s: exit %d, error %v", args[0], r.code, r.json(t)["error"])
		}
		if strings.Contains(r.stdout+r.stderr, tm.key(maya)) || strings.Contains(r.stdout+r.stderr, tm.key(tm.admin)) {
			t.Fatal("creation exposed a person's key")
		}
		created := r.json(t)
		workAssertOwnership(t, tm, maya, created)
	}
	status := s.run("status", "--json").json(t)
	seats := status["seats"].([]any)
	if len(seats) != 3 {
		t.Fatalf("session has %d seats, want original plus two new boards", len(seats))
	}
	found := false
	for _, raw := range seats {
		if raw.(map[string]any)["member_id"] == firstID {
			found = true
		}
	}
	if !found {
		t.Fatal("creating another board replaced the earlier seat")
	}
	loginFile := filepath.Join(maya.configDir(), "servers.json")
	raw, err := os.ReadFile(loginFile)
	if err != nil {
		t.Fatal(err)
	}
	personKey := tm.key(maya)
	if err := os.WriteFile(loginFile, bytes.ReplaceAll(raw, []byte(personKey), []byte("invalid-person-key")), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.runExit("board", "add", "@sam", "--board", "work-second", "--json")
	if r.code != 0 {
		t.Fatalf("selected agent add with no working stored human login: exit %d, error %v", r.code, r.json(t)["error"])
	}
	if field(t, r.json(t), "person.board_role") != "member" || strings.Contains(r.stdout+r.stderr, personKey) {
		t.Fatal("selected agent add used or exposed human authority")
	}
	if err := os.WriteFile(loginFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range credentialsOf(t, maya) {
		if c["token"] == tm.key(maya) || c["token"] == tm.key(tm.admin) {
			t.Fatal("a person's key was saved as an agent credential")
		}
	}
}

func TestTeamWorkGuestsCannotCreateBoardsOrAddPeople(t *testing.T) {
	tm := newTeam(t)
	tm.person("sam")
	board := tm.newBoard(tm.admin, "private")
	status, code := tm.call("POST", "/v1/boards/"+board+"/join-codes", tm.key(tm.admin), map[string]any{"role": "member", "guest": "guest"})
	workStatus(t, status, http.StatusCreated, code)
	status, guest := tm.call("POST", "/v1/guest-join", "", map[string]any{"code": code["code"], "key_name": "guest-machine"})
	workStatus(t, status, http.StatusCreated, guest)
	key := field(t, guest, "key.token").(string)
	d := delegationToken(t, tm, key)
	status, out, _ := workCall(t, tm, "POST", "/v1/delegations/boards", d, "guest-create", map[string]any{"session": "codex:guest-create", "harness": "codex", "name": "guest-created"})
	workStatus(t, status, http.StatusForbidden, out)
	if errorCode(t, out) != "guest_not_allowed" {
		t.Fatalf("guest creation refusal code %s, want guest_not_allowed", errorCode(t, out))
	}
	workRefusal(t, tm, board, guest["token"].(string), "sam", 403, "guest_not_allowed")
	status, out = tm.call("GET", "/v1/boards/guest-created", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusNotFound, out)
}

func TestTeamWorkPersonOptsPrivateBoardIntoAgentAdditionsFromTheCLI(t *testing.T) {
	tm := newTeam(t)
	tm.person("sam")
	tm.person("kim")
	s := tm.admin.claudeSession("work-private-cli")
	r := s.runExit("board", "new", "work-private-cli", "--private", "--json")
	if r.code != 0 {
		t.Fatalf("private session creation: exit %d, error %v", r.code, r.json(t)["error"])
	}
	created := r.json(t)
	if field(t, created, "board.visibility") != "private" || field(t, created, "board.agents_add_people") != false {
		t.Fatal("private creation enabled agent additions")
	}
	refused := s.runExit("board", "add", "@sam", "--board", "work-private-cli", "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "add_people_not_allowed" {
		t.Fatal("agent added a person before its owner opted in")
	}
	tm.admin.run("board", "agents-add-people", "on", "--board", "work-private-cli", "--yes", "--json")
	added := s.run("board", "add", "@sam", "--board", "work-private-cli", "--json").json(t)
	if field(t, added, "person.board_role") != "member" {
		t.Fatal("agent's CLI addition raised the person's board role")
	}
	tm.admin.run("board", "agents-add-people", "off", "--board", "work-private-cli", "--json")
	refused = s.runExit("board", "add", "@kim", "--board", "work-private-cli", "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "add_people_not_allowed" {
		t.Fatal("agent added a person after its owner turned additions off")
	}
}

func TestTeamWorkAgentAddsPeopleOnlyThroughServerAndBoardGates(t *testing.T) {
	tm := newTeam(t)
	for _, name := range []string{"sam", "kim", "pat", "lee"} {
		tm.person(name)
	}
	d := delegationToken(t, tm, tm.key(tm.admin))
	open := workCreate(t, tm, d, "work-open", "open")
	token := open["token"].(string)
	status, added := tm.call("POST", "/v1/boards/work-open/people", token, map[string]any{"handle": "sam"})
	workStatus(t, status, http.StatusCreated, added)
	if added["board_role"] != "member" {
		t.Fatal("agent gave its teammate owner powers")
	}
	status, events := tm.call("GET", "/v1/boards/work-open/events", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusOK, events)
	rows := events["events"].([]any)
	last := rows[len(rows)-1].(map[string]any)
	if last["type"] != "person.added" || field(t, last, "actor.member_id") != field(t, open, "agent.id") || field(t, last, "data.by_owner") != tm.me(tm.key(tm.admin))["id"] {
		t.Fatal("agent addition lost its actor or the person it acts for")
	}
	status, out := tm.call("PATCH", "/v1/settings", tm.key(tm.admin), map[string]any{"agents_add_people": false})
	workStatus(t, status, http.StatusOK, out)
	workRefusal(t, tm, "work-open", token, "kim", 403, "add_people_not_allowed")
	status, out = tm.call("PATCH", "/v1/settings", tm.key(tm.admin), map[string]any{"agents_add_people": true})
	workStatus(t, status, http.StatusOK, out)
	private := workCreate(t, tm, d, "work-private", "private")
	privateToken := private["token"].(string)
	workRefusal(t, tm, "work-private", privateToken, "kim", 403, "add_people_not_allowed")
	status, out = tm.call("PATCH", "/v1/boards/work-private", privateToken, map[string]any{"agents_add_people": true})
	workStatus(t, status, http.StatusForbidden, out)
	status, out = tm.call("PATCH", "/v1/boards/work-private", tm.key(tm.admin), map[string]any{"agents_add_people": true})
	workStatus(t, status, http.StatusOK, out)
	status, out = tm.call("POST", "/v1/boards/work-private/people", privateToken, map[string]any{"handle": "kim"})
	workStatus(t, status, http.StatusCreated, out)
	status, out = tm.call("POST", "/v1/boards/work-open/visibility", tm.key(tm.admin), map[string]any{"visibility": "private"})
	workStatus(t, status, http.StatusOK, out)
	status, out = tm.call("GET", "/v1/boards/work-open", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusOK, out)
	if out["agents_add_people"] != false {
		t.Fatal("turning private did not reset agent additions")
	}
	workRefusal(t, tm, "work-open", token, "pat", 403, "add_people_not_allowed")
	status, out = tm.call("POST", "/v1/boards/work-open/visibility", tm.key(tm.admin), map[string]any{"visibility": "open"})
	workStatus(t, status, http.StatusOK, out)
	workRefusal(t, tm, "work-open", token, "pat", 403, "add_people_not_allowed")
	workRefusal(t, tm, "work-private", token, "lee", 404, "board_not_found")
}

func TestTeamWorkManualSeatsAndLostOwnerAuthorityCannotAddPeople(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	tm.person("sam")
	board := tm.newBoard(maya, "open")
	status, out := tm.call("POST", "/v1/boards/"+board+"/people", tm.key(maya), map[string]any{"handle": "alex"})
	workStatus(t, status, http.StatusCreated, out)
	manual := tm.agentToken(maya, board)
	workRefusal(t, tm, board, manual, "sam", 403, "agent_session_required")
	d := delegationToken(t, tm, tm.key(maya))
	joined := delegatedSeat(t, tm, d, board, http.StatusCreated)
	status, out = tm.call("POST", "/v1/boards/"+board+"/owners", tm.key(maya), map[string]any{"handle": "alex"})
	workStatus(t, status, http.StatusOK, out)
	status, out = tm.call("DELETE", "/v1/boards/"+board+"/people/maya", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusOK, out)
	workRefusal(t, tm, board, joined["token"].(string), "sam", 403, "agent_removed")
}

func TestTeamWorkCreationRechecksParentAndServerCreationPolicy(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	d := delegationToken(t, tm, tm.key(maya))
	status, out := tm.call("PATCH", "/v1/settings", tm.key(tm.admin), map[string]any{"board_creation": "admins"})
	workStatus(t, status, http.StatusOK, out)
	body := map[string]any{"session": "codex:work-policy", "harness": "codex", "name": "work-policy"}
	status, out, _ = workCall(t, tm, "POST", "/v1/delegations/boards", d, "work-policy", body)
	workStatus(t, status, http.StatusForbidden, out)
	if errorCode(t, out) != "board_creation_restricted" {
		t.Fatalf("creation policy refusal code %s, want board_creation_restricted", errorCode(t, out))
	}
	status, out = tm.call("GET", "/v1/boards/work-policy", tm.key(tm.admin), nil)
	workStatus(t, status, http.StatusNotFound, out)
	status, out = tm.call("PATCH", "/v1/settings", tm.key(tm.admin), map[string]any{"board_creation": "members"})
	workStatus(t, status, http.StatusOK, out)
	created := workCreate(t, tm, d, "work-before-revoke", "open")
	status, keys := tm.call("GET", "/v1/keys", tm.key(maya), nil)
	workStatus(t, status, http.StatusOK, keys)
	status, out = tm.call("DELETE", "/v1/keys/"+keys["current_key_id"].(string), tm.key(maya), nil)
	workStatus(t, status, http.StatusOK, out)
	body = map[string]any{"session": "codex:work-before-revoke", "harness": "codex", "template": "general", "name": "work-before-revoke", "visibility": "open"}
	status, out, _ = workCall(t, tm, "POST", "/v1/delegations/boards", d, "work-before-revoke", body)
	workStatus(t, status, http.StatusUnauthorized, out)
	if errorCode(t, out) != "delegation_revoked" {
		t.Fatal("revoked parent still replays delegated creation")
	}
	status, out = tm.call("POST", "/v1/boards/work-before-revoke/people", created["token"].(string), map[string]any{"handle": "alex"})
	workStatus(t, status, http.StatusUnauthorized, out)
}
