package delivery

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestOldGenerationResultsCannotChangeCurrentSeat(t *testing.T) {
	for _, kind := range []string{"inbox", "inbox refusal", "ack", "presence"} {
		t.Run(kind, func(t *testing.T) {
			ref := AgentRef{Server: "local", Board: "docs", Name: "reader", MemberID: "mem_reader"}
			a := newAgentState(ref, false)
			a.generation = 2
			d := &Daemon{cfg: Config{Clock: clock.Real{}}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), modes: map[AgentKey]Mode{ref.Key(): ModeOff}, problems: map[AgentKey]string{}}
			s := &session{d: d, agents: map[AgentKey]*agentState{ref.Key(): a}, reported: map[AgentKey]reportedPresence{}, refreshing: map[int64]bool{}}
			ctx := context.Background()
			switch kind {
			case "inbox":
				s.onInbox(ctx, inboxResult{agent: ref, cursor: 10, msgs: []Message{{Seq: 11}}, generation: 1})
			case "inbox refusal":
				s.onInbox(ctx, inboxResult{agent: ref, err: ErrBoardGone, generation: 1})
			case "ack":
				s.onAck(ctx, ackResult{agent: ref, upTo: 10, generation: 1})
			case "presence":
				s.handle(ctx, sessionMsg{refused: &refusal{agent: ref, err: ErrBoardGone, generation: 1}})
			}
			if a.ackedUpTo != 0 || a.fetched || len(a.unread) != 0 || a.problem != "" {
				t.Fatalf("old %s changed new generation: cursor=%d fetched=%v unread=%d problem=%q", kind, a.ackedUpTo, a.fetched, len(a.unread), a.problem)
			}
		})
	}
}
