package board

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// OnboardingReceipt is the nonsecret original outcome of client-key redemption.
type OnboardingReceipt struct {
	ServerID, PersonID, KeyID, InviteID, Handle string
	Boards                                      []string
	PairingRequestID                            string
}

func canonicalClientToken(token string) bool {
	if len(token) != 47 || token[:4] != "abh_" {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token[4:])
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == token[4:]
}

func (s *Service) inviteBoards(tx ReadTx, p Principal, boards []string) error {
	seen := map[string]bool{}
	for _, id := range boards {
		if seen[id] {
			return adminInvalid("An invite cannot name a board twice.", "Use each immutable board id once.")
		}
		seen[id] = true
		b, _, err := s.adminBoard(tx, p, id)
		if err != nil {
			return err
		}
		if _, _, _, err = s.addPersonAuthority(tx, p, b.Name, ""); err != nil {
			return err
		}
	}
	return nil
}

// GetOnboardingReceipt returns this exact working person key's original outcome,
// filtering resources the person can no longer see without restoring membership.
func (s *Service) GetOnboardingReceipt(ctx context.Context, p Principal) (OnboardingReceipt, error) {
	if err := requireHuman(p); err != nil {
		return OnboardingReceipt{}, err
	}
	if p.Browser {
		return OnboardingReceipt{}, apierr.New(http.StatusForbidden, "human_token_required", "Use the original person access key.", "Resume setup on the machine that redeemed the invite.")
	}
	var out OnboardingReceipt
	err := s.st.Read(ctx, func(tx ReadTx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		out, err = tx.OnboardingReceipt(p.KeyID)
		if errors.Is(err, ErrNotFound) || (err == nil && out.PersonID != h.ID) {
			return apierr.New(http.StatusNotFound, "not_found", "This key has no onboarding receipt.", "Resume setup with its original saved key.")
		}
		if err != nil {
			return err
		}
		boards := make([]string, 0, len(out.Boards))
		for _, id := range out.Boards {
			b, err := tx.BoardByID(id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if _, _, _, err = s.see(tx, Principal{Human: &h, KeyID: p.KeyID}, b.Name); err == nil {
				boards = append(boards, id)
			}
		}
		if out.PairingRequestID != "" {
			if _, err := s.pairingView(tx, p, out.PairingRequestID, false); err != nil {
				out.PairingRequestID = ""
			}
		}

		out.Boards = boards
		return nil
	})
	return out, err
}

func copyInviteInput(in InvitePeopleInput) InvitePeopleInput {
	in.Boards = slices.Clone(in.Boards)
	if in.TTLSeconds != nil {
		ttl := *in.TTLSeconds
		in.TTLSeconds = &ttl
	}
	if in.Pairing != nil {
		pairing := *in.Pairing
		in.Pairing = &pairing
	}
	return in
}

// redeemedInviteBoards checks the issuing person's current membership authority.
// Redemption uses the invite capability, rather than an issuing machine's key.
func (s *Service) redeemedInviteBoards(tx ReadTx, person Human, boards []string) error {
	for _, id := range boards {
		b, err := tx.BoardByID(id)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		m, err := tx.HumanMember(id, person.ID)
		if err != nil {
			return err
		}
		if m.Status != StatusActive {
			return notFoundBoard()
		}
	}
	return nil
}

func (s *Service) admitInvitedPerson(tx Tx, b *Board, issuer, target Human) error {
	me, err := tx.HumanMember(b.ID, issuer.ID)
	if err != nil {
		return err
	}
	_, err = s.admitNewPerson(tx, b, me, target, true, map[string]any{"action_kind": "invited"})
	return err
}

func (s *Service) adminAllowanceCovers(tx ReadTx, p Principal, a AdminAction, allowance Allowance) bool {
	cat := actionCategory(a)
	if cat == "" || !slices.Contains(allowance.Categories, cat) {
		return false
	}
	if a.Kind != "invite_people" || a.Invite == nil || len(a.Invite.Boards) == 0 {
		return true
	}
	if slices.Contains(allowance.Categories, "add-people") {
		return true
	}
	for _, id := range a.Invite.Boards {
		b, err := tx.BoardByID(id)
		if err != nil {
			return false
		}
		if _, _, _, err := s.addPersonAuthority(tx, p, b.Name, ""); err != nil {
			return false
		}
	}
	return true
}

// ServerInviteView contains only currently visible nonsecret invitation metadata.
type ServerInviteView struct {
	Invite ServerInvite
	State  string
}

func inviteManagementPerson(p Principal) error {
	if err := humanOnly(p, "manage your invitations", "aboard invites"); err != nil {
		return err
	}
	if p.Browser {
		return apierr.New(http.StatusForbidden, "human_token_required", "Manage invitations with your own person key.", "Run aboard invites in your terminal.")
	}
	return nil
}

// ListServerInvites lists only the current person's issued invites, even after demotion.
func (s *Service) ListServerInvites(ctx context.Context, p Principal) ([]ServerInviteView, error) {
	if p.Agent == nil {
		if err := inviteManagementPerson(p); err != nil {
			return nil, err
		}
	}
	return s.listServerInvites(ctx, p)
}

// ListInviteNotices lets a person read their agents' issued invites without management authority.
func (s *Service) ListInviteNotices(ctx context.Context, p Principal) ([]ServerInviteView, error) {
	if err := humanOnly(p, "read your invitation notices", "aboard invite list"); err != nil {
		return nil, err
	}
	list, err := s.listServerInvites(ctx, p)
	if err != nil {
		return nil, err
	}
	out := []ServerInviteView{}
	for _, v := range list {
		if v.Invite.IssuingAgentID != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *Service) listServerInvites(ctx context.Context, p Principal) ([]ServerInviteView, error) {
	out := []ServerInviteView{}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if p.Agent != nil && h.Role == ServerGuest {
			return guestNotAllowed("list invitations")
		}
		if p.Agent != nil {
			if _, _, err := seatOf(tx, *p.Agent); err != nil {
				return err
			}
		}
		invites, err := tx.ServerInvites(h.ID)
		if err != nil {
			return err
		}
		for _, i := range invites {
			boards := []string{}
			for _, id := range i.Boards {
				b, err := tx.BoardByID(id)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if _, _, _, err := s.see(tx, p, b.Name); err == nil {
					boards = append(boards, id)
				}
			}
			pairing, err := tx.PairingByInvite(i.ID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if err == nil {
				if _, err := s.pairingView(tx, p, pairing.ID, false); err == nil {
					i.PairingRequestID = pairing.ID
				}
			}

			i.Boards = boards
			i.Digest = ""
			i.Authorization = nil
			state := "active"
			switch {
			case i.UsedAt != nil:
				state = "redeemed"
			case i.RevokedAt != nil:
				state = "revoked"
			case i.ExpiresAt <= stamp(s.clk.Now()):
				state = "expired"
			}
			out = append(out, ServerInviteView{Invite: i, State: state})
		}
		return nil
	})
	return out, err
}

// RevokeServerInvite reduces only the current person's own issued capability.
func (s *Service) RevokeServerInvite(ctx context.Context, p Principal, id string) (bool, error) {
	if err := humanOnly(p, "revoke your invitation", "aboard invites"); err != nil {
		return false, err
	}
	var changed bool
	err := s.writeAs(ctx, p, func(tx Tx) error {
		h, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		i, err := tx.ServerInviteByID(id)
		if errors.Is(err, ErrNotFound) || (err == nil && i.CreatedBy != h.ID) {
			return apierr.New(http.StatusNotFound, "invite_not_found", "No own invitation has that id.", "Run aboard invites to list your invitations.")
		}
		if err != nil {
			return err
		}
		changed, err = tx.RevokeServerInvite(id, stamp(s.clk.Now()))
		return err
	})
	return changed, err
}

func (s *Service) validateInvitePairing(tx ReadTx, p Principal, in InvitePeopleInput) error {
	if in.Pairing == nil {
		return nil
	}
	if len(in.Boards) != 1 {
		return adminInvalid("Pairing needs exactly one bundled board.", "Name one immutable board id.")
	}
	if in.Pairing.Work == "" || !utf8.ValidString(in.Pairing.Work) || utf8.RuneCountInString(in.Pairing.Work) > 4000 {
		return adminInvalid("Invalid pairing work.", "Supply 1 to 4000 characters of proposed work.")
	}
	if p.Agent != nil && p.Agent.ID != in.Pairing.InitiatingAgentID {
		return apierr.New(http.StatusForbidden, "forbidden", "An agent must propose pairing from its own seat.", "Use the acting agent's immutable id.")
	}
	return pairingMember(tx, in.Boards[0], p.personID(), in.Pairing.InitiatingAgentID)
}
