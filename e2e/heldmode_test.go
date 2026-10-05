//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const offRule = "Nothing wakes you or arrives by itself: read your messages with aboard inbox, " +
	"or wait for one with aboard inbox --wait 60."

// memberOn returns a board's member as the holder of token lists it.
func (tm *team) memberOn(board, name, token string) map[string]any {
	tm.t.Helper()
	status, v := tm.call("GET", "/v1/boards/"+board+"/members", token, nil)
	if status != http.StatusOK {
		tm.t.Fatalf("list members: %d %v", status, v)
	}
	for _, m := range v["members"].([]any) {
		if m := m.(map[string]any); m["name"] == name {
			return m
		}
	}
	tm.t.Fatalf("no member %s on %s: %v", name, board, v)
	return nil
}

// The server holds an agent's delivery mode, so its person changes it from any of their
// machines: here from a desktop that has no seat for the agent, while the agent's
// session runs on the laptop. The laptop's delivery daemon follows the change, the
// session is told at its next turn, and the change is in the board's record with who
// made it. Nobody else can change it: not the agent, not the board's owner, who is the
// server's admin, too.
func TestAPersonChangesTheModeFromAnotherMachine(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	laptop := tm.person("maya")
	line := field(t, tm.admin.run("pair", "--name", "writer", "--json").json(t), "join.line").(string)
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	s := laptop.claudeSession("s-laptop")
	agent := field(t, s.run("join", line, "--json").json(t), "agent.name").(string)

	desktopKey := field(t, laptop.run("keys", "create", "desktop", "--server", tm.url(), "--json").json(t), "key.token").(string)
	desktop := newPersonHome(t, "maya")
	if r := desktop.exec(nil, desktopKey+"\n", "login", tm.url(), "--json"); r.code != 0 {
		t.Fatalf("login:\n%s", r)
	}
	tm.link(desktop, board)

	// Only maya changes it: her agent, and alex, the board's owner and the server's admin,
	// are refused.
	status, v := tm.call("PUT", "/v1/boards/"+board+"/members/"+agent+"/delivery", agentToken(t, laptop, agent), map[string]any{"mode": "off"})
	if status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
		t.Fatalf("the agent setting its own mode: %d %v", status, v)
	}
	if r := tm.admin.runExit("delivery", "off", "--as", agent, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "agent_owner_required" {
		t.Fatalf("the board's owner setting maya's agent's mode:\n%s", r)
	}

	out := desktop.run("delivery", "off", "--as", agent, "--json").json(t)
	matchesCLISpec(t, "DeliveryOutput", out)
	if field(t, out, "mode") != "off" || field(t, out, "changed") != true || field(t, out, "board") != board {
		t.Fatalf("maya sets off from her desktop: %v", out)
	}
	revision := field(t, out, "revision").(float64)
	eventually(t, 10*time.Second, "the laptop's daemon to apply off", func() bool {
		return tm.memberOn(board, agent, tm.key(laptop))["delivery"] == "off"
	})

	stop := s.startHook("stop")
	tm.admin.run("say", "--as", "writer", "--to", "@"+agent, "Are you there?")
	if !stop.running(asleepFor) {
		t.Fatalf("a message woke the session in off mode, set from another machine\n%s", stop.wait(time.Second))
	}
	expectLines(t, desktop.run("delivery", "--as", agent),
		agent+" on "+board+": delivery off (delivers nothing; the agent reads its inbox itself)")
	if got := field(t, s.run("status", "--json").json(t), "delivery"); got != "off" {
		t.Fatalf("status in the laptop's session: delivery %v", got)
	}

	// The session's next turn starts with the line saying the mode changed.
	turn := s.hook("prompt", `"prompt":"next"`)
	if want := "Aboard: your delivery mode on " + board + " changed from focused to off. " + offRule; !strings.Contains(turn.stdout, want) {
		t.Fatalf("the turn's start lacks the changed mode:\n%s", turn)
	}
	if r := stop.wait(5 * time.Second); r.code != 0 {
		t.Fatalf("the prompt should release the stop hook\n%s", r)
	}

	// Back to all from the desktop: the message that waited wakes the session, after the
	// line saying the mode changed again.
	stop = s.startHook("stop")
	desktop.run("delivery", "all", "--as", agent, "--board", board)
	woke := stop.wait(10 * time.Second)
	if want := "Aboard: your delivery mode on " + board + " changed from off to all. " + allRule + "\n"; woke.code != 2 ||
		!strings.HasPrefix(woke.stderr, want) || !strings.Contains(woke.stderr, "Are you there?") {
		t.Fatalf("all mode should wake the session with the message that waited, after the changed mode\n%s", woke)
	}

	// The record has both changes, made by maya.
	status, v = tm.call("GET", "/v1/boards/"+board+"/events", tm.key(laptop), nil)
	if status != http.StatusOK {
		t.Fatalf("events: %d %v", status, v)
	}
	var changes []string
	for _, e := range v["events"].([]any) {
		if field(t, e, "type") != "agent.delivery_changed" {
			continue
		}
		if field(t, e, "actor.name") != "maya" || field(t, e, "data.name") != agent {
			t.Fatalf("a change not by maya, or not of her agent: %v", e)
		}
		changes = append(changes, field(t, e, "data.before").(string)+">"+field(t, e, "data.after").(string))
	}
	if strings.Join(changes, ",") != "focused>off,off>all" {
		t.Fatalf("the record's changes: %v", changes)
	}
	if m := tm.memberOn(board, agent, tm.key(tm.admin)); m["delivery_mode"] != "all" || m["delivery_revision"].(float64) <= revision {
		t.Fatalf("the member as alex sees it: %v", m)
	}
	laptop.run("audit", "verify", "--board", board)
}
