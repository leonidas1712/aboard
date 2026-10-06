package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Removing an agent ends its seat for good: its token stops working, the join codes it
// made stop, and its messages and read position stay under its member id. A person
// removes their own agents, a board's owners any agent on it, and a server admin any
// agent on the server; an agent may only leave, removing its own seat.

// MinPruneAge is the shortest disconnection PruneAgents accepts, so a brief outage never
// qualifies an agent for removal.
const MinPruneAge = time.Hour

// RemovedAgent is an agent whose seat ended, with its board and its person. Hidden is
// true when a server admin removed it on a private board they aren't on, so its name
// and the board's are withheld.
type RemovedAgent struct {
	Member Member
	Board  Board
	Owner  Human
	Hidden bool
}

// seatEnded refuses every request made with the token of a seat that was removed or
// left. It says when and which kind of person ended it, never who.
func seatEnded(seat Member, board string) *apierr.Error {
	when := ""
	if seat.RemovedAt != nil && len(*seat.RemovedAt) >= 10 {
		when = " on " + (*seat.RemovedAt)[:10]
	}
	by := deref(seat.RemovedBy)
	msg := fmt.Sprintf("%s was removed from %s%s%s.", seat.Name, board, removedByText(by), when)
	if by == RemovedBySelf {
		msg = fmt.Sprintf("%s left %s%s.", seat.Name, board, when)
	}
	e := apierr.New(http.StatusForbidden, "agent_removed", msg,
		"Ask your person to add a new agent: aboard join --board "+board)
	e.Details = map[string]any{"agent": seat.Name, "board": board, "removed_at": seat.RemovedAt, "removed_by": seat.RemovedBy}
	return e
}

func removedByText(by string) string {
	switch by {
	case RemovedByPerson:
		return " by its person"
	case RemovedByOwner:
		return " by an owner of the board"
	case RemovedByAdmin:
		return " by a server admin"
	}
	return ""
}

// endedSeat returns the refusal for an agent principal whose seat ended on a board that
// still exists, or nil while the seat is on its board.
func endedSeat(tx ReadTx, agent Member) error {
	m, err := tx.MemberByID(agent.ID)
	if errors.Is(err, ErrNotFound) {
		return apierr.Unauthorized()
	}
	if err != nil {
		return err
	}
	if m.Status == StatusActive {
		return nil
	}
	b, err := tx.BoardByID(m.BoardID)
	if err != nil {
		return err
	}
	if lifecycleOf(b) == LifecycleDeleted {
		return apierr.Unauthorized()
	}
	return seatEnded(m, b.Name)
}

// RemoveAgent removes the agent named agentSel (its name or member id) from the board
// boardSel (its name, or its id for a server admin managing a private board they aren't
// on). Only a person may: the agent's own person, one of the board's owners, or a
// server admin. Everything is checked in the write that removes it.
func (s *Service) RemoveAgent(ctx context.Context, p Principal, boardSel, agentSel string) (RemovedAgent, error) {
	if p.Delegation == nil && p.Human == nil {
		return RemovedAgent{}, apierr.New(http.StatusForbidden, "human_token_required",
			"Only a person can remove an agent from a board.",
			"An agent leaves its own seat with aboard leave, when its person asks. To remove another agent, ask your person to run: aboard agent remove "+agentSel+" --board "+boardSel)
	}
	var out RemovedAgent
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		person, err := caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		b, me, hidden, err := s.removalBoard(tx, p, person, boardSel)
		if err != nil {
			return err
		}
		// On a private board hidden from the caller, agents are named only by member id,
		// and a refusal names only what the caller sent.
		if hidden && !strings.HasPrefix(agentSel, "mem_") {
			return agentNotFound(agentSel, boardSel)
		}
		agent, err := agentOn(tx, b, agentSel)
		if hidden && err != nil {
			if e, ok := apierr.As(err); ok && e.Code == "agent_not_found" {
				return agentNotFound(agentSel, boardSel)
			}
		}
		if err != nil {
			return err
		}
		by, err := removerOf(tx, b, me, person, agent)
		if err != nil {
			return err
		}
		actor := actorOf(me)
		if me.ID == "" || me.Status != StatusActive {
			actor = events.Actor{Kind: "human", Name: ptr(person.Name)}
		}
		out, err = s.endSeat(tx, &b, agent, by, actor, now, nil)
		out.Hidden = hidden
		return err
	})
	if err != nil {
		return RemovedAgent{}, err
	}
	s.seatEndedNotice(out)
	return out, nil
}

