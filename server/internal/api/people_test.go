package api_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// invite makes a server invite with token, lasting ttl seconds (the default when 0).
func (s *testServer) invite(token string, ttl int) *api.CreateServerInviteResponse {
	s.t.Helper()
	req := api.CreateInviteRequest{}
	if ttl != 0 {
		req.TtlSeconds = &ttl
	}
	r, err := s.client(token).CreateServerInviteWithResponse(context.Background(), nil, req)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// connect redeems invite for handle.
func (s *testServer) connect(invite, handle string) *api.ConnectResponse {
	s.t.Helper()
	r, err := s.client("").ConnectWithResponse(context.Background(), nil, api.ConnectRequest{Invite: invite, Handle: handle, KeyName: "laptop"})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func wantCode(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.StatusCode() != status || !strings.Contains(bodyOf(r), `"code":"`+code+`"`) {
		t.Fatalf("got %d %s, want %d %s", r.StatusCode(), bodyOf(r), status, code)
	}
}

// An invite works until it expires, and makes one member, never an admin.
func TestServerInviteExpires(t *testing.T) {
	s := newTestServer(t)
	kept, lapsed := s.invite(s.owner, 120), s.invite(s.owner, 60)
	mustStatus(t, kept, nil, 201)
	if kept.JSON201.ServerRole != "member" {
		t.Fatalf("an invite makes %q", kept.JSON201.ServerRole)
	}
	s.clock.Advance(61 * time.Second)
	wantCode(t, s.connect(lapsed.JSON201.Invite, "maya"), 404, "invite_invalid")
	c := s.connect(kept.JSON201.Invite, "maya")
	mustStatus(t, c, nil, 201)
	if c.JSON201.Person.ServerRole != "member" || c.JSON201.Person.Handle != "maya" || !strings.HasPrefix(c.JSON201.Key.Token, "abh_") {
		t.Fatalf("connected: %s", bodyOf(c))
	}
	// The new member can't invite anyone, and nobody invites with a lifetime out of range.
	wantCode(t, s.invite(c.JSON201.Key.Token, 0), 403, "server_admin_required")
	wantCode(t, s.invite(s.owner, 30*24*3600+1), 400, "invalid_request")
}

// A browser, even an admin's, can't make an invite: only a person's own access key can.
func TestBrowserCantInvite(t *testing.T) {
	s := newTestServer(t)
	wantCode(t, s.invite(s.browserToken(s.owner), 0), 403, "human_token_required")
}

// Attempts to redeem invites are limited per client address and across the server, so
// guessing is bounded however many addresses it comes from.
func TestConnectAttemptsAreLimited(t *testing.T) {
	for _, tt := range []struct {
		name         string
		perAddr, all int
	}{
		{"per address", 2, 100},
		{"across the server", 100, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, func(o *api.Options) { o.ConnectsPerMinute, o.ConnectsPerMinuteServer = tt.perAddr, tt.all })
			for range 2 {
				wantCode(t, s.connect("abi_guess", "maya"), 404, "invite_invalid")
			}
			r := s.connect("abi_guess", "maya")
			wantCode(t, r, 429, "rate_limited")
			if r.HTTPResponse.Header.Get("Retry-After") == "" {
				t.Fatal("a refused attempt has no Retry-After")
			}
			s.clock.Advance(time.Minute)
			wantCode(t, s.connect("abi_guess", "maya"), 404, "invite_invalid")
		})
	}
}

// A handle must be a plain name.
func TestConnectRefusesAnInvalidHandle(t *testing.T) {
	s := newTestServer(t)
	inv := s.invite(s.owner, 0)
	wantCode(t, s.connect(inv.JSON201.Invite, "Maya Chen"), 422, "handle_invalid")
	mustStatus(t, s.connect(inv.JSON201.Invite, "maya-chen"), nil, 201)
}

// A revoked or expired access key stops working, and so does everything it started: its
// browser logins and its agents' tokens. Other people's keys keep working.
func TestAKeyEndsWhatItStarted(t *testing.T) {
	for _, change := range []string{
		"UPDATE access_keys SET revoked_at = '2026-10-01T16:00:00.000Z' WHERE human_id = (SELECT id FROM humans WHERE name = 'alex')",
		"UPDATE access_keys SET expires_at = '2026-10-01T16:00:00.000Z' WHERE human_id = (SELECT id FROM humans WHERE name = 'alex')",
	} {
		s := newTestServer(t)
		ctx := context.Background()
		boardName := s.newBoard()
		_, agent := s.joinAs(s.owner, boardName, "writer", "")
		browser := s.browserToken(s.owner)
		maya := s.addHuman("maya")

		db, err := sql.Open("sqlite", "file:"+s.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, change); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()

		for _, token := range []string{s.owner, browser, agent} {
			r, err := s.client(token).GetMeWithResponse(ctx)
			mustStatus(t, r, err, 401)
		}
		r, err := s.client(maya).GetMeWithResponse(ctx)
		mustStatus(t, r, err, 200)
	}
}
