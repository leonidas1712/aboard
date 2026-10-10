package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentDirectoryReadsNeverUseOtherIssuerOrPersonKey(t *testing.T) {
	var foreign atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreign.Add(1); w.WriteHeader(500) }))
	defer first.Close()
	var selected atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer aba_second" {
			t.Errorf("wrong credential: %q", r.Header.Get("Authorization"))
			w.WriteHeader(401)
			return
		}
		selected.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/people":
			_ = json.NewEncoder(w).Encode(map[string]any{"people": []any{}})
		case "/v1/invites":
			_ = json.NewEncoder(w).Encode(map[string]any{"invites": []any{}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer second.Close()
	e := lifecycleMachine(t, first.URL, "never-person", agentCredential{Server: first.URL, Board: "same", Name: "helper", Token: "aba_first", MemberID: "mem_same"})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	if err := a.saveCredential(agentCredential{Server: second.URL, Board: "same", Name: "helper", Token: "aba_second", MemberID: "mem_same"}); err != nil {
		t.Fatal(err)
	}
	e.env["ABOARD_AGENT"] = "helper"
	for _, args := range [][]string{{"people", "--server", second.URL, "--json"}, {"invite", "list", "--server", second.URL, "--json"}} {
		result := e.run(args...)
		if result.code != 0 {
			t.Fatalf("read: %+v", result)
		}
	}
	if foreign.Load() != 0 || selected.Load() != 2 {
		t.Fatalf("foreign=%d selected=%d", foreign.Load(), selected.Load())
	}
}

func TestAgentPeopleReadRefusalNamesIssuerWithoutPersonLogin(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "never-person", agentCredential{Server: "https://issuer.example", Board: "same", Name: "helper", Token: "aba_seat"})
	e.env["ABOARD_AGENT"] = "helper"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	refused := a.peopleReadError(serverRef{Name: "work", URL: "https://issuer.example"}, 401, []byte(`{"error":{"code":"unauthorized","message":"Seat ended.","hint":"Run aboard resume helper --server https://issuer.example.","next":{"command":"aboard resume helper --server 'https://issuer.example'","resume":"Reconnect to https://issuer.example."}}}`))
	if strings.Contains(refused.Hint, "aboard login") || strings.Contains(refused.Hint, "issuer.example") || !strings.Contains(refused.Hint, "--server work") || refused.Next == nil || !strings.Contains(refused.Next.Command, "--server work") {
		t.Fatalf("agent hint: hint=%q next=%+v", refused.Hint, refused.Next)
	}
}