// removalBoard finds the board an agent is removed from, and the caller's membership of
// it. A server admin may name a private board they aren't on by its id, which hides
// its names from them; anyone else must be on the board.
func (s *Service) removalBoard(tx ReadTx, p Principal, person Human, sel string) (Board, Member, bool, error) {
	if strings.HasPrefix(sel, "brd_") {
		b, err := tx.BoardByID(sel)
		if errors.Is(err, ErrNotFound) || (err == nil && lifecycleOf(b) == LifecycleDeleted) {
			return Board{}, Member{}, false, apierr.BoardNotFound(sel)
		}
		if err != nil {
			return Board{}, Member{}, false, err
		}
		me, err := tx.HumanMember(b.ID, person.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Board{}, Member{}, false, err
		}
		on := err == nil && me.Status == StatusActive
		switch {
		case on:
			return b, me, false, nil
		case person.Role == ServerAdmin:
			return b, Member{}, b.Visibility == BoardPrivate, nil
		case b.Visibility == BoardOpen && person.Role != ServerGuest:
			return Board{}, Member{}, false, notOnBoard(b.Name, person.Name)
		}
		return Board{}, Member{}, false, apierr.BoardNotFound(sel)
	}
	b, me, on, err := s.see(tx, p, sel)
	if err != nil {
		return Board{}, Member{}, false, err
	}
	if !on && person.Role != ServerAdmin {
		return Board{}, Member{}, false, notOnBoard(b.Name, person.Name)
	}
	return b, me, false, nil
}

// agentOn finds the agent on b named sel, by name or member id, while it is on the
// board.
func agentOn(tx ReadTx, b Board, sel string) (Member, error) {
	var m Member
	var err error
	if strings.HasPrefix(sel, "mem_") {
		m, err = tx.MemberByID(sel)
		if err == nil && m.BoardID != b.ID {
			err = ErrNotFound
		}
	} else {
		m, err = tx.MemberByName(b.ID, sel)
	}
	if errors.Is(err, ErrNotFound) || (err == nil && (m.Kind != "agent" || m.Status != StatusActive)) {
		return Member{}, agentNotFound(sel, b.Name)
	}
	return m, err
}

// removerOf says in what capacity the caller may remove agent from b: as its person, as
// one of the board's owners, or as a server admin. Anyone else is refused, with the
// owners to ask.
func removerOf(tx ReadTx, b Board, me Member, person Human, agent Member) (string, error) {
	on := me.ID != "" && me.Status == StatusActive
	switch {
	case agent.HumanID == person.ID:
		return RemovedByPerson, nil
	case on && me.Access == rules.AccessAdmin:
		return RemovedByOwner, nil
	case person.Role == ServerAdmin:
		return RemovedByAdmin, nil
	}
	return "", requireOwner(tx, b, me, "remove someone else's agent")
}

// endSeat ends agent's seat on b in the caller's transaction: it records why (with extra
// in the event's data) and stops the join codes the agent made.
func (s *Service) endSeat(tx Tx, b *Board, agent Member, by string, actor events.Actor, now time.Time, extra map[string]any) (RemovedAgent, error) {
	owner, err := tx.HumanByID(agent.HumanID)
	if err != nil {
		return RemovedAgent{}, err
	}
	if err := tx.RemoveAgent(agent.ID, stamp(now), by); err != nil {
		return RemovedAgent{}, err
	}
	typ := events.AgentRemoved
	if by == RemovedBySelf {
		typ = events.AgentLeft
	}
	data := map[string]any{"member_id": agent.ID, "name": agent.Name, "person_id": owner.ID, "owner": owner.Name, "removed_by": by}
	for k, v := range extra {
		data[k] = v
	}
	if _, err := s.append(tx, b, typ, actor, now, data); err != nil {
		return RemovedAgent{}, err
	}
	codes, err := tx.WorkingJoinCodes(b.ID, stamp(now))
	if err != nil {
		return RemovedAgent{}, err
	}
	var stop []JoinCode
	for _, jc := range codes {
		if jc.CreatedBy == agent.ID {
			stop = append(stop, jc)
		}
	}
	if err := s.cancelCodes(tx, b, stop, actor, now); err != nil {
		return RemovedAgent{}, err
	}
	ended, err := tx.MemberByID(agent.ID)
	if err != nil {
		return RemovedAgent{}, err
	}
	return RemovedAgent{Member: ended, Board: *b, Owner: owner}, nil
}

