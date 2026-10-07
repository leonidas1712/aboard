package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/server"
)

// testServer runs a real Aboard server in this process and returns its URL and its
// first person's key.
func testServer(t *testing.T) (url, owner string) {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The server takes the listener itself: a port closed again before it listens could
	// go to another test's server, which knows none of this one's keys.
	addr := l.Addr().String()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{
			Listener: l, DataDir: filepath.Join(dir, "data"), OwnerName: "maya",
			OwnerTokenPath: filepath.Join(dir, "owner-token"), Version: "test",
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	url = "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		if raw, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "owner-token"))); err == nil {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url+"/v1/info", http.NoBody)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
				return url, strings.TrimSpace(string(raw))
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the test server didn't start")
		}
		select {
		case err := <-done:
			t.Fatalf("the test server stopped: %v", err)
		case <-time.After(20 * time.Millisecond): // a poll interval while the server starts
		}
	}
}

// do makes a request to url with token and returns the status and decoded body.
func do(t *testing.T, method, url, token string, body any) (status int, answer map[string]any) {
	t.Helper()
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, payload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// seatOn makes a board and an agent of the owner's on it, and returns the board's name
// and the agent's credential.
func seatOn(t *testing.T, url, owner string) agentCredential {
	t.Helper()
	_, b := do(t, "POST", url+"/v1/boards", owner, map[string]any{"template": "general"})
	board, _ := b["name"].(string)
	st, j := do(t, "POST", url+"/v1/join", owner, map[string]any{"board": board, "role": "member"})
	if st != 201 {
		t.Fatalf("join: %d %v", st, j)
	}
	agent, _ := j["agent"].(map[string]any)
	name, _ := agent["name"].(string)
	id, _ := agent["id"].(string)
	token, _ := j["token"].(string)
	return agentCredential{Server: url, Board: board, Name: name, MemberID: id, Token: token}
}

func postTo(t *testing.T, url, token, board string, n int) {
	t.Helper()
	for range n {
		if st, b := do(t, "POST", url+"/v1/boards/"+board+"/messages", token, map[string]any{"body": "hello"}); st != 201 {
			t.Fatalf("post: %d %v", st, b)
		}
	}
}

func inboxApp(t *testing.T) *app {
	t.Helper()
	home := t.TempDir()
	return &app{env: Env{Rand: rand.Reader, Dir: home, Getenv: func(k string) string {
		return map[string]string{"ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:1"}[k]
	}}}
}

// In a session with several seats, the inbox reads every seat with its own token and
// acknowledges, per seat, only what it shows: --limit applies per seat. A seat whose
// read is refused is counted unavailable, never named, and acknowledges nothing.
func TestTheInboxReadsEverySeat(t *testing.T) {
	url, owner := testServer(t)
	a := inboxApp(t)
	general, plans := seatOn(t, url, owner), seatOn(t, url, owner)
	postTo(t, url, owner, general.Board, 3)
	postTo(t, url, owner, plans.Board, 1)
	// A third seat is on a board that sam made private; sam then removes the seat's
	// person, so its token is refused and the board is hidden from them.
	_, inv := do(t, "POST", url+"/v1/invites", owner, map[string]any{})
	link, _ := inv["invite"].(string)
	_, conn := do(t, "POST", url+"/v1/connect", "", map[string]any{"invite": link, "handle": "sam", "key_name": "laptop"})
	key, _ := conn["key"].(map[string]any)
	sam, _ := key["token"].(string)
	_, b := do(t, "POST", url+"/v1/boards", sam, map[string]any{"template": "general"})
	hidden, _ := b["name"].(string)
	do(t, "POST", url+"/v1/boards/"+hidden+"/people", sam, map[string]any{"handle": "maya"})
	st, j := do(t, "POST", url+"/v1/join", owner, map[string]any{"board": hidden, "role": "member"})
	if st != 201 {
		t.Fatalf("join %s: %d %v", hidden, st, j)
	}
	agent, _ := j["agent"].(map[string]any)
	goneName, _ := agent["name"].(string)
	goneToken, _ := j["token"].(string)
	goneID, _ := agent["id"].(string)
	gone := agentCredential{Server: url, Board: hidden, Name: goneName, MemberID: goneID, Token: goneToken}
	postTo(t, url, sam, hidden, 1)
	do(t, "POST", url+"/v1/boards/"+hidden+"/visibility", sam, map[string]any{"visibility": "private"})
	if st, b := do(t, "DELETE", url+"/v1/boards/"+hidden+"/people/maya", sam, nil); st != 200 {
		t.Fatalf("remove maya: %d %v", st, b)
	}
	// An earlier seat with general's name, whose token no longer works, is never read
	// in its place: seats are found by member id.
	stale := general
	stale.MemberID, stale.Token = "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1A", "aba_stale"
	creds := credentials{Agents: []agentCredential{stale, general, plans, gone}}
	seats := []delivery.AgentRef{}
	for _, c := range []agentCredential{general, plans, gone} {
		seats = append(seats, delivery.AgentRef{Server: c.Server, Board: c.Board, Name: c.Name, MemberID: c.MemberID})
	}
	out, err := a.inboxSeats(context.Background(), seats, creds, 2, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Seats) != 2 || out.Unavailable != 1 {
		t.Fatalf("seats %d, unavailable %d", len(out.Seats), out.Unavailable)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), gone.Board) {
		t.Errorf("the unavailable seat's board is named: %s", raw)
	}
	first := out.Seats[0]
	if first.Board != general.Board || first.MemberID != general.MemberID || len(first.Messages) != 2 || !first.More ||
		first.AckedUpTo == nil || *first.AckedUpTo != first.Messages[1].Seq {
		t.Fatalf("the first seat: %+v", first)
	}
	if out.Bundle == nil || strings.Count(*out.Bundle, "<aboard-messages ") != 2 {
		t.Fatalf("bundle: %v", out.Bundle)
	}
	// What wasn't shown is still unread on that seat; the other seat has nothing left.
	again, err := a.inboxSeats(context.Background(), seats[:2], creds, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Seats[0].Messages) != 1 || len(again.Seats[1].Messages) != 0 || again.Seats[0].AckedUpTo != nil {
		t.Fatalf("after: %+v", again.Seats)
	}
}

