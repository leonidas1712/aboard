package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

// fakeDaemon answers the control socket of a's home: every agents request gets agents,
// and every other operation an empty answer.
func fakeDaemon(t *testing.T, a *app, agents []delivery.AgentRef) {
	t.Helper()
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	l, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				var req delivery.Request
				if delivery.ReadFrame(bufio.NewReader(c), &req) != nil {
					return
				}
				resp := delivery.Response{V: delivery.ProtocolVersion}
				if req.Op == delivery.OpAgents {
					resp.Agents = agents
				}
				_ = delivery.WriteFrame(c, resp)
			}()
		}
	}()
	a.daemonChecked = true
}

// In a session with several seats, a command that acts on one board needs --board even
// when --as or ABOARD_AGENT names the agent, and board_ambiguous comes before
// agent_ambiguous.
func TestBoardAmbiguousEvenWithAnAgentNamed(t *testing.T) {
	const srv = "http://127.0.0.1:1"
	seats := []delivery.AgentRef{{Server: srv, Board: "general", Name: "claude"}, {Server: srv, Board: "plans", Name: "claude"}}
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "general", Name: "claude", Token: "t1"},
		{Server: srv, Board: "plans", Name: "claude", Token: "t2"},
	}}
	for _, tc := range []struct{ name, as, env, board, wantCode, wantToken string }{
		{"--as", "claude", "", "", "board_ambiguous", ""},
		{"ABOARD_AGENT", "", "claude", "", "board_ambiguous", ""},
		{"--as with --board", "claude", "", "plans", "", "t2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			a := &app{env: Env{Dir: home, Getenv: func(k string) string {
				return map[string]string{
					"HOME": home, "ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:1",
					"ABOARD_SESSION": "claude-code:s1", "ABOARD_AGENT": tc.env,
				}[k]
			}}}
			fakeDaemon(t, a, seats)
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
				t.Fatal(err)
			}
			_, cred, err := a.agentTarget(context.Background(), tc.board, tc.as)
			if tc.wantCode != "" {
				if code := asError(err).Code; code != tc.wantCode {
					t.Fatalf("code %q (%v), want %s", code, err, tc.wantCode)
				}
				return
			}
			if err != nil || cred.Token != tc.wantToken {
				t.Fatalf("got %+v, %v", cred, err)
			}
		})
	}
}

// runCommand runs one command as Run would, after its subagent and launch steps, and
// returns its error.
func runCommand(ctx context.Context, a *app, args []string) error {
	for _, c := range commands() {
		if c.name == args[0] {
			return c.run(ctx, a, args[1:])
		}
	}
	return newError("invalid_request", "no command "+args[0], "")
}

// Every command that acts on one board, run in a session with several seats without
// --board, fails with board_ambiguous before it reads or sends anything: with no agent
// named, with --as, and with ABOARD_AGENT.
func TestEveryOneBoardCommandNeedsBoardWithSeveralSeats(t *testing.T) {
	const srv = "http://127.0.0.1:1"
	seats := []delivery.AgentRef{{Server: srv, Board: "general", Name: "claude"}, {Server: srv, Board: "plans", Name: "claude"}}
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "general", Name: "claude", Token: "t1"},
		{Server: srv, Board: "plans", Name: "claude", Token: "t2"},
	}}
	commands := [][]string{
		{"say", "hello"},
		{"say", "--reply", "3", "hello"},
		{"react", "3", "eyes"},
		{"read"},
		{"read", "--thread", "3"},
		{"read", "--threads"},
		{"read", "--receipts", "3"},
		{"board", "title", "New title"},
		{"board", "people"},
		{"delivery"},
		{"audit", "verify"},
		{"status"},
		{"boards"},
		{"inbox"},
	}
	for _, how := range []struct{ name, as, env string }{{"the session's seat", "", ""}, {"--as", "claude", ""}, {"ABOARD_AGENT", "", "claude"}} {
		for _, args := range commands {
			if how.as == "" && how.env == "" && (args[0] == "status" || args[0] == "boards" || args[0] == "audit" || args[0] == "inbox") {
				continue // these list every seat, or act as the person, with no agent named
			}
			if how.as == "" && args[0] == "audit" {
				continue // audit verify acts as an agent only with --as (cli.yaml)
			}
			if how.as != "" {
				args = append(append([]string{}, args...), "--as", how.as)
			}
			t.Run(how.name+"/"+args[0]+"/"+args[len(args)-1], func(t *testing.T) {
				home := t.TempDir()
				var out bytes.Buffer
				exe := func() (string, error) { return "aboard", nil }
				a := &app{json: true, env: Env{Dir: home, Stdout: &out, Stderr: &out, Executable: exe, Getenv: func(k string) string {
					return map[string]string{
						"HOME": home, "ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:1",
						"ABOARD_SESSION": "claude-code:s1", "ABOARD_AGENT": how.env,
					}[k]
				}}}
				a.localChecked = true
				fakeDaemon(t, a, seats)
				p, err := a.paths()
				if err != nil {
					t.Fatal(err)
				}
				if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
					t.Fatal(err)
				}
				err = runCommand(context.Background(), a, args)
				if err == nil {
					t.Fatalf("%v succeeded: %s", args, out.String())
				}
				if code := asError(err).Code; code != "board_ambiguous" {
					raw, _ := json.Marshal(asError(err))
					t.Fatalf("%v: %s %s", args, code, raw)
				}
			})
		}
	}
}

// aboard resume never picks a same-name agent on its own: a name on several boards,
// even on different servers, needs --board.
func TestResumeNeverPicksASameNameAgent(t *testing.T) {
	const srv, other = "http://127.0.0.1:1", "https://team.example.com"
	for _, creds := range []credentials{
		{Agents: []agentCredential{{Server: srv, Board: "general", Name: "claude", Token: "t1"}, {Server: srv, Board: "plans", Name: "claude", Token: "t2"}}},
		{Agents: []agentCredential{{Server: srv, Board: "general", Name: "claude", Token: "t1"}, {Server: other, Board: "general", Name: "claude", Token: "t2"}}},
	} {
		home := t.TempDir()
		var out bytes.Buffer
		a := &app{json: true, env: Env{Dir: home, Stdout: &out, Stderr: &out, Getenv: func(k string) string {
			return map[string]string{"HOME": home, "ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:1", "ABOARD_SESSION": "claude-code:s1"}[k]
		}}}
		a.localChecked = true
		fakeDaemon(t, a, nil)
		p, err := a.paths()
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runCommand(context.Background(), a, []string{"resume", "claude"}); asError(err).Code != "agent_ambiguous" {
			t.Fatalf("resume with %v: %v", creds.Agents, err)
		}
	}
}