// seatEndedNotice wakes whatever waits on the board and on the agent's credentials, so
// the seat's long waits and streams check again and end.
func (s *Service) seatEndedNotice(r RemovedAgent) {
	s.notify.Changed(r.Board.ID)
	s.notify.Changed(credentialsKey(r.Owner.ID))
}

// LeaveAsAgent removes the calling agent's own seat from its board, recorded as left.
// It takes access away and nothing else, so an agent may do it by itself.
func (s *Service) LeaveAsAgent(ctx context.Context, p Principal) (RemovedAgent, error) {
	if p.Agent == nil {
		e := apierr.AgentRequired()
		e.Message = "Only an agent leaves its own seat this way."
		e.Hint = "A person leaves a board with aboard board leave, and removes an agent with aboard agent remove."
		return RemovedAgent{}, e
	}
	var out RemovedAgent
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		if _, err := caller(tx, p, stamp(now)); err != nil {
			return err
		}
		b, me, err := seatOf(tx, *p.Agent)
		if err != nil {
			return err
		}
		out, err = s.endSeat(tx, &b, me, RemovedBySelf, actorOf(me), now, nil)
		return err
	})
	if err != nil {
		return RemovedAgent{}, err
	}
	s.seatEndedNotice(out)
	return out, nil
}

// PruneInput asks PruneAgents for the agents disconnected for at least DisconnectedFor:
// the caller's own, or with All every agent on the server, for a server admin. Without
// DryRun, Agents names the member ids to remove.
type PruneInput struct {
	DisconnectedFor time.Duration
	All             bool
	DryRun          bool
	Agents          []string
}

// PrunedAgent is an agent PruneAgents removed or would remove, and since when it has
// been disconnected. Hidden is true for one on a private board a server admin isn't on.
type PrunedAgent struct {
	Member  Member
	Board   Board
	Owner   Human
	Since   string
	Hidden  bool
	removed RemovedAgent
}

// Pruned is what PruneAgents did: the agents removed, or for a dry run the agents that
// would be, longest disconnected first, and the ones asked for that stayed.
type Pruned struct {
	Input  PruneInput
	Agents []PrunedAgent
	Kept   []string
}

