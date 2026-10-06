package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestDelegatedCreationMakesPersonOwnerAndSessionSeatAtomically(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	dlg := s.delegate(s.owner)
	body := map[string]any{"template": "general", "harness": "codex", "session": "codex:create-1"}
	first := s.call("POST", "/v1/delegations/boards", dlg, body, "create-1")
	s.want(first, 201, "")
	name, token := first.str("board", "name"), first.str("token")
	boardBody, boardOK := first.body["board"].(map[string]any)
	agentBody, agentOK := first.body["agent"].(map[string]any)
	if !boardOK || !agentOK || boardBody["can_delete"] != false || agentBody["access"] != nil {
		t.Fatalf("seat inherited human owner powers: %s", first.raw)
	}
	if _, ok := boardBody["read_up_to"]; ok {
		t.Fatalf("creation leaked person's read position: %s", first.raw)
	}
	if token == "" || first.str("board", "created_by", "kind") != "human" || first.str("agent", "owner") != "alex" {
		t.Fatalf("creation did not preserve person ownership: %s", first.raw)
	}
	noStore(t, first)
	replay := s.call("POST", "/v1/delegations/boards", dlg, body, "create-1")
	s.want(replay, 201, "")
	if replay.raw != first.raw || replay.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("creation retry changed result: %s", replay.raw)
	}
	s.want(s.call("POST", "/v1/boards/"+name+"/messages", token, map[string]any{"body": "ready"}, ""), 201, "")
	invalid := map[string]any{"template": "general", "harness": "codex", "session": "codex:create-2", "role": "missing"}
	s.want(s.call("POST", "/v1/delegations/boards", dlg, invalid, "invalid-role"), 422, "role_not_found")
	list := s.call("GET", "/v1/boards", dlg, nil, "")
	s.want(list, 200, "")
	boards, ok := list.body["boards"].([]any)
	if !ok || len(boards) != 1 {
		t.Fatalf("failed creation left a board: %s", list.raw)
	}
}

func TestSessionAgentAddsPeopleOnlyWhenEveryGateAllowsIt(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	_ = maya
	dlg := s.delegate(s.owner)
	create := s.call("POST", "/v1/delegations/boards", dlg, map[string]any{"harness": "codex", "session": "codex:add-1", "visibility": "private"}, "private")
	s.want(create, 201, "")
	name, token := create.str("board", "name"), create.str("token")
	path := "/v1/boards/" + name
	s.want(s.call("POST", path+"/people", token, map[string]any{"handle": "maya"}, "add"), 403, "add_people_not_allowed")
	s.want(s.call("PATCH", path, token, map[string]any{"agents_add_people": true}, ""), 403, "human_token_required")
	s.want(s.call("PATCH", path, s.owner, map[string]any{"agents_add_people": true}, ""), 200, "")
	s.want(s.call("POST", path+"/people", token, map[string]any{"handle": "maya"}, "add-enabled"), 201, "")
	s.want(s.call("PATCH", "/v1/settings", s.owner, map[string]any{"agents_add_people": false}, ""), 200, "")
	s.want(s.call("POST", path+"/people", token, map[string]any{"handle": "maya"}, "add-enabled"), 403, "add_people_not_allowed")
}

