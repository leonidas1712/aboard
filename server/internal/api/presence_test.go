package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// setPresence reports an agent's presence with its own token.
func (s *testServer) setPresence(token string, p api.Presence) *api.SetPresenceResponse {
	s.t.Helper()
	r, err := s.client(token).SetPresenceWithResponse(context.Background(), nil, api.SetPresenceJSONRequestBody{Presence: p})
	mustStatus(s.t, r, err, 200)
	return r
}

// presenceOf returns a member's presence and since as the board's members list shows
// them, with "null" for a null.
func (s *testServer) presenceOf(token, boardName, member string) (presence, since string) {
	s.t.Helper()
	r, err := s.client(token).ListMembersWithResponse(context.Background(), boardName, nil)
	mustStatus(s.t, r, err, 200)
	for _, m := range r.JSON200.Members {
		if m.Name != member {
			continue
		}
		presence, since = "null", "null"
		if m.Presence != nil {
			presence = string(*m.Presence)
		}
		if m.PresenceSince != nil {
			since = m.PresenceSince.UTC().Format(time.RFC3339)
		}
		return presence, since
	}
	s.t.Fatalf("no member %s on %s", member, boardName)
	return "", ""
}

func at(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// An agent reports its presence with its own token, every member of the board sees it,
// and reporting the same presence again keeps when it began.
func TestAgentReportsPresenceThatMembersSee(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")

	if p, since := s.presenceOf(s.owner, boardName, "writer"); p != "no_session" || since != "null" {
		t.Fatalf("before any report: %s since %s, want no_session since null", p, since)
	}
	if p, since := s.presenceOf(s.owner, boardName, "alex"); p != "null" || since != "null" {
		t.Fatalf("a person's presence: %s since %s, want null", p, since)
	}

	started := s.clock.Now()
	r := s.setPresence(writer, api.PresenceWorking)
	if r.JSON200.Board != boardName || r.JSON200.Agent != "writer" || r.JSON200.Presence != api.PresenceWorking || at(r.JSON200.PresenceSince) != at(started) {
		t.Fatalf("report: %s", bodyOf(r))
	}
	for _, reader := range []string{s.owner, reviewer} {
		if p, since := s.presenceOf(reader, boardName, "writer"); p != "working" || since != at(started) {
			t.Fatalf("writer as another member sees it: %s since %s", p, since)
		}
	}

	s.clock.Advance(time.Minute)
	s.setPresence(writer, api.PresenceWorking)
	if p, since := s.presenceOf(s.owner, boardName, "writer"); p != "working" || since != at(started) {
		t.Fatalf("after renewing: %s since %s, want working since %s", p, since, at(started))
	}
	s.setPresence(writer, api.PresenceIdle)
	if p, since := s.presenceOf(s.owner, boardName, "writer"); p != "idle" || since != at(s.clock.Now()) {
		t.Fatalf("after going idle: %s since %s", p, since)
	}

	// Presence is bookkeeping: the board's log doesn't move.
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, b, err, 200)
	ev, err := s.client(s.owner).ListEventsWithResponse(ctx, boardName, nil)
	mustStatus(t, ev, err, 200)
	for _, e := range ev.JSON200.Events {
		if raw, _ := json.Marshal(e); strings.Contains(string(raw), "presence") {
			t.Fatalf("an event mentions presence: %s", raw)
		}
	}
	if int64(len(ev.JSON200.Events)) != int64(b.JSON200.HeadSeq) {
		t.Fatalf("%d events, head %d", len(ev.JSON200.Events), b.JSON200.HeadSeq)
	}
}

func TestOnlyAgentsReportPresence(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.pair("starter")
	r, err := s.client(s.owner).SetPresenceWithResponse(context.Background(), nil, api.SetPresenceJSONRequestBody{Presence: api.PresenceIdle})
	if c := errorCode(t, r, err, 403); c != "agent_token_required" {
		t.Fatalf("a person reporting presence: %s", c)
	}
	r, err = s.client(s.browserToken(s.owner)).SetPresenceWithResponse(context.Background(), nil, api.SetPresenceJSONRequestBody{Presence: api.PresenceIdle})
	if c := errorCode(t, r, err, 403); c != "agent_token_required" {
		t.Fatalf("a browser reporting presence: %s", c)
	}
}

// A presence that isn't renewed runs out, so an agent whose daemon went away doesn't
// stay working; the next report starts a new presence.
func TestPresenceRunsOutWithoutRenewal(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	boardName, writer, _ := s.pair("starter")
	s.setPresence(writer, api.PresenceWorking)
	last := s.clock.Now()

	s.clock.Advance(3*time.Minute - time.Second)
	if p, _ := s.presenceOf(s.owner, boardName, "writer"); p != "working" {
		t.Fatalf("just before running out: %s", p)
	}
	s.clock.Advance(time.Second)
	if p, since := s.presenceOf(s.owner, boardName, "writer"); p != "no_session" || since != at(last) {
		t.Fatalf("after 3 minutes without a report: %s since %s, want no_session since %s", p, since, at(last))
	}
	s.setPresence(writer, api.PresenceWorking)
	if p, since := s.presenceOf(s.owner, boardName, "writer"); p != "working" || since != at(s.clock.Now()) {
		t.Fatalf("reported again: %s since %s, want working since now", p, since)
	}
}

