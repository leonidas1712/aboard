package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestApprovalCollectionRetriesLostResponseWithOriginalIssuerSeatAndKey(t *testing.T) {
	for _, failure := range []string{"lost_response", "refused"} {
		t.Run(failure, func(t *testing.T) {
			var foreignCalls atomic.Int32
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreignCalls.Add(1); w.WriteHeader(http.StatusForbidden) }))
			defer foreign.Close()
			type observed struct{ method, host, path, token, key string }
			var mu sync.Mutex
			var attempts []observed
			committed := false
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				attempts = append(attempts, observed{r.Method, r.Host, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")})
				w.Header().Set("Content-Type", "application/json")
				if failure == "refused" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"Refused","hint":"Ask your person"}}`))
					return
				}
				if len(attempts) == 1 {
					committed = true
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						t.Error("recording server cannot lose response")
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
				if !committed || len(attempts) != 2 || attempts[0].key == "" || attempts[0].key != attempts[1].key {
					t.Error("retry did not address the committed collection")
					w.WriteHeader(http.StatusConflict)
					return
				}
				link := "http://" + r.Host + "/join#abi_recorded"
				_ = json.NewEncoder(w).Encode(map[string]any{
					"approval":  map[string]any{"id": "apr_own", "person_id": "hum_owner", "agent_id": "mem_shared", "parent_key_id": "key_parent", "state": "executed", "payload_hash": "hash", "created_at": "2026-10-10T00:00:00Z", "action": map[string]any{"kind": "invite_people"}},
					"collected": true,
					"invite":    map[string]any{"id": "inv_own", "invite": "abi_recorded", "server_role": "member", "expires_at": "2026-10-11T00:00:00Z", "link": link, "prompt": "Server-owned colleague handover"},
				})
			}))
			defer issuer.Close()
			e := lifecycleMachine(t, foreign.URL, "must-not-use-human-a", agentCredential{Server: foreign.URL, Board: "work", Name: "writer", MemberID: "mem_shared", Token: "wrong-issuer-seat"})
			p, err := resolvePaths(e.getenv)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSONFile(p.servers(), serverLogins{Default: foreign.URL, Servers: []serverLogin{{URL: foreign.URL, Handle: "alex", Key: "must-not-use-human-a"}, {URL: issuer.URL, Handle: "alex", Key: "must-not-use-human-b"}}}, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := e.app(&bytes.Buffer{}, &bytes.Buffer{}).saveCredential(agentCredential{Server: issuer.URL, Board: "work", Name: "writer", MemberID: "mem_shared", Token: "original-seat-token"}); err != nil {
				t.Fatal(err)
			}
			e.env["ABOARD_AGENT"] = "writer"
			var out bytes.Buffer
			a := &app{env: e.environment(&out, &out), json: true, localChecked: true}
			err = runApprovals(context.Background(), a, []string{"show", "apr_own", "--server", issuer.URL, "--board", "work"})
			mu.Lock()
			recorded := append([]observed(nil), attempts...)
			mu.Unlock()
			if foreignCalls.Load() != 0 {
				t.Fatalf("collection reached foreign issuer %d times", foreignCalls.Load())
			}
			want := 2
			if failure == "refused" {
				want = 1
			}
			if len(recorded) != want {
				t.Fatalf("attempts=%d want=%d err=%v output=%s", len(recorded), want, err, out.String())
			}
			for _, request := range recorded {
				if request.method != http.MethodPost || request.host != strings.TrimPrefix(issuer.URL, "http://") || request.path != "/v1/me/approvals/apr_own/collect" || request.token != "Bearer original-seat-token" || request.key == "" {
					t.Fatalf("collection changed scope: %+v", request)
				}
			}
			if failure == "refused" {
				var refusal *Error
				if !errors.As(err, &refusal) || refusal.Code != "forbidden" {
					t.Fatalf("refusal changed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Prompt    string `json:"prompt"`
				Collected bool   `json:"collected"`
				Invite    struct {
					Link string `json:"link"`
				} `json:"invite"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.Collected || result.Prompt != "Server-owned colleague handover" || result.Invite.Link != issuer.URL+"/join#abi_recorded" {
				t.Fatalf("server handover lost: %s", out.String())
			}
		})
	}
}
