package board

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func approvalMissing() error {
	return apierr.New(http.StatusNotFound, "approval_not_found", "No visible approval has that id.", "List your approvals again.")
}

func approvalClosed() error {
	return apierr.New(http.StatusConflict, "approval_closed", "This approval cannot be decided now.", "List your approvals again.")
}

func personAdminOnly(p Principal) error {
	return humanOnly(p, "decide administrative authority", "aboard approvals")
}

func categoriesValid(categories []string) ([]string, error) {
	out := slices.Clone(categories)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	for i, c := range out {
		if c != "invite-people" && c != "add-people" || i > 0 && out[i-1] == c {
			return nil, adminInvalid("Only distinct invite-people and add-people allowance categories are accepted.", "Choose an ordinary-member admission category.")
		}
	}
	return out, nil
}

func (s *Service) allowance(tx Tx, personID string) (Allowance, error) {
	a, err := tx.Allowance(personID)
	if !errors.Is(err, ErrNotFound) {
		return a, err
	}
	id, err := s.gen.ID("alw", s.clk.Now())
	if err != nil {
		return Allowance{}, err
	}
	a = Allowance{ID: id, PersonID: personID, Categories: []string{}}
	return a, tx.SaveAllowance(a)
}

// GetAllowance reads the current person allowance, creating its stable empty identity if absent.
func (s *Service) GetAllowance(ctx context.Context, p Principal) (Allowance, error) {
	if err := personAdminOnly(p); err != nil {
		return Allowance{}, err
	}
	var out Allowance
	err := s.writeAs(ctx, p, func(tx Tx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if h.Role == ServerGuest {
			return guestNotAllowed("set allowances")
		}
		out, err = s.allowance(tx, h.ID)
		return err
	})
	return out, err
}

// SetAllowance replaces only the ordinary-member admission categories in one transaction.
func (s *Service) SetAllowance(ctx context.Context, p Principal, categories []string) (Allowance, error) {
	if err := personAdminOnly(p); err != nil {
		return Allowance{}, err
	}
	categories, err := categoriesValid(categories)
	if err != nil {
		return Allowance{}, err
	}
	var out Allowance
	err = s.writeAs(ctx, p, func(tx Tx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if h.Role == ServerGuest {
			return guestNotAllowed("set allowances")
		}
		out, err = s.allowance(tx, h.ID)
		if err != nil {
			return err
		}
		if !slices.Equal(out.Categories, categories) {
			out.Revision++
			out.Categories = categories
			return tx.SaveAllowance(out)
		}
		return nil
	})
	return out, err
}

func (s *Service) adminOwner(tx ReadTx, p Principal) (Principal, Member, error) {
	if p.Agent == nil || p.KeyID == "" {
		return Principal{}, Member{}, apierr.New(http.StatusForbidden, "agent_token_required", "Use an owned agent seat to request an administrative action.", "Join an agent to a board first.")
	}
	h, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return Principal{}, Member{}, err
	}
	m, err := tx.MemberByID(p.Agent.ID)
	if err != nil {
		return Principal{}, Member{}, err
	}
	if m.Kind != "agent" || m.HumanID != h.ID || m.KeyID == nil || *m.KeyID != p.KeyID {
		return Principal{}, Member{}, apierr.Unauthorized()
	}
	if _, _, err := seatOf(tx, m); err != nil {
		return Principal{}, Member{}, err
	}
	if h.Role == ServerGuest {
		return Principal{}, Member{}, guestNotAllowed("request administrative actions")
	}
	return Principal{Human: &h, KeyID: p.KeyID}, m, nil
}

func (s *Service) frozenAgent(tx ReadTx, a Approval) (Principal, error) {
	m, err := tx.MemberByID(a.AgentID)
	if err != nil {
		return Principal{}, approvalMissing()
	}
	if m.HumanID != a.PersonID || m.KeyID == nil || *m.KeyID != a.ParentKeyID {
		return Principal{}, apierr.Unauthorized()
	}
	p := Principal{Agent: &m, KeyID: a.ParentKeyID}
	_, _, err = s.adminOwner(tx, p)
	return p, err
}

