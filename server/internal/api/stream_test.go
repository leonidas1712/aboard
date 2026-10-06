package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// streamWait is how long a test waits for the next thing on a stream before failing.
const streamWait = 5 * time.Second

// eventStream reads an open GET /v1/stream one block at a time: an event or a comment,
// without the blank line that ends it.
type eventStream struct {
	t      *testing.T
	blocks chan string
	// unreads holds the `unread` events, kept apart from blocks so that tests of other
	// events read on as a client that ignores them would.
	unreads chan string
	done    chan struct{} // closed when the body has ended
	cancel  context.CancelFunc
}

// openStream opens the event stream as token, failing unless the server answers 200.
func (s *testServer) openStream(token string) *eventStream {
	s.t.Helper()
	return s.openStreamWith(func(r *http.Request) {
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	})
}

// openStreamWith opens the event stream with the credential sign puts on the request.
func (s *testServer) openStreamWith(sign func(*http.Request)) *eventStream {
	s.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/v1/stream", http.NoBody)
	if err != nil {
		cancel()
		s.t.Fatal(err)
	}
	sign(req)
	resp, err := s.httpClient().Do(req)
	if err != nil {
		cancel()
		s.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		s.t.Fatalf("stream status %d: %s", resp.StatusCode, body)
	}
	st := &eventStream{t: s.t, blocks: make(chan string, 256), unreads: make(chan string, 256), done: make(chan struct{}), cancel: cancel}
	go st.read(ctx, resp.Body)
	s.t.Cleanup(func() {
		cancel()
		<-st.done
	})
	return st
}

// read splits body into blocks until it ends or ctx is done, then closes it.
func (st *eventStream) read(ctx context.Context, body io.ReadCloser) {
	defer close(st.done)
	defer func() { _ = body.Close() }()
	sc := bufio.NewScanner(body)
	var block []string
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			block = append(block, line)
			continue
		}
		if len(block) == 0 {
			continue
		}
		out := st.blocks
		if block[0] == "event: unread" {
			out = st.unreads
		}
		select {
		case out <- strings.Join(block, "\n"):
		case <-ctx.Done():
			return
		}
		block = nil
	}
}

func (s *testServer) streamRequest(ctx context.Context, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/v1/stream", http.NoBody)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return s.httpClient().Do(req)
}

// next returns the next block on the stream, failing if none comes in time.
func (st *eventStream) next() string {
	st.t.Helper()
	select {
	case b := <-st.blocks:
		return b
	case <-st.done:
		st.t.Fatal("the stream ended")
	case <-time.After(streamWait):
		st.t.Fatal("nothing arrived on the stream")
	}
	return ""
}

type head struct {
	Board   string `json:"board"`
	BoardID string `json:"board_id"`
	Seq     int    `json:"seq"`
}

// head returns the next block, failing unless it is a head event.
func (st *eventStream) head() head {
	st.t.Helper()
	b := st.next()
	data, ok := strings.CutPrefix(b, "event: head\ndata: ")
	if !ok || strings.Contains(data, "\n") {
		st.t.Fatalf("not a head event: %q", b)
	}
	var h head
	if err := json.Unmarshal([]byte(data), &h); err != nil {
		st.t.Fatalf("head event data %q: %v", data, err)
	}
	return h
}

// keepalive fails unless the next block is the keepalive comment.
func (st *eventStream) keepalive() {
	st.t.Helper()
	if b := st.next(); b != ": keepalive" {
		st.t.Fatalf("got %q, want the keepalive comment", b)
	}
}

// ends fails unless the stream's body ends in time, reading past anything still on it.
func (st *eventStream) ends() {
	st.t.Helper()
	timeout := time.After(streamWait)
	for {
		select {
		case <-st.done:
			return
		case <-st.blocks:
		case <-timeout:
			st.t.Fatal("the stream is still open")
		}
	}
}

