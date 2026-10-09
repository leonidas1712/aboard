package board

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// QueueIdentity names an observed message without retaining its body.
type QueueIdentity struct {
	MessageID string `json:"message_id"`
	Seq       int64  `json:"seq"`
}

// QueueReport is recipient bookkeeping; its fence survives observation expiry.
type QueueReport struct {
	MemberID, BoardID, Session, Boot, CredentialDigest string
	Epoch, Revision                                    int64
	ExpiresAt                                          string
	Messages                                           []QueueIdentity
}

// QueueInput distinguishes an epoch claim from an observation update.
type QueueInput struct {
	Session, Boot                  string
	ExpectedEpoch, Epoch, Revision *int64
	Messages                       *[]QueueIdentity
}

func queueConflict() error {
	return apierr.New(409, "queue_report_conflict", "The queue reporter fence has changed.", "Do not replay through a replacement reporter; reconnect deliberately and read its current fence.")
}

func queueSeat(tx ReadTx, p Principal) (Board, Member, error) {
	if p.Agent == nil {
		return Board{}, Member{}, apierr.AgentRequired()
	}
	return seatOf(tx, *p.Agent)
}

func queueRecord(tx ReadTx, b Board, m Member) (QueueReport, error) {
	q, err := tx.QueueReport(m.ID)
	if errors.Is(err, ErrNotFound) {
		return QueueReport{MemberID: m.ID, BoardID: b.ID}, nil
	}
	return q, err
}

func queueVisible(q QueueReport, m Member, now time.Time) bool {
	if m.TokenDigest == nil || q.CredentialDigest != *m.TokenDigest || q.ExpiresAt == "" {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, q.ExpiresAt)
	return err == nil && now.Before(expires)
}

// DeliveryQueue reads only the authenticated seat's reporter fence.
func (s *Service) DeliveryQueue(ctx context.Context, p Principal) (QueueReport, error) {
	var out QueueReport
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		b, m, err := queueSeat(tx, p)
		if err != nil {
			return err
		}
		out, err = queueRecord(tx, b, m)
		if err != nil {
			return err
		}
		if !queueVisible(out, m, s.clk.Now()) {
			out.ExpiresAt = ""
			out.Messages = nil
		}
		return nil
	})
	return out, err
}

// ReportDeliveryQueue checks identities and updates the fence in one write transaction.
func (s *Service) ReportDeliveryQueue(ctx context.Context, p Principal, in QueueInput) (QueueReport, error) {
	if p.Agent == nil {
		return QueueReport{}, apierr.AgentRequired()
	}
	claim := in.ExpectedEpoch != nil
	if in.Session == "" || in.Boot == "" || len(in.Session) > 256 || len(in.Boot) > 256 ||
		(claim && (in.Epoch != nil || in.Revision != nil || in.Messages != nil || *in.ExpectedEpoch < 0)) ||
		(!claim && (in.Epoch == nil || in.Revision == nil || in.Messages == nil)) {
		return QueueReport{}, invalid("Choose a claim or a complete queue update.", "A claim needs expected_epoch; an update needs epoch, revision and messages.")
	}
	var out QueueReport
	err := s.writeAs(ctx, p, func(tx Tx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		b, m, err := queueSeat(tx, p)
		if err != nil {
			return err
		}
		q, err := queueRecord(tx, b, m)
		if err != nil {
			return err
		}
		if m.TokenDigest == nil {
			return queueConflict()
		}
		if claim {
			if q.Epoch != *in.ExpectedEpoch || q.Epoch == math.MaxInt64 {
				return queueConflict()
			}
			q = QueueReport{MemberID: m.ID, BoardID: b.ID, Session: in.Session, Boot: in.Boot, CredentialDigest: *m.TokenDigest, Epoch: q.Epoch + 1}
		} else {
			if q.Epoch != *in.Epoch || q.Epoch == 0 || q.Revision >= *in.Revision || q.Session != in.Session || q.Boot != in.Boot || q.CredentialDigest != *m.TokenDigest {
				return queueConflict()
			}
			if len(*in.Messages) > 1000 {
				return invalid("The queue report is too large.", "Report at most 1000 message identities.")
			}
			seen := map[string]bool{}
			seqs := make([]int64, 0, len(*in.Messages))
			for _, id := range *in.Messages {
				if id.MessageID == "" || id.Seq <= 0 || seen[id.MessageID] {
					return invalid("A queue identity is missing or repeated.", "Report each exact message id and sequence once.")
				}
				seen[id.MessageID] = true
				seqs = append(seqs, id.Seq)
			}
			msgs, err := tx.MessagesBySeq(b.ID, seqs)
			if err != nil {
				return err
			}
			for _, id := range *in.Messages {
				msg, ok := msgs[id.Seq]
				if !ok || msg.ID != id.MessageID || !rules.CanRead(b.Policy, msg.To, msg.SenderID, m.Rules(), msg.Recipients) {
					return messageNotFound()
				}
			}
			q.Revision = *in.Revision
			q.Messages = append([]QueueIdentity(nil), (*in.Messages)...)
			q.ExpiresAt = ""
			if len(q.Messages) > 0 {
				q.ExpiresAt = stamp(s.clk.Now().Add(45 * time.Second))
			}
		}
		if err := tx.SaveQueueReport(q); err != nil {
			return err
		}
		out = q
		return nil
	})
	return out, err
}

// CheckQueueReplay rechecks the lease without reapplying or renewing bookkeeping.
func (s *Service) CheckQueueReplay(ctx context.Context, p Principal, session, boot string, epoch int64) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		b, m, err := queueSeat(tx, p)
		if err != nil {
			return err
		}
		q, err := queueRecord(tx, b, m)
		if err != nil {
			return err
		}
		if epoch <= 0 || q.Epoch != epoch || q.Session != session || q.Boot != boot || m.TokenDigest == nil || q.CredentialDigest != *m.TokenDigest {
			return queueConflict()
		}
		return nil
	})
}

func queuedReceiptExpiry(tx ReadTx, recipient Member, message Message, now time.Time) (string, error) {
	q, err := tx.QueueReport(recipient.ID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !queueVisible(q, recipient, now) {
		return "", nil
	}
	owner, err := tx.HumanMember(recipient.BoardID, recipient.HumanID)
	if errors.Is(err, ErrNotFound) || (err == nil && owner.Status != StatusActive) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if recipient.KeyID != nil {
		if _, err := workingKey(tx, *recipient.KeyID, stamp(now)); errors.Is(err, ErrNotFound) {
			return "", nil
		} else if err != nil {
			return "", err
		}
	}
	for _, id := range q.Messages {
		if id.MessageID == message.ID && id.Seq == message.Seq {
			return q.ExpiresAt, nil
		}
	}
	return "", nil
}
