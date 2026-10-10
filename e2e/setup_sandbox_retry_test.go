//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestSetupSandboxRefusalResumesTheSavedAccount(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	for _, jsonOutput := range []bool{false, true} {
		invite := tm.admin.run("invite", "--person", "--board", board, "--json").json(t)
		link := field(t, invite, "link").(string)
		if !strings.Contains(field(t, invite, "prompt").(string), "Run Aboard outside your agent's sandbox; approve it when your harness asks.") {
			t.Fatal("invite omitted sandbox approval guidance")
		}
		person := newPersonHome(t, "maya")
		handle := "maya-text"
		if jsonOutput {
			handle = "maya-json"
		}
		args := []string{"setup", link, "--handle", handle}
		if jsonOutput {
			args = append(args, "--json")
		}
		const sessionID = "019a0000-0000-7000-8000-000000000061"
		refused := person.exec([]string{"CODEX_THREAD_ID=" + sessionID, "CODEX_SANDBOX=seatbelt"}, "", args...)
		if refused.code != 1 {
			t.Fatalf("setup should refuse sandboxed daemon startup: %s", refused)
		}
		command := "aboard setup --continue --handle " + handle
		if jsonOutput {
			value := refused.json(t)
			if errorCode(t, value) != "daemon_in_sandbox" || field(t, value, "error.next.command") != command || !strings.Contains(field(t, value, "error.next.resume").(string), "escalated permissions") {
				t.Fatalf("sandbox refusal lost continuation: %s", refused)
			}
		} else if !strings.Contains(refused.stderr, command) || !strings.Contains(refused.stderr, "escalated permissions") {
			t.Fatalf("text refusal lost continuation: %s", refused)
		}
		if strings.Contains(refused.stdout+refused.stderr, link) {
			t.Fatal("refusal repeated invitation secret")
		}
		key := tm.key(person)
		session := person.codexSession(sessionID)
		resumed := session.run(append(strings.Fields(command)[1:], "--json")...).json(t)
		if field(t, resumed, "steps.1.state") != "complete" || field(t, resumed, "steps.2.state") != "complete" || field(t, resumed, "steps.4.state") != "complete" || tm.key(person) != key {
			t.Fatalf("continuation lost account or membership: %v", resumed)
		}
	}
}

func TestSetupNetworkSandboxKeepsAnUncertainContinuation(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--person", "--json").json(t)
	person := newPersonHome(t, "maya")
	link := field(t, invite, "link").(string)
	target, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: target.Scheme, Host: target.Host})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/connect" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	link = front.URL + "/join#" + target.Fragment
	refused := person.exec([]string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-000000000062", "CODEX_SANDBOX=seatbelt", "CODEX_SANDBOX_NETWORK_DISABLED=1"}, "", "setup", link, "--handle", "maya", "--json")
	if refused.code != 1 {
		t.Fatalf("network sandbox should refuse setup: %s", refused)
	}
	value := refused.json(t)
	if field(t, value, "state") != "uncertain" || field(t, value, "next.command") != "aboard setup --continue --handle maya" {
		t.Fatalf("uncertain setup lost continuation: %s", refused)
	}
}
