package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestApprovalReadsUseExactSeatAndNeverCollect(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/me/approvals/apr_own" || r.Header.Get("Authorization") != "Bearer seat" {
			t.Errorf("wrong approval access: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"approval":{"id":"apr_own","agent_id":"mem_own","state":"executed"},"pairing_request_id":"prq_invite","invite":{"link":"private_invite_link"}}`))
	}))
	defer server.Close()
	agent := delivery.AgentRef{Server: server.URL, Board: "docs", MemberID: "mem_own"}
	adapter := NewApprovals(server.URL, tokens{human: map[string]string{server.URL: "must_not_use_parent"}, agents: map[delivery.AgentRef]string{agent: "seat"}})
	decision, err := adapter.Get(t.Context(), "apr_own", agent)
	if err != nil || decision.PairingRequestID != "prq_invite" {
		t.Fatalf("metadata read: %+v %v", decision, err)
	}
	raw, _ := json.Marshal(decision)
	if len(raw) == 0 || strings.Contains(string(raw), "private_invite_link") {
		t.Fatal("secret left metadata adapter")
	}
	foreign := agent
	foreign.Server = "https://other.example"
	if _, err := adapter.Get(context.Background(), "apr_own", foreign); err == nil {
		t.Fatal("foreign issuer accepted")
	}
	if calls != 1 {
		t.Fatalf("foreign issuer reached HTTP: %d", calls)
	}
}
