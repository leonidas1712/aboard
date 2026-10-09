package board

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// PairingEndpoint identifies one runtime session selected by its own person.
type PairingEndpoint struct {
	PersonID, AgentID, SessionBinding string
	Generation                        int
}

// PairingEvidence is one confirmed round trip reported by its receiving runtime.
type PairingEvidence struct {
	PingSeq, ReplySeq int64
	HandoffID         string
}

// PairingRequest is a proposal between two people on a board, with exact endpoints.
type PairingRequest struct {
	ID, ServerID, BoardID, InviterID, InitiatingAgentID, RecipientID, InviteID string
	Work, State, CreatedAt, ExpiresAt                                          string
	InitiatorKeyID, RecipientKeyID                                             string
	Generation                                                                 int
	Initiator, Recipient                                                       *PairingEndpoint
	Accepted                                                                   bool
	Creation                                                                   PairingCreation
	InitialState                                                               string
	Forward, Reverse                                                           *PairingEvidence
}

// PairingCreation binds one creation receipt to its authenticated caller and request.
type PairingCreation struct {
	Scope, Key, Hash string
}

// PairingCredential is nonsecret endpoint metadata and the server's verifier.
type PairingCredential struct {
	ID, RequestID, Side, KeyID, Digest, ExpiresAt string
	Endpoint                                      PairingEndpoint
	SelectionGeneration                           int
}

// PairingSelection is an endpoint person's explicitly selected runtime.
type PairingSelection struct {
	RequestID, Side, AgentID, SessionBinding, ClientToken string
	Generation                                            int
	Replace                                               bool
}

func pairingError(status int, code, message string) error {
	return apierr.New(status, code, message, "Read the pairing request again: aboard pairing list.")
}

func pairingNotFound() error {
	return pairingError(404, "pairing_not_found", "That pairing request is not available to you.")
}

func pairingChanged() error {
	return pairingError(409, "pairing_changed", "The selected pairing endpoint changed.")
}

func pairingClosed() error {
	return pairingError(409, "pairing_closed", "That pairing request has ended.")
}

func pairingTerminal(r PairingRequest) bool {
	return r.State == "ready" || r.State == "declined" || r.State == "cancelled" || r.State == "expired"
}

func pairingSide(r PairingRequest, side string) (string, *PairingEndpoint) {
	if side == "initiator" {
		return r.InviterID, r.Initiator
	}
	return r.RecipientID, r.Recipient
}

func pairingMember(tx ReadTx, boardID, humanID, agentID string) error {
	h, err := tx.HumanByID(humanID)
	if errors.Is(err, ErrNotFound) || err == nil && h.RemovedAt != nil {
		return pairingNotFound()
	}
	if err != nil {
		return err
	}
	hm, err := tx.HumanMember(boardID, humanID)
	if errors.Is(err, ErrNotFound) || err == nil && hm.Status != StatusActive {
		return pairingNotFound()
	}
	if err != nil {
		return err
	}
	m, err := tx.MemberByID(agentID)
	if errors.Is(err, ErrNotFound) || err == nil && (m.Kind != "agent" || m.Status != StatusActive || m.BoardID != boardID || m.HumanID != humanID) {
		return pairingNotFound()
	}
	return err
}

func (s *Service) pairingView(tx ReadTx, p Principal, id string, active bool) (PairingRequest, error) {
	if p.Pairing != nil {
		p.pairingAllowed = true
	}
	if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
		return PairingRequest{}, err
	}
	r, err := tx.PairingByID(id)
	if errors.Is(err, ErrNotFound) {
		return PairingRequest{}, pairingNotFound()
	}
	if err != nil {
		return PairingRequest{}, err
	}
	if p.Pairing != nil && p.Pairing.RequestID != id {
		return PairingRequest{}, pairingNotFound()
	}
	if p.personID() != r.InviterID && p.personID() != r.RecipientID {
		return PairingRequest{}, pairingNotFound()
	}
	b, err := tx.BoardByID(r.BoardID)
	if errors.Is(err, ErrNotFound) || err == nil && lifecycleOf(b) == LifecycleDeleted {
		return PairingRequest{}, pairingNotFound()
	}
	if err != nil {
		return PairingRequest{}, err
	}
	if p.Agent != nil {
		if _, _, err := seatOf(tx, *p.Agent); err != nil || p.Agent.BoardID != b.ID {
			return PairingRequest{}, pairingNotFound()
		}
	}
	hm, err := tx.HumanMember(b.ID, p.personID())
	if errors.Is(err, ErrNotFound) || err == nil && hm.Status != StatusActive {
		return PairingRequest{}, pairingNotFound()
	}
	if err != nil {
		return PairingRequest{}, err
	}
	if active {
		if err := requireActive(b); err != nil {
			return PairingRequest{}, err
		}
	}
	if r.ExpiresAt <= stamp(s.clk.Now()) && !pairingTerminal(r) {
		r.State = "expired"
	}
	return r, nil
}

