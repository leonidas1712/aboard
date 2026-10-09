package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestOnboardingContractOperationsAreExplicitlyUnavailable(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/v1/pairing-credentials", `{"request_id":"prq_01K00000000000000000000000","side":"recipient","agent_id":"mem_01K00000000000000000000000","session_binding":"sha256:` + strings.Repeat("0", 64) + `","generation":1,"client_token":"abp_` + strings.Repeat("A", 43) + `"}`},
		{"GET", "/v1/me/onboarding", ""},
		{"GET", "/v1/pairing-requests", ""},
		{"POST", "/v1/pairing-requests", `{"board_id":"brd_01K00000000000000000000000","recipient_id":"hum_01K00000000000000000000000","initiating_agent_id":"mem_01K00000000000000000000000","work":"review"}`},
		{"GET", "/v1/pairing-requests/prq_01K00000000000000000000000", ""},
		{"POST", "/v1/pairing-requests/prq_01K00000000000000000000000/accept", `{"agent_id":"mem_01K00000000000000000000000","generation":1}`},
		{"POST", "/v1/pairing-requests/prq_01K00000000000000000000000/decline", ""},
		{"POST", "/v1/pairing-requests/prq_01K00000000000000000000000/cancel", ""},
		{"POST", "/v1/pairing-requests/prq_01K00000000000000000000000/verify", `{"generation":1,"direction":"initiator_to_recipient","ping_seq":1,"reply_seq":2,"handoff_id":"owned-confirmed-handoff"}`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			status, body := onboardingCall(t, s, tc.method, tc.path, tc.body, s.owner)
			if status != 501 || !strings.Contains(body, `"code":"not_implemented"`) {
				t.Fatalf("got %d %s; want explicit 501 not_implemented", status, body)
			}
		})
	}
}

func TestUnsupportedOnboardingFieldsHaveNoPartialSideEffects(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	inv := s.invite(s.owner, 0)
	mustStatus(t, inv, nil, 201)
	token := "abh_" + strings.Repeat("A", 43)
	status, body := onboardingCall(t, s, "POST", "/v1/connect", `{"invite":"`+inv.JSON201.Invite+`","handle":"maya","key_name":"laptop","client_token":"`+token+`"}`, "")
	if status != 501 || !strings.Contains(body, `"code":"not_implemented"`) {
		t.Fatalf("client token: %d %s", status, body)
	}
	// Refusing an unsupported token leaves the original invite available to old clients.
	mustStatus(t, s.connect(inv.JSON201.Invite, "maya"), nil, 201)
	status, body = onboardingCall(t, s, "POST", "/v1/invites", `{"boards":["brd_01K00000000000000000000000"]}`, s.owner)
	if status != 501 || !strings.Contains(body, `"code":"not_implemented"`) {
		t.Fatalf("bundled invite: %d %s", status, body)
	}
	// Ordinary invites continue to work, and the attempted bundle created no person.
	mustStatus(t, s.invite(s.owner, 0), nil, 201)
}

func onboardingCall(t *testing.T, s *testServer, method, path, body, token string) (status int, bodyText string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, string(b)
}

func TestClientConnectedContractCannotReturnTheClientToken(t *testing.T) {
	t.Parallel()
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"id": "key_01K00000000000000000000000", "name": "laptop", "created_at": "2026-10-01T00:00:00Z", "expires_at": nil}
	schema := spec.Components.Schemas["ClientKeyMetadata"].Value
	if err := schema.VisitJSON(metadata); err != nil {
		t.Fatalf("metadata refused: %v", err)
	}
	metadata["token"] = "abh_" + strings.Repeat("A", 43)
	if err := schema.VisitJSON(metadata); err == nil {
		t.Fatal("client-generated token accepted as response metadata")
	}
}

func TestOnboardingPersonRefusalsAndHeldActionsRequireCommandHandover(t *testing.T) {
	t.Parallel()
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/me/allowance"},
		{"PUT", "/v1/me/allowance"},
		{"POST", "/v1/me/approvals/{approval}/allow"},
		{"POST", "/v1/me/approvals/{approval}/decline"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			schema := spec.Paths.Map()[tc.path].Operations()[tc.method].Responses.Value("403").Value.Content["application/json"].Schema.Value
			next := map[string]any{"command": "aboard allowance --server https://team.example.com", "resume": "Continue after the person's change."}
			payload := map[string]any{"error": map[string]any{"code": "human_token_required", "message": "The person decides this.", "hint": "Use your terminal.", "next": next}}
			if err := schema.VisitJSON(payload); err != nil {
				t.Fatalf("valid handover refused: %v", err)
			}
			delete(next, "command")
			if err := schema.VisitJSON(payload); err == nil {
				t.Fatal("person-only refusal accepted without next.command")
			}
			next["command"] = ""
			if err := schema.VisitJSON(payload); err == nil {
				t.Fatal("empty command accepted")
			}
			legacy := map[string]any{"error": map[string]any{"code": "agent_removed", "message": "This seat ended.", "hint": "Ask your person."}}
			if err := schema.VisitJSON(legacy); err != nil {
				t.Fatalf("existing removed-seat refusal changed: %v", err)
			}
		})
	}
	schema := spec.Paths.Map()["/v1/me/admin-requests"].Post.Responses.Value("202").Value.Content["application/json"].Schema.Value
	id := "01K00000000000000000000000"
	next := map[string]any{"command": "aboard approvals allow apr_" + id + " --server https://team.example.com", "resume": "Continue when the person decides."}
	payload := map[string]any{"state": "pending", "next": next, "approval": map[string]any{
		"id": "apr_" + id, "person_id": "hum_" + id, "agent_id": "mem_" + id, "parent_key_id": "key_" + id,
		"action":       map[string]any{"kind": "add_people", "board_id": "brd_" + id, "person_id": "hum_" + id},
		"payload_hash": "sha256:" + strings.Repeat("0", 64), "state": "pending", "created_at": "2026-10-01T00:00:00Z",
	}}
	if err := schema.VisitJSON(payload); err != nil {
		t.Fatalf("valid held result refused: %v", err)
	}
	delete(next, "command")
	if err := schema.VisitJSON(payload); err == nil {
		t.Fatal("held action accepted without next.command")
	}
	delete(payload, "next")
	if err := schema.VisitJSON(payload); err == nil {
		t.Fatal("held action accepted without next")
	}
}
