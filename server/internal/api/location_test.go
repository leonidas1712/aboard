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

func TestGuestLocationLookupStaysOnTheInvitedBoard(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, _ := s.pair("starter")
	code := s.guestCode(s.owner, name, "kim")
	mustStatus(t, code, nil, 201)
	joined := s.guestJoin(*code.JSON201.Code)
	mustStatus(t, joined, nil, 201)
	report := api.AgentLocationReport{Harness: "codex", SessionId: "codex:guest-session", Folder: "/work/guest"}
	saved, err := s.client(joined.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, saved, err, 200)
	other := s.newBoard()
	s.joinAs(s.owner, other, "writer", "")
	for _, token := range []string{joined.JSON201.Token, joined.JSON201.Key.Token} {
		found, e := s.client(token).ListOwnAgentsWithResponse(ctx)
		mustStatus(t, found, e, 200)
		if len(found.JSON200.Agents) != 1 || found.JSON200.Agents[0].Board != name || found.JSON200.Agents[0].Id != joined.JSON201.Agent.Id {
			t.Fatalf("guest lookup escaped: %s", found.Body)
		}
	}
	members, err := s.client(writer).ListMembersWithResponse(ctx, name, nil)
	mustStatus(t, members, err, 200)
	if strings.Contains(string(members.Body), report.Folder) || strings.Contains(string(members.Body), report.SessionId) {
		t.Fatalf("guest location leaked to another person's agent: %s", members.Body)
	}
}

func TestOwnLocationsDisappearWhenAccessOrTheIssuingKeyEnds(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	first, second := s.newBoard(), s.newBoard()
	maya := s.addHuman("maya")
	a := s.joinBoard(maya, first, "writer", nil)
	b := s.joinBoard(maya, second, "reviewer", nil)
	report := api.AgentLocationReport{Harness: "codex", SessionId: "codex:private-session", Folder: "/work/private"}
	saved, err := s.client(a.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, saved, err, 200)
	removed, err := s.client(s.owner).RemovePersonWithResponse(ctx, first, "maya", nil)
	mustStatus(t, removed, err, 200)
	removedCaller, err := s.client(a.JSON201.Token).ListOwnAgentsWithResponse(ctx)
	if errorCode(t, removedCaller, err, 403) != "agent_removed" {
		t.Fatalf("caller outlived owner membership: %s", removedCaller.Body)
	}
	removedReport, err := s.client(a.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	if errorCode(t, removedReport, err, 403) != "agent_removed" {
		t.Fatalf("report outlived owner membership: %s", removedReport.Body)
	}
	found, err := s.client(b.JSON201.Token).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, found, err, 200)
	if len(found.JSON200.Agents) != 1 || found.JSON200.Agents[0].Board != second || strings.Contains(string(found.Body), report.Folder) {
		t.Fatalf("former board location remained visible: %s", found.Body)
	}
	keys, err := s.client(maya).ListKeysWithResponse(ctx, nil)
	mustStatus(t, keys, err, 200)
	_, otherKey := s.newKey(maya, "other-machine")
	revoked, err := s.client(maya).RevokeKeyWithResponse(ctx, keys.JSON200.Keys[0].Id, nil)
	mustStatus(t, revoked, err, 200)
	stillOwner, err := s.client(otherKey).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, stillOwner, err, 200)
	if len(stillOwner.JSON200.Agents) != 0 {
		t.Fatalf("revoked issuing key's seats remain findable: %s", stillOwner.Body)
	}
	ended, err := s.client(b.JSON201.Token).ListOwnAgentsWithResponse(ctx)
	if errorCode(t, ended, err, 401) != "unauthorized" {
		t.Fatalf("revoked lookup: %s", ended.Body)
	}
	reportDenied, err := s.client(b.JSON201.Token).SetAgentLocationWithResponse(ctx, nil, report)
	if errorCode(t, reportDenied, err, 401) != "unauthorized" {
		t.Fatalf("revoked report: %s", reportDenied.Body)
	}
}

func TestLocationReportsRejectControlsAndPresenceKeepsTheActivityStamp(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	_, writer, _ := s.pair("starter")
	valid := api.AgentLocationReport{Harness: "codex", SessionId: "codex:conversation", Folder: "C:\\work\\project"}
	for _, bad := range []api.AgentLocationReport{
		{Harness: "codex\nforged", SessionId: valid.SessionId, Folder: valid.Folder},
		{Harness: valid.Harness, SessionId: "codex:\x00secret", Folder: valid.Folder},
		{Harness: valid.Harness, SessionId: valid.SessionId, Folder: "/work/\tprivate"},
	} {
		rejected, err := s.client(writer).SetAgentLocationWithResponse(ctx, nil, bad)
		if errorCode(t, rejected, err, 400) != "invalid_request" {
			t.Fatalf("control accepted: %s", rejected.Body)
		}
	}
	saved, err := s.client(writer).SetAgentLocationWithResponse(ctx, nil, valid)
	mustStatus(t, saved, err, 200)
	s.clock.Advance(time.Minute)
	s.setPresence(writer, api.PresenceWorking)
	found, err := s.client(writer).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, found, err, 200)
	var current *api.AgentLocation
	for _, agent := range found.JSON200.Agents {
		if agent.Location != nil {
			current = agent.Location
		}
	}
	if current == nil || !current.LastActive.Equal(s.clock.Now()) || current.Folder != valid.Folder {
		t.Fatalf("presence did not renew reported activity: %s", found.Body)
	}
	active := current.LastActive
	s.clock.Advance(time.Minute)
	s.setPresence(writer, api.PresenceNoSession)
	ended, err := s.client(writer).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, ended, err, 200)
	for _, agent := range ended.JSON200.Agents {
		if agent.Location != nil && !agent.Location.LastActive.Equal(active) {
			t.Fatalf("no_session renewed activity: %s", ended.Body)
		}
	}
}

func TestPrivateAgentLocationsDoNotRevealTheirBoardToAnotherAdmin(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	maya := s.addHuman("maya")
	name, template := "private-work", "writer-reviewer"
	visibility := api.BoardVisibilityPrivate
	created, err := s.client(maya).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &name, Template: &template, Visibility: &visibility})
	mustStatus(t, created, err, 201)
	_, seat := s.joinAs(maya, name, "writer", "codex")
	report := api.AgentLocationReport{Harness: "codex", SessionId: "codex:hidden-conversation", Folder: "/work/hidden"}
	saved, err := s.client(seat).SetAgentLocationWithResponse(ctx, nil, report)
	mustStatus(t, saved, err, 200)
	own, err := s.client(seat).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, own, err, 200)
	if len(own.JSON200.Agents) != 1 || own.JSON200.Agents[0].Board != name || own.JSON200.Agents[0].Location == nil {
		t.Fatalf("own private agent missing: %s", own.Body)
	}
	admin, err := s.client(s.owner).ListOwnAgentsWithResponse(ctx)
	mustStatus(t, admin, err, 200)
	for _, secret := range []string{name, report.SessionId, report.Folder} {
		if strings.Contains(string(admin.Body), secret) {
			t.Fatalf("private agent leaked to admin: %s", admin.Body)
		}
	}
	foreign, err := s.client(s.owner).ListMembersWithResponse(ctx, name, nil)
	if errorCode(t, foreign, err, 404) != "board_not_found" {
		t.Fatalf("admin learned private board: %s", foreign.Body)
	}
}
