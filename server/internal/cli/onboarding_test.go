package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestSetupRecoversCommittedAccountOnlyWithItsSavedToken(t *testing.T) {
	var token string
	var redemptions int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/connect":
			redemptions++
			if r.Header.Get("Authorization") != "" {
				t.Error("redemption used an existing login")
			}
			var in api.ConnectRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.ClientToken == nil {
				t.Error("missing machine-held token")
				w.WriteHeader(400)
				return
			}
			token = *in.ClientToken
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("recording server cannot close its connection")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case "/v1/me/onboarding":
			if token == "" || r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("recovery did not use original pending key")
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"invite_id":"inv_one","server_id":"srv_one","person_id":"hum_new","handle":"newperson","key_id":"key_one","boards":["brd_work"]}`))
		case "/v1/me":
			_, _ = w.Write([]byte(`{"id":"hum_new","name":"newperson","kind":"human"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "must-not-use-human", agentCredential{})
	// Remove the existing fixture login: an invite is consent for a new account only.
	p, _ := resolvePaths(e.getenv)
	if err := writeJSONFile(p.servers(), serverLogins{}, 0o600); err != nil {
		t.Fatal(err)
	}
	e.env["ABOARD_AGENT"] = "unbound"
	args := []string{"setup", srv.URL + "/join#abi_fixture", "--handle", "newperson", "--name", "test-machine", "--json"}
	var first bytes.Buffer
	code := Run(context.Background(), args, e.environment(&first, &first))
	if code != 0 && code != 1 {
		t.Fatalf("unexpected first status %d: %s", code, first.String())
	}
	var out bytes.Buffer
	code = Run(context.Background(), args, e.environment(&out, &out))
	var progress struct {
		State string                         `json:"state"`
		Steps []struct{ Step, State string } `json:"steps"`
	}
	if err := json.Unmarshal(out.Bytes(), &progress); err != nil {
		t.Fatal(err)
	}
	if code != 0 || redemptions != 1 || len(progress.Steps) != 6 || progress.Steps[1].State != "complete" || progress.Steps[2].State != "complete" {
		t.Fatalf("code=%d redemptions=%d steps=%v output=%s", code, redemptions, progress.Steps, out.String())
	}
	if token == "" || bytes.Contains(out.Bytes(), []byte(token)) || bytes.Contains(first.Bytes(), []byte(token)) || bytes.Contains(out.Bytes(), []byte("abi_fixture")) {
		t.Fatal("missing token or secret in setup output")
	}
	logins, err := e.app(&bytes.Buffer{}, &bytes.Buffer{}).readServerLogins()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := logins.find(srv.URL)
	if !ok || got.Key != token || got.PersonID != "hum_new" {
		t.Fatal("original pending credential was not promoted")
	}
}

func TestSetupRevokedPendingKeyNeverRedeemsAgain(t *testing.T) {
	var token string
	var redemptions int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/connect" {
			redemptions++
			var in api.ConnectRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.ClientToken != nil {
				token = *in.ClientToken
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("recording server cannot close its connection")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if r.URL.Path != "/v1/me/onboarding" || token == "" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("recovery left original issuer/key")
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"The key no longer works.","hint":"Ask your person."}}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{})
	p, _ := resolvePaths(e.getenv)
	if err := writeJSONFile(p.servers(), serverLogins{}, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"setup", srv.URL + "/join#abi_fixture", "--handle", "newperson", "--json"}
	for range 2 {
		var out bytes.Buffer
		code := Run(context.Background(), args, e.environment(&out, &out))
		var result struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(out.Bytes(), &result)
		if code != 1 || result.State != "uncertain" {
			t.Fatalf("code=%d output=%s", code, out.String())
		}
	}
	if redemptions != 1 {
		t.Fatalf("uncertain redemption repeated %d times", redemptions)
	}
}

