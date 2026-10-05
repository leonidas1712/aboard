package apiserver

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestWrongSeatResponsesNeverAdvanceDelivery(t *testing.T) {
	for _, id := range []string{"", "mem_expected", "mem_other"} {
		t.Run(id, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				member := ""
				if id != "" {
					member = `,"member_id":"` + id + `"`
				}
				if r.Method == http.MethodPost {
					_, _ = w.Write([]byte(`{"cursor":99` + member + `}`))
				} else {
					_, _ = w.Write([]byte(`{"board":"docs","agent":"writer","messages":[],"cursor":99,"more":false` + member + `}`))
				}
			}))
			defer srv.Close()
			ref := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_expected"}
			s := New(srv.URL, tokens{agents: map[delivery.AgentRef]string{ref: "aba_fixture"}}, rand.Reader)
			_, cursor, _, readErr := s.Inbox(context.Background(), ref)
			ackErr := s.Ack(context.Background(), ref, 99)
			if id == "mem_other" {
				if !errors.Is(readErr, delivery.ErrUnauthorized) || cursor != 0 || !errors.Is(ackErr, delivery.ErrUnauthorized) {
					t.Fatalf("wrong identity accepted: cursor=%d read=%v ack=%v", cursor, readErr, ackErr)
				}
			} else if readErr != nil || ackErr != nil || cursor != 99 {
				t.Fatalf("compatible seat response refused: cursor=%d read=%v ack=%v", cursor, readErr, ackErr)
			}
		})
	}
}