func TestCreationRetrySurvivesResponseCacheSaveFailure(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "CREATE TRIGGER refuse_response_save BEFORE INSERT ON idempotency BEGIN SELECT RAISE(FAIL, 'cache unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	dlg := s.delegate(s.owner)
	body := map[string]any{"harness": "codex", "session": "codex:cache-failure"}
	first := s.call("POST", "/v1/delegations/boards", dlg, body, "durable")
	s.want(first, 201, "")
	second := s.call("POST", "/v1/delegations/boards", dlg, body, "durable")
	s.want(second, 201, "")
	if first.raw != second.raw || second.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("cache failure created another board: %s / %s", first.raw, second.raw)
	}
	// Ensure the fixture exercises the generated endpoint's response, not just transport success.
	if _, err := s.client(first.str("token")).GetMeWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCreationReceiptRejectsChangedBodyAndReplacedToken(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "CREATE TRIGGER refuse_response_save BEFORE INSERT ON idempotency BEGIN SELECT RAISE(FAIL, 'cache unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	dlg := s.delegate(s.owner)
	body := map[string]any{"harness": "codex", "session": "codex:receipt-check"}
	first := s.call("POST", "/v1/delegations/boards", dlg, body, "receipt-check")
	s.want(first, 201, "")
	changed := map[string]any{"harness": "codex", "session": "codex:receipt-check", "title": "changed"}
	s.want(s.call("POST", "/v1/delegations/boards", dlg, changed, "receipt-check"), 422, "idempotency_conflict")
	rotated := s.joinSession(dlg, first.str("board", "name"), "codex:receipt-check", nil)
	s.want(rotated, 200, "")
	s.want(s.call("POST", "/v1/delegations/boards", dlg, body, "receipt-check"), 409, "seat_token_replaced")
	list := s.call("GET", "/v1/boards", dlg, nil, "")
	s.want(list, 200, "")
	boards, ok := list.body["boards"].([]any)
	if !ok || len(boards) != 1 {
		t.Fatalf("receipt conflict created another resource: %s", list.raw)
	}
}

func TestAgentAddNeedsItsStoredRolesExplicitPermission(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.addHuman("maya")
	dlg := s.delegate(s.owner)
	first := s.call("POST", "/v1/delegations/boards", dlg, map[string]any{"harness": "codex", "session": "codex:custom-role"}, "custom-role")
	s.want(first, 201, "")
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "UPDATE boards SET roles_json = ? WHERE id = ?", `{"member":{"can":["post","invite"]}}`, first.str("board", "id")); err != nil {
		t.Fatal(err)
	}
	name := first.str("board", "name")
	for _, target := range []string{"maya", "missing"} {
		s.want(s.call("POST", "/v1/boards/"+name+"/people", first.str("token"), map[string]any{"handle": target}, ""), 403, "add_people_not_allowed")
	}
	boardResult := s.call("GET", "/v1/boards/"+name, s.owner, nil, "")
	s.want(boardResult, 200, "")
	if boardResult.body["head_seq"] != float64(3) {
		t.Fatalf("denied role wrote event: %s", boardResult.raw)
	}
}

func TestDelegatedCreationReceiptExpiresAndIsPurgedAt24Hours(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	dlg := s.delegate(s.owner)
	body := map[string]any{"harness": "codex", "session": "codex:expiry"}
	first := s.call("POST", "/v1/delegations/boards", dlg, body, "expiry")
	s.want(first, 201, "")
	s.clock.Advance(24 * time.Hour)
	if err := s.st.PurgeResponses(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := s.call("POST", "/v1/delegations/boards", dlg, body, "expiry")
	s.want(second, 201, "")
	if second.str("board", "id") == first.str("board", "id") || second.header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("expired receipt reused resource: %s", second.raw)
	}
}

func TestDelegatedCreationRefusalsNeverCacheSecrets(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	dlg := s.delegate(s.owner)
	body := map[string]any{"harness": "codex", "session": "codex:refusal"}
	for _, tc := range []struct {
		token, key string
		status     int
		code       string
	}{
		{"", "missing-auth", 401, "unauthorized"},
		{s.owner, "person-key", 403, "forbidden"},
		{dlg, "", 400, "invalid_request"},
	} {
		result := s.call("POST", "/v1/delegations/boards", tc.token, body, tc.key)
		s.want(result, tc.status, tc.code)
		noStore(t, result)
	}
	mismatch := s.call("POST", "/v1/delegations/boards", dlg, map[string]any{"harness": "codex", "session": "claude-code:refusal"}, "mismatch")
	s.want(mismatch, 422, "invalid_request")
	noStore(t, mismatch)
}

