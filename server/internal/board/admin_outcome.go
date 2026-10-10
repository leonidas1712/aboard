package board

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// ApprovalOutcomeRecord retains only the derivation identity and one-time receipt.
type ApprovalOutcomeRecord struct {
	ApprovalID, InviteID string
	Version              int
	Capsule              []byte
	ExpiresAt            string
	WinningKeyHash       string
	ConsumedAt           *string
}

// ApprovalOutcome separates readable decision metadata from a one-time invite reveal.
type ApprovalOutcome struct {
	Approval         Approval
	Collected        bool
	Invite           *NewServerInvite
	PairingRequestID string
}

// approvalCapsule keeps a random invitation encrypted separately from the record.
// A purpose-derived key and issuer/approval/invite AAD prevent tokens or ciphertext
// from being substituted between outcomes; only the digest is kept on the invite.
func (s *Service) approvalCapsule(approvalID, inviteID string) (cipher.AEAD, []byte, error) {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("aboard-approval-outcome-encryption-v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	aad, _ := json.Marshal([]string{"aboard-approval-outcome-v1", s.cfg.ServerID, approvalID, inviteID})
	return aead, aad, nil
}

func (s *Service) sealApprovalInvite(approvalID, inviteID, secret string) ([]byte, error) {
	aead, aad, err := s.approvalCapsule(approvalID, inviteID)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(secret), aad), nil
}

func (s *Service) openApprovalInvite(rec ApprovalOutcomeRecord) (string, error) {
	aead, aad, err := s.approvalCapsule(rec.ApprovalID, rec.InviteID)
	if err != nil {
		return "", err
	}
	if len(rec.Capsule) < aead.NonceSize() {
		return "", errors.New("invalid approval capsule")
	}
	plain, err := aead.Open(nil, rec.Capsule[:aead.NonceSize()], rec.Capsule[aead.NonceSize():], aad)
	return string(plain), err
}

func (s *Service) collectionKeyHash(id, key string) string {
	if key == "" {
		return ""
	}
	raw, _ := json.Marshal([]string{"aboard-approval-collection-key-v1", s.cfg.ServerID, id, key})
	return ids.Digest(s.key, string(raw))
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
func (s *Service) CollectApproval(ctx context.Context, p Principal, id string, keys ...string) (ApprovalOutcome, error) {
	var out ApprovalOutcome
	var refusal error
	key := ""
	if len(keys) > 0 {
		key = keys[0]
	}
	err := s.st.Write(ctx, func(tx Tx) error {
		a, err := s.ownApproval(tx, p, id, true)
		if err != nil {
			// Authority loss also invalidates the held capability. Commit the clearing
			// independently of the refusal, which must not roll it back.
			if previous, e := tx.Approval(id); e == nil {
				requester, e := s.frozenAgent(tx, previous)
				if e == nil {
					owner, _, checkErr := s.adminOwner(tx, requester)
					e = checkErr
					if e == nil {
						e = s.validateAdmin(tx, owner, previous.Action)
					}
				}
				if e != nil {
					if e = tx.ClearApprovalOutcome(id); e != nil {
						return e
					}
				}
			}
			refusal = err
			return nil
		}
		out.Approval = a
		s.outcomePairing(tx, p, &out)
		if a.State != "executed" || a.Execution == nil || a.Execution.Authorization.Via != "approval" || a.Execution.InviteID == "" {
			return nil
		}
		rec, err := tx.ApprovalOutcome(id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := s.clk.Now()
		if rec.Version != 1 || rec.InviteID != a.Execution.InviteID || rec.ExpiresAt <= stamp(now) {
			return tx.ClearApprovalOutcome(id)
		}
		if len(rec.Capsule) == 0 {
			return nil
		}
		secret, err := s.openApprovalInvite(rec)
		if err != nil {
			return tx.ClearApprovalOutcome(id)
		}
		inv, err := tx.ServerInviteByID(rec.InviteID)
		if errors.Is(err, ErrNotFound) {
			return tx.ClearApprovalOutcome(id)
		}
		if err != nil {
			return err
		}
		if !hmac.Equal([]byte(inv.Digest), []byte(ids.Digest(s.key, secret))) {
			return tx.ClearApprovalOutcome(id)
		}
		valid, _, err := s.validServerInvite(tx, secret)
		if err != nil {
			if e, ok := apierr.As(err); ok && e.Code == "invite_invalid" {
				return tx.ClearApprovalOutcome(id)
			}
			return err
		}
		if valid.ID != inv.ID || inv.CreatedBy != a.PersonID || inv.IssuingAgentID != a.AgentID || inv.ParentKeyID != a.ParentKeyID || inv.Authorization == nil || inv.Authorization.ApprovalID != a.ID || inv.Authorization.PayloadHash != a.PayloadHash {
			return tx.ClearApprovalOutcome(id)
		}
		keyHash := s.collectionKeyHash(id, key)
		if rec.ConsumedAt != nil {
			if keyHash == "" || rec.WinningKeyHash == "" || !hmac.Equal([]byte(keyHash), []byte(rec.WinningKeyHash)) {
				return nil
			}
		} else {
			until := stamp(now.Add(10 * time.Minute))
			if until > rec.ExpiresAt {
				until = rec.ExpiresAt
			}
			consumed, err := tx.ConsumeApprovalOutcome(id, stamp(now), keyHash, until)
			if err != nil {
				return err
			}
			if !consumed {
				return nil
			}
			out.Collected = true
		}
		inv.PairingRequestID = out.PairingRequestID
		out.Invite = &NewServerInvite{Invite: inv, Secret: secret}
		return nil
	})
	if err == nil {
		err = refusal
	}
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
