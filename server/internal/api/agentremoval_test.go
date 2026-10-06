package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// seat is an agent on a board: its token, name and member id.
type seat struct{ token, name, id string }

// agentOf joins an agent of the person whose key is token to board with a pairing code
// they make themselves; they must already be on the board.
func (s *testServer) agentOf(token, board string) seat {
	s.t.Helper()
	ctx := context.Background()
	code, err := s.client(token).CreateJoinCodeWithResponse(ctx, board, nil, api.CreateJoinCodeRequest{Role: "member"})
	mustStatus(s.t, code, err, 201)
	j, err := s.client(token).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(s.t, j, err, 201)
	return seat{j.JSON201.Token, j.JSON201.Agent.Name, j.JSON201.Agent.Id}
}

// teamBoard is an open board alex owns, with maya and sam on it as members and an agent
// of each.
type teamBoard struct {
	board            string
	maya, sam        string
	mayaAgent, samAg seat
}

func (s *testServer) teamBoard() teamBoard {
	s.t.Helper()
	tb := teamBoard{board: s.openBoard(s.owner), maya: s.addHuman("maya"), sam: s.addHuman("sam")}
	for _, h := range []string{"maya", "sam"} {
		s.want(s.call("POST", "/v1/boards/"+tb.board+"/people", s.owner, map[string]any{"handle": h}, ""), 201, "")
	}
	tb.mayaAgent, tb.samAg = s.agentOf(tb.maya, tb.board), s.agentOf(tb.sam, tb.board)
	return tb
}

