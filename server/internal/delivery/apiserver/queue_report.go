package apiserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func queueFence(v *api.DeliveryQueueView, agent delivery.AgentRef, token string) (delivery.QueueFence, error) {
	if v == nil || v.MemberId != agent.MemberID || v.BoardId == "" {
		return delivery.QueueFence{}, fmt.Errorf("%w: queue fence identifies a different seat", delivery.ErrUnauthorized)
	}
	digest := sha256.Sum256([]byte(token))
	return delivery.QueueFence{BoardID: v.BoardId, MemberID: v.MemberId, Epoch: v.Epoch, Revision: v.Revision, CredentialGeneration: hex.EncodeToString(digest[:])}, nil
}

// QueueFence reads the authenticated seat's current reporter fence.
func (s *Server) QueueFence(ctx context.Context, agent delivery.AgentRef) (delivery.QueueFence, error) {
	if strings.TrimRight(agent.Server, "/") != s.url {
		return delivery.QueueFence{}, delivery.ErrUnauthorized
	}
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return delivery.QueueFence{}, err
	}
	c, err := s.client(token)
	if err != nil {
		return delivery.QueueFence{}, err
	}
	r, err := c.GetDeliveryQueueWithResponse(ctx)
	if err != nil {
		return delivery.QueueFence{}, err
	}
	if r.JSON200 == nil {
		return delivery.QueueFence{}, statusError("read queue fence", r.StatusCode(), r.Body)
	}
	return queueFence(r.JSON200, agent, token)
}

// ReportQueue sends one immutable report without changing its retry key.
func (s *Server) ReportQueue(ctx context.Context, agent delivery.AgentRef, in delivery.QueueReportIntent) (delivery.QueueFence, error) {
	if in.IdempotencyKey == "" {
		return delivery.QueueFence{}, fmt.Errorf("queue report needs its retained idempotency key")
	}
	if strings.TrimRight(agent.Server, "/") != s.url {
		return delivery.QueueFence{}, delivery.ErrUnauthorized
	}
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return delivery.QueueFence{}, err
	}
	digest := sha256.Sum256([]byte(token))
	if in.CredentialGeneration == "" || in.CredentialGeneration != hex.EncodeToString(digest[:]) {
		return delivery.QueueFence{}, delivery.ErrUnauthorized
	}
	c, err := s.client(token)
	if err != nil {
		return delivery.QueueFence{}, err
	}
	body := api.DeliveryQueueReport{Session: in.Session, Boot: in.Boot, ExpectedEpoch: in.ExpectedEpoch}
	if in.ExpectedEpoch == nil {
		body.Epoch, body.Revision = &in.Epoch, &in.Revision
		ids := make([]api.QueuedMessageIdentity, 0, len(in.Messages))
		for _, id := range in.Messages {
			ids = append(ids, api.QueuedMessageIdentity{MessageId: id.MessageID, Seq: id.Seq})
		}
		body.Messages = &ids
	}
	r, err := c.ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{}, body, func(_ context.Context, req *http.Request) error {
		req.Header.Set("Idempotency-Key", in.IdempotencyKey)
		return nil
	})
	if err != nil {
		return delivery.QueueFence{}, err
	}
	if r.JSON200 == nil {
		var wire struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(r.Body, &wire)
		if r.StatusCode() == http.StatusConflict && wire.Error.Code == "queue_report_conflict" {
			return delivery.QueueFence{}, delivery.ErrQueueReportConflict
		}
		return delivery.QueueFence{}, statusError("report queue", r.StatusCode(), r.Body)
	}
	return queueFence(r.JSON200, agent, token)
}
