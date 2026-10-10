//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestSuggestedInviteHandleTravelsWithoutReservingPerson(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--person", "--handle", "maya", "--server", tm.url(), "--json").json(t)
	link := field(t, invite, "link").(string)
	if !strings.Contains(field(t, invite, "prompt").(string), "--handle maya.") {
		t.Fatalf("suggested prompt: %v", invite)
	}
	status, preview := tm.call("POST", "/v1/invites/preview", "", map[string]any{"invite": strings.Split(link, "#")[1]})
	if status != 200 || preview["suggested_handle"] != "maya" {
		t.Fatalf("preview %d %v", status, preview)
	}
	listed := tm.admin.run("invite", "list", "--server", tm.url(), "--json").json(t)
	if field(t, listed, "invites.0.suggested_handle") != "maya" {
		t.Fatalf("metadata: %v", listed)
	}
	// A suggestion neither creates the person nor reserves the handle.
	tm.person("maya")
	person := newPersonHome(t, "recipient")
	setup := person.claudeSession("suggested-override").run("setup", link, "--handle", "sam", "--json").json(t)
	if field(t, setup, "person.handle") != "sam" {
		t.Fatalf("override lost: %v", setup)
	}
}

func TestAgentsListOnlyTheirPersonsInvitesWithSeatCredential(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	other := tm.person("maya")
	tm.admin.run("invite", "--person", "--handle", "new-one", "--server", tm.url(), "--json")
	board := tm.newBoard(tm.admin, "private")
	session := tm.admin.claudeSession("read-people")
	session.run("join", "--board", board, "--server", tm.url(), "--json")
	people := session.run("people", "--server", tm.url(), "--json").json(t)
	if len(field(t, people, "people").([]any)) != 2 {
		t.Fatalf("people: %v", people)
	}
	invites := session.run("invite", "list", "--server", tm.url(), "--json").json(t)
	if len(field(t, invites, "invites").([]any)) != 2 {
		t.Fatalf("own invite list: %v", invites)
	}
	otherBoard := tm.newBoard(other, "private")
	otherSession := other.claudeSession("other-people")
	otherSession.run("join", "--board", otherBoard, "--server", tm.url(), "--json")
	own := otherSession.run("invite", "list", "--server", tm.url(), "--json").json(t)
	if len(field(t, own, "invites").([]any)) != 0 {
		t.Fatalf("foreign invitations: %v", own)
	}
	id := field(t, invites, "invites.0.id").(string)
	refused := session.runExit("invite", "revoke", id, "--server", tm.url(), "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "human_command_in_session" {
		t.Fatalf("agent revoke: %s", refused)
	}
}

func TestSetupDefaultsToSuggestedInviteHandle(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	invite := tm.admin.run("invite", "--person", "--handle", "maya", "--server", tm.url(), "--json").json(t)
	person := newPersonHome(t, "different-machine-user")
	result := person.claudeSession("suggested-default").run("setup", field(t, invite, "link").(string), "--json").json(t)
	if field(t, result, "person.handle") != "maya" {
		t.Fatalf("suggested default not used: %v", result)
	}
}

func TestAgentPeopleAndInviteReadsSelectExactIssuer(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	first.person("only-first")
	second.person("only-second")
	first.admin.run("connect", second.invite(), "--handle", "alex2", "--json")
	second.admin.run("people", "role", "alex2", "admin", "--json")
	first.admin.run("invite", "--person", "--server", first.url(), "--handle", "first-hint", "--json")
	first.admin.run("invite", "--person", "--server", second.url(), "--handle", "second-hint", "--json")
	a := first.newBoard(first.admin, "private")
	b := second.newBoard(first.admin, "private")
	session := first.admin.claudeSession("read-two-issuers")
	session.run("join", "--board", a, "--server", first.url(), "--name", "helper", "--json")
	session.run("join", "--board", b, "--server", second.url(), "--name", "helper", "--json")
	for _, target := range []struct{ url, included, excluded, hint string }{{first.url(), "only-first", "only-second", "first-hint"}, {second.url(), "only-second", "only-first", "second-hint"}} {
		people := session.run("people", "--server", target.url, "--json").json(t)
		if field(t, people, "server.url") != target.url {
			t.Fatalf("wrong issuer: %v", people)
		}
		text := session.run("people", "--server", target.url).stdout
		if !strings.Contains(text, "@"+target.included) || strings.Contains(text, "@"+target.excluded) {
			t.Fatalf("issuer mixed people: %s", text)
		}
		invites := session.run("invite", "list", "--server", target.url, "--json").json(t)
		rows := field(t, invites, "invites").([]any)
		found := false
		for _, row := range rows {
			if row.(map[string]any)["suggested_handle"] == target.hint {
				found = true
			}
		}
		if !found || field(t, invites, "server.url") != target.url {
			t.Fatalf("issuer mixed invitations: %v", invites)
		}
	}
}
