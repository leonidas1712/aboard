package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// The person who creates a board is its admin; a person who joins because their agent
// did is a member, the log records which, and only an admin may change the policy.
func TestOnlyAdminsChangePolicy(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	code, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, code, err, 201)
	priya := s.addHuman("priya")
	j, err := s.client(priya).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(t, j, err, 201)

	ms, err := s.client(priya).ListMembersWithResponse(ctx, boardName)
	mustStatus(t, ms, err, 200)
	for _, m := range ms.JSON200.Members {
		access := "null"
		if m.Access != nil {
			access = string(*m.Access)
		}
		want := map[string]string{"alex": "admin", "priya": "member"}[m.Name]
		if m.Kind == api.MemberKindAgent {
			want = "null"
		}
		if access != want {
			t.Errorf("%s: access %s, want %s", m.Name, access, want)
		}
	}

	ev, err := s.client(s.owner).ListEventsWithResponse(ctx, boardName, nil)
	mustStatus(t, ev, err, 200)
	var page struct {
		Events []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(ev.Body, &page); err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Events {
		if e.Type != "member.joined" {
			continue
		}
		access, ok := e.Data["access"]
		name, _ := e.Data["name"].(string)
		want := map[string]any{"alex": "admin", "priya": "member"}[name]
		if !ok || access != want {
			t.Errorf("member.joined for %v: access %v (present %v), want %v", e.Data["name"], access, ok, want)
		}
	}

	preset := api.PolicyPreset("recommended")
	change := api.UpdateBoardRequest{Policy: api.PolicyChange{Preset: &preset}}
	u, err := s.client(priya).UpdateBoardWithResponse(ctx, boardName, nil, change)
	if c := errorCode(t, u, err, 403); c != "admin_required" {
		t.Fatalf("policy change by a member: %s", c)
	}
	if !strings.Contains(string(u.Body), "alex") {
		t.Errorf("admin_required doesn't name the admins: %s", u.Body)
	}
	u, err = s.client(writer).UpdateBoardWithResponse(ctx, boardName, nil, change)
	if c := errorCode(t, u, err, 403); c != "human_token_required" {
		t.Fatalf("policy change by an agent: %s", c)
	}
	u, err = s.client(s.owner).UpdateBoardWithResponse(ctx, boardName, nil, change)
	mustStatus(t, u, err, 200)
}
