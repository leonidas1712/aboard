package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

func TestAgentCreationWithoutASessionNeverReadsAPersonKey(t *testing.T) {
	for _, command := range [][]string{{"board", "new", "work"}, {"board", "new", "work", "--as", "scout"}, {"pair", "--new"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			home := t.TempDir()
			var out bytes.Buffer
			env := Env{Dir: home, Stdout: &out, Stderr: &out, Getenv: func(k string) string {
				return map[string]string{"HOME": home, "ABOARD_HOME": home, "ABOARD_AGENT": "scout"}[k]
			}}
			a := &app{env: env}
			var err error
			if command[0] == "pair" {
				err = runPair(context.Background(), a, command[1:])
			} else {
				err = runBoard(context.Background(), a, command[1:])
			}
			if asError(err).Code != "agent_session_required" {
				t.Fatalf("wanted local session refusal, got %v", err)
			}
		})
	}
}

func TestAgentBoardAddUsesOnlyTheSeatsToken(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me":
			_, _ = w.Write([]byte(`{"name":"scout","kind":"agent","owner":"alex"}`))
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"mem_pat","name":"pat","handle":"pat","board_role":"member","server_role":"member"}`))
		}
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "person-key-must-not-be-sent", agentCredential{Server: srv.URL, Board: "work", Name: "scout", MemberID: "mem_scout", Token: "seat-only"})
	e.env["ABOARD_AGENT"] = "scout"
	var out bytes.Buffer
	a := e.app(&out, &out)
	a.json = true
	if err := runBoard(context.Background(), a, []string{"add", "@pat", "--board", "work"}); err != nil {
		t.Fatal(err)
	}
	for _, got := range auth {
		if got != "Bearer seat-only" {
			t.Fatalf("wrong credential reached API: %q", got)
		}
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["by_agent"] != "scout" || result["by_owner"] != "alex" {
		t.Fatalf("missing provenance: %v", result)
	}
}

func TestPrivateAgentsAddPeopleRequiresConfirmationAndKeepsFlags(t *testing.T) {
	var patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			patches++
		}
		_, _ = w.Write([]byte(`{"name":"work","visibility":"private"}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "test-person", agentCredential{Server: srv.URL, Board: "work", Name: "scout", Token: "test-seat"})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	err := runBoard(context.Background(), a, []string{"agents-add-people", "on", "--board", "work", "--server", srv.URL})
	got := asError(err)
	if got.Code != "confirmation_required" || !strings.Contains(got.Hint, "--server "+srv.URL) || !strings.Contains(got.Message, "whole history and files") || patches != 0 {
		t.Fatalf("confirmation: %+v, writes=%d", got, patches)
	}
}

func TestCreationTransportRetryKeepsOneOperationAndNeverReadsAHumanKey(t *testing.T) {
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Rand: rand.Reader, Getenv: func(k string) string { return map[string]string{"HOME": home, "ABOARD_HOME": home}[k] }}, daemonChecked: true}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var seen []delivery.Request
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var req delivery.Request
			if err := delivery.ReadFrame(bufio.NewReader(conn), &req); err != nil {
				_ = conn.Close()
				return
			}
			seen = append(seen, req)
			resp := delivery.Response{V: delivery.ProtocolVersion}
			if i == 0 {
				resp.Error = &delivery.WireError{Code: "server_unreachable", Message: "lost answer", Hint: "retry"}
			} else {
				resp.Joined = &delivery.SeatRef{Server: "https://team.example", Board: "work", Name: "codex", MemberID: "mem_new"}
				resp.Board = json.RawMessage(`{"name":"work"}`)
				resp.Member = json.RawMessage(`{"id":"mem_new","name":"codex"}`)
			}
			_ = delivery.WriteFrame(conn, resp)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	grant, _, err := a.createSessionBoard(context.Background(), delivery.SessionKey{Harness: "codex", ID: "s1"}, serverRef{URL: "https://team.example"}, delivery.BoardCreateOptions{Name: "work", Title: "Work"}, "member", "codex")
	<-done
	if err != nil || grant.Agent.Id != "mem_new" {
		t.Fatalf("retry: %+v %v", grant, err)
	}
	if len(seen) != 2 || seen[0].IdempotencyKey == "" || seen[0].IdempotencyKey != seen[1].IdempotencyKey || *seen[0].Create != *seen[1].Create {
		t.Fatalf("retry changed request: %v", seen)
	}
}

func TestAgentsCannotEnableTheirOwnAddPeopleGate(t *testing.T) {
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Getenv: func(k string) string {
		return map[string]string{"HOME": home, "ABOARD_HOME": home, "ABOARD_AGENT": "scout"}[k]
	}}}
	for _, args := range [][]string{{"agents-add-people", "on"}, {"agents-add-people", "off", "--as", "scout"}} {
		err := runBoard(context.Background(), a, args)
		if asError(err).Code != "human_command_in_session" {
			t.Fatalf("gate used person login: %v", err)
		}
	}
}

func TestAgentAddHandoffNamesItsSeatServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/me" {
			_, _ = w.Write([]byte(`{"name":"scout","kind":"agent","owner":"alex"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"add_people_not_allowed","message":"gate is off","hint":"ask person"}}`))
	}))
	defer srv.Close()
	e := lifecycleMachine(t, "https://linked.example", "human-only", agentCredential{Server: srv.URL, Board: "work", Name: "scout", Token: "seat-only"})
	e.env["ABOARD_AGENT"] = "scout"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	err := runBoard(context.Background(), a, []string{"add", "@pat", "--board", "work"})
	got := asError(err)
	if got.Code != "add_people_not_allowed" || !strings.Contains(got.Hint, "--server "+srv.URL) || strings.Contains(got.Hint, "linked.example") {
		t.Fatalf("wrong handoff: %+v", got)
	}
	err = runBoard(context.Background(), a, []string{"add", "@pat", "--board", "work", "--server", "https://linked.example"})
	if asError(err).Code != "agent_not_selected" {
		t.Fatalf("explicit server ignored: %v", err)
	}
}
