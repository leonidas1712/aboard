package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type approvalNoticeOrigins interface {
	ApprovalSeatWatches(context.Context, AgentRef) ([]ApprovalWatch, error)
}

func noticeID(kind, issuer, source string) string {
	hash := sha256.Sum256([]byte(kind + "\x00" + issuer + "\x00" + source))
	return "ntc_" + hex.EncodeToString(hash[:])
}

// noticeTransportID reserves negative JavaScript-safe ids for existing legacy delivery.
func noticeTransportID(id string) int64 {
	hash := sha256.Sum256([]byte(id))
	value := binary.BigEndian.Uint64(hash[:8]) & ((1 << 53) - 1)
	if value == 0 {
		value = 1
	}
	return -int64(value)
}

func (s *session) collectApprovalNotices(ctx context.Context) {
	j, ok := s.d.cfg.Journal.(NoticeJournal)
	origins, exists := s.d.cfg.Journal.(approvalNoticeOrigins)
	if !ok || !exists || s.d.cfg.ApprovalFor == nil {
		return
	}
	for _, a := range s.agents {
		if a.adopting || a.problem != "" {
			continue
		}
		rows, err := origins.ApprovalSeatWatches(ctx, a.ref)
		if err != nil {
			continue
		}
		for _, w := range rows {
			if w.Notified || w.Agent.Key() != a.ref.Key() {
				continue
			}
			_ = j.SaveNotice(ctx, DurableNotice{ID: noticeID("approval_outcome", w.Agent.Server, w.ID), Kind: "approval_outcome", SourceID: w.ID, BoardID: w.BoardID, Agent: w.Agent, Session: w.Session, Boot: w.Boot, Generation: w.Generation})
		}
	}
}

// renderNotice rechecks authority with the bound seat's issuer before emitting metadata.
func (s *session) renderNotice(ctx context.Context, n DurableNotice, a *agentState) (string, bool) {
	switch n.Kind {
	case "approval_outcome":
		if s.d.cfg.ApprovalFor == nil {
			return "", false
		}
		runtime := s.d.cfg.ApprovalFor(a.ref.Server)
		if runtime == nil {
			return "", false
		}
		row, err := runtime.Get(ctx, n.SourceID, a.ref)
		if err != nil {
			return "", noticeAuthorityRefused(err)
		}
		if row.ID != n.SourceID || row.AgentID != a.ref.MemberID {
			return "", true
		}
		switch row.State {
		case "executed", "declined", "expired":
		default:
			return "", false
		}
		// Only the original exact runtime may select an endpoint, never a resumed notice recipient.
		if n.Session == s.key && n.Boot == s.boot && n.Generation == a.generation {
			_, selected := s.selectApprovalPairing(ctx, row, ApprovalWatch{ID: n.SourceID, BoardID: n.BoardID, Agent: n.Agent, Session: n.Session, Boot: n.Boot, Generation: n.Generation})
			if selected {
				return fmt.Sprintf("Aboard approval %s on %s was %s. Run aboard approvals show %s --server %s --board %s --json to read its outcome.\nYour invite was approved. Collect its link with the command above.", row.ID, a.ref.Server, row.State, shellWord(row.ID), shellWord(a.ref.Server), shellWord(a.ref.Board)), false
			}
		}
		cmd := "aboard approvals show " + shellWord(row.ID) + " --server " + shellWord(a.ref.Server) + " --board " + shellWord(a.ref.Board) + " --json"
		return fmt.Sprintf("Aboard approval %s on %s was %s. Run %s to read its outcome.", row.ID, a.ref.Server, row.State, cmd), false
	case "colleague_arrival":
		if s.d.cfg.PairingFor == nil {
			return "", false
		}
		runtime := s.d.cfg.PairingFor(a.ref.Server)
		if runtime == nil {
			return "", false
		}
		row, err := runtime.Get(ctx, n.SourceID)
		if err != nil {
			return "", noticeAuthorityRefused(err)
		}
		if row.ID != n.SourceID || row.InitiatingAgentID != a.ref.MemberID || (n.BoardID != "" && row.BoardID != n.BoardID) {
			return "", true
		}
		if row.State == "declined" || row.State == "cancelled" || row.State == "expired" {
			return "", true
		}
		if row.RecipientID == "" {
			return "", false
		}
		var display struct {
			Handle string `json:"recipient_handle"`
		}
		_ = json.Unmarshal(row.Display, &display)
		return fmt.Sprintf("Aboard colleague %s joined board %s at %s. Say hello to them on this board.", display.Handle, a.ref.Board, a.ref.Server), false
	}
	return "", true
}

