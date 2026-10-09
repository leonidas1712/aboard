//go:build e2e

package e2e

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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
	person := newPersonHome(t, "newcomer")
	session := person.claudeSession("newcomer-setup")
	first := session.run("setup", link, "--handle", "newcomer", "--json")
	out := first.json(t)
	matchesCLISpec(t, "SetupOutput", out)
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
	proxy.Transport = transport
	proxy.ErrorLog = nil
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	link := strings.Replace(field(t, invite, "link").(string), tm.url(), server.URL, 1)
	person := newPersonHome(t, "recoverer")
	session := person.claudeSession("recovering-setup")
	first := session.runExit("setup", link, "--handle", "recoverer", "--json")
	if first.json(t)["state"] != "uncertain" || !transport.dropped.Load() {
		t.Fatalf("lost commit response did not remain uncertain: %s", first)
	}
	recovered := session.run("setup", link, "--handle", "recoverer", "--json").json(t)
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