type presenceEvent struct {
	Board         string  `json:"board"`
	BoardID       string  `json:"board_id"`
	Agent         string  `json:"agent"`
	MemberID      string  `json:"member_id"`
	Presence      string  `json:"presence"`
	PresenceSince *string `json:"presence_since"`
	Delivery      *string `json:"delivery"`
}

// presence returns the next block, failing unless it is a presence event.
func (st *eventStream) presence() presenceEvent {
	st.t.Helper()
	b := st.next()
	data, ok := strings.CutPrefix(b, "event: presence\ndata: ")
	if !ok || strings.Contains(data, "\n") {
		st.t.Fatalf("not a presence event: %q", b)
	}
	var p presenceEvent
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		st.t.Fatalf("presence event data %q: %v", data, err)
	}
	return p
}

// The stream tells the board's people when an agent's presence changes, including when
// it runs out, and says nothing when a report only renews it.
func TestStreamSendsPresenceChanges(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	boardName, writer, _ := s.pair("starter")
	st := s.openStream(s.owner)
	first := st.head()

	s.setPresence(writer, api.PresenceWorking)
	me, err := s.client(writer).GetMeWithResponse(context.Background())
	mustStatus(t, me, err, 200)
	if p := st.presence(); p.Board != boardName || p.BoardID != first.BoardID || p.Agent != "writer" || p.MemberID != me.JSON200.Id ||
		p.Presence != "working" || p.PresenceSince == nil {
		t.Fatalf("presence event: %+v", p)
	}
	s.setPresence(writer, api.PresenceWorking) // renews only
	s.setPresence(writer, api.PresenceIdle)
	if p := st.presence(); p.Agent != "writer" || p.Presence != "idle" {
		t.Fatalf("after going idle: %+v", p)
	}

	// A daemon that reports applying another delivery mode is a change too, so the board
	// view shows what the daemon applies beside what the agent's person set.
	off := api.DeliveryModeOff
	r, err := s.client(writer).SetPresenceWithResponse(context.Background(), nil, api.SetPresenceJSONRequestBody{Presence: api.PresenceIdle, Delivery: &off})
	mustStatus(t, r, err, 200)
	if p := st.presence(); p.Agent != "writer" || p.Presence != "idle" || p.Delivery == nil || *p.Delivery != "off" {
		t.Fatalf("after reporting off: %+v", p)
	}

	// Nothing renews it: the stream notices it ran out at a keepalive.
	s.clock.Advance(3 * time.Minute)
	if p := st.presence(); p.Agent != "writer" || p.Presence != "no_session" {
		t.Fatalf("after running out: %+v", p)
	}
	st.keepalive()
}

// An agent's delivery mode is reported with its presence and shown to the board's
// members; a report without one keeps the mode reported before.
func TestDeliveryModeIsReportedWithPresence(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	modeOf := func(member string) string {
		r, err := s.client(reviewer).ListMembersWithResponse(ctx, boardName, nil)
		mustStatus(t, r, err, 200)
		for _, m := range r.JSON200.Members {
			if m.Name == member {
				if m.Delivery == nil {
					return "null"
				}
				return string(*m.Delivery)
			}
		}
		t.Fatalf("no member %s", member)
		return ""
	}
	if got := modeOf("writer"); got != "null" {
		t.Fatalf("before any report: delivery %s, want null", got)
	}
	humans := api.DeliveryModeHumans
	r, err := s.client(writer).SetPresenceWithResponse(ctx, nil, api.SetPresenceJSONRequestBody{Presence: api.PresenceIdle, Delivery: &humans})
	mustStatus(t, r, err, 200)
	if got := modeOf("writer"); got != "humans" {
		t.Fatalf("after reporting humans: %s", got)
	}
	s.setPresence(writer, api.PresenceWorking)
	if got := modeOf("writer"); got != "humans" {
		t.Fatalf("a report without a mode changed it to %s", got)
	}
	if got := modeOf("alex"); got != "null" {
		t.Fatalf("a person's delivery: %s, want null", got)
	}
	bad := api.DeliveryMode("loud")
	r, err = s.client(writer).SetPresenceWithResponse(ctx, nil, api.SetPresenceJSONRequestBody{Presence: api.PresenceIdle, Delivery: &bad})
	mustStatus(t, r, err, 400)
}