func (s *session) nextNotice(ctx context.Context) (notice DurableNotice, text string) {
	j, ok := s.d.cfg.Journal.(NoticeJournal)
	if !ok || !s.open {
		return DurableNotice{}, ""
	}
	s.collectApprovalNotices(ctx)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		if a.problem == ReasonUnauthorized || a.problem == ReasonBoardGone {
			rows, _ := j.Notices(rctx, ref)
			for _, n := range rows {
				if !n.Handed {
					_ = j.CancelNotice(rctx, n.ID)
				}
			}
			continue
		}
		if a.adopting || a.problem != "" || s.d.mode(ref) == ModeOff || s.beingRead(ref) {
			continue
		}
		rows, err := j.Notices(rctx, ref)
		if err != nil {
			continue
		}
		for _, n := range rows {
			if n.Handed || n.Cancelled {
				continue
			}
			text, invalid := s.renderNotice(rctx, n, a)
			if invalid {
				_ = j.CancelNotice(rctx, n.ID)
				continue
			}
			if text == "" {
				continue
			}
			return n, text
		}
	}
	return DurableNotice{}, ""
}

func (s *session) tryNotice(ctx context.Context) {
	if !s.open || s.noticeAt.IsZero() || !s.canTake() || len(s.refreshing) > 0 || len(s.offers(s.queueFilter())) > 0 {
		return
	}
	defer s.armNotices(ctx)
	n, text := s.nextNotice(ctx)
	if text == "" || !s.recheck(ctx) {
		return
	}
	// The fresh read can discover messages that were not in the cached offers.
	// Keep the waiter for their ordinary combined handoff.
	if len(s.offers(s.queueFilter())) > 0 {
		return
	}
	a := s.agents[n.Agent.Key()]
	if a == nil || a.adopting || a.problem != "" || s.d.mode(a.ref) == ModeOff || s.beingRead(a.ref) || s.d.owner(a.ref) != s || s.d.generation(a.ref) != a.generation {
		return
	}
	_, err := s.hand(ctx, Handover{SessionID: s.key.ID, ID: noticeTransportID(n.ID), Class: ClassMixed, Bundle: text, Waiter: s.waiter})
	if s.adapter.WaitsForIdle() || s.waiter != nil {
		s.waiter = nil
	}
	if err != nil || s.d.owner(a.ref) != s || s.d.generation(a.ref) != a.generation {
		return
	}
	s.markNoticeHanded(ctx, n)
}

func (s *session) markNoticeHanded(ctx context.Context, n DurableNotice) {
	if n.ID == "" {
		return
	}
	a := s.agents[n.Agent.Key()]
	if a == nil || a.adopting || a.problem != "" || s.d.owner(a.ref) != s || s.d.generation(a.ref) != a.generation {
		return
	}
	if j, ok := s.d.cfg.Journal.(NoticeJournal); ok {
		if err := j.MarkNoticeHanded(ctx, n.ID); err != nil {
			s.d.log.Warn("save handed notice", "notice", n.ID)
		}
	}
	s.armNotices(ctx)
}

func (d *Daemon) retainArrivalNotice(ctx context.Context, key SessionKey, row PairingRequest, a AgentRef) {
	j, ok := d.cfg.Journal.(NoticeJournal)
	if !ok || row.InitiatingAgentID != a.MemberID {
		return
	}
	sessions, err := d.cfg.Journal.Sessions(ctx)
	if err != nil {
		return
	}
	boot := ""
	for _, s := range sessions {
		if s.Key == key && s.Open {
			boot = s.Boot
		}
	}
	if boot == "" {
		return
	}
	bindings, err := d.cfg.Journal.Bindings(ctx)
	if err != nil {
		return
	}
	for _, b := range bindings {
		if b.Session == key && b.Agent.Key() == a.Key() {
			err := j.SaveNotice(ctx, DurableNotice{ID: noticeID("colleague_arrival", a.Server, row.ID), Kind: "colleague_arrival", SourceID: row.ID, BoardID: row.BoardID, Agent: a, Session: key, Boot: boot, Generation: b.Generation})
			if err == nil {
				if s := d.session(key, false); s != nil {
					s.mail.put(sessionMsg{noticeWake: true})
				}
			}
			return
		}
	}
}

func noticeAuthorityRefused(err error) bool {
	if problemOf(err) != "" {
		return true
	}
	var wire *WireError
	if !errors.As(err, &wire) {
		return false
	}
	switch wire.Code {
	case "forbidden", "unauthorized", "invalid_token", "agent_removed", "board_not_found", "approval_not_found", "pairing_not_found":
		return true
	}
	return false
}

func (s *session) armNotices(ctx context.Context) {
	j, ok := s.d.cfg.Journal.(NoticeJournal)
	if !ok {
		return
	}
	s.collectApprovalNotices(ctx)
	for _, a := range s.agents {
		rows, err := j.Notices(ctx, a.ref)
		if err != nil {
			continue
		}
		for _, n := range rows {
			if !n.Handed && !n.Cancelled {
				s.noticeAt = s.now().Add(time.Second)
				return
			}
		}
	}
	s.noticeAt = time.Time{}
}
