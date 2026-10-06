//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func lifecycleRequest(t *testing.T, tm *team, method, path, token, idem string, input any, want int, code string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, tm.url()+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("%s %s: status %d, invalid JSON", method, path, resp.StatusCode)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d, error %v", method, path, resp.StatusCode, want, body["error"])
	}
	if code != "" && errorCode(t, body) != code {
		t.Fatalf("%s %s: error %v, want %s", method, path, body["error"], code)
	}
	return body
}

func lifecycleReceipt(t *testing.T, tm *team, target, op, token, idem, state string, changed bool) map[string]any {
	t.Helper()
	b := lifecycleRequest(t, tm, "POST", "/v1/boards/"+target+"/"+op, token, idem, nil, 200, "")
	id, validID := b["id"].(string)
	if len(b) != 3 || !validID || !strings.HasPrefix(id, "brd_") || b["lifecycle"] != state || b["changed"] != changed {
		t.Fatalf("%s: receipt fields %v", op, b)
	}
	return b
}

func lifecycleRead(t *testing.T, tm *team, board, token string) map[string]any {
	t.Helper()
	return lifecycleRequest(t, tm, "GET", "/v1/boards/"+board, token, "", nil, 200, "")
}

func TestPublicBoardLifecycleFreezeAndRestore(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	b := tm.newBoard(tm.admin, "open")
	key := tm.key(tm.admin)
	tm.link(tm.admin, b)
	tm.admin.run("board", "add", "@maya")
	agent := tm.agentToken(maya, b)
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/messages", agent, "", map[string]any{"body": "before archive"}, 201, "")
	code := lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/join-codes", key, "", map[string]any{"role": "member"}, 201, "")["code"]
	lifecycleReceipt(t, tm, b, "archive", key, "", "archived", true)
	head := lifecycleRead(t, tm, b, key)["head_seq"]
	lifecycleReceipt(t, tm, b, "archive", key, "", "archived", false)
	lifecycleRequest(t, tm, "GET", "/v1/boards/"+b+"/messages", agent, "", nil, 200, "")
	for _, tc := range []struct {
		path  string
		input any
	}{
		{"/v1/boards/" + b + "/messages", map[string]any{"body": "must not append"}},
		{"/v1/boards/" + b + "/join-codes", map[string]any{"role": "member"}},
		{"/v1/join", map[string]any{"code": code}},
	} {
		lifecycleRequest(t, tm, "POST", tc.path, key, "", tc.input, 409, "board_archived")
	}
	if got := lifecycleRead(t, tm, b, key)["head_seq"]; got != head {
		t.Fatalf("archive refusals changed head: %v to %v", head, got)
	}
	lifecycleReceipt(t, tm, b, "restore", key, "", "active", true)
	lifecycleReceipt(t, tm, b, "restore", key, "", "active", false)
	lifecycleRequest(t, tm, "POST", "/v1/join", key, "", map[string]any{"code": code}, 201, "")
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/messages", agent, "", map[string]any{"body": "after restore"}, 201, "")
}

func TestPublicBoardLifecycleAuthorityAndHiddenAdminID(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	sam := tm.person("sam")
	b := tm.newBoard(maya, "private")
	key := tm.key(maya)
	tm.link(maya, b)
	maya.run("board", "add", "@sam")
	maya.run("board", "owner", "@sam")
	creatorAgent := tm.agentToken(maya, b)
	otherAgent := tm.agentToken(sam, b)
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/archive", tm.key(sam), "", nil, 403, "board_creator_required")
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/archive", otherAgent, "", nil, 403, "board_creator_required")
	lifecycleReceipt(t, tm, b, "archive", creatorAgent, "", "archived", true)
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/delete", creatorAgent, "", nil, 403, "human_token_required")
	lifecycleReceipt(t, tm, b, "restore", creatorAgent, "", "active", true)
	list := lifecycleRequest(t, tm, "GET", "/v1/boards?all=true", tm.key(tm.admin), "", nil, 200, "")
	var id string
	for _, row := range list["hidden_boards"].([]any) {
		h := row.(map[string]any)
		if h["id"] == lifecycleRead(t, tm, b, key)["id"] {
			id = h["id"].(string)
		}
	}
	if id == "" {
		t.Fatal("admin missing hidden board id")
	}
	lifecycleReceipt(t, tm, id, "archive", tm.key(tm.admin), "", "archived", true)
	lifecycleRequest(t, tm, "GET", "/v1/boards/"+b, tm.key(tm.admin), "", nil, 404, "board_not_found")
	lifecycleReceipt(t, tm, id, "restore", tm.key(tm.admin), "", "active", true)
}

