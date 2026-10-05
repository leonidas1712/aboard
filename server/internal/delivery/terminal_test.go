package delivery

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// A session's synchronous recheck can refuse access before an earlier server
// connection result leaves its mailbox. That older result cannot revive the agent.
func TestLateResultsCannotReviveAGoneAgent(t *testing.T) {
	ref := AgentRef{Server: "local", Board: "docs", Name: "reviewer"}
	for _, tc := range []struct {
		name   string
		result sessionMsg
	}{
		{"inbox success", sessionMsg{inbox: &inboxResult{agent: ref, msgs: []Message{{Seq: 1}}, cursor: 1, refresh: 3}}},
		{"inbox rejection", sessionMsg{inbox: &inboxResult{agent: ref, err: ErrUnauthorized, refresh: 3}}},
		{"ack success", sessionMsg{ack: &ackResult{agent: ref, upTo: 1}}},
		{"ack rejection", sessionMsg{ack: &ackResult{agent: ref, err: ErrUnauthorized}}},
		{"presence rejection", sessionMsg{refused: &refusal{agent: ref, err: ErrUnauthorized}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{
				cfg: Config{Clock: clock.Real{}}, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				problems: map[AgentKey]string{}, modes: map[AgentKey]Mode{ref.Key(): ModeOff},
			}
			a := newAgentState(ref, false)
			s := &session{
				d: d, agents: map[AgentKey]*agentState{ref.Key(): a},
				reported: map[AgentKey]reportedPresence{}, refreshing: map[int64]bool{3: true},
			}
			s.setProblem(a, ReasonBoardGone)
			s.handle(context.Background(), tc.result)
			if !a.gone() || d.problems[ref.Key()] != ReasonBoardGone {
				t.Fatalf("late result changed terminal problem: agent=%q, daemon=%q", a.problem, d.problems[ref.Key()])
			}
			if a.fetched || a.ackedUpTo != 0 || len(a.unread) != 0 || a.acking {
				t.Fatalf("late result consumed content or changed read state: %+v", a)
			}
			if tc.result.inbox != nil && s.refreshing[3] {
				t.Fatal("terminal result left a refresh gate waiting")
			}
		})
	}
}
