package board

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// ApprovalOutcomeRecord retains only the derivation identity and one-time receipt.
type ApprovalOutcomeRecord struct {
	ApprovalID, InviteID string
	Version              int
	ConsumedAt           *string
}

type ApprovalOutcome struct {
	Approval         Approval
	Collected        bool
	Invite           *NewServerInvite
	PairingRequestID string
}

// approvedInviteSecret separates recoverable approval invites from token digests.
func (s *Service) approvedInviteSecret(approvalID, inviteID string) string {
	mac := hmac.New(sha256.New, s.key)
	raw, _ := json.Marshal([]string{"aboard-approved-invite-v1", s.cfg.ServerID, approvalID, inviteID})
	_, _ = mac.Write(raw)
	return invitePrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) ownApproval(tx ReadTx, p Principal, id string, collect bool) (Approval, error) {
	if collect && (p.Agent == nil || p.Delegation != nil || p.Browser) {
		return Approval{}, apierr.New(http.StatusForbidden, "agent_token_required", "Only the requesting agent can collect this outcome.", "Run aboard approvals show "+id+" in the requesting session.")
	}
	if p.Delegation != nil {
		return Approval{}, approvalMissing()
	}
	h, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return Approval{}, err
	}
	a, err := tx.Approval(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Approval{}, approvalMissing()
		}
		return Approval{}, err
	}
	if a.PersonID != h.ID || (p.Agent != nil && (p.Agent.ID != a.AgentID || p.KeyID != a.ParentKeyID)) {
		return Approval{}, approvalMissing()
	}
	if err := s.approvalVisible(tx, Principal{Human: &h, KeyID: p.KeyID}, a); err != nil {
		return Approval{}, approvalMissing()
	}
	hash, err := actionHash(a.Action)
	if err != nil {
		return Approval{}, err
	}
	if hash != a.PayloadHash {
		return Approval{}, approvalMissing()
	}
	if a.State == "pending" {
		deadline, err := approvalDeadline(a)
		if err != nil {
			return Approval{}, err
		}
		if !s.clk.Now().Before(deadline) {
			a.State = "expired"
		}
	}
	return a, nil
}

// GetApproval reads current own metadata without consuming or revealing an outcome.
func (s *Service) GetApproval(ctx context.Context, p Principal, id string) (ApprovalOutcome, error) {
	var out ApprovalOutcome
	err := s.st.Read(ctx, func(tx ReadTx) error {
		var err error
		out.Approval, err = s.ownApproval(tx, p, id, false)
		if err == nil {
			s.outcomePairing(tx, p, &out)
		}
		return err
	})
	return out, err
}

// CollectApproval consumes a supported invite in the same transaction as all checks.
func (s *Service) CollectApproval(ctx context.Context, p Principal, id string) (ApprovalOutcome, error) {
	var out ApprovalOutcome
	err := s.writeAs(ctx, p, func(tx Tx) error {
		a, err := s.ownApproval(tx, p, id, true)
		if err != nil {
			return err
		}
		out.Approval = a
		s.outcomePairing(tx, p, &out)
		if a.State != "executed" || a.Execution == nil || a.Execution.Authorization.Via != "approval" || a.Execution.InviteID == "" {
			return nil
		}
		receipt, err := tx.ApprovalOutcome(id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if receipt.ConsumedAt != nil || receipt.Version != 1 || receipt.InviteID != a.Execution.InviteID {
			return nil
		}
		secret := s.approvedInviteSecret(id, receipt.InviteID)
		inv, err := tx.ServerInviteByID(receipt.InviteID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !hmac.Equal([]byte(inv.Digest), []byte(ids.Digest(s.key, secret))) {
			return nil
		}
		valid, _, err := s.validServerInvite(tx, secret)
		if err != nil {
			if e, ok := apierr.As(err); ok && e.Code == "invite_invalid" {
				return nil
			}
			return err
		}
		if valid.ID != inv.ID || inv.CreatedBy != a.PersonID || inv.IssuingAgentID != a.AgentID || inv.ParentKeyID != a.ParentKeyID || inv.Authorization == nil || inv.Authorization.ApprovalID != a.ID || inv.Authorization.PayloadHash != a.PayloadHash {
			return nil
		}
		consumed, err := tx.ConsumeApprovalOutcome(id, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if consumed {
			out.Collected = true
			inv.PairingRequestID = out.PairingRequestID
			out.Invite = &NewServerInvite{Invite: inv, Secret: secret}
		}
		return nil
	})
	return out, err
}

func (s *Service) outcomePairing(tx ReadTx, p Principal, out *ApprovalOutcome) {
	a := out.Approval
	if a.State != "executed" || a.Execution == nil || a.Execution.InviteID == "" {
		return
	}
	r, err := tx.PairingByInvite(a.Execution.InviteID)
	if err != nil || r.InviteID != a.Execution.InviteID || r.InviterID != a.PersonID || r.InitiatingAgentID != a.AgentID {
		return
	}
	if _, err = s.pairingView(tx, p, r.ID, true); err == nil {
		out.PairingRequestID = r.ID
	}
}
