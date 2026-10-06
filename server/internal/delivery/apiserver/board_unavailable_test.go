package apiserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestUnavailableRefreshesOnlyIDsObservedOnThisConnection(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		want         []delivery.Head
	}{
		{name: "known board", stream: "event: head\ndata: {\"board\":\"docs\",\"board_id\":\"brd_docs\",\"seq\":7}\n\nevent: board_unavailable\ndata: {\"board_id\":\"brd_docs\",\"member_id\":\"mem_old\"}\n\n", want: []delivery.Head{{Board: "docs", Seq: 7}, {Board: "docs"}}},
		{name: "unknown and malformed", stream: "event: board_unavailable\ndata: {\"board_id\":\"brd_unknown\",\"board\":\"docs\"}\n\nevent: board_unavailable\ndata: {}\n\nevent: board_unavailable\ndata: nope\n\n"},
		{name: "legacy head cannot identify hint", stream: "event: head\ndata: {\"board\":\"docs\",\"seq\":7}\n\nevent: board_unavailable\ndata: {\"board_id\":\"brd_docs\"}\n\n", want: []delivery.Head{{Board: "docs", Seq: 7}}},
		{name: "read cannot establish board identity", stream: "event: read\ndata: {\"board\":\"docs\",\"board_id\":\"brd_docs\",\"agent\":\"writer\",\"member_id\":\"mem_writer\",\"read_up_to\":4}\n\nevent: board_unavailable\ndata: {\"board_id\":\"brd_docs\"}\n\n", want: []delivery.Head{{Board: "docs", Read: &delivery.ReadPosition{Agent: "writer", MemberID: "mem_writer", UpTo: 4}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer abk_person" {
					t.Error("stream did not use its own person's credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tc.stream)
			}))
			defer ts.Close()
			s := New(ts.URL, tokens{human: map[string]string{ts.URL: "abk_person"}}, rand.Reader)
			var got []delivery.Head
			err := s.Follow(t.Context(), func() {}, func(h delivery.Head) { got = append(got, h) })
			if !errors.Is(err, errStreamEnded) {
				t.Fatalf("Follow: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("heads=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUnavailableObservationDoesNotSurviveReconnect(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			_, _ = fmt.Fprint(w, "event: head\ndata: {\"board\":\"docs\",\"board_id\":\"brd_docs\",\"seq\":7}\n\n")
		} else {
			_, _ = fmt.Fprint(w, "event: board_unavailable\ndata: {\"board_id\":\"brd_docs\"}\n\n")
		}
	}))
	defer ts.Close()
	s := New(ts.URL, tokens{human: map[string]string{ts.URL: "abk_person"}}, rand.Reader)
	for call := 1; call <= 2; call++ {
		var got []delivery.Head
		err := s.Follow(t.Context(), func() {}, func(h delivery.Head) { got = append(got, h) })
		if !errors.Is(err, errStreamEnded) {
			t.Fatal(err)
		}
		if call == 1 && len(got) != 1 {
			t.Fatalf("first connection heads=%v", got)
		}
		if call == 2 && len(got) != 0 {
			t.Fatalf("reconnected stream reused an earlier observation: %v", got)
		}
	}
}

// A stale hint names neither a token nor a terminal state. The consumer requests
// the current seat's inbox, and one refusal cannot end the stream or a sibling.
func TestUnavailableRefreshUsesFreshSeatCredentialsAndKeepsSiblings(t *testing.T) {
	var ts *httptest.Server
	fresh := delivery.AgentRef{Board: "docs", Name: "writer", MemberID: "mem_new"}
	denied := delivery.AgentRef{Board: "other", Name: "writer", MemberID: "mem_other"}
	requested := map[string]int{}
	var requestsMu sync.Mutex
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: head\ndata: {\"board\":\"docs\",\"board_id\":\"brd_docs\",\"seq\":1}\n\nevent: head\ndata: {\"board\":\"other\",\"board_id\":\"brd_other\",\"seq\":1}\n\nevent: board_unavailable\ndata: {\"board_id\":\"brd_docs\",\"member_id\":\"mem_old\"}\n\nevent: board_unavailable\ndata: {\"board_id\":\"brd_other\"}\n\nevent: head\ndata: {\"board\":\"docs\",\"board_id\":\"brd_docs\",\"seq\":2}\n\n")
			return
		}
		token := r.Header.Get("Authorization")
		requestsMu.Lock()
		requested[token]++
		requestsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch token {
		case "Bearer aba_fresh":
			_, _ = fmt.Fprint(w, `{"board":"docs","agent":"writer","member_id":"mem_new","messages":[],"cursor":9,"more":false}`)
		case "Bearer aba_denied":
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"code":"board_not_found","message":"Unavailable","hint":"Join another board"}}`)
		default:
			t.Error("inbox used an unexpected credential")
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer ts.Close()
	fresh.Server, denied.Server = ts.URL, ts.URL
	creds := tokens{human: map[string]string{ts.URL: "abk_person"}, agents: map[delivery.AgentRef]string{fresh: "aba_old", denied: "aba_denied"}}
	s := New(ts.URL, creds, rand.Reader)
	authorized, ended, continued := 0, 0, false
	err := s.Follow(t.Context(), func() {}, func(h delivery.Head) {
		if h.Seq == 1 && h.Board == "docs" {
			creds.agents[fresh] = "aba_fresh"
			return
		}
		if h.Seq == 2 {
			continued = true
			return
		}
		if h.Seq != 0 {
			return
		}
		agent := fresh
		if h.Board == "other" {
			agent = denied
		}
		_, cursor, _, err := s.Inbox(context.Background(), agent)
		if agent == fresh {
			if err != nil || cursor != 9 {
				t.Errorf("authorized replacement rejected: cursor=%d err=%v", cursor, err)
			}
			authorized++
		} else {
			if !errors.Is(err, delivery.ErrBoardGone) || cursor != 0 {
				t.Errorf("current refusal: cursor=%d err=%v", cursor, err)
			}
			ended++
		}
	})
	if !errors.Is(err, errStreamEnded) || authorized != 1 || ended != 1 || !continued {
		t.Fatalf("stream=%v authorized=%d ended=%d continued=%v", err, authorized, ended, continued)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if requested["Bearer aba_fresh"] != 1 || requested["Bearer aba_denied"] != 1 || len(requested) != 2 {
		t.Fatalf("unexpected credential request counts: fresh=%d denied=%d total=%d", requested["Bearer aba_fresh"], requested["Bearer aba_denied"], len(requested))
	}
}