// agentsIn returns the agents an answer lists.
func agentsIn(a answer) []map[string]any {
	list, _ := a.body["agents"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

func (s *testServer) removeAgent(token, board, agent, key string) answer {
	s.t.Helper()
	return s.call("DELETE", "/v1/boards/"+board+"/members/"+agent, token, nil, key)
}

// Who may remove which agent: its own person, the board's owners, and a server admin;
// another member is told to ask an owner, and no agent removes another.
func TestWhoMayRemoveAnAgent(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()

	s.want(s.removeAgent(tb.sam, tb.board, tb.mayaAgent.name, ""), 403, "owner_required")
	s.want(s.removeAgent(tb.samAg.token, tb.board, tb.mayaAgent.name, ""), 403, "human_token_required")
	s.want(s.removeAgent(s.delegate(tb.sam), tb.board, tb.mayaAgent.name, ""), 403, "forbidden")
	s.want(s.removeAgent(tb.sam, tb.board, "maya", ""), 404, "agent_not_found")
	s.want(s.removeAgent(tb.sam, tb.board, "nobody", ""), 404, "agent_not_found")

	own := s.removeAgent(tb.maya, tb.board, tb.mayaAgent.name, "")
	s.want(own, 200, "")
	if own.str("removed_by") != "person" || own.str("status") != "removed" || own.str("name") != tb.mayaAgent.name ||
		own.str("board") != tb.board || own.str("owner") != "maya" || own.str("id") != tb.mayaAgent.id {
		t.Fatalf("maya removing her own agent: %s", own.raw)
	}
	s.want(s.removeAgent(tb.maya, tb.board, tb.mayaAgent.name, ""), 404, "agent_not_found")

	byOwner := s.removeAgent(s.owner, tb.board, tb.samAg.id, "")
	s.want(byOwner, 200, "")
	if byOwner.str("removed_by") != "board_owner" {
		t.Fatalf("the board's owner removing sam's agent: %s", byOwner.raw)
	}
	evs := s.eventsAfter(s.owner, tb.board, 0)
	last := evs[len(evs)-1]
	data, _ := last["data"].(map[string]any)
	actor, _ := last["actor"].(map[string]any)
	if last["type"] != "agent.removed" || data["member_id"] != tb.samAg.id || data["owner"] != "sam" ||
		data["removed_by"] != "board_owner" || actor["name"] != "alex" {
		t.Fatalf("the record of the owner's removal: %v", last)
	}
}

// A server admin removes an agent on a private board they aren't on only by the board's
// id and the agent's member id, and learns no names doing it.
func TestAnAdminRemovesAnAgentOnAHiddenBoardByIDOnly(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name := s.privateBoard(maya)
	agent := s.agentOf(maya, name)
	b := s.call("GET", "/v1/boards/"+name, maya, nil, "")
	boardID := b.str("id")

	s.want(s.removeAgent(s.owner, name, agent.name, ""), 404, "board_not_found")
	byName := s.removeAgent(s.owner, boardID, agent.name, "")
	s.want(byName, 404, "agent_not_found")
	if strings.Contains(byName.raw, name) {
		t.Fatalf("the refusal names the hidden board: %s", byName.raw)
	}
	r := s.removeAgent(s.owner, boardID, agent.id, "")
	s.want(r, 200, "")
	if r.body["board"] != nil || r.body["name"] != nil || r.str("board_id") != boardID || r.str("removed_by") != "admin" {
		t.Fatalf("the admin's removal on a hidden board: %s", r.raw)
	}
	evs := s.eventsAfter(maya, name, 0)
	last := evs[len(evs)-1]
	actor, _ := last["actor"].(map[string]any)
	if last["type"] != "agent.removed" || actor["member_id"] != nil || actor["name"] != "alex" {
		t.Fatalf("the record names the admin who removed it: %v", last)
	}
	// Another member can't use the id to reach a private board they aren't on.
	sam := s.addHuman("sam")
	s.want(s.removeAgent(sam, boardID, agent.id, ""), 404, "board_not_found")
}

// A removed agent's token is refused on every path with agent_removed, saying when and
// by what kind of person, never who; its messages stay on the board under its id.
func TestARemovedAgentIsToldOnEveryPathAndItsRecordStays(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	ag := tb.mayaAgent
	s.want(s.call("POST", "/v1/boards/"+tb.board+"/messages", ag.token, map[string]any{"body": "my findings"}, ""), 201, "")
	s.want(s.removeAgent(s.owner, tb.board, ag.name, ""), 200, "")

	for _, p := range []struct{ method, path string }{
		{"GET", "/v1/me"},
		{"GET", "/v1/me/inbox"},
		{"GET", "/v1/boards"},
		{"GET", "/v1/boards/" + tb.board},
		{"GET", "/v1/boards/" + tb.board + "/messages"},
		{"POST", "/v1/boards/" + tb.board + "/messages"},
		{"PUT", "/v1/me/presence"},
		{"POST", "/v1/me/leave"},
	} {
		var body any
		switch p.method {
		case "POST":
			body = map[string]any{"body": "still here?"}
		case "PUT":
			body = map[string]any{"presence": "idle"}
		}
		if p.path == "/v1/me/leave" {
			body = nil
		}
		a := s.call(p.method, p.path, ag.token, body, "")
		s.want(a, 403, "agent_removed")
		if !strings.Contains(a.str("error", "message"), "was removed from "+tb.board+" by an owner of the board on 2026-10-01") ||
			!strings.Contains(a.str("error", "hint"), "aboard join --board "+tb.board) ||
			a.str("error", "details", "removed_by") != "board_owner" || a.str("error", "details", "board") != tb.board {
			t.Fatalf("%s %s: %s", p.method, p.path, a.raw)
		}
	}
	timeline := s.call("GET", "/v1/boards/"+tb.board+"/messages", s.owner, nil, "")
	if !strings.Contains(timeline.raw, "my findings") || !strings.Contains(timeline.raw, `"name":"`+ag.name+`"`) {
		t.Fatalf("the removed agent's message left the record: %s", timeline.raw)
	}
	if types := types(s.eventsAfter(s.owner, tb.board, 0)); types[len(types)-1] != "agent.removed" {
		t.Fatalf("the removal's events: %v", types)
	}
}

// Re-adding a person never revives an agent of theirs that was removed: the old token
// is refused, and a new agent is a new seat with another name.
func TestReaddingAPersonNeverRevivesARemovedAgent(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	s.want(s.removeAgent(tb.sam, tb.board, tb.samAg.name, ""), 200, "")
	s.want(s.call("DELETE", "/v1/boards/"+tb.board+"/people/sam", s.owner, nil, ""), 200, "")
	s.want(s.call("POST", "/v1/boards/"+tb.board+"/people", s.owner, map[string]any{"handle": "sam"}, ""), 201, "")

	old := s.call("GET", "/v1/me/inbox", tb.samAg.token, nil, "")
	s.want(old, 403, "agent_removed")
	if old.str("error", "details", "removed_by") != "person" {
		t.Fatalf("the old token after sam came back: %s", old.raw)
	}
	again := s.agentOf(tb.sam, tb.board)
	if again.id == tb.samAg.id || again.name == tb.samAg.name {
		t.Fatalf("the new agent took the removed seat: %+v, removed %+v", again, tb.samAg)
	}
	s.want(s.call("GET", "/v1/me/inbox", again.token, nil, ""), 200, "")
	s.want(s.call("GET", "/v1/me/inbox", tb.samAg.token, nil, ""), 403, "agent_removed")
}

// An agent may leave by itself, removing only its own seat, recorded as left.
func TestAnAgentLeavesItsOwnSeat(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	s.want(s.call("POST", "/v1/me/leave", tb.sam, nil, ""), 403, "agent_token_required")

	left := s.call("POST", "/v1/me/leave", tb.samAg.token, nil, "leave-1")
	s.want(left, 200, "")
	if left.str("status") != "left" || left.str("removed_by") != "self" || left.str("board") != tb.board {
		t.Fatalf("leaving: %s", left.raw)
	}
	again := s.call("POST", "/v1/me/leave", tb.samAg.token, nil, "leave-1")
	if again.status != 200 || again.raw != left.raw || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("the repeat with the same key: %d %s", again.status, again.raw)
	}
	gone := s.call("GET", "/v1/me", tb.samAg.token, nil, "")
	s.want(gone, 403, "agent_removed")
	if !strings.Contains(gone.str("error", "message"), tb.samAg.name+" left "+tb.board) || gone.str("error", "details", "removed_by") != "self" {
		t.Fatalf("the token after leaving: %s", gone.raw)
	}
	evs := s.eventsAfter(s.owner, tb.board, 0)
	last := evs[len(evs)-1]
	actor, _ := last["actor"].(map[string]any)
	if last["type"] != "agent.left" || actor["member_id"] != tb.samAg.id {
		t.Fatalf("the record of leaving: %v", last)
	}
	s.want(s.call("GET", "/v1/me/inbox", tb.mayaAgent.token, nil, ""), 200, "")
}

// Removing an agent ends its waiting inbox read at once, with agent_removed.
func TestRemovalEndsTheAgentsWaitingInbox(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	done := make(chan answer, 1)
	go func() { done <- s.call("GET", "/v1/me/inbox?wait=60", tb.samAg.token, nil, "") }()
	// The wait can't be observed from outside; removing before it starts gives the same
	// answer, so the test holds either way.
	s.want(s.removeAgent(tb.sam, tb.board, tb.samAg.name, ""), 200, "")
	select {
	case a := <-done:
		s.want(a, 403, "agent_removed")
	case <-time.After(10 * time.Second):
		t.Fatal("the inbox kept waiting after its agent was removed")
	}
}

// Removal racing the agent's own writes and reads: every post either landed before the
// removal in the record or was refused with agent_removed, and none lands after it.
func TestRemovalRacingTheAgentsWritesAndReads(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	const n = 40
	var wg sync.WaitGroup
	results := make(chan answer, 2*n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			results <- s.call("POST", "/v1/boards/"+tb.board+"/messages", tb.samAg.token, map[string]any{"body": fmt.Sprintf("note %d", i)}, "")
		}()
		go func() {
			defer wg.Done()
			<-start
			results <- s.call("GET", "/v1/boards/"+tb.board+"/messages", tb.samAg.token, nil, "")
		}()
	}
	close(start)
	removed := s.removeAgent(tb.sam, tb.board, tb.samAg.name, "")
	wg.Wait()
	close(results)
	s.want(removed, 200, "")
	posted := 0
	for a := range results {
		switch {
		case a.status == 201:
			posted++
		case a.status == 200:
		case a.status == 403 && a.code() == "agent_removed":
		default:
			t.Fatalf("a racing request: %d %s", a.status, a.raw)
		}
	}
	evs := s.eventsAfter(s.owner, tb.board, 0)
	removalAt, inRecord := -1, 0
	for i, e := range evs {
		data, _ := e["data"].(map[string]any)
		switch {
		case e["type"] == "agent.removed" && data["member_id"] == tb.samAg.id:
			removalAt = i
		case e["type"] == "message.posted":
			actor, _ := e["actor"].(map[string]any)
			if actor["member_id"] != tb.samAg.id {
				continue
			}
			inRecord++
			if removalAt >= 0 {
				t.Fatalf("a post landed after the removal: %v", e)
			}
		}
	}
	if removalAt < 0 || inRecord != posted {
		t.Fatalf("removal at %d, %d posts in the record, %d answered 201", removalAt, inRecord, posted)
	}
	s.want(s.call("POST", "/v1/boards/"+tb.board+"/messages", tb.samAg.token, map[string]any{"body": "late"}, ""), 403, "agent_removed")
}