// With --wait, the inbox returns once any seat has a message.
func TestTheInboxWaitsForAnySeat(t *testing.T) {
	url, owner := testServer(t)
	a := inboxApp(t)
	one, two := seatOn(t, url, owner), seatOn(t, url, owner)
	creds := credentials{Agents: []agentCredential{one, two}}
	seats := []delivery.AgentRef{
		{Server: url, Board: one.Board, Name: one.Name, MemberID: one.MemberID}, {Server: url, Board: two.Board, Name: two.Name, MemberID: two.MemberID},
	}
	go func() {
		time.Sleep(300 * time.Millisecond) // the message arrives while the inbox waits
		postTo(t, url, owner, two.Board, 1)
	}()
	start := time.Now()
	out, err := a.inboxSeats(context.Background(), seats, creds, 0, true, 20)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second || len(out.Seats[1].Messages) != 1 {
		t.Fatalf("waited %s: %+v", time.Since(start), out.Seats)
	}
}

// A seat that can't be reached never hides the seats read before it: their messages are
// shown and acknowledged, and it is only counted.
func TestAnUnreachableSeatHidesNoOtherSeat(t *testing.T) {
	srvURL, owner := testServer(t)
	a := inboxApp(t)
	first := seatOn(t, srvURL, owner)
	postTo(t, srvURL, owner, first.Board, 2)
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + l.Addr().String()
	_ = l.Close()
	lost := agentCredential{Server: dead, Board: "zz-lost", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1Z", Token: "aba_lost"}
	creds := credentials{Agents: []agentCredential{first, lost}}
	seats := []delivery.AgentRef{
		{Server: srvURL, Board: first.Board, Name: first.Name, MemberID: first.MemberID},
		{Server: dead, Board: lost.Board, Name: lost.Name, MemberID: lost.MemberID},
	}
	out, err := a.inboxSeats(context.Background(), seats, creds, 0, true, 0)
	if err != nil {
		t.Fatalf("an unreachable seat failed the whole inbox: %v", err)
	}
	if len(out.Seats) != 1 || len(out.Seats[0].Messages) != 2 || out.Seats[0].AckedUpTo == nil || out.Unavailable != 1 {
		t.Fatalf("out: %+v", out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "zz-lost") {
		t.Errorf("the unreachable seat is named: %s", raw)
	}
}

// An acknowledgement that fails hides nothing: every seat read is shown, the failed one
// is counted as unacknowledged, and its messages come again.
func TestAFailedAcknowledgementHidesNoBlock(t *testing.T) {
	srvURL, owner := testServer(t)
	a := inboxApp(t)
	one, two := seatOn(t, srvURL, owner), seatOn(t, srvURL, owner)
	postTo(t, srvURL, owner, one.Board, 1)
	postTo(t, srvURL, owner, two.Board, 1)
	target, _ := neturl.Parse(srvURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ack") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"no","hint":"no"}}`))
			return
		}
		r.Host = target.Host // the server answers only its own host
		proxy.ServeHTTP(w, r)
	}))
	defer failing.Close()
	// One seat is read through a server whose acknowledgements fail.
	one.Server = failing.URL
	creds := credentials{Agents: []agentCredential{one, two}}
	seats := []delivery.AgentRef{
		{Server: one.Server, Board: one.Board, Name: one.Name, MemberID: one.MemberID},
		{Server: two.Server, Board: two.Board, Name: two.Name, MemberID: two.MemberID},
	}
	out, err := a.inboxSeats(context.Background(), seats, creds, 0, true, 0)
	if err != nil {
		t.Fatalf("a failed acknowledgement failed the inbox: %v", err)
	}
	failed, worked := 0, 1
	if out.Seats[0].Board != one.Board {
		failed, worked = 1, 0
	}
	if len(out.Seats) != 2 || out.Unacknowledged != 1 || out.Seats[failed].AckedUpTo != nil || len(out.Seats[failed].Messages) != 1 ||
		out.Seats[worked].AckedUpTo == nil || len(out.Seats[worked].Messages) != 1 || out.Bundle == nil || strings.Count(*out.Bundle, "<aboard-messages ") != 2 {
		t.Fatalf("out: %+v", out)
	}
	again, err := a.inboxSeats(context.Background(), seats, creds, 0, false, 0)
	if err != nil || len(again.Seats[failed].Messages) != 1 || len(again.Seats[worked].Messages) != 0 {
		t.Fatalf("again: %+v %v", again.Seats, err)
	}
}
