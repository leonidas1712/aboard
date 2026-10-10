package api_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
)

func heldInvite(t *testing.T, s *testServer, agent string) string {
	t.Helper()
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	var out struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 202 || out.Approval.ID == "" {
		t.Fatalf("hold invite: %d %s %v", status, raw, err)
	}
	return "/v1/me/approvals/" + out.Approval.ID
}

func TestBrowserApprovedInviteCanBeCollectedOnceAfterRestart(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	page := s.cookieBrowser(s.owner)
	allowed := page.do("POST", path+"/allow", map[string]any{})
	if allowed.status != 200 {
		t.Fatalf("browser allow: %d %s", allowed.status, allowed.raw)
	}
	var execution struct {
		Invite struct {
			Invite string `json:"invite"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(allowed.raw), &execution); err != nil || execution.Invite.Invite == "" {
		t.Fatalf("missing approver invite: %s %v", allowed.raw, err)
	}
	s.restart()
	status, raw := onboardingCall(t, s, "GET", path, "", agent)
	if status != 200 || strings.Contains(raw, "abi_") {
		t.Fatalf("metadata: %d %s", status, raw)
	}
	collect := func() call {
		return s.send("POST", path+"/collect", nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+agent)
		})
	}
	first := collect()
	if first.status != 200 || first.body["collected"] != true || !strings.Contains(string(first.raw), execution.Invite.Invite) {
		t.Fatalf("first collection: %d %s", first.status, first.raw)
	}
	s.restart()
	repeat := collect()
	if repeat.status != 200 || repeat.body["collected"] != false || strings.Contains(string(repeat.raw), "abi_") {
		t.Fatalf("repeat collection: %d %s", repeat.status, repeat.raw)
	}
	mustStatus(t, s.connect(execution.Invite.Invite, "collected"), nil, 201)
}

func TestConcurrentApprovalCollectorsHaveOneSecretBearingWinner(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	var wg sync.WaitGroup
	results := make(chan call, 8)
	for n := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.send("POST", path+"/collect", nil, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+agent)
				r.Header.Set("Idempotency-Key", fmt.Sprint("collector-", n))
			})
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for c := range results {
		if c.status != 200 {
			t.Fatalf("collect: %d %s", c.status, c.raw)
		}
		if c.body["collected"] == true {
			winners++
			if !strings.Contains(string(c.raw), "abi_") {
				t.Fatal("winner missing invite")
			}
		} else if strings.Contains(string(c.raw), "abi_") {
			t.Fatal("repeat exposed invite")
		}
	}
	if winners != 1 {
		t.Fatalf("secret-bearing winners: %d", winners)
	}
}

func TestApprovalCollectionRefusesOtherCredentialKindsAndSeats(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, sibling := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	foreign := s.addHuman("foreign")
	for name, token := range map[string]string{"person": s.owner, "sibling": sibling, "foreign": foreign, "delegation": s.delegate(s.owner)} {
		t.Run(name, func(t *testing.T) {
			status, raw := onboardingCall(t, s, "POST", path+"/collect", `{}`, token)
			if status < 400 || strings.Contains(raw, "abi_") {
				t.Fatalf("foreign collection: %d %s", status, raw)
			}
		})
	}
	page := s.cookieBrowser(s.owner)
	c := page.do("POST", path+"/collect", nil)
	if c.status != 403 || strings.Contains(string(c.raw), "abi_") {
		t.Fatalf("browser collection: %d %s", c.status, c.raw)
	}
	for _, token := range []string{sibling, foreign} {
		status, raw = onboardingCall(t, s, "GET", path, "", token)
		if status != 404 || !strings.Contains(raw, "approval_not_found") {
			t.Fatalf("foreign metadata: %d %s", status, raw)
		}
	}
	status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
	if status != 200 || !strings.Contains(raw, "abi_") {
		t.Fatalf("refusals consumed requesting outcome: %d %s", status, raw)
	}
}

func TestUnavailableApprovalInvitesNeverRevealAnOutcome(t *testing.T) {
	for _, state := range []string{"pending", "declined", "revoked", "redeemed", "expired", "removed"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name, agent, _ := s.pair("starter")
			path := heldInvite(t, s, agent)
			if state == "declined" {
				status, raw := onboardingCall(t, s, "POST", path+"/decline", `{}`, s.owner)
				if status != 200 {
					t.Fatalf("decline: %d %s", status, raw)
				}
			}
			if state != "pending" && state != "declined" {
				status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
				var out struct {
					Invite struct {
						ID     string `json:"id"`
						Invite string `json:"invite"`
					} `json:"invite"`
				}
				if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 200 {
					t.Fatalf("allow: %d %s %v", status, raw, err)
				}
				switch state {
				case "revoked":
					c, err := s.client(s.owner).RevokeServerInviteWithResponse(t.Context(), out.Invite.ID, nil)
					mustStatus(t, c, err, 200)
				case "redeemed":
					mustStatus(t, s.connect(out.Invite.Invite, "redeemed"), nil, 201)
				case "expired":
					s.clock.Advance(25 * time.Hour)
				case "removed":
					c, err := s.client(s.owner).RemoveAgentWithResponse(t.Context(), name, "writer", nil)
					mustStatus(t, c, err, 200)
				}
			}
			status, raw := onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
			if strings.Contains(raw, "abi_") || strings.Contains(raw, `"collected":true`) {
				t.Fatalf("unavailable secret exposed: %d %s", status, raw)
			}
			if state != "removed" && status != 200 {
				t.Fatalf("unavailable metadata: %d %s", status, raw)
			}
		})
	}
}

func TestApprovalCollectionRechecksParentPersonAndBoardAuthority(t *testing.T) {
	for _, change := range []string{"parent_revoked", "person_demoted", "private_access_lost", "board_archived", "board_deleted"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			ctx := t.Context()
			name := s.privateBoard(s.owner)
			parentID, parent := s.newKey(s.owner, "request-parent")
			_, agent := s.joinAs(parent, name, "member", "codex")
			b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
			mustStatus(t, b, err, 200)
			maya := s.addHuman("maya")
			added, err := s.client(s.owner).AddPersonWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "maya"})
			mustStatus(t, added, err, 201)
			owner, err := s.client(s.owner).AddOwnerWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "maya"})
			mustStatus(t, owner, err, 200)
			status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"boards":["`+b.JSON200.Id+`"]}}`, agent)
			var held struct {
				Approval struct {
					ID string `json:"id"`
				} `json:"approval"`
			}
			if err := json.Unmarshal([]byte(raw), &held); err != nil || status != 202 {
				t.Fatalf("hold: %d %s %v", status, raw, err)
			}
			path := "/v1/me/approvals/" + held.Approval.ID
			status, raw = onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
			if status != 200 {
				t.Fatalf("allow: %d %s", status, raw)
			}
			switch change {
			case "parent_revoked":
				c, err := s.client(s.owner).RevokeKeyWithResponse(ctx, parentID, nil)
				mustStatus(t, c, err, 200)
			case "person_demoted":
				c, err := s.client(s.owner).SetServerRoleWithResponse(ctx, "maya", nil, api.ServerRoleChange{ServerRole: "admin"})
				mustStatus(t, c, err, 200)
				c, err = s.client(maya).SetServerRoleWithResponse(ctx, "alex", nil, api.ServerRoleChange{ServerRole: "member"})
				mustStatus(t, c, err, 200)
			case "private_access_lost":
				c, err := s.client(maya).RemovePersonWithResponse(ctx, name, "alex", nil)
				mustStatus(t, c, err, 200)
			case "board_archived":
				c, err := s.client(s.owner).ArchiveBoardWithResponse(ctx, name, nil)
				mustStatus(t, c, err, 200)
			case "board_deleted":
				archived, e := s.client(s.owner).ArchiveBoardWithResponse(ctx, name, nil)
				mustStatus(t, archived, e, 200)
				c := s.lifecycleCall(s.owner, "POST", "/v1/boards/"+name+"/delete", "delete", nil)
				requireLifecycleCall(t, c, 200, "")
			}
			status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
			if strings.Contains(raw, "abi_") || strings.Contains(raw, `"collected":true`) {
				t.Fatalf("lost authority exposed invite: %d %s", status, raw)
			}
		})
	}
}

