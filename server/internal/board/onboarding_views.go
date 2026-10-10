package board

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/ids"
)

// OnboardingBoardDisplay is a visible board's current label, never authority.
type OnboardingBoardDisplay struct{ ID, Name, Title string }

// OnboardingDisplay contains current labels for already authorized resources.
type OnboardingDisplay struct {
	PersonHandle, AgentName, AgentHarness string
	Boards                                []OnboardingBoardDisplay
	RequestedOn                           *OnboardingBoardDisplay
}

// OnboardingLabels resolves only boards and agents currently visible to the caller.
func (s *Service) OnboardingLabels(ctx context.Context, p Principal, personID, agentID string, boardIDs []string) (OnboardingDisplay, error) {
	out := OnboardingDisplay{Boards: []OnboardingBoardDisplay{}}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if _, err := caller(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		if h, err := tx.HumanByID(personID); err == nil && h.RemovedAt == nil {
			out.PersonHandle = h.Name
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, id := range boardIDs {
			b, err := tx.BoardByID(id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, _, _, err := s.see(tx, p, b.Name); err != nil {
				continue
			}
			out.Boards = append(out.Boards, boardDisplay(b))
		}
		if agentID != "" {
			m, err := tx.MemberByID(agentID)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			b, err := tx.BoardByID(m.BoardID)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if _, _, _, err := s.see(tx, p, b.Name); err == nil {
				source := boardDisplay(b)
				out.RequestedOn = &source
				out.AgentName = m.Name
				if m.Harness != nil && (p.Human != nil || b.Policy.ShowHarness) {
					out.AgentHarness = *m.Harness
				}
			}
		}
		return nil
	})
	return out, err
}

func boardDisplay(b Board) OnboardingBoardDisplay {
	out := OnboardingBoardDisplay{ID: b.ID, Name: b.Name}
	if b.Title != nil {
		out.Title = *b.Title
	}
	return out
}

// InvitePreview is the nonsecret content offered to the invitation holder.
type InvitePreview struct {
	InviterHandle, Work, ExpiresAt, SuggestedHandle string
	Boards                                          []OnboardingBoardDisplay
}

// PreviewServerInvite reads a valid invitation without spending it or changing cursors.
func (s *Service) PreviewServerInvite(ctx context.Context, secret string) (InvitePreview, error) {
	out := InvitePreview{Boards: []OnboardingBoardDisplay{}}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		inv, issuer, err := s.validServerInvite(tx, secret)
		if err != nil {
			return err
		}
		out.InviterHandle, out.ExpiresAt, out.SuggestedHandle = issuer.Name, inv.ExpiresAt, inv.SuggestedHandle
		for _, id := range inv.Boards {
			b, err := tx.BoardByID(id)
			if err != nil {
				return err
			}
			out.Boards = append(out.Boards, boardDisplay(b))
		}
		if r, err := tx.PairingByInvite(inv.ID); err == nil {
			if r.State != "awaiting_account" || r.ExpiresAt <= stamp(s.clk.Now()) {
				return inviteInvalid()
			}
			if err := pairingMember(tx, r.BoardID, r.InviterID, r.InitiatingAgentID); err != nil {
				return inviteInvalid()
			}
			out.Work = r.Work
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	})
	return out, err
}

func (s *Service) validServerInvite(tx ReadTx, secret string) (ServerInvite, Human, error) {
	if !strings.HasPrefix(secret, invitePrefix) {
		return ServerInvite{}, Human{}, inviteInvalid()
	}
	inv, err := tx.ServerInviteByDigest(ids.Digest(s.key, secret))
	if errors.Is(err, ErrNotFound) {
		return inv, Human{}, inviteInvalid()
	}
	if err != nil {
		return inv, Human{}, err
	}
	now := stamp(s.clk.Now())
	if inv.UsedAt != nil || inv.RevokedAt != nil || inv.ExpiresAt <= now {
		return inv, Human{}, inviteInvalid()
	}
	issuer, err := tx.HumanByID(inv.CreatedBy)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return inv, issuer, err
	}
	if err != nil || issuer.Role != ServerAdmin || issuer.RemovedAt != nil {
		return inv, issuer, inviteInvalid()
	}
	if inv.IssuingAgentID != "" {
		if _, err := s.frozenAgent(tx, Approval{AgentID: inv.IssuingAgentID, ParentKeyID: inv.ParentKeyID, PersonID: inv.CreatedBy}); err != nil {
			return inv, issuer, inviteInvalid()
		}
	}
	if err := s.redeemedInviteBoards(tx, issuer, inv.Boards); err != nil {
		return inv, issuer, inviteInvalid()
	}
	return inv, issuer, nil
}

func approvalDeadline(a Approval) (time.Time, error) {
	if a.ExpiresAt != nil {
		return time.Parse(time.RFC3339Nano, *a.ExpiresAt)
	}
	created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
	return created.Add(24 * time.Hour), err
}

// ApprovalKeyName resolves a current key label only after rechecking the approval's visibility.
func (s *Service) ApprovalKeyName(ctx context.Context, p Principal, id string) (string, error) {
	var name string
	err := s.st.Read(ctx, func(tx ReadTx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		a, err := tx.Approval(id)
		if err != nil {
			return err
		}
		if a.PersonID != h.ID || (p.Agent != nil && p.Agent.ID != a.AgentID) || a.Action.Kind != "revoke_key" {
			return approvalMissing()
		}
		if err := s.approvalVisible(tx, Principal{Human: &h, KeyID: p.KeyID}, a); err != nil {
			return err
		}
		k, err := tx.AccessKeyByID(a.Action.KeyID)
		if err != nil {
			return err
		}
		name = k.Name
		return nil
	})
	return name, err
}
