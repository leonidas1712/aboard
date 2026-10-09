package apiserver

import (
	"crypto/rand"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestBookkeepingRepeatsWithoutResponseCacheKeys(t *testing.T) {
	url, owner := localServer(t)
	board, writerToken, token := pairedAgents(t, url, owner)
	writer := apiClient(t, url, writerToken)
	posted, err := writer.PostMessageWithResponse(t.Context(), board, &api.PostMessageParams{}, api.PostMessageRequest{Body: "read this"})
	if err != nil || posted.JSON201 == nil {
		t.Fatalf("post: %v", err)
	}
	ref := delivery.AgentRef{Server: url, Board: board, Name: "reviewer"}
	s := New(url, tokens{agents: map[delivery.AgentRef]string{ref: token}}, rand.Reader)
	record := &bookkeepingRequests{next: http.DefaultTransport}
	s.http.Transport = record
	for _, cursor := range []int{posted.JSON201.Seq, posted.JSON201.Seq, 0} {
		if err := s.Ack(t.Context(), ref, cursor); err != nil {
			t.Fatal(err)
		}
	}
	_, cursor, _, err := s.Inbox(t.Context(), ref)
	if err != nil || cursor != posted.JSON201.Seq {
		t.Fatalf("repeated or lower acknowledgement moved cursor: %d, %v", cursor, err)
	}
	for range 2 {
		if err := s.SetPresence(t.Context(), ref, delivery.PresenceWorking, delivery.ModeHumans); err != nil {
			t.Fatal(err)
		}
	}
	person := apiClient(t, url, owner)
	mode, err := person.SetDeliveryModeWithResponse(t.Context(), board, ref.Name, &api.SetDeliveryModeParams{}, api.SetDeliveryModeJSONRequestBody{Mode: api.DeliveryModeSettingOff})
	if err != nil || mode.JSON200 == nil {
		t.Fatalf("person changes mode: %v", err)
	}
	if err := s.SetPresence(t.Context(), ref, delivery.PresenceIdle, ""); err != nil {
		t.Fatal(err)
	}
	me, err := apiClient(t, url, token).GetMeWithResponse(t.Context())
	if err != nil || me.JSON200 == nil || me.JSON200.DeliveryMode == nil || *me.JSON200.DeliveryMode != api.MeDeliveryModeOff {
		t.Fatalf("empty-mode presence overwrote person's setting: %v", err)
	}
	if record.writes.Load() != 6 || record.keyed.Load() != 0 {
		t.Fatalf("bookkeeping writes=%d keyed=%d; want 6 writes without response-cache keys", record.writes.Load(), record.keyed.Load())
	}
}

type bookkeepingRequests struct {
	next   http.RoundTripper
	writes atomic.Int32
	keyed  atomic.Int32
}

func (b *bookkeepingRequests) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		b.writes.Add(1)
		if r.Header.Get("Idempotency-Key") != "" {
			b.keyed.Add(1)
		}
	}
	return b.next.RoundTrip(r)
}
