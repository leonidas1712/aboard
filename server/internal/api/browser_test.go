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

	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

func init() {
	// GET /login answers a bad code with an HTML page; let the conformance check read it.
	openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.FileBodyDecoder)
}

// loginCode asks for a browser login code with token.
func (s *testServer) loginCode(token string) string {
	s.t.Helper()
	r, err := s.client(token).CreateLoginCodeWithResponse(context.Background(), nil)
	mustStatus(s.t, r, err, 201)
	return r.JSON201.Code
}

// loginResult is what a browser gets back from /login.
type loginResult struct {
	status   int
	location string
	cookie   *http.Cookie // aboard_session, if set
	body     string
}

// login opens /login with code the way a browser would, without following the
// redirect.
func (s *testServer) login(code, boardName string) loginResult {
	s.t.Helper()
	c := s.httpClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.url+"/login?code="+code+"&board="+boardName, http.NoBody)
	if err != nil {
		s.t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	r := loginResult{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(body)}
	for _, c := range resp.Cookies() {
		if c.Name == "aboard_session" {
			r.cookie = c
		}
	}
	return r
}

// browser returns an API client that sends only the browser login cookie.
func (s *testServer) browser(cookie *http.Cookie) *api.ClientWithResponses {
	s.t.Helper()
	c, err := api.NewClientWithResponses(s.url, api.WithHTTPClient(s.httpClient()),
		api.WithRequestEditorFn(func(_ context.Context, r *http.Request) error { r.AddCookie(cookie); return nil }))
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

func sessionCookie(t *testing.T, r loginResult) *http.Cookie {
	t.Helper()
	if r.cookie == nil {
		t.Fatalf("no aboard_session cookie: %d %s", r.status, r.body)
	}
	return r.cookie
}

func TestLoginCodeLogsABrowserInOnce(t *testing.T) {
	s := newTestServer(t)
	boardName, _, _ := s.pair("starter")
	code := s.loginCode(s.owner)

	first := s.login(code, boardName)
	if first.status != http.StatusSeeOther || first.location != "/?board="+boardName {
		t.Fatalf("first login: %d to %q", first.status, first.location)
	}
	c := sessionCookie(t, first)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 30*24*60*60 {
		t.Fatalf("cookie attributes: %+v", c)
	}

	again := s.login(code, boardName)
	if again.status != http.StatusNotFound || !strings.Contains(again.body, "aboard open") || again.cookie != nil {
		t.Fatalf("second use: %d %s", again.status, again.body)
	}
}

func TestLoginCodeExpiresAfterAMinute(t *testing.T) {
	s := newTestServer(t)
	code := s.loginCode(s.owner)
	s.clock.Advance(61 * time.Second)
	if r := s.login(code, ""); r.status != http.StatusNotFound {
		t.Fatalf("expired code: %d", r.status)
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

func TestBrowserLoginReadsButCannotWrite(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	mustStatus(t, say(s, writer, boardName, nil, "hello"), nil, 201)
	b := s.browser(sessionCookie(t, s.login(s.loginCode(s.owner), boardName)))

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

	post, err := b.PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "from the browser"})
	if code := errorCode(t, post, err, 403); code != "browser_read_only" {
		t.Fatalf("post with the cookie: %s", code)
	}
	created, err := b.CreateLoginCodeWithResponse(ctx, nil)
	if code := errorCode(t, created, err, 403); code != "browser_read_only" {
		t.Fatalf("login code with the cookie: %s", code)
	}

	s.clock.Advance(30*24*time.Hour + time.Second)
	ended, err := b.ListBoardsWithResponse(ctx)
	if code := errorCode(t, ended, err, 401); code != "unauthorized" {
		t.Fatalf("after 30 days: %s", code)
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
	get := func(host, path string) (int, string) {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	for _, host := range []string{"127.0.0.1:7400", "localhost:7400"} {
		if code, body := get(host, "/"); code != 200 || body != "the board list" {
			t.Fatalf("UI at %s: %d %q", host, code, body)
		}
		if code, _ := get(host, "/v1/info"); code != 200 {
			t.Fatalf("info at %s: %d", host, code)
		}
	}
	for _, host := range []string{"evil.example:7400", "127.0.0.1:7401", "evil.example"} {
		if code, body := get(host, "/v1/info"); code != http.StatusMisdirectedRequest || !strings.Contains(body, "host_not_allowed") {
			t.Fatalf("host %s: %d %s", host, code, body)
		}
	}
}