func TestOlderApprovalWithoutAnOutcomeNeverDerivesASecret(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Simulate an execution written before outcome collection was supported.
	if _, err = db.ExecContext(t.Context(), "DELETE FROM admin_approval_outcomes WHERE approval_id=?", strings.TrimPrefix(path, "/v1/me/approvals/")); err != nil {
		t.Fatal(err)
	}
	status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
	if status != 200 || strings.Contains(raw, "abi_") || strings.Contains(raw, `"collected":true`) {
		t.Fatalf("legacy execution manufactured secret: %d %s", status, raw)
	}
}

func TestApprovalOutcomeCarriesOnlyTheVisibleExactInvitePairing(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := t.Context()
	name := s.privateBoard(s.owner)
	_, agent := s.joinAs(s.owner, name, "member", "codex")
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	me, err := s.client(agent).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"boards":["`+b.JSON200.Id+`"],"pairing":{"initiating_agent_id":"`+me.JSON200.Id+`","work":"proposed work"}}}`, agent)
	var held struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil || status != 202 {
		t.Fatalf("hold pairinginvite: %d %s %v", status, raw, err)
	}
	path := "/v1/me/approvals/" + held.Approval.ID
	status, raw = onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	var out struct {
		Invite struct {
			Pairing string `json:"pairing_request_id"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 200 || out.Invite.Pairing == "" {
		t.Fatalf("allow pairinginvite: %d %s %v", status, raw, err)
	}
	for _, token := range []string{agent, s.owner} {
		status, raw = onboardingCall(t, s, "GET", path, "", token)
		if status != 200 || strings.Contains(raw, "abi_") || !strings.Contains(raw, `"pairing_request_id":"`+out.Invite.Pairing+`"`) {
			t.Fatalf("safe pairing outcome: %d %s", status, raw)
		}
	}
	status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
	if status != 200 || !strings.Contains(raw, "abi_") || !strings.Contains(raw, `"pairing_request_id":"`+out.Invite.Pairing+`"`) {
		t.Fatalf("collected pairing outcome: %d %s", status, raw)
	}
}