func (s *Service) adminBoard(tx ReadTx, p Principal, id string) (Board, Member, error) {
	b, err := tx.BoardByID(id)
	if err != nil {
		return Board{}, Member{}, notFoundBoard()
	}
	visible, me, on, err := s.see(tx, p, b.Name)
	if err != nil {
		return Board{}, Member{}, err
	}
	if !on {
		return Board{}, Member{}, notFoundBoard()
	}
	if err := requireActive(visible); err != nil {
		return Board{}, Member{}, err
	}
	return visible, me, nil
}

func (s *Service) validateAdmin(tx ReadTx, owner Principal, a AdminAction) error {
	now := stamp(s.clk.Now())
	switch a.Kind {
	case "invite_people":
		if a.Invite == nil || a.BoardID != "" || a.PersonID != "" || a.Role != "" || a.KeyID != "" || a.Policy != nil {
			return adminInvalid("An invite action accepts only invite options.", "Use invite_people with an invite payload.")
		}
		if a.Invite.SuggestedHandle != "" && !validName(a.Invite.SuggestedHandle) {
			return adminInvalid("The suggested handle is invalid.", "Use lowercase letters, digits and single dashes, at most 40 characters.")
		}
		if a.Invite.TTLSeconds != nil {
			if *a.Invite.TTLSeconds < 60 || *a.Invite.TTLSeconds > 2592000 {
				return adminInvalid("Invalid invite lifetime.", "Use 60 to 2592000 seconds.")
			}
		}
		if _, err := requireServerAdmin(tx, owner, now, "invite people"); err != nil {
			return err
		}
		if err := s.inviteBoards(tx, owner, a.Invite.Boards); err != nil {
			return err
		}
		return s.validateInvitePairing(tx, owner, *a.Invite)
	case "add_people", "set_board_role", "set_board_policy":
		b, me, err := s.adminBoard(tx, owner, a.BoardID)
		if err != nil {
			return err
		}
		if a.Kind == "set_board_policy" {
			if a.Policy == nil {
				return adminInvalid("A policy action needs a policy.", "Supply the exact policy change.")
			}
			if err := requireAdmin(tx, b, me, "change its policy"); err != nil {
				return err
			}
			_, err := b.Policy.Apply(*a.Policy)
			if err != nil {
				return adminInvalid(err.Error(), "Use a supported board policy.")
			}
			return nil
		}
		h, err := tx.HumanByID(a.PersonID)
		if err != nil || h.RemovedAt != nil {
			return noSuchPerson(a.PersonID)
		}
		if h.Role == ServerGuest {
			return personIsGuest(h.Name, "Use a member account.")
		}

		if a.Kind == "add_people" {
			_, _, on, err := s.addPersonAuthority(tx, owner, b.Name, h.Name)
			if err != nil {
				return err
			}
			if !on && h.ID != owner.personID() {
				return notOnBoard(b.Name, owner.Human.Name)
			}
		}
		if a.Kind == "set_board_role" {
			if a.Role != "owner" && a.Role != "member" {
				return adminInvalid("Invalid board role.", "Use owner or member.")
			}
			if err := requireOwner(tx, b, me, "change board roles"); err != nil {
				return err
			}
			target, err := tx.HumanMember(b.ID, h.ID)
			if err != nil || target.Status != StatusActive {
				return personNotOnBoard(h.Name, b.Name)
			}
			if a.Role == "member" && target.Access == rules.AccessAdmin {
				people, err := peopleOn(tx, b)
				if err != nil {
					return err
				}
				if len(ownersOf(people)) == 1 {
					return apierr.New(http.StatusConflict, "last_owner", "The board must keep an owner.", "Make another person an owner first.")
				}
			}
		}
		return nil
	case "set_server_role", "remove_person":
		if a.Kind == "remove_person" && a.BoardID != "" {
			b, me, err := s.adminBoard(tx, owner, a.BoardID)
			if err != nil {
				return err
			}
			target, err := tx.HumanByID(a.PersonID)
			if err != nil || target.RemovedAt != nil {
				return noSuchPerson(a.PersonID)
			}
			member, err := tx.HumanMember(b.ID, target.ID)
			if err != nil || member.Status != StatusActive {
				return personNotOnBoard(target.Name, b.Name)
			}
			if member.ID == me.ID {
				if member.Access == rules.AccessAdmin {
					people, err := peopleOn(tx, b)
					if err != nil {
						return err
					}
					if len(ownersOf(people)) == 1 {
						return apierr.New(http.StatusConflict, "last_owner", "The board must keep an owner.", "Make another person an owner first.")
					}
				}
				return nil
			}
			return requireOwner(tx, b, me, "remove people")
		}
		if _, err := requireServerAdmin(tx, owner, now, "manage server people"); err != nil {
			return err
		}
		h, err := tx.HumanByID(a.PersonID)
		if err != nil || h.RemovedAt != nil {
			return noSuchPerson(a.PersonID)
		}
		if a.Kind == "set_server_role" {
			if a.Role != ServerAdmin && a.Role != ServerMember {
				return adminInvalid("Invalid server role.", "Use admin or member.")
			}
			if h.Role == ServerGuest {
				return personIsGuest(h.Name, "Invite a new member identity instead.")
			}
			if h.Role == a.Role {
				return nil
			}
		}
		if h.Role == ServerAdmin {
			n, err := tx.AdminCount()
			if err != nil {
				return err
			}
			if n <= 1 {
				return lastAdmin("removed or demoted")
			}
		}
		return nil
	case "revoke_key":
		k, err := tx.AccessKeyByID(a.KeyID)
		if err != nil {
			return keyNotFound()
		}
		h, err := caller(tx, owner, now)
		if err != nil {
			return err
		}
		if k.HumanID != h.ID && h.Role != ServerAdmin {
			return keyNotFound()
		}
		return nil
	default:
		return adminInvalid("Unknown administrative action.", "Use a supported exact administrative action.")
	}
}

