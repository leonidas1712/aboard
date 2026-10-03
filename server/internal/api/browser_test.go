package api_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// loginCode asks for a browser login code with token.
func (s *testServer) loginCode(token string) string {
	s.t.Helper()
	r, err := s.client(token).CreateLoginCodeWithResponse(context.Background(), nil)
	mustStatus(s.t, r, err, 201)
	return r.JSON201.Code
}

// exchange trades a login code for a browser token, the way the web UI does: with no
// token of its own.
func (s *testServer) exchange(code string) (*api.CreateBrowserTokenResponse, error) {
	s.t.Helper()
	return s.client("").CreateBrowserTokenWithResponse(context.Background(), api.BrowserTokenRequest{Code: code})
}

// browserToken logs a browser in as token's human and returns its browser token.
func (s *testServer) browserToken(token string) string {
	s.t.Helper()
	r, err := s.exchange(s.loginCode(token))
	mustStatus(s.t, r, err, 201)
	return r.JSON201.Token
}

func TestLoginCodeGivesOneBrowserTokenOnce(t *testing.T) {
	s := newTestServer(t)
	code := s.loginCode(s.owner)

	first, err := s.exchange(code)
	mustStatus(t, first, err, 201)
	if !strings.HasPrefix(first.JSON201.Token, "abb_") {
		t.Fatalf("browser token %q doesn't start with abb_", first.JSON201.Token)
	}
	if want := s.clock.Now().Add(30 * 24 * time.Hour); !first.JSON201.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at %v, want %v", first.JSON201.ExpiresAt, want)
	}

	again, err := s.exchange(code)
	if c := errorCode(t, again, err, 404); c != "login_code_invalid" || !strings.Contains(again.JSON404.Error.Hint, "aboard open") {
		t.Fatalf("second use: %s", bodyOf(again))
	}
	wrong, err := s.exchange("abl_nothing")
	if c := errorCode(t, wrong, err, 404); c != "login_code_invalid" {
		t.Fatalf("wrong code: %s", c)
	}
}

func TestLoginCodeExpiresAfterAMinute(t *testing.T) {
	s := newTestServer(t)
	code := s.loginCode(s.owner)
	s.clock.Advance(61 * time.Second)
	r, err := s.exchange(code)
	if c := errorCode(t, r, err, 404); c != "login_code_invalid" {
		t.Fatalf("expired code: %s", c)
	}
}

func TestOnlyHumansGetLoginCodes(t *testing.T) {
	s := newTestServer(t)
	_, writer, _ := s.pair("starter")
	r, err := s.client(writer).CreateLoginCodeWithResponse(context.Background(), nil)
	if code := errorCode(t, r, err, 403); code != "human_token_required" {
		t.Fatalf("agent asking for a login code: %s", code)
	}
}

// A browser token acts as the person who logged it in, with that person's permissions:
// it reads and posts like their CLI, an admin's changes the board's policy and a
// member's is refused, and it can't make another browser login.
func TestBrowserTokenActsAsItsPerson(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	mustStatus(t, say(s, writer, boardName, nil, "hello"), nil, 201)
	token := s.browserToken(s.owner)
	b := s.client(token)

	boards, err := b.ListBoardsWithResponse(ctx)
	mustStatus(t, boards, err, 200)
	if len(boards.JSON200.Boards) != 1 || boards.JSON200.Boards[0].Name != boardName {
		t.Fatalf("boards: %s", bodyOf(boards))
	}
	msgs, err := b.ListMessagesWithResponse(ctx, boardName, nil)
	mustStatus(t, msgs, err, 200)
	if len(msgs.JSON200.Messages) != 1 || msgs.JSON200.Messages[0].Body != "hello" {
		t.Fatalf("messages: %s", bodyOf(msgs))
	}
	members, err := b.ListMembersWithResponse(ctx, boardName)
	mustStatus(t, members, err, 200)
	if first := s.openStream(token).next(); !strings.HasPrefix(first, "event: head\n") {
		t.Fatalf("stream with the browser token: %q", first)
	}

	post, err := b.PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "from the browser"})
	mustStatus(t, post, err, 201)
	if from := post.JSON201.From; from.Name != "alex" || from.Kind != api.MemberRefKindHuman {
		t.Fatalf("a post from the browser is from %s (%s), want alex (human)", from.Name, from.Kind)
	}
	preset := api.PolicyPreset("recommended")
	change := api.UpdateBoardRequest{Policy: &api.PolicyChange{Preset: &preset}}
	policy, err := b.UpdateBoardWithResponse(ctx, boardName, nil, change)
	mustStatus(t, policy, err, 200)

	code, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, code, err, 201)
	priya := s.addHuman("priya")
	j, err := s.client(priya).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(t, j, err, 201)
	refused, err := s.client(s.browserToken(priya)).UpdateBoardWithResponse(ctx, boardName, nil, change)
	if c := errorCode(t, refused, err, 403); c != "admin_required" {
		t.Fatalf("policy change from a member's browser: %s", c)
	}

	created, err := b.CreateLoginCodeWithResponse(ctx, nil)
	if c := errorCode(t, created, err, 403); c != "human_token_required" || !strings.Contains(created.JSON403.Error.Hint, "aboard open") {
		t.Fatalf("login code with the browser token: %s", bodyOf(created))
	}

	s.clock.Advance(30*24*time.Hour + time.Second)
	ended, err := b.ListBoardsWithResponse(ctx)
	if code := errorCode(t, ended, err, 401); code != "unauthorized" || !strings.Contains(ended.JSON401.Error.Hint, "aboard open") {
		t.Fatalf("after 30 days: %s", bodyOf(ended))
	}
	late, err := b.PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "too late"})
	if code := errorCode(t, late, err, 401); code != "unauthorized" {
		t.Fatalf("post after 30 days: %s", code)
	}
}

