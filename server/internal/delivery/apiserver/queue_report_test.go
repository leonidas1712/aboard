package apiserver

import (
	"crypto/rand"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestQueueReportsUseTheOwnSeatAndStableRetryKey(t *testing.T) {
	url, owner := localServer(t)
	board, _, token := pairedAgents(t, url, owner)
	c := apiClient(t, url, token)
	me, err := c.GetMeWithResponse(t.Context())
	if err != nil || me.JSON200 == nil {
		t.Fatalf("identify: %v", err)
	}
	ref := delivery.AgentRef{Server: url, Board: board, Name: me.JSON200.Name, MemberID: me.JSON200.Id}
	credentials := tokens{agents: map[delivery.AgentRef]string{ref: token}}
	s := New(url, credentials, rand.Reader)
	fence, err := s.QueueFence(t.Context(), ref)
	if err != nil || fence.MemberID != ref.MemberID || fence.Epoch != 0 {
		t.Fatalf("fence: %+v %v", fence, err)
	}
	expected := fence.Epoch
	claim := delivery.QueueReportIntent{Session: "codex:reporter", Boot: "b1", IdempotencyKey: "claim-stable", ExpectedEpoch: &expected, CredentialGeneration: fence.CredentialGeneration}
	first, err := s.ReportQueue(t.Context(), ref, claim)
	if err != nil || first.Epoch != 1 {
		t.Fatalf("claim: %+v %v", first, err)
	}
	retry, err := s.ReportQueue(t.Context(), ref, claim)
	if err != nil || retry != first {
		t.Fatalf("retry changed lease: %+v %v", retry, err)
	}
	update := delivery.QueueReportIntent{Session: claim.Session, Boot: claim.Boot, IdempotencyKey: "update-stable", Epoch: first.Epoch, CredentialGeneration: first.CredentialGeneration, Revision: 1, Messages: []delivery.QueuedMessage{}}
	updated, err := s.ReportQueue(t.Context(), ref, update)
	if err != nil || updated.Revision != 1 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	retry, err = s.ReportQueue(t.Context(), ref, update)
	if err != nil || retry != updated {
		t.Fatalf("update retry: %+v %v", retry, err)
	}
	claim.IdempotencyKey = "stale-claim"
	if _, err := s.ReportQueue(t.Context(), ref, claim); !errors.Is(err, delivery.ErrQueueReportConflict) {
		t.Fatalf("stale claim: %v", err)
	}
	transport := &blockedQueueTransport{}
	s.http.Transport = transport
	credentials.agents[ref] = token + "-replacement"
	if _, err := s.ReportQueue(t.Context(), ref, update); !errors.Is(err, delivery.ErrUnauthorized) {
		t.Fatalf("replacement credential replay: %v", err)
	}
	if transport.calls.Load() != 0 {
		t.Fatal("retained report sent a replacement credential")
	}
}

type blockedQueueTransport struct{ calls atomic.Int32 }

func (b *blockedQueueTransport) RoundTrip(*http.Request) (*http.Response, error) {
	b.calls.Add(1)
	return nil, errors.New("unexpected HTTP request with replacement credential")
}
