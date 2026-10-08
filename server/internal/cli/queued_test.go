package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestQueuedInboxRequiresALiveSessionWithoutStartingOne(t *testing.T) {
	a := inboxApp(t)
	err := runInbox(context.Background(), a, []string{"--queued"})
	if err == nil || asError(err).Code != "queue_unknown" {
		t.Fatalf("want unknown queue, got %v", err)
	}
}

func TestQueuedPreviewReadsAcceptedMessageWithOnlyTheCurrentSeatToken(t *testing.T) {
	issuer, owner := testServer(t)
	a := inboxApp(t)
	cred := seatOn(t, issuer, owner)
	status, posted := do(t, "POST", issuer+"/v1/boards/"+cred.Board+"/messages", owner, map[string]any{"to": []string{"@" + cred.Name}, "body": "queued body"})
	if status != 201 {
		t.Fatal(status, posted)
	}
	rawSeq, ok := posted["seq"].(float64)
	if !ok {
		t.Fatal("post omitted sequence")
	}
	seq := int(rawSeq)
	id, ok := posted["id"].(string)
	if !ok {
		t.Fatal("post omitted message id")
	}
	_, initial := do(t, "GET", issuer+"/v1/me/inbox", cred.Token, nil)
	boardID, ok := initial["board_id"].(string)
	if !ok {
		t.Fatal("inbox omitted board id")
	}
	do(t, "POST", issuer+"/v1/me/inbox/ack", cred.Token, map[string]any{"up_to": seq})
	upstream, err := url.Parse(issuer)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(upstream); r.Out.Host = upstream.Host }}
	var mu sync.Mutex
	var requests []struct{ method, auth string }
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, struct{ method, auth string }{r.Method, r.Header.Get("Authorization")})
		mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	defer recorder.Close()
	cred.Server = recorder.URL
	ref := delivery.AgentRef{Server: cred.Server, Board: cred.Board, Name: cred.Name, MemberID: cred.MemberID}
	observation := delivery.Response{Agents: []delivery.AgentRef{ref}, Queued: &delivery.QueuedMessages{Count: 1, Messages: []delivery.QueuedMessage{{BoardID: boardID, MemberID: cred.MemberID, MessageID: id, Seq: seq, From: "@maya", Boundary: "turn_end"}}}}
	stale := cred
	stale.MemberID, stale.Token = "mem_stale", owner
	out, err := a.queuedPreview(context.Background(), observation, credentials{Agents: []agentCredential{stale, cred}}, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Seats) != 1 || len(out.Seats[0].msgs) != 1 || out.Seats[0].msgs[0].Id != id || out.Seats[0].AckedUpTo != nil {
		t.Fatalf("queued preview: %+v", out)
	}
	mu.Lock()
	for _, request := range requests {
		if request.method != "GET" || (request.auth != "" && request.auth != "Bearer "+cred.Token) {
			t.Errorf("preview wrote or used another credential: %s", request.method)
		}
	}
	mu.Unlock()
	_, current := do(t, "GET", issuer+"/v1/me/inbox", cred.Token, nil)
	cursor, ok := current["cursor"].(float64)
	if !ok || int(cursor) != seq {
		t.Fatal("preview moved cursor")
	}
	do(t, "DELETE", issuer+"/v1/boards/"+cred.Board+"/members/"+cred.Name, owner, nil)
	if _, err := a.queuedPreview(context.Background(), observation, credentials{Agents: []agentCredential{cred}}, "", "", 0); err == nil {
		t.Fatal("removed seat previewed cached queued body")
	}
}
