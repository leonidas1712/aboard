//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// Two people on one server. leo's agent adds maya to a board: she learns it from her
// board list, her status, her agent's inbox and, once and quietly, her sessions, until
// her agent joins. Then aboard board people lists each person's agents under them, so
// her agent can find leo's by its person.
func TestAddedNoticeAndPeoplesAgents(t *testing.T) {
	t.Parallel()
	leo := newPersonHome(t, "leo")
	leo.run("up")
	tm := &team{t: t, admin: leo}
	link := ""
	for _, f := range strings.Fields(leo.run("invite", "--server").stdout) {
		if strings.Contains(f, "/join#abi_") {
			link = f
		}
	}
	maya := newPersonHome(t, "maya")
	maya.run("connect", link, "--handle", "maya")
	maya.run("init", "--yes")
	ls := leo.claudeSession("s-leo")
	ls.run("board", "new", "retry-design", "--title", "Retry design")
	// maya's session is open, on no board yet.
	ms := maya.claudeSession("s-maya")

	expectLines(t, ls.run("board", "add", "@maya"), "Added maya to retry-design (by claude, for leo).")

	// Her board list marks the board new and says who added her; leo's doesn't.
	expectLines(t, maya.run("boards"),
		"Your boards on "+tm.url()+":",
		`  retry-design "Retry design" · open · member · 2 people · 1 agent · new, added by leo's agent claude`)
	boards := maya.run("boards", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", boards)
	if field(t, boards, "boards.0.added.by") != "claude" || field(t, boards, "boards.0.added.by_owner") != "leo" ||
		field(t, boards, "boards.0.added.join") != "aboard join --board retry-design" {
		t.Fatalf("maya's boards --json: %v", boards)
	}
	if r := leo.run("boards"); strings.Contains(r.stdout, "new, added") {
		t.Fatalf("leo created the board, yet his list marks it new:\n%s", r)
	}
	// The board view reads the same field.
	status, list := tm.call("GET", "/v1/boards", tm.key(maya), nil)
	if status != http.StatusOK || field(t, list, "boards.0.added.by.kind") != "agent" || field(t, list, "boards.0.added.by.owner") != "leo" {
		t.Fatalf("maya's board list in the board view: %d %v", status, list)
	}

	// Her status, in a terminal, says it at the end.
	st := maya.run("status")
	if !strings.HasSuffix(st.stdout, "Added to retry-design by leo's agent claude · aboard join --board retry-design\n") {
		t.Fatalf("maya's status:\n%s", st)
	}
	matchesCLISpec(t, "StatusOutput", maya.run("status", "--json").json(t))

	// Her open session hears it at its next prompt, once, as quiet context; never a wake.
	quiet := "Your person was added to retry-design by leo's agent claude; join with aboard join --board retry-design if they ask."
	if r := ms.hook("prompt", `"prompt":"hello"`); r.code != 0 || !strings.Contains(r.stdout, quiet) || !strings.Contains(r.stdout, `"additionalContext"`) {
		t.Fatalf("maya's next prompt should carry the quiet line\n%s", r)
	}
	if r := ms.hook("prompt", `"prompt":"again"`); strings.Contains(r.stdout, "added to") {
		t.Fatalf("the quiet line came twice\n%s", r)
	}

	// leo, the person, adds her to a second board: a new session hears that one only.
	leo.run("board", "new", "retry-ops")
	expectLines(t, leo.run("board", "add", "@maya", "--board", "retry-ops"), "Added maya to retry-ops.")
	ms2 := maya.claudeSession("s-maya-2")
	if !strings.Contains(ms2.started.stdout, "Your person was added to retry-ops by leo; join with aboard join --board retry-ops if they ask.") ||
		strings.Contains(ms2.started.stdout, "retry-design") {
		t.Fatalf("a new session should hear of retry-ops only\n%s", ms2.started)
	}

	// Her agent joins retry-design: that board is no longer new; retry-ops still is, and
	// her agent's inbox says so.
	if r := ms.run("join", "--board", "retry-design"); !strings.HasPrefix(r.stdout, "Joined board retry-design as claude-2 (member, owner maya)\n") {
		t.Fatalf("join --board:\n%s", r)
	}
	expectLines(t, maya.run("boards"),
		"Your boards on "+tm.url()+":",
		`  retry-design "Retry design" · open · member · 2 people · 2 agents · default`,
		`  retry-ops · open · member · 2 people · 0 agents · new, added by leo`)
	expectLines(t, ms.run("inbox"),
		"retry-design · no new messages",
		"Added to retry-ops by leo · aboard join --board retry-ops")
	matchesCLISpec(t, "InboxOutput", ms.run("inbox", "--json").json(t))

	// Each person with their agents underneath, from an agent session.
	people := ms.run("board", "people")
	lines := people.lines()
	if len(lines) != 5 || lines[0] != "retry-design · open · 2 people" || lines[1] != "  leo (owner)" ||
		!strings.HasPrefix(lines[2], "    @claude · claude-code · ") || lines[3] != "  maya" ||
		!strings.HasPrefix(lines[4], "    @claude-2 · claude-code · ") {
		t.Fatalf("board people from maya's agent:\n%s", people)
	}
	pj := ms.run("board", "people", "--json").json(t)
	matchesCLISpec(t, "BoardPeopleOutput", pj)
	if field(t, pj, "people.1.agents.0.name") != "claude-2" || field(t, pj, "people.0.agents.0.harness") != "claude-code" {
		t.Fatalf("board people --json: %v", pj)
	}

	// Visibility stays as it was: a person looking at an open board they aren't on sees
	// its people but not their agents.
	open := tm.newBoard(maya, "open")
	outside := leo.run("board", "people", "--board", open, "--json").json(t)
	matchesCLISpec(t, "BoardPeopleOutput", outside)
	if field(t, outside, "people.0.handle") != "maya" || field(t, outside, "people.0.agents") != nil {
		t.Fatalf("leo outside maya's open board: %v", outside)
	}
	if r := leo.run("board", "people", "--board", open); strings.Contains(r.stdout, "@") {
		t.Fatalf("leo outside the board sees agents:\n%s", r)
	}
}