func (s *testServer) boardHead(token, name string) head {
	s.t.Helper()
	r, err := s.client(token).GetBoardWithResponse(context.Background(), name)
	mustStatus(s.t, r, err, 200)
	return head{Board: r.JSON200.Name, BoardID: r.JSON200.Id, Seq: r.JSON200.HeadSeq}
}

func TestStreamStartsWithTheHeadOfEveryBoardTheHumanIsOn(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	paired, _, _ := s.pair("starter")
	name := "zeta"
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &name})
	mustStatus(t, b, err, 201)
	sam := s.addHuman("sam")
	other := "sams-board"
	b, err = s.client(sam).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &other})
	mustStatus(t, b, err, 201)

	st := s.openStream(s.owner)
	for _, want := range []head{s.boardHead(s.owner, paired), s.boardHead(s.owner, "zeta")} {
		if got := st.head(); got != want {
			t.Fatalf("head %+v, want %+v", got, want)
		}
	}
	// Nothing about sam's board comes before the next keepalive.
	s.clock.Advance(25 * time.Second)
	st.keepalive()
}

func TestStreamSendsAHeadWhenAMessageIsPosted(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	boardName, writer, _ := s.pair("starter")
	st := s.openStream(s.owner)
	st.head()

	r := say(s, writer, boardName, []string{"@reviewer"}, "draft is ready")
	mustStatus(t, r, nil, 201)
	got := st.head()
	if got.Board != boardName || got.Seq != r.JSON201.Seq {
		t.Fatalf("head %+v, want board %s at seq %d", got, boardName, r.JSON201.Seq)
	}
	if strings.Contains(got.Board+got.BoardID, "draft") {
		t.Fatal("the event carries message content")
	}
}

func TestStreamPicksUpBoardsTheHumanJoinsAfterConnecting(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	sam := s.addHuman("sam")
	st := s.openStream(sam) // sam is on no board yet

	added, err := s.client(s.owner).AddPersonWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: "sam"})
	mustStatus(t, added, err, 201)
	if got, want := st.head(), s.boardHead(sam, boardName); got != want {
		t.Fatalf("head after joining %+v, want %+v", got, want)
	}

	name := "sams-board"
	b, err := s.client(sam).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &name})
	mustStatus(t, b, err, 201)
	if got, want := st.head(), s.boardHead(sam, name); got != want {
		t.Fatalf("head after creating a board %+v, want %+v", got, want)
	}
}

func TestStreamSendsAKeepaliveEvery25Seconds(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.pair("starter")
	st := s.openStream(s.owner)
	st.head()
	for range 2 {
		s.clock.Advance(25 * time.Second)
		st.keepalive()
	}
}

func TestStreamNeedsAHumanToken(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, writer, _ := s.pair("starter")
	for _, tc := range []struct {
		name, token, code string
		status            int
	}{
		{"agent", writer, "human_token_required", 403},
		{"no token", "", "unauthorized", 401},
		{"unknown token", "abh_" + strings.Repeat("x", 43), "unauthorized", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := s.streamRequest(context.Background(), tc.token)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			r := &rawResponse{Status: resp.StatusCode, Body: body}
			if code := errorCode(t, r, err, tc.status); code != tc.code {
				t.Fatalf("code %s, want %s", code, tc.code)
			}
		})
	}
}

// rawResponse is a response read without the generated client.
type rawResponse struct {
	Status int
	Body   []byte
}

func (r *rawResponse) StatusCode() int { return r.Status }

func TestStreamEndsWhenTheClientDisconnects(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.pair("starter")
	st := s.openStream(s.owner)
	st.head()
	st.cancel()
	st.ends()

	// Close waits for every request in flight, so it only returns once the stream's
	// handler has.
	closed := make(chan struct{})
	go func() {
		s.srv.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(streamWait):
		t.Fatal("the stream's handler is still running after the client left")
	}
}

func TestStreamEndsWhenTheServerShutsDown(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.pair("starter")
	st := s.openStream(s.owner)
	st.head()
	s.shutdown()
	st.ends()
}
