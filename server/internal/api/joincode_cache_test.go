package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestJoinCodeCreationNeverStoresOrReplaysItsSecret(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"archived", "expired", "revoked"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name := "code-cache-" + state
			requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", "/v1/boards", "", map[string]any{"name": name}), 201, "")
			path := "/v1/boards/" + name + "/join-codes"
			body := map[string]any{"role": "member", "ttl_seconds": 60}
			created := s.lifecycleCall(s.owner, "POST", path, "creation-code", body)
			requireLifecycleCall(t, created, 201, "")
			code, hasCode := created.body["code"].(string)
			line, hasLine := created.body["join_line"].(string)
			if !hasCode || code == "" || !hasLine || line == "" {
				t.Fatal("initial creation did not return its one-time secret response")
			}
			sum := sha256.Sum256([]byte(s.owner))
			scope := hex.EncodeToString(sum[:])
			if _, found, err := s.st.SavedResponse(context.Background(), scope, "creation-code"); err != nil || found {
				t.Errorf("join-code creation retained a secret response: found=%t err=%v", found, err)
			}
			switch state {
			case "archived":
				requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", "/v1/boards/"+name+"/archive", "", nil), 200, "")
			case "expired":
				s.clock.Advance(61 * time.Second)
			case "revoked":
				id, ok := created.body["id"].(string)
				if !ok || id == "" {
					t.Fatal("creation did not return a join-code id")
				}
				requireLifecycleCall(t, s.lifecycleCall(s.owner, "DELETE", path+"/"+id, "revoke-code", nil), 200, "")
				if _, found, err := s.st.SavedResponse(context.Background(), scope, "revoke-code"); err != nil || !found {
					t.Errorf("revocation lost its idempotent receipt: found=%t err=%v", found, err)
				}
			}
			repeated := s.lifecycleCall(s.owner, "POST", path, "creation-code", body)
			if state == "archived" {
				requireLifecycleCall(t, repeated, 409, "board_archived")
				if repeated.body["code"] != nil || repeated.body["join_line"] != nil {
					t.Error("archived retry disclosed a code")
				}
			} else {
				requireLifecycleCall(t, repeated, 201, "")
				if repeated.body["id"] == created.body["id"] || repeated.body["code"] == created.body["code"] {
					t.Error("retry replayed an expired or revoked code instead of creating a fresh one")
				}
			}
			if _, found, err := s.st.SavedResponse(context.Background(), scope, "creation-code"); err != nil || found {
				t.Errorf("retry retained a secret response: found=%t err=%v", found, err)
			}
		})
	}
}
