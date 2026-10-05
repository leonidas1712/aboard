package cli

import (
	"bufio"
	"context"
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
	const srv = "http://127.0.0.1:7400"
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
					"ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:7400",
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
