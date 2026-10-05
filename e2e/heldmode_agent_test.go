//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// An agent command never falls back to its person's login (rule 10): aboard delivery
// run for an agent, with ABOARD_AGENT set or inside a harness session, about an agent
// this machine has no seat for, answers agent_not_selected here and sends the person's
// key nowhere. The board's server is a stand-in that records every request.
func TestShowingAModeForAnAgentNeverUsesThePersonsLogin(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		http.Error(w, `{"error":{"code":"not_found","message":"no","hint":"no"}}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	const key = "abh_personskeythatmustneverbesent0000000000"
	logins, err := json.Marshal(map[string]any{"servers": []map[string]string{{"url": srv.URL, "handle": "maya", "key": key}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.configDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.configDir(), "servers.json"), logins, 0o600); err != nil {
		t.Fatal(err)
	}
	project := `{"server":{"name":"` + srv.URL + `","url":"` + srv.URL + `"},"board":"docs"}`
	if err := os.WriteFile(filepath.Join(e.dir, ".aboard"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		vars []string
		args []string
	}{
		{"ABOARD_AGENT", []string{"ABOARD_AGENT=ghost"}, []string{"--as", "ghost"}},
		{"ABOARD_AGENT and --board", []string{"ABOARD_AGENT=ghost"}, []string{"--as", "ghost", "--board", "docs"}},
		{"a harness session", []string{"CLAUDECODE=1"}, []string{"--as", "ghost"}},
		{"a harness session and --board", []string{"CLAUDECODE=1"}, []string{"--as", "ghost", "--board", "docs"}},
	} {
		r := e.exec(tc.vars, "", append([]string{"delivery", "--json"}, tc.args...)...)
		if r.code != 1 || field(t, r.json(t), "error.code") != "agent_not_selected" {
			t.Errorf("%s: want agent_not_selected from this machine\n%s", tc.name, r)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 0 {
		t.Fatalf("agent resolution should refuse locally without requests: %v", seen)
	}
}
