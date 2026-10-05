package delivery

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestSeatRenameKeepsItsModeAndSession(t *testing.T) {
	old := AgentRef{Server: "local", Board: "docs", Name: "reviewer", MemberID: "mem_original"}
	renamed := old
	renamed.Name = "reader"
	s := &session{}
	d := &Daemon{modes: map[AgentKey]Mode{old.Key(): ModeHumans}, owners: map[AgentKey]*session{old.Key(): s}}
	if got := d.mode(renamed); got != ModeHumans {
		t.Errorf("rename changed delivery mode to %s", got)
	}
	if got := d.owner(renamed); got != s {
		t.Error("rename lost the bound session")
	}
	replacement := old
	replacement.MemberID = "mem_replacement"
	if d.owner(replacement) != nil || d.mode(replacement) != ModeFocused {
		t.Error("same-name replacement inherited session or mode")
	}
}

func TestSeatRenameCannotBypassTerminalState(t *testing.T) {
	old := AgentRef{Server: "local", Board: "docs", Name: "reviewer", MemberID: "mem_original"}
	renamed := old
	renamed.Name = "reader"
	d := &Daemon{
		cfg: Config{Clock: clock.Real{}}, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		problems: map[AgentKey]string{}, modes: map[AgentKey]Mode{old.Key(): ModeOff},
	}
	a := newAgentState(old, false)
	s := &session{
		d: d, agents: map[AgentKey]*agentState{old.Key(): a},
		reported: map[AgentKey]reportedPresence{}, refreshing: map[int64]bool{3: true},
	}
	s.handle(context.Background(), sessionMsg{refused: &refusal{agent: renamed, err: ErrBoardGone}})
	if !a.gone() {
		t.Fatal("renamed seat escaped its terminal refusal")
	}
	s.handle(context.Background(), sessionMsg{inbox: &inboxResult{
		agent: old,
		msgs:  []Message{{Seq: 1}}, cursor: 1, refresh: 3,
	}})
	if a.fetched || a.ackedUpTo != 0 || len(a.unread) != 0 {
		t.Fatal("older display name revived terminal state")
	}
}

func TestSeatRenameKeepsReplyAndInboxHolds(t *testing.T) {
	old := AgentRef{Server: "local", Board: "docs", Name: "reviewer", MemberID: "mem_original"}
	renamed := old
	renamed.Name = "reader"
	s := &session{holds: map[*hold]bool{{agent: old, replyTo: 4}: true}, readers: map[*reading]bool{{agent: old}: true}}
	if !s.held(renamed, Message{ReplyToSeq: 4}) || !s.beingRead(renamed) {
		t.Fatal("rename escaped a command's held delivery")
	}
	replacement := old
	replacement.MemberID = "mem_replacement"
	if s.held(replacement, Message{ReplyToSeq: 4}) || s.beingRead(replacement) {
		t.Fatal("replacement inherited another seat's holds")
	}
}
