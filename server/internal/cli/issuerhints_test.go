package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hintMachine(t *testing.T, first, second string) *lifecycleEnv {
	t.Helper()
	e := lifecycleMachine(t, first, "abh_person", agentCredential{})
	p, err := resolvePaths(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.servers(), serverLogins{Default: first, Servers: []serverLogin{{URL: first, Key: "abh_person"}, {URL: second, Key: "abh_person"}}}, 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLifecycleHintsRetainSelectedIssuer(t *testing.T) {
	first := newLifecycleServer(t, paymentsDesign())
	second := newLifecycleServer(t, paymentsDesign())
	e := hintMachine(t, first.URL, second.URL)
	r := e.run("board", "archive", "payments-design", "--server", second.URL)
	restore := "aboard board restore payments-design --server " + second.URL
	if r.code != 0 || !strings.Contains(r.stdout, restore) {
		t.Fatalf("archive hint lost issuer: %+v", r)
	}
	r = e.run(strings.Fields(restore)[1:]...)
	if r.code != 0 {
		t.Fatalf("follow restore hint: %+v", r)
	}
	for _, request := range first.sent() {
		if strings.HasPrefix(request, "POST ") {
			t.Fatalf("follow-up wrote to default issuer: %s", request)
		}
	}
	for _, tc := range []struct {
		args          []string
		code, command string
	}{
		{[]string{"board", "delete", "payments-design", "--server", second.URL, "--json"}, "confirmation_required", "aboard board delete payments-design --server " + second.URL + " --yes"},
		{[]string{"board", "delete", "payments-design", "--server", second.URL, "--yes", "--json"}, "board_not_archived", "aboard board archive payments-design --server " + second.URL},
		{[]string{"board", "delete", "payments-design", "--server", second.URL, "--as", "writer", "--json"}, "human_command_in_session", "aboard board delete payments-design --server " + second.URL},
	} {
		r := e.run(tc.args...)
		code, hint := r.errorCode(t)
		if code != tc.code || !strings.Contains(hint, tc.command) {
			t.Errorf("%v: code=%s hint=%s", tc.args, code, hint)
		}
	}
}

func TestBoardListAddedJoinRetainsSelectedIssuer(t *testing.T) {
	first := newLifecycleServer(t, paymentsDesign())
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/info" && r.Header.Get("Authorization") != "Bearer abh_person" {
			t.Error("wrong issuer credential")
		}
		var result any
		switch r.URL.Path {
		case "/v1/info":
			result = map[string]any{"version": version, "mode": "team"}
		case "/v1/me":
			result = map[string]any{"name": "alex", "kind": "human"}
		case "/v1/boards":
			result = map[string]any{"boards": []any{map[string]any{"id": "brd_added", "name": "payments-design", "visibility": "open", "on_board": true, "added": map[string]any{"seq": 3, "by": map[string]any{"kind": "human", "name": "pat"}}}}}
		case "/v1/boards/payments-design/people":
			result = map[string]any{"people": []any{}}
		case "/v1/boards/payments-design/members":
			result = map[string]any{"members": []any{}}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer second.Close()
	e := hintMachine(t, first.URL, second.URL)
	r := e.run("boards", "--server", second.URL, "--json")
	if r.code != 0 {
		t.Fatalf("boards: %+v", r)
	}
	var out struct {
		Boards []struct {
			Added *addedNotice `json:"added"`
		} `json:"boards"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	expected := "aboard join --board payments-design --server " + second.URL
	if len(out.Boards) != 1 || out.Boards[0].Added == nil || out.Boards[0].Added.Join != expected {
		t.Fatalf("added join lost issuer: %s", r.stdout)
	}
}
