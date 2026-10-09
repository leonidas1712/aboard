package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestOwnAgentLocationIsPrivateAcrossBoardsAndNeverMovesTheRecord(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	first, second := s.newBoard(), s.newBoard()
	maya := s.addHuman("maya")
	a := s.joinBoard(maya, first, "writer", nil)
	b := s.joinBoard(maya, second, "reviewer", nil)
	before, err := s.client(a.JSON201.Token).GetBoardWithResponse(ctx, first)
	mustStatus(t, before, err, 200)
	report := api.AgentLocationReport{Harness: "codex", SessionId: "codex:conversation-one", Folder: "/work/private-project"}
	recorded, err := s.client(a.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, recorded, err, 200)
	if recorded.JSON200.Machine == nil || *recorded.JSON200.Machine != "laptop" || !recorded.JSON200.LastActive.Equal(s.clock.Now()) {
		t.Fatalf("machine or time was not server-derived: %s", recorded.Body)
	}
	for _, token := range []string{maya, a.JSON201.Token, b.JSON201.Token, s.browserToken(maya)} {
		found, e := s.client(token).ListOwnAgentsWithResponse(ctx)
		mustStatus(t, found, e, 200)
		if len(found.JSON200.Agents) != 2 {
			t.Fatalf("own agents across boards: %s", found.Body)
		}
		for _, agent := range found.JSON200.Agents {
			if agent.Owner == nil || *agent.Owner != "maya" {
				t.Fatalf("another person's agent: %s", found.Body)
			}
			if agent.Id == a.JSON201.Agent.Id && (agent.Location == nil || agent.Location.Folder != report.Folder || agent.Location.SessionId != report.SessionId) {
				t.Fatalf("own location missing: %s", found.Body)
			}
			if agent.Id == b.JSON201.Agent.Id && agent.Location != nil {
				t.Fatalf("unreported location invented: %s", found.Body)
			}
		}
	}
	foreign, err := s.client(s.owner).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, foreign, err, 200)
	if len(foreign.JSON200.Agents) != 0 {
		t.Fatalf("admin learned another person's agents: %s", foreign.Body)
	}
	for _, token := range []string{s.owner, maya, a.JSON201.Token} {
		members, e := s.client(token).ListMembersWithResponse(ctx, first, nil)
		mustStatus(t, members, e, 200)
		for _, member := range members.JSON200.Members {
			if member.Id != a.JSON201.Agent.Id {
				continue
			}
			if token == s.owner && member.Location != nil {
				t.Fatalf("admin learned private location: %s", members.Body)
			}
			if token != s.owner && member.Location == nil {
				t.Fatalf("owner's member projection lost location: %s", members.Body)
			}
		}
	}
	after, err := s.client(a.JSON201.Token).GetBoardWithResponse(ctx, first)
	mustStatus(t, after, err, 200)
	if before.JSON200.HeadSeq != after.JSON200.HeadSeq || before.JSON200.ReadUpTo == nil || after.JSON200.ReadUpTo == nil || *before.JSON200.ReadUpTo != *after.JSON200.ReadUpTo {
		t.Fatalf("location moved event log or read cursor: before %s after %s", before.Body, after.Body)
	}
	s.clock.Advance(time.Minute)
	report.Folder = "/work/another-project"
	updated, err := s.client(a.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, updated, err, 200)
	if !updated.JSON200.LastActive.Equal(s.clock.Now()) || updated.JSON200.Folder != report.Folder {
		t.Fatalf("last report not updated: %s", updated.Body)
	}
}

func TestLocationReportsRequireTheOwnActiveAgentAndHideEndedSeats(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	report := api.AgentLocationReport{Harness: "codex", SessionId: "codex:conversation-one", Folder: "/work/private-project"}
	for _, token := range []string{s.owner, s.browserToken(s.owner)} {
		refused, err := s.client(token).SetAgentLocationWithResponse(ctx, nil, report)
		if errorCode(t, refused, err, 403) != "agent_token_required" {
			t.Fatalf("person report: %s", refused.Body)
		}
	}
	recorded, err := s.client(writer).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, recorded, err, 200)
	status, raw := s.raw("PUT", "/v1/me/location", reviewer, map[string]any{"harness": "codex", "session_id": "fake", "folder": "/fake", "machine": "forged", "agent_id": "writer"})
	if status != 400 {
		t.Fatalf("body accepted target or machine authority: %d %s", status, raw)
	}
	removed, err := s.client(s.owner).RemoveAgentWithResponse(ctx, name, "writer", nil)
	mustStatus(t, removed, err, 200)
	ended, err := s.client(writer).SetAgentLocationWithResponse(ctx, nil, report)
	if errorCode(t, ended, err, 403) != "agent_removed" {
		t.Fatalf("removed agent report: %s", ended.Body)
	}
	found, err := s.client(reviewer).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, found, err, 200)
	if len(found.JSON200.Agents) != 1 || found.JSON200.Agents[0].Name != "reviewer" {
		t.Fatalf("ended seat returned: %s", found.Body)
	}
	members, err := s.client(s.owner).ListMembersWithResponse(ctx, name, &api.ListMembersParams{Removed: ptr(true)})
	mustStatus(t, members, err, 200)
	if strings.Contains(string(members.Body), report.Folder) || strings.Contains(string(members.Body), report.SessionId) {
		t.Fatalf("ended location remained exposed: %s", members.Body)
	}
}
