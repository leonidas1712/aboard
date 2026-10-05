package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// setDelivery sets an agent's delivery mode as the holder of token.
func (s *testServer) setDelivery(token, boardName, agent, mode string) *api.SetDeliveryModeResponse {
	s.t.Helper()
	r, err := s.client(token).SetDeliveryModeWithResponse(context.Background(), boardName, agent, nil,
		api.SetDeliveryModeJSONRequestBody{Mode: api.DeliveryModeSetting(mode)})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// memberNamed returns a board's member as the holder of token lists it.
func (s *testServer) memberNamed(token, boardName, name string) api.Member {
	s.t.Helper()
	r, err := s.client(token).ListMembersWithResponse(context.Background(), boardName)
	mustStatus(s.t, r, err, 200)
	for _, m := range r.JSON200.Members {
		if m.Name == name {
			return m
		}
	}
	s.t.Fatalf("no member %s on %s: %s", name, boardName, r.Body)
	return api.Member{}
}

// A board where alex (the server's admin and the board's owner), maya and sam are on
// it, and maya has an agent. It returns the board, the agent's name and its token.
func (s *testServer) boardWithMayasAgent(maya, sam string) (boardName, agent, agentToken string) {
	s.t.Helper()
	boardName = s.newBoard()
	for _, who := range []struct{ token, handle string }{{maya, "maya"}, {sam, "sam"}} {
		if st, body := s.raw("POST", "/v1/boards/"+boardName+"/people", who.token, map[string]any{"handle": who.handle}); st != 201 {
			s.t.Fatalf("%s joins: %d %s", who.handle, st, body)
		}
	}
	agent, agentToken = s.joinAs(maya, boardName, "reviewer", "claude-code")
	return boardName, agent, agentToken
}

// The server holds an agent's delivery mode, and only the agent's person changes it:
// not the agent itself, not another person on the board, not the board's owner and not
// the server's admin. A change is in the record with who made it; setting the same mode
// again changes nothing. The member, the agent's own view of itself and its inbox all
// carry the mode and its revision.
func TestOnlyAnAgentsPersonSetsItsDeliveryMode(t *testing.T) {
	s := newTestServer(t)
	maya, sam, lee := s.addHuman("maya"), s.addHuman("sam"), s.addHuman("lee")
	b, agent, agentToken := s.boardWithMayasAgent(maya, sam)

	m := s.memberNamed(maya, b, agent)
	if m.DeliveryMode == nil || *m.DeliveryMode != "focused" || m.DeliveryRevision == nil || *m.DeliveryRevision != 0 {
		t.Fatalf("an agent never set is focused at revision 0: %+v", m)
	}

	for who, tc := range map[string]struct {
		token, code string
		status      int
	}{
		"the agent":                   {agentToken, "human_token_required", 403},
		"another person on the board": {sam, "agent_owner_required", 403},
		"the board owner and admin":   {s.owner, "agent_owner_required", 403},
		"a person not on the board":   {lee, "not_on_board", 403},
	} {
		r := s.setDelivery(tc.token, b, agent, "off")
		if code := errorCode(t, r, nil, tc.status); code != tc.code {
			t.Errorf("%s sets the mode: %s, want %s", who, code, tc.code)
		}
	}
	if r := s.setDelivery(sam, b, agent, "off"); !strings.Contains(string(r.Body), "aboard delivery off --as "+agent) {
		t.Errorf("the refusal should name the command for maya: %s", r.Body)
	}

	r := s.setDelivery(maya, b, agent, "off")
	mustStatus(t, r, nil, 200)
	got := *r.JSON200
	if got.Board != b || got.Agent != agent || got.Mode != "off" || !got.Changed || got.Revision == 0 {
		t.Fatalf("maya sets off: %+v", got)
	}
	again := s.setDelivery(maya, b, agent, "off")
	mustStatus(t, again, nil, 200)
	if again.JSON200.Changed || again.JSON200.Revision != got.Revision {
		t.Fatalf("setting the same mode again: %+v, want unchanged at %d", *again.JSON200, got.Revision)
	}

	evs, _ := s.eventsOf(maya, b)
	var changes []map[string]any
	for _, e := range evs {
		if e.Type != "agent.delivery_changed" {
			continue
		}
		if e.Actor.Name == nil || *e.Actor.Name != "maya" || e.Actor.Kind != "human" || int(e.Seq) != got.Revision {
			t.Errorf("change event: actor %+v at seq %d, want maya at %d", e.Actor, e.Seq, got.Revision)
		}
		var data map[string]any
		if err := json.Unmarshal(e.Data, &data); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, data)
	}
	if len(changes) != 1 || changes[0]["name"] != agent || changes[0]["before"] != "focused" || changes[0]["after"] != "off" ||
		changes[0]["member_id"] != s.memberNamed(maya, b, agent).Id {
		t.Fatalf("the record should hold one change, focused to off, by the agent's seat: %v", changes)
	}

	m = s.memberNamed(sam, b, agent)
	if *m.DeliveryMode != "off" || *m.DeliveryRevision != got.Revision || m.Delivery != nil {
		t.Errorf("member as sam sees it: mode %v revision %v reported %v", *m.DeliveryMode, *m.DeliveryRevision, m.Delivery)
	}
	me, err := s.client(agentToken).GetMeWithResponse(context.Background())
	mustStatus(t, me, err, 200)
	if me.JSON200.DeliveryMode == nil || *me.JSON200.DeliveryMode != "off" || *me.JSON200.DeliveryRevision != got.Revision {
		t.Errorf("the agent's /v1/me: %s", me.Body)
	}
	in, err := s.client(agentToken).GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, in, err, 200)
	if in.JSON200.DeliveryMode == nil || *in.JSON200.DeliveryMode != "off" || *in.JSON200.DeliveryRevision != got.Revision {
		t.Errorf("the agent's inbox: %s", in.Body)
	}

	// A browser acts as its person.
	browser := s.browserToken(maya)
	if r := s.setDelivery(browser, b, agent, "humans"); r.StatusCode() != 200 || !r.JSON200.Changed || r.JSON200.Revision <= got.Revision {
		t.Errorf("maya's browser sets humans: %d %s", r.StatusCode(), r.Body)
	}

	for name, code := range map[string]string{"nobody": "agent_not_found", "maya": "agent_not_found"} {
		if c := errorCode(t, s.setDelivery(maya, b, name, "off"), nil, 404); c != code {
			t.Errorf("setting the mode of %s: %s, want %s", name, c, code)
		}
	}
	if st, body := s.raw("PUT", "/v1/boards/"+b+"/members/"+agent+"/delivery", maya, map[string]any{"mode": "auto"}); st < 400 || st >= 500 || !strings.Contains(body, "invalid_request") {
		t.Errorf("a mode that isn't one: %d %s", st, body)
	}
}