// A repeat of a removal with the same Idempotency-Key gets the first answer again.
func TestARemovalRepeatGetsTheSameAnswer(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	first := s.removeAgent(tb.sam, tb.board, tb.samAg.name, "rm-1")
	s.want(first, 200, "")
	again := s.removeAgent(tb.sam, tb.board, tb.samAg.name, "rm-1")
	if again.status != 200 || again.raw != first.raw || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("the repeat: %d %s", again.status, again.raw)
	}
	s.want(s.removeAgent(tb.sam, tb.board, tb.samAg.name, "rm-2"), 404, "agent_not_found")
}

// Prune lists the person's own agents disconnected for at least the time asked, skips
// agents whose presence is unknown, and removes only what is still disconnected when it
// removes it.
func TestPruneRemovesOnlyAgentsStillDisconnected(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	away, back, unknown := tb.samAg, s.agentOf(tb.sam, tb.board), s.agentOf(tb.sam, tb.board)
	working := s.agentOf(tb.sam, tb.board)
	for _, a := range []seat{away, back, working} {
		s.setPresence(a.token, api.PresenceNoSession)
	}
	s.setPresence(tb.mayaAgent.token, api.PresenceNoSession) // maya's, never sam's to prune
	_ = unknown
	s.clock.Advance(8 * 24 * time.Hour)
	s.setPresence(working.token, api.PresenceWorking) // connected again now

	s.want(s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 60, "dry_run": true}, ""), 400, "invalid_request")
	s.want(s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 604800}, ""), 422, "invalid_request")
	s.want(s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 604800, "all": true, "dry_run": true}, ""), 403, "server_admin_required")
	s.want(s.call("POST", "/v1/agents/prune", tb.samAg.token, map[string]any{"disconnected_for": 604800, "dry_run": true}, ""), 403, "human_token_required")

	preview := s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 604800, "dry_run": true}, "")
	s.want(preview, 200, "")
	listed := map[string]bool{}
	ids := []string{}
	for _, m := range agentsIn(preview) {
		name, _ := m["name"].(string)
		id, _ := m["id"].(string)
		listed[name] = true
		ids = append(ids, id)
	}
	if len(listed) != 2 || !listed[away.name] || !listed[back.name] {
		t.Fatalf("the preview: %s", preview.raw)
	}
	if len(s.eventsAfter(s.owner, tb.board, s.head(s.owner, tb.board))) != 0 {
		t.Fatal("a dry run wrote to the record")
	}
	s.setPresence(back.token, api.PresenceIdle) // reconnects after the preview

	done := s.call("POST", "/v1/agents/prune", tb.sam, map[string]any{"disconnected_for": 604800, "agents": ids}, "")
	s.want(done, 200, "")
	agents, kept := agentsIn(done), done.body["kept"]
	if len(agents) != 1 || agents[0]["id"] != away.id || fmt.Sprint(kept) != "["+back.id+"]" {
		t.Fatalf("the prune: %s", done.raw)
	}
	s.want(s.call("GET", "/v1/me/inbox", away.token, nil, ""), 403, "agent_removed")
	for _, a := range []seat{back, unknown, working, tb.mayaAgent} {
		s.want(s.call("GET", "/v1/me/inbox", a.token, nil, ""), 200, "")
	}
	evs := s.eventsAfter(s.owner, tb.board, 0)
	data, _ := evs[len(evs)-1]["data"].(map[string]any)
	if evs[len(evs)-1]["type"] != "agent.removed" || data["pruned"] != true || data["removed_by"] != "person" {
		t.Fatalf("the prune's record: %v", evs[len(evs)-1])
	}
}

