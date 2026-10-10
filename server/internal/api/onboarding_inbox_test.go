package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestBoardAddInboxProvidesIssuerBoundPromptWithoutWakingAgents(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name, writer, _ := s.pair("starter")
	status, raw := s.raw("POST", "/v1/boards/"+name+"/people", s.owner, map[string]string{"handle": "maya"})
	if status != 201 && status != 200 {
		t.Fatalf("add: %d %s", status, raw)
	}
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", maya, nil)
	if status != 200 {
		t.Fatalf("Inbox: %d %s", status, raw)
	}
	var inbox struct {
		Adds []struct {
			Command string `json:"join_command"`
			Prompt  string `json:"join_prompt"`
		} `json:"board_adds"`
	}
	if err := json.Unmarshal([]byte(raw), &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox.Adds) != 1 || !strings.Contains(inbox.Adds[0].Command, name) || !strings.Contains(inbox.Adds[0].Command, "--server") || !strings.Contains(inbox.Adds[0].Prompt, inbox.Adds[0].Command) {
		t.Fatalf("join handover: %s", raw)
	}
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", writer, nil)
	if status != 403 {
		t.Fatalf("agent Inbox: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/me/onboarding-inbox", "", s.browserToken(maya))
	if status != 200 || !strings.Contains(raw, name) {
		t.Fatalf("browser Inbox: %d %s", status, raw)
	}
	// A join is the person's explicit choice; merely reading this item made no seat.
	joined, err := s.client(maya).JoinWithResponse(context.Background(), nil, api.JoinRequest{Board: &name, Role: ptr("writer")})
	mustStatus(t, joined, err, 201)
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", maya, nil)
	if status != 200 || !strings.Contains(raw, `"board_adds":[]`) {
		t.Fatalf("joined Inbox: %d %s", status, raw)
	}
}

func TestInviteArrivalInboxUsesCurrentVisibleMembershipAndExpires(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	ids := []string{b.JSON200.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &ids})
	mustStatus(t, inv, err, 201)
	connected := s.connect(inv.JSON201.Invite, "maya")
	mustStatus(t, connected, nil, 201)
	token := connected.JSON201.Key.Token
	status, raw := s.raw("GET", "/v1/me/onboarding-inbox", s.owner, nil)
	if status != 200 || !strings.Contains(raw, `"arrivals":[]`) {
		t.Fatalf("no agent: %d %s", status, raw)
	}
	harness := "codex"
	joined, err := s.client(token).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &name, Role: ptr("writer"), Harness: &harness})
	mustStatus(t, joined, err, 201)
	for range 2 {
		status, raw = s.raw("GET", "/v1/me/onboarding-inbox", s.owner, nil)
		if status != 200 || !strings.Contains(raw, `"handle":"maya"`) || !strings.Contains(raw, joined.JSON201.Agent.Id) || strings.Contains(raw, inv.JSON201.Invite) {
			t.Fatalf("arrival: %d %s", status, raw)
		}
	}
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", token, nil)
	if status != 200 || !strings.Contains(raw, `"arrivals":[]`) {
		t.Fatalf("foreign arrivals: %d %s", status, raw)
	}
	s.clock.Advance(7*24*time.Hour + time.Second)
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", s.owner, nil)
	if status != 200 || !strings.Contains(raw, `"arrivals":[]`) {
		t.Fatalf("old arrival: %d %s", status, raw)
	}
}

func TestArrivalInboxDropsAPrivateBoardWhenTheInviterLeaves(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	status, raw := s.raw("POST", "/v1/boards/"+name+"/visibility", s.owner, map[string]string{"visibility": "private"})
	if status != 200 {
		t.Fatalf("private: %d %s", status, raw)
	}
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	ids := []string{b.JSON200.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &ids})
	mustStatus(t, inv, err, 201)
	connected := s.connect(inv.JSON201.Invite, "maya")
	mustStatus(t, connected, nil, 201)
	joined, err := s.client(connected.JSON201.Key.Token).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &name, Role: ptr("writer")})
	mustStatus(t, joined, err, 201)
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", s.owner, nil)
	if status != 200 || !strings.Contains(raw, joined.JSON201.Agent.Id) {
		t.Fatalf("before leave: %d %s", status, raw)
	}
	status, raw = s.raw("POST", "/v1/boards/"+name+"/owners", s.owner, map[string]string{"handle": "maya"})
	if status != 200 {
		t.Fatalf("owner: %d %s", status, raw)
	}
	status, raw = s.raw("POST", "/v1/boards/"+name+"/leave", s.owner, nil)
	if status != 200 {
		t.Fatalf("leave: %d %s", status, raw)
	}
	status, raw = s.raw("GET", "/v1/me/onboarding-inbox", s.owner, nil)
	if status != 200 || strings.Contains(raw, b.JSON200.Id) || strings.Contains(raw, joined.JSON201.Agent.Id) || !strings.Contains(raw, `"arrivals":[]`) {
		t.Fatalf("off-board admin: %d %s", status, raw)
	}
}