func actionCategory(a AdminAction) string {
	if a.Kind == "invite_people" {
		return "invite-people"
	}
	if a.Kind == "add_people" {
		return "add-people"
	}
	return ""
}

// RequestAdminAction freezes an owned-agent action and holds or executes it under current authority.
func (s *Service) RequestAdminAction(ctx context.Context, p Principal, action AdminAction, key string) (AdminActionResult, error) {
	var out AdminActionResult
	notes := newAdminNotes(s.notify)
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var err error
		out, err = s.requestAdminTx(ctx, tx, p, action, key, notes)
		return err
	})
	if err == nil {
		notes.flush()
	}
	return out, err
}

func (s *Service) requestAdminTx(ctx context.Context, tx Tx, p Principal, action AdminAction, key string, notes *adminNotes) (AdminActionResult, error) {
	owner, m, err := s.adminOwner(tx, p)
	if err != nil {
		return AdminActionResult{}, err
	}
	if err := adminActionShape(action); err != nil {
		return AdminActionResult{}, err
	}
	// Freeze all caller-owned slices and pointers before they can enter the record.
	raw, err := events.Canonical(AdminActionJSON(action))
	if err != nil {
		return AdminActionResult{}, err
	}
	action, err = AdminActionFromJSON(raw)
	if err != nil {
		return AdminActionResult{}, err
	}
	if action.Invite != nil {
		if err := s.validateInvitePairing(tx, p, *action.Invite); err != nil {
			return AdminActionResult{}, err
		}
	}
	hash, err := actionHash(action)
	if err != nil {
		return AdminActionResult{}, err
	}
	allowance, err := s.allowance(tx, owner.Human.ID)
	if err != nil {
		return AdminActionResult{}, err
	}
	if key != "" {
		if err := tx.ExpireAdminRequestKeys(stamp(s.clk.Now().Add(-24 * time.Hour))); err != nil {
			return AdminActionResult{}, err
		}
		existing, err := tx.ApprovalByRequest(m.ID, key)
		if err == nil {
			if existing.PayloadHash != hash {
				return AdminActionResult{}, apierr.New(http.StatusConflict, "idempotency_conflict", "This request key belongs to another exact action.", "Use a new key for a different action.")
			}
			if existing.State == "pending" {
				deadline, err := approvalDeadline(existing)
				if err != nil {
					return AdminActionResult{}, err
				}
				if !s.clk.Now().Before(deadline) {
					return AdminActionResult{}, approvalClosed()
				}
			}
			if existing.State != "pending" && existing.State != "executed" {
				return AdminActionResult{}, approvalClosed()
			}
			if existing.State == "executed" {
				err = s.adminReplayAuthority(tx, owner, action)
			} else {
				err = s.validateAdmin(tx, owner, action)
			}
			if err != nil {
				return AdminActionResult{}, err
			}
			if _, err = s.frozenAgent(tx, existing); err != nil {
				return AdminActionResult{}, err
			}
			if existing.Execution != nil && existing.Execution.Authorization.Via == "allowance" && !slices.Contains(allowance.Categories, actionCategory(action)) {
				return AdminActionResult{}, apierr.New(http.StatusForbidden, "forbidden", "The allowance no longer permits this action.", "Ask the person to review their allowance.")
			}
			return AdminActionResult{State: existing.State, Approval: existing}, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return AdminActionResult{}, err
		}
	}
	if err := s.validateAdmin(tx, owner, action); err != nil {
		return AdminActionResult{}, err
	}
	now := s.clk.Now()
	id, err := s.gen.ID("apr", now)
	if err != nil {
		return AdminActionResult{}, err
	}
	approval := Approval{ID: id, PersonID: owner.Human.ID, AgentID: m.ID, ParentKeyID: p.KeyID, Action: action, PayloadHash: hash, State: "pending", CreatedAt: stamp(now), ExpiresAt: ptr(stamp(now.Add(24 * time.Hour))), RequestKey: key}
	out := AdminActionResult{State: "pending", Approval: approval}
	if s.adminAllowanceCovers(tx, p, action, allowance) {
		auth := AdminAuthorization{Kind: authorizationKind(action), PersonID: approval.PersonID, AgentID: m.ID, ParentKeyID: p.KeyID, Via: "allowance", AllowanceID: allowance.ID, AllowanceRevision: allowance.Revision, PayloadHash: hash}
		invite, err := s.executeAdminTx(ctx, tx, owner, m, action, auth, notes)
		if err != nil {
			return AdminActionResult{}, err
		}
		out.State = "executed"
		out.Approval.State = "executed"
		out.Invite = invite
		out.Approval.DecidedAt = ptr(stamp(now))
		out.Approval.Execution = &AdminExecution{At: stamp(now), Authorization: auth}
		if invite != nil {
			out.Approval.Execution.InviteID = invite.Invite.ID
		}
	}
	return out, tx.SaveApproval(out.Approval)
}