func TestAllowanceInviteDoesNotGainASecondSecretReveal(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":["invite-people"]}`, s.owner)
	if status != 200 {
		t.Fatalf("allowance: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	var held struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil || status != 201 || !strings.Contains(raw, "abi_") {
		t.Fatalf("automatic invite: %d %s %v", status, raw, err)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/collect", `{}`, agent)
	if status != 200 || strings.Contains(raw, "abi_") || strings.Contains(raw, `"collected":true`) {
		t.Fatalf("automatic outcome got extra reveal: %d %s", status, raw)
	}
}

func TestLostApprovalCollectionResponseRecoversOnlyWithTheWinningKey(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	collect := func(key string) call {
		return s.send("POST", path+"/collect", nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+agent)
			if key != "" {
				r.Header.Set("Idempotency-Key", key)
			}
		})
	}
	first := collect("lost-response")
	if first.status != 200 || first.body["collected"] != true || !strings.Contains(first.raw, "abi_") {
		t.Fatalf("first collection: %d %s", first.status, first.raw)
	}
	s.restart()
	recovered := collect("lost-response")
	if recovered.status != 200 || recovered.body["collected"] != false || recovered.body["invite"] == nil {
		t.Fatalf("lost response not recoverable: %d %s", recovered.status, recovered.raw)
	}
	for _, key := range []string{"other", ""} {
		c := collect(key)
		if c.status != 200 || c.body["invite"] != nil {
			t.Fatalf("new key recovered secret: %d %s", c.status, c.raw)
		}
	}
	s.clock.Advance(10 * time.Minute)
	expired := collect("lost-response")
	if expired.status != 200 || expired.body["invite"] != nil {
		t.Fatalf("expired recovery: %d %s", expired.status, expired.raw)
	}
}

