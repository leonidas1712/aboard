//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSetupBundledInviteKeepsOneAccountAndSavedKey(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	name := tm.newBoard(tm.admin, "private")
	status, b := tm.call("GET", "/v1/boards/"+name, tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("board lookup: %d", status)
	}
	boardID := field(t, b, "id").(string)
	invite := tm.admin.run("invite", "--server", tm.url(), "--board", name, "--json").json(t)
	matchesCLISpec(t, "ServerInviteOutput", invite)
	link := field(t, invite, "link").(string)
	wantPrompt := "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh, read aboard skill, then run aboard setup " + link + " --handle <name you'd like teammates to see>. Verify you can exchange messages with the inviting agent."
	if field(t, invite, "prompt") != wantPrompt {
		t.Fatalf("colleague JSON prompt: %v", invite)
	}
	person := newPersonHome(t, "newcomer")
	session := person.claudeSession("newcomer-setup")
	first := session.run("connect", link, "--handle", "newcomer", "--json")
	out := first.json(t)
	matchesCLISpec(t, "SetupOutput", out)
	if !strings.Contains(field(t, out, "next.resume").(string), "Read the aboard skill now") {
		t.Fatalf("pairing next lost current skill instruction: %v", out)
	}
	if out["state"] == "complete" {
		t.Fatal("setup claimed delivery verified without a pairing handshake")
	}
	for i, step := range []string{"installed", "account", "memberships"} {
		if field(t, out, "steps."+string(rune('0'+i))+".step") != step || field(t, out, "steps."+string(rune('0'+i))+".state") != "complete" {
			t.Fatalf("incomplete %s step: %v", step, out)
		}
	}
	key := tm.key(person)
	if strings.Contains(first.stdout+first.stderr, key) || strings.Contains(first.stdout+first.stderr, link) {
		t.Fatal("setup output contains an invitation or access key")
	}
	status, me := tm.call("GET", "/v1/me", key, nil)
	if status != http.StatusOK {
		t.Fatalf("saved key did not authenticate: %d", status)
	}
	personID := field(t, me, "id")
	status, receipt := tm.call("GET", "/v1/me/onboarding", key, nil)
	if status != http.StatusOK || field(t, receipt, "boards.0") != boardID {
		t.Fatalf("onboarding membership receipt: %d %v", status, receipt)
	}
	status, members := tm.call("GET", "/v1/boards/"+name+"/people", key, nil)
	if status != http.StatusOK || len(field(t, members, "people").([]any)) != 2 {
		t.Fatalf("bundled private admission: %d %v", status, members)
	}
	second := session.run("setup", link, "--handle", "newcomer", "--json").json(t)
	matchesCLISpec(t, "SetupOutput", second)
	if tm.key(person) != key {
		t.Fatal("setup retry replaced its account or key")
	}
	status, current := tm.call("GET", "/v1/me", key, nil)
	if status != http.StatusOK || field(t, current, "id") != personID {
		t.Fatal("setup retry selected a different person")
	}
	status, directory := tm.call("GET", "/v1/people", tm.key(tm.admin), nil)
	if status != http.StatusOK || len(field(t, directory, "people").([]any)) != 2 {
		t.Fatalf("setup created more than one newcomer: %d %v", status, directory)
	}
}

func TestAgentConnectRejectsAnInvalidFirstInvite(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	for _, jsonOutput := range []bool{false, true} {
		person := newPersonHome(t, "newcomer")
		args := []string{"connect", tm.url() + "/join#abi_x", "--handle", "newcomer"}
		if jsonOutput {
			args = append(args, "--json")
		}
		result := person.exec([]string{"ABOARD_AGENT=helper"}, "", args...)
		if result.code != 1 {
			t.Fatalf("invalid first invite exited %d: %s", result.code, result)
		}
		if jsonOutput {
			out := result.json(t)
			if errorCode(t, out) != "invite_invalid" || field(t, out, "error.next.command") == "" {
				t.Fatalf("invalid invite lost its error or next step: %s", result)
			}
		} else if !strings.Contains(result.stdout+result.stderr, "invite_invalid") {
			t.Fatalf("text output lost the definite refusal: %s", result)
		}
	}
}

// The proxy drops the committed account response, then allows the original saved
// key to recover its receipt. It never simulates an uncommitted server failure.
func TestSetupRecoversACommittedInviteWithoutAnotherAccount(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	name := tm.newBoard(tm.admin, "private")
	invite := tm.admin.run("invite", "--server", tm.url(), "--board", name, "--json").json(t)
	target, err := url.Parse(tm.url())
	if err != nil {
		t.Fatal(err)
	}
	transport := &setupLostResponseTransport{next: http.DefaultTransport}
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(r *http.Request) {
		direct(r)
		r.Host = target.Host
	}
	proxy.Transport = transport
	proxy.ErrorLog = nil
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	link := strings.Replace(field(t, invite, "link").(string), tm.url(), server.URL, 1)
	person := newPersonHome(t, "different-machine-user")
	session := person.claudeSession("recovering-setup")
	first := session.runExit("setup", link, "--handle", "recoverer", "--json")
	if first.code != 1 || first.json(t)["state"] != "uncertain" || !transport.dropped.Load() {
		t.Fatalf("lost commit response did not remain uncertain: %s", first)
	}
	recovered := session.run("setup", link, "--json").json(t)
	matchesCLISpec(t, "SetupOutput", recovered)
	if field(t, recovered, "steps.1.state") != "complete" || transport.connects.Load() != 1 {
		t.Fatalf("recovery repeated redemption instead of authenticating its saved key: %v", recovered)
	}
	status, people := tm.call("GET", "/v1/people", tm.key(tm.admin), nil)
	if status != http.StatusOK || len(field(t, people, "people").([]any)) != 2 {
		t.Fatalf("recovery created another account: %d %v", status, people)
	}
}

