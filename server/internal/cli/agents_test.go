package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentsListingNeverUsesThePersonsKeyInAnAgentContext(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/me/agents" || r.Header.Get("Authorization") != "Bearer exact-seat" {
			t.Errorf("wrong issuer credential or endpoint: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[]}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "must-not-use-person", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "exact-seat"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	code := Run(context.Background(), []string{"agents", "--board", "work", "--json"}, e.environment(&out, &out))
	if code != 0 || requests != 1 {
		t.Fatalf("code=%d requests=%d output=%s", code, requests, out.String())
	}
}