func TestAgentBundledInviteRequiresItsTrustedCurrentSession(t *testing.T) {
	var requested api.InvitePeopleAction
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer own-seat" {
			t.Error("bundled invite used another credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/boards/work":
			_, _ = w.Write([]byte(`{"id":"brd_work","name":"work"}`))
		case "/v1/me":
			_, _ = w.Write([]byte(`{"id":"mem_scout","name":"scout","kind":"agent","board":"work","owner":"owner"}`))
		case "/v1/me/admin-requests":
			if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
				t.Error(err)
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"state":"pending","approval":{"id":"apr_one","agent_id":"mem_scout","state":"pending"},"next":{"command":"aboard approvals allow apr_one --server issuer","resume":"Continue after approval."}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-person", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "own-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	e.env["ABOARD_SESSION"] = ""
	var out bytes.Buffer
	code := Run(context.Background(), []string{"invite", "--person", "--server", srv.URL, "--board", "work", "--pairing", "Review the change", "--json"}, e.environment(&out, &out))
	var result wireError
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "agent_session_required" || requested.Kind != "" {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
}

func TestSetupStopsBeforeRedemptionIfProofCannotBeSaved(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { requests++; t.Error("unsaved proof reached server") }))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{})
	p, _ := resolvePaths(e.getenv)
	if err := os.MkdirAll(p.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.state, "onboarding"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := Run(context.Background(), []string{"setup", srv.URL + "/join#abi_fixture", "--handle", "newperson", "--json"}, e.environment(&out, &out))
	if code != 1 || requests != 0 {
		t.Fatalf("code=%d requests=%d output=%s", code, requests, out.String())
	}
}

func TestSetupPendingProofIsPrivateAndIssuerBound(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "never-human", agentCredential{})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	srv := serverRef{URL: "https://issuer.example", Name: "issuer"}
	path, err := a.setupPendingPath(srv, "abi_fixture")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockSetup(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	pending, err := a.createSetupPending(srv, "abi_fixture", "newperson", "machine", nil)
	if err != nil {
		t.Fatal(err)
	}
	bits, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(pending.Token, "abh_"))
	if err != nil || len(bits) != 32 || !strings.HasPrefix(pending.Token, "abh_") {
		t.Fatal("pending token does not match the API canonical bearer format")
	}
	if err := saveSetupPending(path, pending); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("pending proof mode %o", st.Mode().Perm())
	}
	loaded, err := readSetupPending(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server != srv.URL || loaded.Invite != "abi_fixture" || loaded.Token != pending.Token {
		t.Fatal("pending proof binding changed")
	}
	other, err := a.setupPendingPath(serverRef{URL: "https://other.example"}, "abi_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if other == path {
		t.Fatal("same invite selected another issuer's proof")
	}
	// #nosec G302 -- Deliberately weaken the scratch proof to check that reading it refuses.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSetupPending(path); err == nil {
		t.Fatal("accepted world-readable pending proof")
	}
}

func TestAgentConnectWithoutInviteStillRefusesBeforePersonLogin(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { requests++; t.Error("agent reached D188 login") }))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{})
	e.env["ABOARD_AGENT"] = "unbound"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"connect", srv.URL, "--handle", "existing", "--json"}, e.environment(&out, &out))
	var result wireError
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "human_command_in_session" || requests != 0 {
		t.Fatalf("code=%d requests=%d output=%s", code, requests, out.String())
	}
}

func TestPersonCanListAndRevokeOnlyIssuerInvitesWithoutSecrets(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer person-only" {
			t.Error("wrong invite issuer credential")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/invites" && r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"invites":[{"id":"inv_one","state":"active","created_at":"2026-10-09T00:00:00Z","expires_at":"2026-10-10T00:00:00Z","boards":["brd_work"]}]}`))
			return
		}
		if r.URL.Path == "/v1/invites/inv_one" {
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing revocation idempotency")
			}
			_, _ = w.Write([]byte(`{"id":"inv_one","revoked":true,"changed":true}`))
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "person-only", agentCredential{})
	for _, args := range [][]string{{"invite", "list", "--server", srv.URL, "--json"}, {"invite", "revoke", "inv_one", "--server", srv.URL, "--json"}} {
		var out bytes.Buffer
		code := Run(context.Background(), args, e.environment(&out, &out))
		if code != 0 {
			t.Fatalf("code=%d output=%s", code, out.String())
		}
		if bytes.Contains(out.Bytes(), []byte("person-only")) || bytes.Contains(out.Bytes(), []byte("abi_")) {
			t.Fatal("secret in invite metadata output")
		}
	}
	if calls != 2 {
		t.Fatalf("requests %d", calls)
	}
}

func TestAgentCannotListOrRevokeInvitesThroughPersonKey(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
		t.Error("agent invite management used authenticated API")
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{})
	e.env["ABOARD_AGENT"] = "unbound"
	for _, args := range [][]string{{"invite", "list", "--server", srv.URL, "--json"}, {"invite", "revoke", "inv_one", "--server", srv.URL, "--json"}} {
		var out bytes.Buffer
		code := Run(context.Background(), args, e.environment(&out, &out))
		var result wireError
		_ = json.Unmarshal(out.Bytes(), &result)
		want := "human_command_in_session"
		if args[1] == "list" {
			want = "agent_not_selected"
		}
		if code != 1 || result.Error.Code != want || calls != 0 {
			t.Fatalf("code=%d calls=%d output=%s", code, calls, out.String())
		}
	}
}