// The board view sets a mode with the person's browser session, whose writes need the
// session's CSRF token, as every write with a cookie does.
func TestABrowserSessionSetsTheModeWithItsCSRFToken(t *testing.T) {
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	b, agent, _ := s.boardWithMayasAgent(maya, sam)
	page := s.cookieBrowser(maya)
	path := "/v1/boards/" + b + "/members/" + agent + "/delivery"
	forged := s.send(http.MethodPut, path, map[string]any{"mode": "off"}, func(r *http.Request) {
		r.AddCookie(page.cookie)
		s.fromPage(r)
	})
	if forged.status != http.StatusForbidden || forged.code() != "csrf_token_invalid" {
		t.Fatalf("a write without the CSRF token: %d %s", forged.status, forged.raw)
	}
	if got := page.do(http.MethodPut, path, map[string]any{"mode": "off"}); got.status != http.StatusOK || got.body["mode"] != "off" {
		t.Fatalf("the page sets off: %d %s", got.status, got.raw)
	}
	if got := s.cookieBrowser(sam).do(http.MethodPut, path, map[string]any{"mode": "all"}); got.code() != "agent_owner_required" {
		t.Fatalf("sam's page sets maya's agent's mode: %d %s", got.status, got.raw)
	}
}

// Setting a mode accepts an Idempotency-Key: the same request again gets the same answer.
func TestSettingADeliveryModeIsIdempotent(t *testing.T) {
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	b, agent, _ := s.boardWithMayasAgent(maya, sam)
	key := api.IdempotencyKey("mode-1")
	put := func() *api.SetDeliveryModeResponse {
		r, err := s.client(maya).SetDeliveryModeWithResponse(context.Background(), b, agent,
			&api.SetDeliveryModeParams{IdempotencyKey: &key}, api.SetDeliveryModeJSONRequestBody{Mode: "all"})
		mustStatus(t, r, err, 200)
		return r
	}
	first, second := put(), put()
	if !bytes.Equal(first.Body, second.Body) || !second.JSON200.Changed {
		t.Fatalf("a repeated request should get the first answer: %s then %s", first.Body, second.Body)
	}
}
