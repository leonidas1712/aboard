//go:build e2e

package e2e

import "testing"

// ABOARD_AGENT alone, with no session and no --as, makes board title act as that agent:
// the record names the agent, not the person.
func TestBoardTitleWithABOARDAGENTActsAsTheAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "--name", "writer", "--json").json(t), "join.line").(string)
	e.run("join", line, "--name", "scout")
	r := e.exec([]string{"ABOARD_AGENT=scout"}, "", "board", "title", "Set", "by", "scout", "--json")
	if r.code != 0 || field(t, r.json(t), "after") != "Set by scout" {
		t.Fatalf("board title as ABOARD_AGENT:\n%s", r)
	}
	evs := e.getAsOwner("/v1/boards/general/events?limit=200")
	var actor any
	for _, ev := range evs["events"].([]any) {
		if field(t, ev, "type") == "board.titled" {
			actor = field(t, ev, "actor.name")
		}
	}
	if actor != "scout" {
		t.Fatalf("board.titled actor = %v, want scout", actor)
	}
}