func TestChangingBoardCreationDoesNotReenableAgentAdditions(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.want(s.call("PATCH", "/v1/settings", s.owner, map[string]any{"agents_add_people": false}, ""), 200, "")
	s.want(s.call("PATCH", "/v1/settings", s.owner, map[string]any{"board_creation": "admins"}, ""), 200, "")
	settings := s.call("GET", "/v1/settings", s.owner, nil, "")
	s.want(settings, 200, "")
	if settings.body["agents_add_people"] != false {
		t.Fatalf("unrelated settings change enabled agent additions: %s", settings.raw)
	}
}

func TestFailedReceiptWriteRollsBackBoardAndSeat(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	dlg := s.delegate(s.owner)
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "CREATE TRIGGER refuse_creation_receipt BEFORE INSERT ON delegated_creations BEGIN SELECT RAISE(FAIL, 'receipt unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"harness": "codex", "session": "codex:receipt-write"}
	s.want(s.call("POST", "/v1/delegations/boards", dlg, body, "receipt-write"), 500, "internal")
	list := s.call("GET", "/v1/boards", dlg, nil, "")
	s.want(list, 200, "")
	boards, ok := list.body["boards"].([]any)
	if !ok || len(boards) != 0 {
		t.Fatalf("receipt failure committed resource: %s", list.raw)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER refuse_creation_receipt"); err != nil {
		t.Fatal(err)
	}
	s.want(s.call("POST", "/v1/delegations/boards", dlg, body, "receipt-write"), 201, "")
}

func TestCreationReceiptHashesExactBodyWhenResponseCacheFails(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	dlg := s.delegate(s.owner)
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "CREATE TRIGGER refuse_response_save BEFORE INSERT ON idempotency BEGIN SELECT RAISE(FAIL, 'cache unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"harness": "codex", "session": "codex:exact-body"}
	s.want(s.call("POST", "/v1/delegations/boards", dlg, body, "exact-body"), 201, "")
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST", s.url+"/v1/delegations/boards", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+dlg)
	req.Header.Set("Idempotency-Key", "exact-body")
	req.Header.Set("Content-Type", "application/json")
	response, err := s.httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var result struct{ Error struct{ Code string } }
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 422 || result.Error.Code != "idempotency_conflict" {
		t.Fatalf("normalized body reused receipt: %d %+v", response.StatusCode, result)
	}
}

func TestCreationReplayRechecksParentKeyAndCreatorAccess(t *testing.T) {
	for _, which := range []string{"parent expiry", "creator removed"} {
		for _, cache := range []bool{true, false} {
			t.Run(which+"/cache="+fmt.Sprint(cache), func(t *testing.T) {
				t.Parallel()
				s := newTestServer(t)
				db, err := sql.Open("sqlite", s.path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = db.Close() }()
				if !cache {
					if _, err := db.ExecContext(context.Background(), "CREATE TRIGGER refuse_response_save BEFORE INSERT ON idempotency BEGIN SELECT RAISE(FAIL, 'cache unavailable'); END"); err != nil {
						t.Fatal(err)
					}
				}
				dlg := s.delegate(s.owner)
				body := map[string]any{"harness": "codex", "session": "codex:authority"}
				first := s.call("POST", "/v1/delegations/boards", dlg, body, "authority")
				s.want(first, 201, "")
				status, code := 404, "board_not_found"
				if which == "parent expiry" {
					_, err = db.ExecContext(context.Background(), "UPDATE access_keys SET expires_at = ?", s.clock.Now().Add(-time.Second).Format(time.RFC3339Nano))
					status, code = 401, "delegation_revoked"
				} else {
					_, err = db.ExecContext(context.Background(), "UPDATE members SET status = 'removed' WHERE id = (SELECT created_by FROM boards WHERE id = ?)", first.str("board", "id"))
				}
				if err != nil {
					t.Fatal(err)
				}
				replay := s.call("POST", "/v1/delegations/boards", dlg, body, "authority")
				s.want(replay, status, code)
				noStore(t, replay)
				if _, ok := replay.body["token"]; ok {
					t.Fatalf("lost authority returned a seat token: %s", replay.raw)
				}
			})
		}
	}
}
