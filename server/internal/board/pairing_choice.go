package board

import (
	"context"
	"fmt"
)

// ChoosePairingAgent asks one owned seat to accept; it never binds a runtime endpoint.
func (s *Service) ChoosePairingAgent(ctx context.Context, p Principal, id, agentID string, generation int) (PairingRequest, error) {
	var out PairingRequest
	var notice Message
	if err := humanOnly(p, "choose your pairing agent", "aboard pairing accept --here"); err != nil {
		return out, err
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		if r.RecipientID != p.personID() {
			return pairingError(403, "forbidden", "Only the recipient may choose their own agent.")
		}
		if pairingTerminal(r) {
			return pairingClosed()
		}
		if r.Generation != generation {
			return pairingChanged()
		}
		if r.AdmissionApprovalID != "" && r.State == "awaiting_endpoint" {
			return pairingChanged()
		}
		if err := pairingMember(tx, r.BoardID, p.personID(), agentID); err != nil {
			return err
		}
		if agentID == r.InitiatingAgentID {
			return pairingChanged()
		}
		if r.ChosenRecipientAgentID != "" {
			if r.ChosenRecipientAgentID != agentID {
				return pairingChanged()
			}
			out = r
			return nil
		}
		if r.Recipient != nil {
			return pairingChanged()
		}
		b, err := tx.BoardByID(r.BoardID)
		if err != nil {
			return err
		}
		agent, err := tx.MemberByID(agentID)
		if err != nil {
			return err
		}
		command := fmt.Sprintf("aboard pairing accept %s --here --server '%s'", r.ID, s.cfg.IssuerURL)
		notice, err = s.postMessageTx(tx, p, b.Name, NewMessage{To: []string{"@" + agent.Name}, Body: "Please accept this pairing request in this session: " + command + "\nProposed work: " + r.Work, ExpectsReply: true})
		if err != nil {
			return err
		}
		r.ChosenRecipientAgentID, r.ChoiceMessageID = agentID, notice.ID
		if err := tx.SavePairing(r); err != nil {
			return err
		}
		out = r
		return nil
	})
	if err == nil && notice.ID != "" {
		s.notify.Changed(notice.BoardID)
	}
	return out, err
}

// CheckPairingChoiceReplay retains current recipient and selected-seat checks on cache hits.
func (s *Service) CheckPairingChoiceReplay(ctx context.Context, p Principal, id, agentID string, generation int) error {
	if err := humanOnly(p, "choose your pairing agent", "aboard pairing accept --here"); err != nil {
		return err
	}
	return s.st.Read(ctx, func(tx ReadTx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		if r.RecipientID != p.personID() {
			return pairingError(403, "forbidden", "Only the recipient may choose their own agent.")
		}
		if pairingTerminal(r) {
			return pairingClosed()
		}
		if r.Generation != generation || r.ChosenRecipientAgentID != agentID {
			return pairingChanged()
		}
		return pairingMember(tx, r.BoardID, p.personID(), agentID)
	})
}
