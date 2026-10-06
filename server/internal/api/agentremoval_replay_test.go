package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// An admin who removed or pruned agents while on a private board, then left it, gets
// the stored answers again on a repeat, but without the names of the board they can no
// longer see.
func TestARepeatedRemovalOrPruneWithholdsNamesTheCallerNoLongerSees(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name := s.privateBoard(maya)
	boardID := s.call("GET", "/v1/boards/"+name, maya, nil, "").str("id")
	s.want(s.call("POST", "/v1/boards/"+name+"/people", maya, map[string]any{"handle": "alex"}, ""), 201, "")
	removed, idle := s.agentOf(maya, name), s.agentOf(maya, name)
	s.setPresence(idle.token, api.PresenceNoSession)
	s.clock.Advance(8 * 24 * time.Hour)

	first := s.removeAgent(s.owner, boardID, removed.id, "rm-key")
	s.want(first, 200, "")
	if first.str("board") != name || first.str("name") != removed.name {
		t.Fatalf("the removal while alex is on the board: %s", first.raw)
	}
	prune := map[string]any{"disconnected_for": 604800, "all": true, "agents": []string{idle.id}}
	pruned := s.call("POST", "/v1/agents/prune", s.owner, prune, "prune-key")
	s.want(pruned, 200, "")
	if !strings.Contains(pruned.raw, name) {
		t.Fatalf("the prune while alex is on the board: %s", pruned.raw)
	}

	s.want(s.call("POST", "/v1/boards/"+name+"/leave", s.owner, nil, ""), 200, "")

	again := s.removeAgent(s.owner, boardID, removed.id, "rm-key")
	s.want(again, 200, "")
	if again.header.Get("Idempotent-Replayed") != "true" || again.body["board"] != nil || again.body["name"] != nil || again.str("id") != removed.id {
		t.Fatalf("the repeated removal after alex left: %s", again.raw)
	}
	repeat := s.call("POST", "/v1/agents/prune", s.owner, prune, "prune-key")
	s.want(repeat, 200, "")
	if strings.Contains(repeat.raw, name) || strings.Contains(repeat.raw, `"`+idle.name+`"`) || !strings.Contains(repeat.raw, idle.id) {
		t.Fatalf("the repeated prune after alex left: %s", repeat.raw)
	}
	// By the board's name, which alex can't see now, the repeat is refused outright.
	s.want(s.removeAgent(s.owner, name, removed.name, "rm-by-name"), 404, "board_not_found")
}

// A session reported as ended stays ended after that report runs out: an agent whose
// daemon renewed "no session" on day 6, having reported it on day 0, has been
// disconnected for 8 days on day 8.
func TestPruneCountsFromWhenTheSessionEndedNotTheLastRenewal(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	s.setPresence(tb.samAg.token, api.PresenceNoSession)
	s.clock.Advance(6 * 24 * time.Hour)
	s.setPresence(tb.samAg.token, api.PresenceNoSession)
	s.clock.Advance(2 * 24 * time.Hour)

	preview := s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 604800, "dry_run": true}, "")
	s.want(preview, 200, "")
	listed := agentsIn(preview)
	if len(listed) != 1 || listed[0]["id"] != tb.samAg.id || listed[0]["disconnected_since"] != "2026-10-01T16:00:00Z" {
		t.Fatalf("the preview on day 8: %s", preview.raw)
	}
}
