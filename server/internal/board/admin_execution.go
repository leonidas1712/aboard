package board

import (
	"context"
	"errors"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// adminTxStore runs the existing operation rules on the caller's transaction. It
// never starts, commits or rolls back a database transaction.
type adminTxStore struct{ tx Tx }

func (st adminTxStore) Read(_ context.Context, fn func(ReadTx) error) error { return fn(st.tx) }
func (st adminTxStore) Write(_ context.Context, fn func(Tx) error) error    { return fn(st.tx) }

type adminNotes struct {
	target Notifier
	keys   map[string]bool
}

func newAdminNotes(n Notifier) *adminNotes             { return &adminNotes{target: n, keys: map[string]bool{}} }
func (n *adminNotes) Watch(key string) <-chan struct{} { return n.target.Watch(key) }
func (n *adminNotes) Changed(key string)               { n.keys[key] = true }
func (n *adminNotes) flush() {
	for key := range n.keys {
		n.target.Changed(key)
	}
}

func (s *Service) executeAdminTx(ctx context.Context, tx Tx, owner Principal, agent Member, a AdminAction, auth AdminAuthorization, notes *adminNotes) (*NewServerInvite, error) {
	execution := *s
	execution.st = adminTxStore{tx: tx}
	execution.notify = notes
	execution.adminAuthorization = &auth
	actor := actorOf(agent)
	execution.adminActor = &actor
	var name, handle string
	if a.BoardID != "" {
		b, err := tx.BoardByID(a.BoardID)
		if err != nil {
			return nil, err
		}
		name = b.Name
	}
	if a.PersonID != "" {
		h, err := tx.HumanByID(a.PersonID)
		if err != nil {
			return nil, err
		}
		handle = h.Name
	}
	switch a.Kind {
	case "invite_people":
		out, err := execution.CreateServerInviteWithInput(ctx, owner, *a.Invite)
		return &out, err
	case "add_people":
		if m, err := tx.HumanMember(a.BoardID, a.PersonID); err == nil && m.Status == StatusActive {
			return nil, nil
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		_, err := execution.AddPerson(ctx, owner, name, handle)
		return nil, err
	case "set_server_role":
		_, err := execution.SetServerRole(ctx, owner, handle, a.Role)
		return nil, err
	case "set_board_role":
		if a.Role == "owner" {
			_, _, err := execution.MakeOwner(ctx, owner, name, handle)
			return nil, err
		}
		b, me, err := execution.access(tx, owner, name)
		if err != nil {
			return nil, err
		}
		target, err := onBoardByHandle(tx, b, handle)
		if err != nil {
			return nil, err
		}
		if target.Member.Access == rules.AccessMember {
			return nil, nil
		}
		if err := tx.SetMemberAccess(target.Member.ID, rules.AccessMember); err != nil {
			return nil, err
		}
		if _, err = execution.append(tx, &b, events.PersonRoleChanged, actorOf(me), s.clk.Now(), map[string]any{"member_id": target.Member.ID, "person_id": target.Person.ID, "name": target.Member.Name, "before": "owner", "after": "member"}); err != nil {
			return nil, err
		}
		notes.Changed(b.ID)
		return nil, nil
	case "remove_person":
		if a.BoardID == "" {
			_, err := execution.RemoveFromServer(ctx, owner, handle, false)
			return nil, err
		}
		_, err := execution.RemovePerson(ctx, owner, name, handle)
		return nil, err
	case "revoke_key":
		_, err := execution.RevokeKey(ctx, owner, a.KeyID)
		return nil, err
	case "set_board_policy":
		_, err := execution.UpdateBoard(ctx, owner, name, Change{Policy: a.Policy})
		return nil, err
	}
	return nil, adminInvalid("Unknown administrative action.", "Use a supported action.")
}

// RequestAddPersonTx admits or holds one immutable admission inside the caller's
// transaction. It never starts a transaction or publishes notifications. After a
// successful commit, the caller must wake TouchedKeys using NotifyAdminResult.
func (s *Service) RequestAddPersonTx(ctx context.Context, tx Tx, p Principal, boardID, personID string) (AdminActionResult, error) {
	notes := newAdminNotes(s.notify)
	var out AdminActionResult
	var err error
	owner, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return out, err
	}
	check := Principal{Human: &owner, KeyID: p.KeyID, Browser: p.Browser, browserDigest: p.browserDigest}
	if p.Agent != nil {
		check, _, err = s.adminOwner(tx, p)
		if err != nil {
			return out, err
		}
	}
	if err := s.validateAdmin(tx, check, AdminAction{Kind: "add_people", BoardID: boardID, PersonID: personID}); err != nil {
		return out, err
	}
	if member, err := tx.HumanMember(boardID, personID); err == nil && member.Status == StatusActive {
		out.State = "executed"
		return out, nil
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if p.Agent != nil {
		b, lookupErr := tx.BoardByID(boardID)
		if lookupErr != nil {
			return out, lookupErr
		}
		h, lookupErr := tx.HumanByID(personID)
		if lookupErr != nil {
			return out, lookupErr
		}
		_, _, _, authorityErr := s.addPersonAuthority(tx, p, b.Name, h.Name)
		if authorityErr == nil {
			direct := *s
			direct.st = adminTxStore{tx: tx}
			direct.notify = notes
			_, err = direct.AddPerson(ctx, p, b.Name, h.Name)
			if err != nil {
				return AdminActionResult{}, err
			}
			out.State = "executed"
		} else if e, ok := apierr.As(authorityErr); ok && e.Code == "add_people_not_allowed" {
			out, err = s.requestAdminTx(ctx, tx, p, AdminAction{Kind: "add_people", BoardID: boardID, PersonID: personID}, "", notes)
		} else {
			return out, authorityErr
		}
	} else {
		owner, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return out, err
		}
		actorPrincipal := Principal{Human: &owner, KeyID: p.KeyID, Browser: p.Browser, browserDigest: p.browserDigest}
		a := AdminAction{Kind: "add_people", BoardID: boardID, PersonID: personID}
		if err := s.validateAdmin(tx, actorPrincipal, a); err != nil {
			return out, err
		}
		b, err := tx.BoardByID(boardID)
		if err != nil {
			return out, err
		}
		h, err := tx.HumanByID(personID)
		if err != nil {
			return out, err
		}
		if m, err := tx.HumanMember(boardID, personID); err == nil && m.Status == StatusActive {
			out.State = "executed"
			return out, nil
		}
		execution := *s
		execution.st = adminTxStore{tx: tx}
		execution.notify = notes
		_, err = execution.AddPerson(ctx, actorPrincipal, b.Name, h.Name)
		if err != nil {
			return AdminActionResult{}, err
		}
		out.State = "executed"
	}
	for key := range notes.keys {
		out.TouchedKeys = append(out.TouchedKeys, key)
	}
	return out, err
}

// NotifyAdminResult wakes readers only after the caller's transaction commits.
func (s *Service) NotifyAdminResult(out AdminActionResult) {
	for _, key := range out.TouchedKeys {
		s.notify.Changed(key)
	}
}
