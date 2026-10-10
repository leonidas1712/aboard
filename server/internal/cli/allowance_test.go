package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestAllowanceCLIChangesOneCategoryWithoutDroppingAnother(t *testing.T) {
	var categories []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer person-only" {
			t.Error("allowance used another issuer's or seat's credential")
		}
		if r.URL.Path != "/v1/me/allowance" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing write idempotency key")
			}
			var in struct {
				Categories []string `json:"categories"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			categories = in.Categories
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "alw_test", "person_id": "hum_owner", "revision": 2, "categories": []string{"add-people"}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "person-only", agentCredential{})
	var out bytes.Buffer
	code := Run(context.Background(), []string{"allowance", "set", "invite-people", "on", "--server", srv.URL, "--json"}, e.environment(&out, &out))
	if code != 0 || !reflect.DeepEqual(categories, []string{"add-people", "invite-people"}) {
		t.Fatalf("code=%d categories=%v output=%s", code, categories, out.String())
	}
}

func TestApprovalCLIListingUsesOnlyItsSelectedSeat(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer exact-seat" || r.URL.Path != "/v1/me/approvals" {
			t.Errorf("unexpected issuer request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"approvals":[]}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "must-not-use-person", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"approvals", "--board", "work", "--json"}, e.environment(&out, &out))
	if code != 0 || requests != 1 {
		t.Fatalf("code=%d requests=%d output=%s", code, requests, out.String())
	}
}

func TestAllowanceAgentRefusalPreservesTheAPINextCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer exact-seat" {
			t.Error("agent allowance read used human login")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "human_command_in_session", "message": "Only your person can change allowances.", "hint": "Use the person's terminal.", "next": map[string]any{"command": "aboard allowance on --server issuer", "resume": "Continue after the person allows it."}}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "must-not-use-person", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"allowance", "on", "--json"}, e.environment(&out, &out))
	var result struct {
		Error struct {
			Code string `json:"code"`
			Next struct {
				Command string `json:"command"`
			} `json:"next"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "human_command_in_session" || result.Error.Next.Command != "aboard allowance on --server issuer" {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
}

func TestAgentInviteReturnsPendingWithoutUsingPersonLogin(t *testing.T) {
	var action api.InvitePeopleAction
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer exact-seat" || r.URL.Path != "/v1/me/admin-requests" {
			t.Errorf("unexpected issuer request %s", r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing idempotency key")
		}
		if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"state":"pending","approval":{"id":"apr_one","agent_id":"mem_scout","state":"pending"},"next":{"command":"aboard approvals allow apr_one --server issuer","resume":"Continue after approval."}}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"invite", "--person", "--server", srv.URL, "--ttl", "2h", "--json"}, e.environment(&out, &out))
	var result struct {
		State string       `json:"state"`
		Next  api.NextStep `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 0 || result.State != "pending" || result.Next.Command == "" || action.Kind != api.InvitePeopleActionKindInvitePeople || action.Invite.TtlSeconds == nil || *action.Invite.TtlSeconds != 7200 {
		t.Fatalf("code=%d action=%+v output=%s", code, action, out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("never-human")) {
		t.Fatal("person key leaked")
	}
}

func TestAgentBoardAddFreezesExactIdentitiesBeforeRequest(t *testing.T) {
	var action api.AddPeopleAction
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer exact-seat" {
			t.Error("used human login")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me":
			_, _ = w.Write([]byte(`{"name":"scout","owner":"owner"}`))
		case "/v1/boards/work/people":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"add_people_not_allowed","message":"private admission is off","hint":"Ask your person."}}`))
		case "/v1/boards/work":
			_, _ = w.Write([]byte(`{"id":"brd_permanent","name":"work"}`))
		case "/v1/people":
			if r.URL.Query().Get("handle") != "newperson" {
				t.Error("did not use exact lookup")
			}
			_, _ = w.Write([]byte(`{"id":"hum_target","handle":"newperson","display_name":null}`))
		case "/v1/me/admin-requests":
			if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"state":"pending","approval":{"id":"apr_add","agent_id":"mem_scout","state":"pending"},"next":{"command":"aboard approvals allow apr_add --server issuer","resume":"Continue after approval."}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"board", "add", "@newperson", "--board", "work", "--json"}, e.environment(&out, &out))
	if code != 0 || action.BoardId != "brd_permanent" || action.PersonId != "hum_target" || action.Kind != api.AddPeopleActionKindAddPeople {
		t.Fatalf("code=%d action=%+v output=%s", code, action, out.String())
	}
}

func TestApprovalDecisionsInAgentContextPreservePersonNext(t *testing.T) {
	for _, decision := range []string{"allow", "decline"} {
		t.Run(decision, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer exact-seat" {
					t.Error("decision used person key")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "human_command_in_session", "message": "Only your person can decide.", "hint": "Use a terminal.", "next": map[string]string{"command": "aboard approvals " + decision + " apr_one --server issuer", "resume": "Continue after the decision."}}})
			}))
			defer srv.Close()
			e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
			e.env["ABOARD_AGENT"] = "scout"
			var out bytes.Buffer
			code := Run(context.Background(), []string{"approvals", decision, "apr_one", "--json"}, e.environment(&out, &out))
			var result wireError
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if code != 1 || result.Error.Code != "human_command_in_session" || result.Error.Next == nil || result.Error.Next.Command != "aboard approvals "+decision+" apr_one --server issuer" {
				t.Fatalf("code=%d output=%s", code, out.String())
			}
		})
	}
}

func TestApprovalListingRefusesAnotherIssuerBeforeAuthenticatedRequest(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { requests++; t.Error("unexpected authenticated request") }))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"approvals", "--server", "https://other.example", "--json"}, e.environment(&out, &out))
	var result wireError
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "agent_not_selected" || requests != 0 {
		t.Fatalf("code=%d requests=%d output=%s", code, requests, out.String())
	}
}

func TestAgentBoardAddDoesNotTurnAuthorityRefusalIntoApproval(t *testing.T) {
	for _, code := range []string{"board_owner_required", "board_archived", "guest_not_allowed", "agent_session_required"} {
		t.Run(code, func(t *testing.T) {
			var writes int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer exact-seat" {
					t.Error("used human credential")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/me" {
					_, _ = w.Write([]byte(`{"name":"scout","kind":"agent","owner":"owner"}`))
					return
				}
				if r.URL.Path != "/v1/boards/work/people" {
					t.Errorf("authority refusal followed by %s", r.URL.Path)
				}
				writes++
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": "Refused", "hint": "Ask your person."}})
			}))
			defer srv.Close()
			e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
			e.env["ABOARD_AGENT"] = "scout"
			var out bytes.Buffer
			status := Run(context.Background(), []string{"board", "add", "@newperson", "--board", "work", "--json"}, e.environment(&out, &out))
			var result wireError
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if status != 1 || result.Error.Code != code || writes != 1 {
				t.Fatalf("status=%d writes=%d output=%s", status, writes, out.String())
			}
		})
	}
}