// An admin prunes across the server, without learning the names on private boards they
// aren't on.
func TestAnAdminPrunesAcrossTheServer(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	hidden := s.privateBoard(maya)
	agent := s.agentOf(maya, hidden)
	s.setPresence(agent.token, api.PresenceNoSession)
	s.clock.Advance(8 * 24 * time.Hour)

	mine := s.call("POST", "/v1/agents/prune", s.owner, map[string]any{"disconnected_for": 604800, "dry_run": true}, "")
	s.want(mine, 200, "")
	if len(agentsIn(mine)) != 0 {
		t.Fatalf("the admin's own prune lists maya's agent: %s", mine.raw)
	}
	all := s.call("POST", "/v1/agents/prune", s.owner, map[string]any{"disconnected_for": 604800, "all": true, "dry_run": true}, "")
	s.want(all, 200, "")
	if strings.Contains(all.raw, hidden) || strings.Contains(all.raw, agent.name+"\"") {
		t.Fatalf("the admin's prune names the hidden board or agent: %s", all.raw)
	}
	listed := agentsIn(all)
	if len(listed) != 1 || listed[0]["id"] != agent.id || listed[0]["owner"] != "maya" {
		t.Fatalf("the admin's prune: %s", all.raw)
	}
	done := s.call("POST", "/v1/agents/prune", s.owner, map[string]any{"disconnected_for": 604800, "all": true, "agents": []string{agent.id}}, "")
	s.want(done, 200, "")
	gone := s.call("GET", "/v1/me", agent.token, nil, "")
	s.want(gone, 403, "agent_removed")
	if gone.str("error", "details", "removed_by") != "admin" || gone.status != http.StatusForbidden {
		t.Fatalf("maya's agent after the admin's prune: %s", gone.raw)
	}
}