// CreatePairing proposes work to an existing ordinary member without selecting their session.
func (s *Service) CreatePairing(ctx context.Context, p Principal, boardID, recipientID, agentID, work string, creation PairingCreation) (PairingRequest, error) {
	var out PairingRequest
	if work == "" || !utf8.ValidString(work) || utf8.RuneCountInString(work) > 4000 {
		return out, invalid("A pairing needs work of at most 4000 characters.", "Describe the work to do together.")
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, err := tx.BoardByID(boardID)
		if errors.Is(err, ErrNotFound) {
			return pairingNotFound()
		}
		if err != nil {
			return err
		}
		if _, _, err = s.access(tx, p, b.Name); err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if person.Role == ServerGuest {
			return guestNotAllowed("request pairing")
		}
		if err := pairingMember(tx, b.ID, person.ID, agentID); err != nil {
			return err
		}
		if p.Agent != nil && p.Agent.ID != agentID {
			return pairingError(403, "forbidden", "An agent can request pairing only from its own seat.")
		}
		h, err := tx.HumanByID(recipientID)
		if errors.Is(err, ErrNotFound) || err == nil && h.RemovedAt != nil {
			return pairingNotFound()
		}
		if err != nil {
			return err
		}
		if h.Role == ServerGuest {
			return guestNotAllowed("request pairing with a guest")
		}
		membership, err := tx.HumanMember(b.ID, recipientID)
		if errors.Is(err, ErrNotFound) || err == nil && membership.Status != StatusActive {
			return pairingError(403, "forbidden", "The recipient must first join this board through an authorized admission.")
		}
		if err != nil {
			return err
		}
		now := s.clk.Now()
		if creation.Key != "" {
			if creation.Scope == "" || creation.Hash == "" {
				return invalid("The creation receipt is incomplete.", "Retry the same request.")
			}
			previous, err := tx.PairingByCreation(creation.Scope, creation.Key)
			if err == nil {
				createdAt, err := time.Parse(time.RFC3339Nano, previous.CreatedAt)
				if err != nil {
					return err
				}
				if now.Before(createdAt.Add(24 * time.Hour)) {
					if previous.Creation.Hash != creation.Hash {
						return apierr.New(422, "idempotency_conflict", "This Idempotency-Key was already used for a different request.", "Use a new Idempotency-Key for new work.")
					}
					if _, err := s.pairingView(tx, p, previous.ID, true); err != nil {
						return err
					}
					out = previous
					out.State = previous.InitialState
					out.Generation = 1
					out.Initiator = nil
					out.Recipient = nil
					out.Forward = nil
					out.Reverse = nil
					out.Accepted = false
					return nil
				}
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		id, err := s.gen.ID("prq", now)
		if err != nil {
			return err
		}
		out = PairingRequest{ID: id, ServerID: s.cfg.ServerID, BoardID: b.ID, InviterID: person.ID, InitiatingAgentID: agentID, RecipientID: recipientID, Work: work, State: "awaiting_session", InitialState: "awaiting_session", Creation: creation, Generation: 1, CreatedAt: stamp(now), ExpiresAt: stamp(now.Add(7 * 24 * time.Hour))}
		return tx.SavePairing(out)
	})
	return out, err
}

// ListPairings returns only requests the caller can currently read.
func (s *Service) ListPairings(ctx context.Context, p Principal) ([]PairingRequest, error) {
	out := []PairingRequest{}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		rs, err := tx.PairingsOf(p.personID())
		if err != nil {
			return err
		}
		for _, r := range rs {
			v, e := s.pairingView(tx, p, r.ID, false)
			if e != nil {
				var a *apierr.Error
				if errors.As(e, &a) && a.Code == "pairing_not_found" {
					continue
				}
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// GetPairing reads one request without changing any delivery cursor.
func (s *Service) GetPairing(ctx context.Context, p Principal, id string) (PairingRequest, error) {
	var out PairingRequest
	err := s.st.Read(ctx, func(tx ReadTx) error { var err error; out, err = s.pairingView(tx, p, id, false); return err })
	return out, err
}

// ClosePairing lets the recipient decline or the inviter cancel, never a peer agent.
func (s *Service) ClosePairing(ctx context.Context, p Principal, id, state string) (PairingRequest, error) {
	var out PairingRequest
	err := s.writeAs(ctx, p, func(tx Tx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		owner := r.InviterID
		if state == "declined" {
			owner = r.RecipientID
		}
		if p.personID() != owner {
			return pairingError(403, "forbidden", "Only that side's person can close this request.")
		}
		if pairingTerminal(r) && r.State != state {
			return pairingClosed()
		}
		r.State = state
		out = r
		return tx.SavePairing(r)
	})
	return out, err
}

// MintPairingCredential binds a caller-generated secret to one owned, exact endpoint.
func (s *Service) MintPairingCredential(ctx context.Context, p Principal, in PairingSelection) (PairingCredential, PairingRequest, error) {
	var out PairingCredential
	var request PairingRequest
	if err := requireOwnKey(p); err != nil {
		return out, request, err
	}
	if !validClientToken(in.ClientToken, "abp_") || !validBinding(in.SessionBinding) || (in.Side != "initiator" && in.Side != "recipient") {
		return out, request, invalid("The endpoint token or session binding is invalid.", "Retry from the trusted runtime.")
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		r, err := s.pairingView(tx, p, in.RequestID, true)
		if err != nil {
			return err
		}
		if pairingTerminal(r) {
			return pairingClosed()
		}
		human, old := pairingSide(r, in.Side)
		if human != p.personID() {
			return pairingError(403, "forbidden", "Only the endpoint's own person can select its session.")
		}
		if err := pairingMember(tx, r.BoardID, human, in.AgentID); err != nil {
			return err
		}
		if in.Side == "initiator" && in.AgentID != r.InitiatingAgentID && !in.Replace {
			return pairingChanged()
		}
		_, other := pairingSide(r, "recipient")
		if in.Side == "recipient" {
			_, other = pairingSide(r, "initiator")
		}
		if other != nil && other.AgentID == in.AgentID {
			return pairingChanged()
		}
		parent, err := workingKey(tx, p.KeyID, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		expiresAt := pairingExpiry(s.clk.Now(), r.ExpiresAt)
		if parent.ExpiresAt != nil && *parent.ExpiresAt < expiresAt {
			expiresAt = *parent.ExpiresAt
		}
		same := old != nil && old.AgentID == in.AgentID && old.SessionBinding == in.SessionBinding
		oldKey := r.InitiatorKeyID
		if in.Side == "recipient" {
			oldKey = r.RecipientKeyID
		}
		same = same && oldKey == p.KeyID
		digest := ids.Digest(s.key, in.ClientToken)
		existing, lookupErr := tx.PairingCredentialByDigest(digest)
		if lookupErr == nil {
			if !same || existing.RequestID != r.ID || existing.Side != in.Side || existing.KeyID != p.KeyID || existing.Endpoint != *old || (in.Generation != r.Generation && in.Generation != existing.SelectionGeneration) {
				return pairingChanged()
			}
			out = existing
			if out.ExpiresAt <= stamp(s.clk.Now()) {
				out.ExpiresAt = expiresAt
				if err := tx.SavePairingCredential(out); err != nil {
					return err
				}
			}
			request = r
			return nil
		}
		if !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
		if in.Generation != r.Generation {
			return pairingChanged()
		}
		if old != nil && !same {
			if !in.Replace {
				return pairingChanged()
			}
			r.Generation++
			r.Forward = nil
			r.Reverse = nil
			r.Accepted = false
			if r.Initiator != nil {
				r.Initiator.Generation = r.Generation
			}
			if r.Recipient != nil {
				r.Recipient.Generation = r.Generation
			}
			if err := tx.DeletePairingCredentials(r.ID); err != nil {
				return err
			}
		}
		endpoint := PairingEndpoint{PersonID: human, AgentID: in.AgentID, SessionBinding: in.SessionBinding, Generation: r.Generation}
		if in.Side == "initiator" {
			r.Initiator = &endpoint
			r.InitiatorKeyID = p.KeyID
		} else {
			r.Recipient = &endpoint
			r.RecipientKeyID = p.KeyID
		}
		r.State = "awaiting_endpoint"
		if r.Initiator != nil && r.Recipient != nil {
			r.State = "verifying"
		}
		now := s.clk.Now()
		id, err := s.gen.ID("pcr", now)
		if err != nil {
			return err
		}
		out = PairingCredential{ID: id, RequestID: r.ID, Side: in.Side, KeyID: p.KeyID, Digest: digest, Endpoint: endpoint, SelectionGeneration: in.Generation, ExpiresAt: expiresAt}
		if err := tx.SavePairingCredential(out); err != nil {
			return err
		}
		request = r
		return tx.SavePairing(r)
	})
	return out, request, err
}

func pairingExpiry(now time.Time, requestExpiry string) string {
	expires := stamp(now.Add(10 * time.Minute))
	if requestExpiry < expires {
		return requestExpiry
	}
	return expires
}

func validClientToken(token, prefix string) bool {
	if len(token) != len(prefix)+43 || !strings.HasPrefix(token, prefix) {
		return false
	}
	for _, r := range token[len(prefix):] {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if !valid {
			return false
		}
	}
	return true
}

func validBinding(binding string) bool {
	if !strings.HasPrefix(binding, "sha256:") || len(binding) != 71 {
		return false
	}
	for _, r := range binding[7:] {
		valid := r >= '0' && r <= '9' || r >= 'a' && r <= 'f'
		if !valid {
			return false
		}
	}
	return true
}

func (s *Service) pairingCredentialState(tx ReadTx, c PairingCredential, now string) (Human, error) {
	current, err := tx.PairingCredentialByDigest(c.Digest)
	if err != nil {
		return Human{}, apierr.Unauthorized()
	}
	if current.ID != c.ID || current.ExpiresAt <= now {
		return Human{}, apierr.Unauthorized()
	}
	key, err := workingKey(tx, c.KeyID, now)
	if err != nil || key.HumanID != c.Endpoint.PersonID {
		return Human{}, apierr.Unauthorized()
	}
	r, err := tx.PairingByID(c.RequestID)
	if err != nil || r.Generation != c.Endpoint.Generation || pairingTerminal(r) || r.ExpiresAt <= now {
		return Human{}, apierr.Unauthorized()
	}
	person, endpoint := pairingSide(r, c.Side)
	if endpoint == nil || *endpoint != c.Endpoint || person != key.HumanID {
		return Human{}, apierr.Unauthorized()
	}
	if err := pairingMember(tx, r.BoardID, person, c.Endpoint.AgentID); err != nil {
		return Human{}, apierr.Unauthorized()
	}
	b, err := tx.BoardByID(r.BoardID)
	if err != nil || lifecycleOf(b) != LifecycleActive {
		return Human{}, apierr.Unauthorized()
	}
	return tx.HumanByID(person)
}

func (s *Service) authenticatePairing(ctx context.Context, token string) (Principal, error) {
	var p Principal
	err := s.st.Read(ctx, func(tx ReadTx) error {
		c, err := tx.PairingCredentialByDigest(ids.Digest(s.key, token))
		if errors.Is(err, ErrNotFound) {
			return apierr.Unauthorized()
		}
		if err != nil {
			return err
		}
		h, err := s.pairingCredentialState(tx, c, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		p = Principal{Pairing: &c, Human: &h, KeyID: c.KeyID}
		return nil
	})
	return p, err
}

// AcceptPairing accepts only the recipient endpoint already bound by its runtime.
func (s *Service) AcceptPairing(ctx context.Context, p Principal, id, agentID string, generation int) (PairingRequest, error) {
	var out PairingRequest
	if p.Pairing == nil || p.Pairing.Side != "recipient" {
		return out, pairingError(403, "forbidden", "Only the selected recipient runtime can accept this pairing.")
	}
	p.pairingAllowed = true
	err := s.writeAs(ctx, p, func(tx Tx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		if generation != r.Generation || p.Pairing.Endpoint.AgentID != agentID {
			return pairingChanged()
		}
		r.Accepted = true
		r.State = "awaiting_endpoint"
		if r.Initiator != nil && r.Recipient != nil {
			r.State = "verifying"
		}
		out = r
		return tx.SavePairing(r)
	})
	return out, err
}

// PairingCorrelation is the exact request/generation/direction returned by a handshake.
func PairingCorrelation(id string, generation int, direction string) string {
	return fmt.Sprintf("ABOARD-PAIRING %s generation=%d direction=%s", id, generation, direction)
}

// VerifyPairing accepts one runtime's confirmed reply and derives readiness from both sides.
func (s *Service) VerifyPairing(ctx context.Context, p Principal, id, direction string, generation int, pingSeq, replySeq int64, handoffID string) (PairingRequest, error) {
	var out PairingRequest
	if p.Pairing == nil {
		return out, pairingError(403, "forbidden", "Only a selected runtime can report pairing delivery.")
	}
	p.pairingAllowed = true
	err := s.writeAs(ctx, p, func(tx Tx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		if generation != r.Generation || r.Initiator == nil || r.Recipient == nil || !r.Accepted {
			return pairingChanged()
		}
		sender, peer := r.Initiator, r.Recipient
		side := "initiator"
		if direction == "recipient_to_initiator" {
			sender, peer = r.Recipient, r.Initiator
			side = "recipient"
		} else if direction != "initiator_to_recipient" {
			return invalid("The pairing direction is invalid.", "Use a current pairing direction.")
		}
		if p.Pairing.Side != side {
			return pairingChanged()
		}
		if err := pairingMember(tx, r.BoardID, sender.PersonID, sender.AgentID); err != nil {
			return err
		}
		if err := pairingMember(tx, r.BoardID, peer.PersonID, peer.AgentID); err != nil {
			return err
		}
		for _, keyID := range []string{r.InitiatorKeyID, r.RecipientKeyID} {
			if _, err := workingKey(tx, keyID, stamp(s.clk.Now())); err != nil {
				return pairingChanged()
			}
		}
		messages, err := tx.MessagesBySeq(r.BoardID, []int64{pingSeq, replySeq})
		if err != nil {
			return err
		}
		ping, pok := messages[pingSeq]
		reply, rok := messages[replySeq]
		correlation := PairingCorrelation(r.ID, r.Generation, direction)
		direct := func(m Message, to string) bool {
			return len(m.Recipients) == 1 && m.Recipients[0] == to && len(m.To) == 1 && strings.HasPrefix(m.To[0], "@")
		}
		if !pok || !rok || replySeq <= pingSeq || ping.SenderID != sender.AgentID || reply.SenderID != peer.AgentID || !direct(ping, peer.AgentID) || !direct(reply, sender.AgentID) || reply.ReplyTo == nil || *reply.ReplyTo != ping.ID || !strings.Contains(ping.Body, correlation+" kind=ping") || !strings.Contains(reply.Body, correlation+" kind=reply") || handoffID == "" || len(handoffID) > 200 {
			return pairingChanged()
		}
		evidence := &PairingEvidence{PingSeq: pingSeq, ReplySeq: replySeq, HandoffID: handoffID}
		old := r.Forward
		if side == "recipient" {
			old = r.Reverse
		}
		if old != nil && *old != *evidence {
			return pairingChanged()
		}
		if side == "initiator" {
			r.Forward = evidence
		} else {
			r.Reverse = evidence
		}
		r.State = "verifying"
		if r.Forward != nil && r.Reverse != nil {
			r.State = "ready"
		}
		out = r
		return tx.SavePairing(r)
	})
	return out, err
}

// CheckPairingReplay rechecks authority and endpoint generation before a cached receipt.
func (s *Service) CheckPairingReplay(ctx context.Context, p Principal, id string, generation int, side string) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		r, err := s.pairingView(tx, p, id, true)
		if err != nil {
			return err
		}
		if generation != 0 && generation != r.Generation {
			return pairingChanged()
		}
		if side != "" {
			person, endpoint := pairingSide(r, side)
			if person != p.personID() {
				return pairingNotFound()
			}
			if endpoint != nil {
				if err := pairingMember(tx, r.BoardID, person, endpoint.AgentID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// CreateInvitedPairingTx records a proposal in the same transaction as its server invite.
func (s *Service) CreateInvitedPairingTx(tx Tx, p Principal, inviteID, boardID, agentID, work, expiresAt string) (PairingRequest, error) {
	if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
		return PairingRequest{}, err
	}
	b, err := tx.BoardByID(boardID)
	if err != nil {
		return PairingRequest{}, err
	}
	if _, _, err := s.access(tx, p, b.Name); err != nil {
		return PairingRequest{}, err
	}
	if err := requireActive(b); err != nil {
		return PairingRequest{}, err
	}
	person, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return PairingRequest{}, err
	}
	if person.Role == ServerGuest {
		return PairingRequest{}, guestNotAllowed("request pairing")
	}
	if p.Agent != nil && p.Agent.ID != agentID {
		return PairingRequest{}, pairingError(403, "forbidden", "A pairing proposal must use the initiating agent's own seat.")
	}
	if err := pairingMember(tx, boardID, person.ID, agentID); err != nil {
		return PairingRequest{}, err
	}
	if work == "" || !utf8.ValidString(work) || utf8.RuneCountInString(work) > 4000 || expiresAt <= stamp(s.clk.Now()) {
		return PairingRequest{}, invalid("The pairing work or expiry is invalid.", "Create a new pairing proposal.")
	}
	now := s.clk.Now()
	id, err := s.gen.ID("prq", now)
	if err != nil {
		return PairingRequest{}, err
	}
	r := PairingRequest{ID: id, ServerID: s.cfg.ServerID, BoardID: boardID, InviterID: person.ID, InitiatingAgentID: agentID, InviteID: inviteID, Work: work, State: "awaiting_account", Generation: 1, CreatedAt: stamp(now), ExpiresAt: expiresAt}
	return r, tx.SavePairing(r)
}

// RedeemInvitedPairingTx binds the newly admitted person while redeeming their invite.
func (s *Service) RedeemInvitedPairingTx(tx Tx, inviteID, recipientID string) (string, error) {
	r, err := tx.PairingByInvite(inviteID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if r.RecipientID != "" {
		if r.RecipientID != recipientID {
			return "", pairingChanged()
		}
		return r.ID, nil
	}
	if r.State != "awaiting_account" || r.ExpiresAt <= stamp(s.clk.Now()) {
		return "", pairingClosed()
	}
	h, err := tx.HumanByID(recipientID)
	if err != nil {
		return "", err
	}
	if h.RemovedAt != nil || h.Role == ServerGuest {
		return "", pairingNotFound()
	}
	membership, err := tx.HumanMember(r.BoardID, recipientID)
	if err != nil {
		return "", err
	}
	if membership.Status != StatusActive {
		return "", pairingNotFound()
	}
	if err := pairingMember(tx, r.BoardID, r.InviterID, r.InitiatingAgentID); err != nil {
		return "", err
	}
	b, err := tx.BoardByID(r.BoardID)
	if err != nil {
		return "", err
	}
	if err := requireActive(b); err != nil {
		return "", err
	}
	r.RecipientID = recipientID
	r.State = "awaiting_session"
	return r.ID, tx.SavePairing(r)
}
