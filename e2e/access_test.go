//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// getAsOwner reads an API path with this machine's person's login and decodes the JSON.
func (e *env) getAsOwner(path string) map[string]any {
	e.t.Helper()
	token, err := os.ReadFile(filepath.Join(e.home, ".config", "aboard", "local-owner-token"))
	if err != nil {
		e.t.Fatalf("no owner login: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+e.addr+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET %s: status %d, %v", path, resp.StatusCode, err)
	}
	return v
}

// The person who pairs is the board's admin, and the API says so, but a solo user never
// meets the word: pair, status and board policy don't mention it and status lists no
// people.
func TestSoloOwnerIsAdminWithoutBeingTold(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pair := e.run("pair", "writer-reviewer")
	status := e.run("status")
	policy := e.run("board", "policy", "recommended")
	for _, r := range []result{pair, status, policy} {
		if strings.Contains(strings.ToLower(r.stdout+r.stderr), "admin") {
			t.Fatalf("a solo user's output mentions admin:\n%s", r)
		}
	}
	if strings.Contains(status.stdout, "People:") {
		t.Fatalf("status lists people on a board with one person:\n%s", status)
	}
	st := e.run("status", "--json").json(t)
	if people, ok := st["people"]; !ok || people != nil {
		t.Fatalf("status --json people = %v (present %v), want null", people, ok)
	}

	board, _ := field(t, st, "board").(string)
	members, _ := field(t, e.getAsOwner("/v1/boards/"+board+"/members"), "members").([]any)
	if len(members) != 2 {
		t.Fatalf("members %v", members)
	}
	for _, m := range members {
		kind, access := field(t, m, "kind"), field(t, m, "access")
		if (kind == "human" && access != "admin") || (kind == "agent" && access != nil) {
			t.Fatalf("member %v: access %v", field(t, m, "name"), access)
		}
	}
}