func TestApprovalCapsuleIsEncryptedBoundedAndPurged(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"ttl_seconds":172800}}`, agent)
	var held struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil || status != 202 {
		t.Fatalf("hold: %d %s %v", status, raw, err)
	}
	path := "/v1/me/approvals/" + held.Approval.ID
	status, raw = onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	var out struct {
		Invite struct {
			Invite string `json:"invite"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 200 {
		t.Fatalf("allow: %d %s %v", status, raw, err)
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var capsule []byte
	var expires string
	if err = db.QueryRowContext(t.Context(), "SELECT capsule,expires_at FROM admin_approval_outcomes WHERE approval_id=?", held.Approval.ID).Scan(&capsule, &expires); err != nil {
		t.Fatal(err)
	}
	if len(capsule) == 0 || bytes.Contains(capsule, []byte(out.Invite.Invite)) {
		t.Fatal("outcome stored a plaintext invite")
	}
	var plaintext string
	if err = db.QueryRowContext(t.Context(), "SELECT action||coalesce(execution,'') FROM admin_approvals WHERE id=?", held.Approval.ID).Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plaintext, out.Invite.Invite) {
		t.Fatal("approval record stored a secret")
	}
	var cached int
	if err = db.QueryRowContext(t.Context(), "SELECT count(*) FROM idempotency WHERE instr(CAST(body AS TEXT),?)>0", out.Invite.Invite).Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if cached != 0 {
		t.Fatal("invite entered general response cache")
	}
	deadline, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		t.Fatal(err)
	}
	if deadline.Sub(s.clock.Now()) != 24*time.Hour {
		t.Fatalf("capsule deadline: %s", expires)
	}
	s.clock.Advance(24 * time.Hour)
	status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
	if status != 200 || strings.Contains(raw, "abi_") {
		t.Fatalf("old held capsule revealed: %d %s", status, raw)
	}
	if err = s.st.PurgeResponses(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(t.Context(), "SELECT capsule FROM admin_approval_outcomes WHERE approval_id=?", held.Approval.ID).Scan(&capsule); err != nil {
		t.Fatal(err)
	}
	if len(capsule) != 0 {
		t.Fatal("expired capsule was not cleared")
	}
	mustStatus(t, s.connect(out.Invite.Invite, "still-valid"), nil, 201)
}

func TestSameKeyRecoveryRechecksAuthorityAndClearsItsCapability(t *testing.T) {
	for _, change := range []string{"redeemed", "revoked", "parent_revoked", "person_demoted"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name, _, _ := s.pair("starter")
			parentID, parent := s.newKey(s.owner, "recover-parent")
			_, agent := s.joinAs(parent, name, "writer", "codex")
			path := heldInvite(t, s, agent)
			status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
			if status != 200 {
				t.Fatalf("allow: %d %s", status, raw)
			}
			collect := func() call {
				return s.send("POST", path+"/collect", nil, func(r *http.Request) {
					r.Header.Set("Authorization", "Bearer "+agent)
					r.Header.Set("Idempotency-Key", "lost-key")
				})
			}
			first := collect()
			if first.status != 200 || first.body["invite"] == nil {
				t.Fatalf("first: %d %s", first.status, first.raw)
			}
			var out struct {
				Invite struct {
					ID     string `json:"id"`
					Invite string `json:"invite"`
				} `json:"invite"`
			}
			if err := json.Unmarshal([]byte(first.raw), &out); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "redeemed":
				mustStatus(t, s.connect(out.Invite.Invite, "redeemed"), nil, 201)
			case "revoked":
				c, err := s.client(s.owner).RevokeServerInviteWithResponse(t.Context(), out.Invite.ID, nil)
				mustStatus(t, c, err, 200)
			case "parent_revoked":
				c, err := s.client(s.owner).RevokeKeyWithResponse(t.Context(), parentID, nil)
				mustStatus(t, c, err, 200)
			case "person_demoted":
				maya := s.addHuman("maya")
				c, err := s.client(s.owner).SetServerRoleWithResponse(t.Context(), "maya", nil, api.ServerRoleChange{ServerRole: "admin"})
				mustStatus(t, c, err, 200)
				c, err = s.client(maya).SetServerRoleWithResponse(t.Context(), "alex", nil, api.ServerRoleChange{ServerRole: "member"})
				mustStatus(t, c, err, 200)
			}
			repeated := collect()
			if repeated.body["invite"] != nil || strings.Contains(repeated.raw, out.Invite.Invite) {
				t.Fatalf("lost authority recovered secret: %d %s", repeated.status, repeated.raw)
			}
			db, err := sql.Open("sqlite", s.path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var capsule []byte
			if err = db.QueryRowContext(t.Context(), "SELECT capsule FROM admin_approval_outcomes WHERE approval_id=?", strings.TrimPrefix(path, "/v1/me/approvals/")).Scan(&capsule); err != nil {
				t.Fatal(err)
			}
			if len(capsule) != 0 {
				t.Fatal("invalidated capsule survived")
			}
		})
	}
}