// ListApprovals returns only the caller's requests whose payloads remain visible.
func (s *Service) ListApprovals(ctx context.Context, p Principal, states ...string) ([]Approval, error) {
	state := "all"
	if len(states) > 0 {
		state = states[0]
	}
	out := []Approval{}
	err := s.writeBookkeepingAs(ctx, p, func(tx Tx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if h.Role == ServerGuest {
			return guestNotAllowed("list approvals")
		}
		all, err := tx.Approvals(h.ID)
		if err != nil {
			return err
		}
		owner := Principal{Human: &h, KeyID: p.KeyID}
		for _, a := range all {
			if p.Agent != nil && p.Agent.ID != a.AgentID {
				continue
			}
			if err := s.approvalVisible(tx, owner, a); err != nil {
				continue
			}
			deadline, err := approvalDeadline(a)
			if err != nil {
				return err
			}
			if a.ExpiresAt == nil {
				a.ExpiresAt = ptr(stamp(deadline))
			}
			if a.State == "pending" && !s.clk.Now().Before(deadline) {
				a.State = "expired"
				a.DecidedAt = ptr(stamp(deadline))
				if err := tx.SaveApproval(a); err != nil {
					return err
				}
			}
			if a.State == "pending" {
				if state == "decided" {
					continue
				}
			} else {
				if state == "pending" {
					continue
				}
				decided := a.CreatedAt
				if a.DecidedAt != nil {
					decided = *a.DecidedAt
				}
				at, err := time.Parse(time.RFC3339Nano, decided)
				if err != nil {
					return err
				}
				if !s.clk.Now().Before(at.Add(7 * 24 * time.Hour)) {
					continue
				}
			}
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// approvalVisible checks current payload access without requiring the frozen
// operation to remain executable (a terminal action may have removed its target).
func (s *Service) approvalVisible(tx ReadTx, p Principal, a Approval) error {
	if a.Action.BoardID != "" {
		b, err := tx.BoardByID(a.Action.BoardID)
		if err != nil {
			return approvalMissing()
		}
		if _, _, _, err := s.see(tx, p, b.Name); err != nil {
			return approvalMissing()
		}
	}
	if a.Action.Invite != nil {
		for _, id := range a.Action.Invite.Boards {
			b, err := tx.BoardByID(id)
			if err != nil {
				return approvalMissing()
			}
			if _, _, _, err := s.see(tx, p, b.Name); err != nil {
				return approvalMissing()
			}
		}
	}
	if a.Action.Kind == "revoke_key" {
		k, err := tx.AccessKeyByID(a.Action.KeyID)
		if err != nil || (k.HumanID != a.PersonID && p.Human.Role != ServerAdmin) {
			return approvalMissing()
		}
	}
	_, err := s.frozenAgent(tx, a)
	return err
}

// AllowApproval executes the immutable action once and optionally enables its admission category.
func (s *Service) AllowApproval(ctx context.Context, p Principal, id string, always bool) (AdminActionResult, error) {
	if err := personAdminOnly(p); err != nil {
		return AdminActionResult{}, err
	}
	var out AdminActionResult
	notes := newAdminNotes(s.notify)
	err := s.writeAs(ctx, p, func(tx Tx) error {
		a, err := tx.Approval(id)
		if err != nil || a.PersonID != p.personID() {
			return approvalMissing()
		}
		if always && actionCategory(a.Action) == "" {
			return adminInvalid("This action can never be allowed automatically.", "Allow this exact request once without --always.")
		}
		agent, err := s.frozenAgent(tx, a)
		if err != nil {
			return err
		}
		owner, m, err := s.adminOwner(tx, agent)
		if err != nil {
			return err
		}
		if a.State == "executed" {
			err = s.adminReplayAuthority(tx, owner, a.Action)
		} else {
			err = s.validateAdmin(tx, owner, a.Action)
		}
		if err != nil {
			return err
		}
		hash, err := actionHash(a.Action)
		if err != nil {
			return err
		}
		if hash != a.PayloadHash {
			return approvalClosed()
		}
		deadline, err := approvalDeadline(a)
		if err != nil {
			return err
		}
		if a.State == "pending" && !s.clk.Now().Before(deadline) {
			return approvalClosed()
		}
		if a.State == "executed" {
			out = AdminActionResult{State: a.State, Approval: a}
			return nil
		}
		if a.State != "pending" {
			return approvalClosed()
		}
		if always {
			allowance, err := s.allowance(tx, a.PersonID)
			if err != nil {
				return err
			}
			cat := actionCategory(a.Action)
			if !slices.Contains(allowance.Categories, cat) {
				allowance.Categories = append(allowance.Categories, cat)
				slices.Sort(allowance.Categories)
				allowance.Revision++
				if err := tx.SaveAllowance(allowance); err != nil {
					return err
				}
			}
		}
		auth := AdminAuthorization{Kind: authorizationKind(a.Action), PersonID: a.PersonID, AgentID: a.AgentID, ParentKeyID: a.ParentKeyID, Via: "approval", ApprovalID: a.ID, PayloadHash: a.PayloadHash}
		invite, err := s.executeAdminTx(ctx, tx, owner, m, a.Action, auth, notes)
		if err != nil {
			return err
		}
		decision := "once"
		if always {
			decision = "always"
		}
		a.State = "executed"
		a.DecidedAt = ptr(stamp(s.clk.Now()))
		a.Execution = &AdminExecution{At: *a.DecidedAt, Decision: decision, Authorization: auth}
		if invite != nil {
			a.Execution.InviteID = invite.Invite.ID
		}
		if err := tx.SaveApproval(a); err != nil {
			return err
		}
		out = AdminActionResult{State: a.State, Approval: a, Invite: invite}
		return nil
	})
	if err == nil {
		notes.flush()
	}
	return out, err
}

// DeclineApproval closes a pending request without executing it.
func (s *Service) DeclineApproval(ctx context.Context, p Principal, id string) (Approval, error) {
	if err := personAdminOnly(p); err != nil {
		return Approval{}, err
	}
	var out Approval
	err := s.writeAs(ctx, p, func(tx Tx) error {
		a, err := tx.Approval(id)
		if err != nil || a.PersonID != p.personID() {
			return approvalMissing()
		}
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if err := s.approvalVisible(tx, Principal{Human: &h, KeyID: p.KeyID}, a); err != nil {
			return err
		}
		deadline, err := approvalDeadline(a)
		if err != nil {
			return err
		}
		if a.State == "pending" && !s.clk.Now().Before(deadline) {
			return approvalClosed()
		}
		if a.State == "declined" {
			out = a
			return nil
		}
		if a.State != "pending" {
			return approvalClosed()
		}
		a.State = "declined"
		a.DecidedAt = ptr(stamp(s.clk.Now()))
		out = a
		return tx.SaveApproval(a)
	})
	return out, err
}

func notFoundBoard() error {
	return apierr.New(http.StatusNotFound, "board_not_found", "No visible board has that id.", "List your boards again.")
}

func adminActionShape(a AdminAction) error {
	valid := false
	switch a.Kind {
	case "invite_people":
		valid = a.Invite != nil && a.BoardID == "" && a.PersonID == "" && a.Role == "" && a.KeyID == "" && a.Policy == nil
	case "add_people":
		valid = a.Invite == nil && a.BoardID != "" && a.PersonID != "" && a.Role == "" && a.KeyID == "" && a.Policy == nil
	case "set_server_role":
		valid = a.Invite == nil && a.BoardID == "" && a.PersonID != "" && a.Role != "" && a.KeyID == "" && a.Policy == nil
	case "set_board_role":
		valid = a.Invite == nil && a.BoardID != "" && a.PersonID != "" && a.Role != "" && a.KeyID == "" && a.Policy == nil
	case "remove_person":
		valid = a.Invite == nil && a.PersonID != "" && a.Role == "" && a.KeyID == "" && a.Policy == nil
	case "revoke_key":
		valid = a.Invite == nil && a.BoardID == "" && a.PersonID == "" && a.Role == "" && a.KeyID != "" && a.Policy == nil
	case "set_board_policy":
		valid = a.Invite == nil && a.BoardID != "" && a.PersonID == "" && a.Role == "" && a.KeyID == "" && a.Policy != nil && *a.Policy != (rules.PolicyChange{})
	}
	if !valid {
		return adminInvalid("The action contains missing or incompatible fields.", "Submit one exact supported action.")
	}
	return nil
}

// Replay checks current authority without requiring the completed side effect to be
// possible again (for example, a removed person no longer has a live membership).
func (s *Service) adminReplayAuthority(tx ReadTx, owner Principal, a AdminAction) error {
	if a.Kind == "invite_people" {
		if _, err := requireServerAdmin(tx, owner, stamp(s.clk.Now()), "invite people"); err != nil {
			return err
		}
		return s.inviteBoards(tx, owner, a.Invite.Boards)
	}

	if a.Kind == "invite_people" || a.Kind == "set_server_role" || a.Kind == "remove_person" && a.BoardID == "" {
		_, err := requireServerAdmin(tx, owner, stamp(s.clk.Now()), "perform this administrative action")
		return err
	}
	if a.Kind == "revoke_key" {
		return s.validateAdmin(tx, owner, a)
	}
	b, me, err := s.adminBoard(tx, owner, a.BoardID)
	if err != nil {
		return err
	}
	if a.Kind == "add_people" {
		_, _, _, err := s.addPersonAuthority(tx, owner, b.Name, a.PersonID)
		return err
	}
	if a.Kind == "set_board_policy" {
		return requireAdmin(tx, b, me, "change its policy")
	}
	return requireOwner(tx, b, me, "perform this administrative action")
}

func adminInvalid(message, hint string) error {
	return apierr.New(http.StatusBadRequest, "invalid_request", message, hint)
}
