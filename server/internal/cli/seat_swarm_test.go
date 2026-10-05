package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSwarmChecksItsRecordedSeatWithoutUsingAReusedNamesToken(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Error("swarm sent a replacement seat's credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"ended","hint":"join"}}`))
	}))
	defer srv.Close()
	a := seatApp(t)
	for _, c := range []agentCredential{
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"},
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"},
	} {
		if err := a.saveCredential(c); err != nil {
			t.Fatal(err)
		}
	}
	rows := []swarmAgent{{Name: "writer", memberID: "mem_old"}}
	a.checkSeats(context.Background(), a.serverRefFor(srv.URL), "docs", rows)
	if requests.Load() != 1 || deref(rows[0].SeatCredential) != seatEnded {
		t.Fatalf("recorded seat check: %+v, %d requests", rows, requests.Load())
	}
}

func TestSwarmDoesNotShowAReplacementSeatsPresence(t *testing.T) {
	a := seatApp(t)
	presence := api.MemberPresenceWorking
	got := a.swarmRow("writer", &swarmAgentRecord{MemberID: "mem_old", Mode: "headless"}, "running",
		delivery.BindingStatus{}, map[string]api.Member{"writer": {Id: "mem_new", Name: "writer", Presence: &presence}})
	if got.Presence != nil || got.Seated {
		t.Fatalf("replacement presence attached to old record: %+v", got)
	}
}