func TestApprovalCapsuleDepartureMatchesExactAuthority(t *testing.T) {
	for _, departure := range []string{"sibling_agent", "issuing_agent", "person_on_issuing_board", "bundled_board_archived", "person_on_bundled_board"} {
		t.Run(departure, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name, agent, _ := s.pair("starter")
			_, sibling := s.joinAs(s.owner, name, "member", "codex")
			b, err := s.client(s.owner).GetBoardWithResponse(t.Context(), name)
			mustStatus(t, b, err, 200)
			if departure == "person_on_bundled_board" {
				name = s.privateBoard(s.owner)
				b, err = s.client(s.owner).GetBoardWithResponse(t.Context(), name)
				mustStatus(t, b, err, 200)
			}
			path := ""
			if departure == "person_on_issuing_board" {
				path = heldInvite(t, s, agent)
			} else {
				status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"boards":["`+b.JSON200.Id+`"]}}`, agent)
				var held struct {
					Approval struct {
						ID string `json:"id"`
					} `json:"approval"`
				}
				if e := json.Unmarshal([]byte(raw), &held); e != nil || status != 202 {
					t.Fatalf("hold: %d %s %v", status, raw, e)
				}
				path = "/v1/me/approvals/" + held.Approval.ID
			}
			status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
			if status != 200 {
				t.Fatalf("allow: %d %s", status, raw)
			}
			db, err := sql.Open("sqlite", s.path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			token := agent
			if departure == "sibling_agent" {
				token = sibling
			}
			me, err := s.client(token).GetMeWithResponse(t.Context())
			mustStatus(t, me, err, 200)
			memberID := me.JSON200.Id
			if departure == "person_on_issuing_board" || departure == "person_on_bundled_board" {
				if err = db.QueryRowContext(t.Context(), "SELECT id FROM members WHERE board_id=? AND kind='human'", b.JSON200.Id).Scan(&memberID); err != nil {
					t.Fatal(err)
				}
			}
			if departure == "bundled_board_archived" {
				archived, e := s.client(s.owner).ArchiveBoardWithResponse(t.Context(), name, nil)
				mustStatus(t, archived, e, 200)
			} else if err = s.st.Write(t.Context(), func(tx board.Tx) error { return tx.SetMemberStatus(memberID, string(board.StatusLeft)) }); err != nil {
				t.Fatal(err)
			}
			var capsule []byte
			if err = db.QueryRowContext(t.Context(), "SELECT capsule FROM admin_approval_outcomes WHERE approval_id=?", strings.TrimPrefix(path, "/v1/me/approvals/")).Scan(&capsule); err != nil {
				t.Fatal(err)
			}
			if departure == "sibling_agent" {
				if len(capsule) == 0 {
					t.Fatal("unrelated agent departure cleared the issuer's capability")
				}
				status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, sibling)
				if strings.Contains(raw, "abi_") {
					t.Fatalf("sibling revealed invite: %d %s", status, raw)
				}
				status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
				if status != 200 || !strings.Contains(raw, "abi_") {
					t.Fatalf("issuer lost valid collection: %d %s", status, raw)
				}
			} else {
				if len(capsule) != 0 {
					t.Fatal("lost issuing authority did not erase the capability")
				}
				status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
				if strings.Contains(raw, "abi_") {
					t.Fatalf("lost authority revealed invite: %d %s", status, raw)
				}
			}
		})
	}
}