type setupLostResponseTransport struct {
	next     http.RoundTripper
	connects atomic.Int64
	dropped  atomic.Bool
}

func TestOwnInviteCommandsListMetadataAndRevokeOnce(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--server", tm.url(), "--json").json(t)
	link := field(t, invite, "link").(string)
	listed := tm.admin.run("invite", "list", "--server", tm.url(), "--json")
	matchesCLISpec(t, "ServerInvitesOutput", listed.json(t))
	id := field(t, listed.json(t), "invites.0.id").(string)
	if strings.Contains(listed.stdout+listed.stderr, link) {
		t.Fatal("invite list omitted the issued invite or exposed its link")
	}
	for i, changed := range []bool{true, false} {
		out := tm.admin.run("invite", "revoke", id, "--server", tm.url(), "--json").json(t)
		matchesCLISpec(t, "ServerInviteRevocationOutput", out)
		if field(t, out, "changed") != changed || field(t, out, "id") != id {
			t.Fatalf("revocation %d: %v", i, out)
		}
	}
	listed = tm.admin.run("invite", "list", "--server", tm.url(), "--json")
	if field(t, listed.json(t), "invites.0.state") != "revoked" {
		t.Fatal("revocation is not reflected in invite metadata")
	}
}

func (p *setupLostResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
		p.connects.Add(1)
	}
	response, err := p.next.RoundTrip(r)
	if err == nil && r.Method == http.MethodPost && r.URL.Path == "/v1/connect" && response.StatusCode == http.StatusOK && p.dropped.CompareAndSwap(false, true) {
		_ = response.Body.Close()
		return nil, errors.New("test dropped the committed connection response")
	}
	return response, err
}

func TestSetupWaitsForThePersonsHandleWithoutSpendingInvite(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--server", tm.url(), "--json").json(t)
	link := field(t, invite, "link").(string)
	person := newPersonHome(t, "newcomer")
	session := person.claudeSession("missing-handle")
	out := session.run("setup", link, "--json").json(t)
	matchesCLISpec(t, "SetupOutput", out)
	if out["state"] != "pending" || field(t, out, "steps.1.state") != "pending" || out["person"] != nil {
		t.Fatalf("setup redeemed without a person-selected handle: %v", out)
	}
	if !strings.Contains(field(t, out, "next.resume").(string), "Ask your person") || !strings.Contains(field(t, out, "next.command").(string), "--handle") {
		t.Fatalf("missing actionable name choice: %v", out)
	}
	ordinary := newPersonHome(t, "terminal-choice")
	waiting := ordinary.run("setup", link, "--json").json(t)
	if waiting["person"] != nil || field(t, waiting, "steps.1.state") != "pending" {
		t.Fatalf("non-TTY setup redeemed: %v", waiting)
	}
	if strings.Contains(field(t, out, "next.command").(string), link) {
		t.Fatal("setup repeated invite secret")
	}
	if field(t, out, "next.command") != "aboard setup --continue --handle newcomer" {
		t.Fatalf("setup continuation is not runnable without a secret: %v", out)
	}
	saved, err := filepath.Glob(filepath.Join(person.stateDir(), "onboarding", "*.json"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("missing saved invitation: %v files=%d", err, len(saved))
	}
	st, err := os.Stat(saved[0])
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("saved invitation is not private: %v", err)
	}
	savedBytes, err := os.ReadFile(saved[0])
	if err != nil {
		t.Fatal(err)
	}
	var staged map[string]any
	if err := json.Unmarshal(savedBytes, &staged); err != nil {
		t.Fatal(err)
	}
	if staged["token"] != "" || staged["handle"] != "" || staged["attempted"] != false {
		t.Fatal("waiting for a name created or attempted an account proof")
	}
	resumed := session.run("setup", "--continue", "--handle", "newcomer", "--json").json(t)
	if !strings.Contains(field(t, resumed, "next.resume").(string), "Read the aboard skill now") {
		t.Fatalf("current session was not told to load skill: %v", resumed)
	}
	if field(t, resumed, "person.handle") != "newcomer" {
		t.Fatalf("invite was spent or account changed: %v", resumed)
	}
}

func TestSetupTakenHandleRetainsSecretFreeContinuation(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--server", tm.url(), "--json").json(t)
	link := field(t, invite, "link").(string)
	person := newPersonHome(t, "newcomer")
	session := person.claudeSession("taken-handle")
	session.run("setup", link, "--json")
	refused := session.runExit("setup", "--continue", "--handle", "alex", "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "handle_taken" || !strings.HasPrefix(field(t, refused.json(t), "error.next.command").(string), "aboard setup --continue") {
		t.Fatalf("taken handle lost continuation: %s", refused)
	}
	if strings.Contains(refused.stdout+refused.stderr, link) {
		t.Fatal("handle refusal repeated invite secret")
	}
	text := session.runExit("setup", "--continue", "--handle", "alex")
	if text.code != 1 || !strings.Contains(text.stdout+text.stderr, "aboard setup --continue --handle NAME") || strings.Contains(text.stdout+text.stderr, link) {
		t.Fatalf("text refusal lost its secret-free next step: %s", text)
	}
	resumed := session.run("setup", "--continue", "--handle", "newcomer", "--json").json(t)
	if field(t, resumed, "person.handle") != "newcomer" {
		t.Fatal("handle_taken spent invitation or replaced the requested account")
	}
}
