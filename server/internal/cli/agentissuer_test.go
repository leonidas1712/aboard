package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestAgentCommandsFilterIssuerBeforeUsingIdenticalSeatNames(t *testing.T) {
	var callsA, callsB atomic.Int32
	serving := func(token string, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/info" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "features": []string{"tasks"}})
				return
			}
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("credential crossed issuer")
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path != "/v1/boards/work/tasks" {
				t.Errorf("unexpected route %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"tasks":[],"counts":{"open":0,"in_progress":0,"blocked":0,"done":0,"cancelled":0}}`))
		}))
	}
	first := serving("seat-a", &callsA)
	defer first.Close()
	second := serving("seat-b", &callsB)
	defer second.Close()
	for _, selectBy := range []string{"flag", "env", "session"} {
		t.Run(selectBy, func(t *testing.T) {
			e := lifecycleMachine(t, first.URL, "never-human", agentCredential{Server: first.URL, Board: "work", Name: "writer", MemberID: "mem_a", Token: "seat-a"})
			p, _ := resolvePaths(e.getenv)
			if err := writeJSONFile(p.credentials(), credentials{Agents: []agentCredential{{Server: first.URL, Board: "work", Name: "writer", MemberID: "mem_a", Token: "seat-a"}, {Server: second.URL, Board: "work", Name: "writer", MemberID: "mem_b", Token: "seat-b"}}}, 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			a := &app{env: e.environment(&out, &out), json: true, localChecked: true}
			args := []string{"list", "--board", "work", "--server", second.URL}
			if selectBy == "flag" {
				args = append(args, "--as", "writer")
			}
			if selectBy == "env" {
				e.env["ABOARD_AGENT"] = "writer"
			}
			if selectBy == "session" {
				e.env["ABOARD_SESSION"] = "claude-code:s1"
				fakeDaemon(t, a, []delivery.AgentRef{{Server: first.URL, Board: "work", Name: "writer", MemberID: "mem_a"}, {Server: second.URL, Board: "work", Name: "writer", MemberID: "mem_b"}})
			}
			beforeA, beforeB := callsA.Load(), callsB.Load()
			if err := runTask(context.Background(), a, args); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Server serverRef `json:"server"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if callsA.Load() != beforeA || callsB.Load() != beforeB+1 || result.Server.URL != second.URL {
				t.Fatalf("wrong issuer result=%s A=%d B=%d", out.String(), callsA.Load()-beforeA, callsB.Load()-beforeB)
			}
		})
	}
}

func TestNamedAgentNeverUsesFolderToResolveIssuerAmbiguity(t *testing.T) {
	e := lifecycleMachine(t, "https://one.example", "never-human", agentCredential{})
	a := &app{env: e.environment(&bytes.Buffer{}, &bytes.Buffer{})}
	creds := credentials{Agents: []agentCredential{{Server: "https://one.example", Board: "work", Name: "writer", Token: "one"}, {Server: "https://two.example", Board: "work", Name: "writer", Token: "two"}}}
	_, _, err := a.agentByName(creds, "writer", "work")
	if err == nil || asError(err).Code != "agent_ambiguous" || !strings.Contains(asError(err).Hint, "--server") {
		t.Fatalf("folder selected issuer or missing qualification: %v", err)
	}
}

func TestUniqueNamedSeatNarrowsBeforeSessionAmbiguity(t *testing.T) {
	e := lifecycleMachine(t, "https://one.example", "never-human", agentCredential{Server: "https://one.example", Board: "work", Name: "writer", MemberID: "mem_a", Token: "own-seat"})
	e.env["ABOARD_SESSION"] = "claude-code:s1"
	a := &app{env: e.environment(&bytes.Buffer{}, &bytes.Buffer{})}
	fakeDaemon(t, a, []delivery.AgentRef{{Server: "https://one.example", Board: "work", Name: "writer", MemberID: "mem_a"}, {Server: "https://two.example", Board: "work", Name: "reviewer", MemberID: "mem_b"}})
	selected, cred, err := a.agentTarget(context.Background(), "", "writer")
	if err != nil || selected.server.URL != "https://one.example" || cred.Token != "own-seat" {
		t.Fatalf("named seat: %+v %+v %v", selected, cred, err)
	}
}

func TestUnknownAndUnboundAgentSelectionsNeverUsePersonCredential(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	for _, mode := range []string{"unknown name", "unknown issuer", "unbound session"} {
		t.Run(mode, func(t *testing.T) {
			e := lifecycleMachine(t, srv.URL, "available-person-key", agentCredential{Server: srv.URL, Board: "work", Name: "writer", MemberID: "mem_one", Token: "own-seat"})
			var out bytes.Buffer
			a := &app{env: e.environment(&out, &out), json: true, localChecked: true}
			args := []string{"list", "--as", "missing"}
			if mode == "unknown issuer" {
				args = []string{"list", "--as", "writer", "--server", "https://unknown.example"}
			}
			if mode == "unbound session" {
				args = []string{"list"}
				e.env["ABOARD_SESSION"] = "claude-code:s1"
				fakeDaemon(t, a, nil)
			}
			before := calls.Load()
			err := runTask(context.Background(), a, args)
			if err == nil || asError(err).Code != "agent_not_selected" || calls.Load() != before {
				t.Fatalf("fallback: mode=%s calls=%d err=%v", mode, calls.Load()-before, err)
			}
		})
	}
}

func TestMultiIssuerSeatSummariesQualifyBoardsAndFollowups(t *testing.T) {
	rows := []seatRow{{Server: "https://one.example", Board: "work", Name: "writer"}, {Server: "https://two.example", Board: "work", Name: "writer"}}
	text, usage := seatsText(rows[0].Server, rows)
	if !strings.Contains(text, "https://one.example") || !strings.Contains(text, "https://two.example") || !strings.Contains(strings.Join(usage, " "), "--server https://two.example --board work") {
		t.Fatalf("unqualified status: %s %v", text, usage)
	}
	inbox := inboxSeatsText(inboxSeatsOutput{Seats: []inboxSeat{{Server: rows[0].Server, Board: "work"}, {Server: rows[1].Server, Board: "work"}}})
	if !strings.Contains(inbox, "https://one.example · work") || !strings.Contains(inbox, "https://two.example · work") {
		t.Fatalf("unqualified inbox: %s", inbox)
	}
}

func TestSelectedServerFlagKeepsExplicitBoardIssuerPrecedence(t *testing.T) {
	a := &app{agentServerFlag: "agent-issuer"}
	if got := a.selectedServerFlag(); got != "agent-issuer" {
		t.Fatalf("common selector: %s", got)
	}
	a.boardServerFlag = "board-issuer"
	if got := a.selectedServerFlag(); got != "board-issuer" {
		t.Fatalf("board selector: %s", got)
	}
}