func TestPublicBoardLifecycleListingScopeAndCounts(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	key := tm.key(tm.admin)
	active := tm.newBoard(tm.admin, "open")
	archived := tm.newBoard(tm.admin, "open")
	hidden := tm.newBoard(maya, "private")
	hiddenID := lifecycleRead(t, tm, hidden, tm.key(maya))["id"].(string)
	lifecycleReceipt(t, tm, archived, "archive", key, "", "archived", true)
	lifecycleReceipt(t, tm, hiddenID, "archive", key, "", "archived", true)
	for _, tc := range []struct {
		query            string
		ordinary, hidden int
	}{
		{"", 1, 0}, {"?all=true", 1, 0}, {"?all=true&lifecycle=archived", 1, 1}, {"?all=true&lifecycle=all", 2, 1},
	} {
		out := lifecycleRequest(t, tm, "GET", "/v1/boards"+tc.query, key, "", nil, 200, "")
		rows := out["boards"].([]any)
		n := 0
		if h, ok := out["hidden_boards"].([]any); ok {
			n = len(h)
		}
		if len(rows) != tc.ordinary || n != tc.hidden || out["archived_count"] != float64(1) {
			t.Fatalf("list %s: boards %d, hidden %d, archived count %v", tc.query, len(rows), n, out["archived_count"])
		}
		for _, r := range rows {
			name := r.(map[string]any)["name"]
			if name != active && name != archived {
				t.Fatalf("unexpected ordinary board %v", name)
			}
		}
	}
}

func TestPublicBoardLifecycleRestoreDoesNotReviveRemovedSeat(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	b := tm.newBoard(tm.admin, "private")
	key := tm.key(tm.admin)
	tm.link(tm.admin, b)
	tm.admin.run("board", "add", "@maya")
	d := delegationToken(t, tm, tm.key(maya))
	seat := delegatedSeat(t, tm, d, b, 201)
	token := seat["token"].(string)
	lifecycleReceipt(t, tm, b, "archive", key, "", "archived", true)
	tm.admin.run("board", "remove", "@maya")
	lifecycleReceipt(t, tm, b, "restore", key, "", "active", true)
	tm.admin.run("board", "add", "@maya")
	before := lifecycleRead(t, tm, b, key)["head_seq"]
	lifecycleRequest(t, tm, "GET", "/v1/boards/"+b+"/messages", token, "", nil, 404, "board_not_found")
	refused := delegatedSeat(t, tm, d, b, 403)
	if errorCode(t, refused) != "agent_removed" {
		t.Fatalf("reuse removed seat: error %v", refused["error"])
	}
	if after := lifecycleRead(t, tm, b, key)["head_seq"]; before != after {
		t.Fatalf("removed-seat refusal changed head: %v to %v", before, after)
	}
}

func TestPublicBoardLifecycleDeleteTombstoneAndReplayAuthority(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	b := tm.newBoard(maya, "private")
	key := tm.key(maya)
	token := tm.agentToken(maya, b)
	id := lifecycleRead(t, tm, b, key)["id"].(string)
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/delete", key, "", nil, 409, "board_not_archived")
	lifecycleReceipt(t, tm, b, "archive", key, "archive-receipt", "archived", true)
	lifecycleReceipt(t, tm, b, "restore", key, "", "active", true)
	lifecycleReceipt(t, tm, b, "archive", key, "archive-receipt", "archived", true)
	if state := lifecycleRead(t, tm, b, key)["lifecycle"]; state != "active" {
		t.Fatalf("cached archive receipt changed current state: %v", state)
	}
	lifecycleReceipt(t, tm, b, "archive", key, "", "archived", true)
	lifecycleReceipt(t, tm, b, "delete", key, "delete-receipt", "deleted", true)
	lifecycleReceipt(t, tm, b, "delete", key, "delete-receipt", "deleted", true)
	for _, path := range []string{"/v1/boards/" + b, "/v1/boards/" + b + "/messages", "/v1/boards/" + b + "/events", "/v1/me"} {
		use := key
		if path == "/v1/me" {
			use = token
		}
		lifecycleRequest(t, tm, "GET", path, use, "", nil, 404, "board_not_found")
	}
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+id+"/restore", key, "", nil, 404, "board_not_found")
	lifecycleRequest(t, tm, "POST", "/v1/boards", key, "", map[string]any{"name": b, "template": "general", "visibility": "private"}, 409, "board_name_taken")
	list := lifecycleRequest(t, tm, "GET", "/v1/boards?all=true&lifecycle=all", tm.key(tm.admin), "", nil, 200, "")
	if len(list["boards"].([]any)) != 0 {
		t.Fatal("deleted board remained in ordinary list")
	}
	if hidden, ok := list["hidden_boards"].([]any); ok && len(hidden) != 0 {
		t.Fatal("deleted board remained in hidden list")
	}
	tm.admin.run("people", "remove", "@maya", "--yes", "--json")
	lifecycleRequest(t, tm, "POST", "/v1/boards/"+b+"/delete", key, "delete-receipt", nil, 401, "")
}