func TestBrowserTokensEndWithTheServer(t *testing.T) {
	s := newTestServer(t)
	token := s.browserToken(s.owner)

	// A new server process on the same database: browser tokens lived only in the old
	// one's memory.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := board.New(s.st, notify.NewInProcess(), s.clock, ids.New(rand.Reader), s.key, board.Config{ServerID: "srv_TEST", Mode: "local"}, log)
	h, err := api.NewHandler(api.Options{Service: svc, Responses: s.st, Clock: s.clock, Log: log, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(h)
	t.Cleanup(restarted.Close)
	s.url = restarted.URL

	r, err := s.client(token).ListBoardsWithResponse(context.Background())
	if code := errorCode(t, r, err, 401); code != "unauthorized" {
		t.Fatalf("browser token after a restart: %s", code)
	}
	if r, err := s.client(s.owner).ListBoardsWithResponse(context.Background()); err != nil || r.StatusCode() != 200 {
		t.Fatalf("owner token after a restart: %v %v", r.StatusCode(), err)
	}
}

func TestServerRefusesOtherHostsAndServesTheUI(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := board.New(st, notify.NewInProcess(), clk, ids.New(rand.Reader), []byte("key"), board.Config{ServerID: "srv_TEST", Mode: "local"}, log)
	h, err := api.NewHandler(api.Options{
		Service: svc, Responses: st, Clock: clk, Log: log, Version: "test",
		Hosts: api.LocalHosts("127.0.0.1:7400"), UI: fstest.MapFS{"index.html": {Data: []byte("the board list")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, host, path string) *httptest.ResponseRecorder {
		var body io.Reader = http.NoBody
		if method == http.MethodPost {
			body = strings.NewReader(`{"code":"abl_x"}`)
		}
		req := httptest.NewRequestWithContext(ctx, method, path, body)
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if c := w.Header().Values("Set-Cookie"); len(c) > 0 {
			t.Fatalf("%s %s set a cookie: %q", method, path, c)
		}
		return w
	}
	for _, host := range []string{"127.0.0.1:7400", "localhost:7400"} {
		if w := do(http.MethodGet, host, "/"); w.Code != 200 || w.Body.String() != "the board list" {
			t.Fatalf("UI at %s: %d %q", host, w.Code, w.Body)
		}
		if w := do(http.MethodGet, host, "/v1/info"); w.Code != 200 {
			t.Fatalf("info at %s: %d", host, w.Code)
		}
	}
	for _, host := range []string{"evil.example:7400", "127.0.0.1:7401", "evil.example"} {
		for _, req := range [][2]string{{http.MethodGet, "/v1/info"}, {http.MethodPost, "/v1/browser-tokens"}, {http.MethodGet, "/"}} {
			if w := do(req[0], host, req[1]); w.Code != http.StatusMisdirectedRequest || !strings.Contains(w.Body.String(), "host_not_allowed") {
				t.Fatalf("%s %s at host %s: %d %s", req[0], req[1], host, w.Code, w.Body)
			}
		}
	}
	if w := do(http.MethodGet, "127.0.0.1:7400", "/login?code=abl_x"); w.Code == http.StatusSeeOther {
		t.Fatalf("GET /login still logs in: %d", w.Code)
	}
}

func TestLoginCodesAreNeverSavedForRepeats(t *testing.T) {
	s := newTestServer(t)
	key := "open-1"
	var codes []string
	for range 2 {
		r, err := s.client(s.owner).CreateLoginCodeWithResponse(context.Background(), &api.CreateLoginCodeParams{IdempotencyKey: &key})
		mustStatus(t, r, err, 201)
		if r.HTTPResponse.Header.Get("Idempotent-Replayed") != "" {
			t.Fatal("a login code was replayed from a saved response")
		}
		codes = append(codes, r.JSON201.Code)
	}
	if codes[0] == codes[1] {
		t.Fatal("the same Idempotency-Key gave the same login code twice")
	}
}