func TestSetupPairingNeedsTheIntendedSessionBeforeAuthenticatedHTTP(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "must-not-use-human", agentCredential{})
	var out bytes.Buffer
	code := Run(context.Background(), []string{"setup", "prq_exact", "--server", srv.URL, "--json"}, e.environment(&out, &out))
	if code != 0 {
		t.Fatalf("setup pairing: exit %d: %s", code, out.String())
	}
	var result setupOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || result.State != "pending" || result.Steps[4].State != "pending" || result.Steps[5].State != "pending" || result.Steps[3].State != "pending" || result.Next == nil || result.Next.Command != "aboard init" || !strings.Contains(result.Next.Resume, "restart") {
		t.Fatalf("setup bypassed the intended session: requests=%d result=%+v", requests, result)
	}
}

func TestExecutedBundledInviteSelectsItsOriginalSessionWithoutReissuing(t *testing.T) {
	for _, state := range []string{"selected", "failed", "held"} {
		t.Run(state, func(t *testing.T) {
			fail := state == "failed"
			held := state == "held"
			var issued, selected atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer own-seat" {
					t.Error("human or other-seat credential used")
				}
				switch r.URL.Path {
				case "/v1/boards/work":
					_, _ = w.Write([]byte(`{"id":"brd_work","name":"work"}`))
				case "/v1/me":
					_, _ = w.Write([]byte(`{"id":"mem_scout","name":"scout","kind":"agent","board":"work","owner":"owner"}`))
				case "/v1/me/admin-requests":
					issued.Add(1)
					if held {
						w.WriteHeader(202)
						_, _ = w.Write([]byte(`{"state":"pending","approval":{"id":"apr_one","state":"pending","agent_id":"mem_scout"},"next":{"command":"aboard approvals allow apr_one","resume":"Continue after approval."}}`))
						return
					}
					_, _ = w.Write([]byte(`{"state":"executed","approval":{"id":"apr_one","state":"executed","agent_id":"mem_scout"},"invite":{"id":"inv_one","invite":"abi_once","expires_at":"2026-10-15T00:00:00Z","server_role":"member","pairing_request_id":"prq_exact"}}`))
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			e := lifecycleMachine(t, srv.URL, "never-human", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "own-seat"})
			e.env["ABOARD_AGENT"] = "scout"
			e.env["ABOARD_SESSION"] = "claude-code:original"
			var out bytes.Buffer
			a := &app{env: e.environment(&out, &out), json: true, localChecked: true}
			fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
				resp := delivery.Response{V: delivery.ProtocolVersion}
				if req.Harness != "claude-code" || req.Session != "original" {
					t.Errorf("wrong session %+v", req)
				}
				if req.Op == delivery.OpAgents {
					resp.Agents = []delivery.AgentRef{{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout"}}
				}
				if req.Op == delivery.OpPairing {
					selected.Add(1)
					if req.PairingAction != "select" || req.PairingID != "prq_exact" || req.Server != srv.URL {
						t.Errorf("wrong selection %+v", req)
					}
					if fail {
						resp.Error = &delivery.WireError{Code: "pairing_changed", Message: "The session changed.", Hint: "Select the original endpoint."}
					} else {
						resp.Server = srv.URL
						resp.PairingBoard = "work"
						resp.Pairing = &delivery.PairingRequest{ID: "prq_exact", ServerID: "srv_one", BoardID: "brd_work", State: "awaiting_account", Generation: 1}
					}
				}
				return resp
			})
			if err := runInvite(context.Background(), a, []string{"--person", "--server", srv.URL, "--board", "work", "--pairing", "Review this change"}); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Invite *api.ServerInvite `json:"invite"`
				Next   *api.NextStep     `json:"next"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if held {
				if issued.Load() != 1 || selected.Load() != 0 || result.Invite != nil || result.Next == nil || !strings.Contains(result.Next.Resume, "original session") {
					t.Fatalf("held selected an endpoint: %s", out.String())
				}
				return
			}
			if issued.Load() != 1 || selected.Load() != 1 || result.Invite == nil || result.Invite.Invite != "abi_once" {
				t.Fatalf("issued=%d selected=%d output=%s", issued.Load(), selected.Load(), out.String())
			}
			if fail && (result.Next == nil || !strings.Contains(result.Next.Command, "pairing select prq_exact --here --server")) {
				t.Fatalf("missing same-invite recovery: %s", out.String())
			}
		})
	}
}
