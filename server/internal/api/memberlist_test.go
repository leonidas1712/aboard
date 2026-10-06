package api_test

import "testing"

// members lists a board's members as token reads them.
func (s *testServer) members(token, board string, removed bool) []map[string]any {
	s.t.Helper()
	path := "/v1/boards/" + board + "/members"
	if removed {
		path += "?removed=true"
	}
	a := s.call("GET", path, token, nil, "")
	s.want(a, 200, "")
	list, _ := a.body["members"].([]any)
	out := []map[string]any{}
	for _, v := range list {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

// The members list says which agents a person may remove, and with removed=true also
// lists the agents whose seats ended, apart from the others.
func TestTheMembersListShowsWhoMayRemoveAndTheRemoved(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	can := func(token string) map[string]any {
		out := map[string]any{}
		for _, m := range s.members(token, tb.board, false) {
			if name, _ := m["name"].(string); m["kind"] == "agent" {
				out[name] = m["can_remove"]
			}
		}
		return out
	}
	if got := can(tb.sam); got[tb.samAg.name] != true || got[tb.mayaAgent.name] != false {
		t.Fatalf("sam may remove: %v", got)
	}
	if got := can(s.owner); got[tb.samAg.name] != true || got[tb.mayaAgent.name] != true {
		t.Fatalf("the board's owner may remove: %v", got)
	}
	if got := can(tb.samAg.token); got[tb.samAg.name] != nil {
		t.Fatalf("an agent is told what it may remove: %v", got)
	}
	s.want(s.removeAgent(tb.sam, tb.board, tb.samAg.name, ""), 200, "")
	for _, m := range s.members(tb.maya, tb.board, false) {
		if m["name"] == tb.samAg.name {
			t.Fatalf("the removed agent is listed without removed=true: %v", m)
		}
	}
	found := false
	for _, m := range s.members(tb.maya, tb.board, true) {
		if m["name"] == tb.samAg.name {
			found = true
			if m["status"] != "removed" || m["removed_by"] != "person" || m["removed_at"] == nil || m["can_remove"] != nil {
				t.Fatalf("the removed agent: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("removed=true doesn't list the removed agent")
	}
}