// PruneAgents lists, or removes, agents whose sessions have been disconnected for at
// least in.DisconnectedFor without a break. Agents no session ever reported are left
// out. Removing checks each agent named again in the write, so one that reconnected
// since a dry run stays.
func (s *Service) PruneAgents(ctx context.Context, p Principal, in PruneInput) (Pruned, error) {
	if p.Delegation == nil {
		if err := humanOnly(p, "prune agents", "aboard agent prune"); err != nil {
			return Pruned{}, err
		}
	}
	if in.DisconnectedFor < MinPruneAge {
		return Pruned{}, invalid("An agent must have been disconnected for at least an hour to be pruned.",
			"Use a longer time, such as --disconnected-for 7d.")
	}
	if !in.DryRun && len(in.Agents) == 0 {
		return Pruned{}, invalid("Removing needs the agents to remove, by member id, as a dry run listed them.",
			"Ask with dry_run first, then send the ids it listed in agents.")
	}
	out := Pruned{Input: in}
	run := func(tx ReadTx, write Tx) error {
		now := s.clk.Now()
		person, err := caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		if in.All && person.Role != ServerAdmin {
			return apierr.New(http.StatusForbidden, "server_admin_required",
				"Only an admin of this server can prune agents across the server.",
				"Prune your own agents: aboard agent prune.")
		}
		found, err := s.pruneCandidates(tx, person, in, now)
		if err != nil {
			return err
		}
		if in.DryRun {
			out.Agents = found
			return nil
		}
		byID := map[string]PrunedAgent{}
		// Several agents on one board append to its log in turn, so they share its head.
		heads := map[string]*Board{}
		for _, c := range found {
			byID[c.Member.ID] = c
			if heads[c.Board.ID] == nil {
				b := c.Board
				heads[b.ID] = &b
			}
		}
		for _, id := range in.Agents {
			c, ok := byID[id]
			if !ok {
				out.Kept = append(out.Kept, id)
				continue
			}
			delete(byID, id)
			by := RemovedByPerson
			actor := events.Actor{Kind: "human", Name: ptr(person.Name)}
			if me, err := tx.HumanMember(c.Board.ID, person.ID); err == nil && me.Status == StatusActive {
				actor = actorOf(me)
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if c.Member.HumanID != person.ID {
				by = RemovedByAdmin
			}
			if c.removed, err = s.endSeat(write, heads[c.Board.ID], c.Member, by, actor, now,
				map[string]any{"pruned": true, "disconnected_since": c.Since}); err != nil {
				return err
			}
			out.Agents = append(out.Agents, c)
		}
		return nil
	}
	var err error
	if in.DryRun {
		err = s.st.Read(ctx, func(tx ReadTx) error { return run(tx, nil) })
	} else {
		err = s.writeAs(ctx, p, func(tx Tx) error { return run(tx, tx) })
	}
	if err != nil {
		return Pruned{}, err
	}
	for _, a := range out.Agents {
		if !in.DryRun {
			s.seatEndedNotice(a.removed)
		}
	}
	if out.Kept == nil {
		out.Kept = []string{}
	}
	return out, nil
}

// pruneCandidates lists the agents in scope that have been disconnected for at least
// in.DisconnectedFor at now, longest disconnected first: the person's own on the
// boards they're on, or for in.All every agent on a board that isn't deleted.
func (s *Service) pruneCandidates(tx ReadTx, person Human, in PruneInput, now time.Time) ([]PrunedAgent, error) {
	var boards []Board
	var err error
	if in.All {
		seen, err := tx.BoardsSeenBy(person.ID)
		if err != nil {
			return nil, err
		}
		hidden, err := tx.PrivateBoardsNotOn(person.ID)
		if err != nil {
			return nil, err
		}
		boards = append(append(boards, seen...), hidden...)
	} else if boards, err = tx.BoardsOfHuman(person.ID); err != nil {
		return nil, err
	}
	var out []PrunedAgent
	owners := map[string]Human{}
	for _, b := range boards {
		if lifecycleOf(b) == LifecycleDeleted {
			continue
		}
		members, err := tx.Members(b.ID)
		if err != nil {
			return nil, err
		}
		onBoard := false
		for _, m := range members {
			onBoard = onBoard || (m.Kind == "human" && m.HumanID == person.ID && m.Status == StatusActive)
		}
		for _, m := range present(members) {
			if m.Kind != "agent" || (!in.All && m.HumanID != person.ID) {
				continue
			}
			since, ok := disconnectedSince(m, now)
			if !ok || now.Sub(since) < in.DisconnectedFor {
				continue
			}
			owner, known := owners[m.HumanID]
			if !known {
				if owner, err = tx.HumanByID(m.HumanID); err != nil {
					return nil, err
				}
				owners[m.HumanID] = owner
			}
			out = append(out, PrunedAgent{
				Member: m, Board: b, Owner: owner, Since: stamp(since),
				Hidden: !onBoard && b.Visibility == BoardPrivate,
			})
		}
	}
	slices.SortStableFunc(out, func(x, y PrunedAgent) int { return strings.Compare(x.Since, y.Since) })
	return out, nil
}

// disconnectedSince returns since when an agent has had no session, as the server saw it
// without a break, and false for an agent with a session now or whose presence no
// session ever reported.
func disconnectedSince(m Member, now time.Time) (time.Time, bool) {
	if m.Presence.State == "" {
		return time.Time{}, false
	}
	p := m.CurrentPresence(now)
	if p.State != PresenceNoSession {
		return time.Time{}, false
	}
	since, err := time.Parse(time.RFC3339Nano, p.Since)
	if err != nil {
		return time.Time{}, false
	}
	return since, true
}
