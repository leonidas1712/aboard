package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// browser is a browser signed in with a session cookie, as the board view is: it sends
// the cookie, its page's Origin and the session's CSRF token, and never sees the secret
// from a response body.
type browser struct {
	s      *testServer
	cookie *http.Cookie
	csrf   string
	id     string
}

// call is a finished request: its status, decoded body and Set-Cookie header.
type call struct {
	status int
	body   map[string]any
	raw    string
	cookie *http.Cookie
}

func (c call) code() string {
	e, _ := c.body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// send makes a request to the test server through the conformance checker. edit sets
// what the caller sends besides the body.
func (s *testServer) send(method, path string, body any, edit func(*http.Request)) call {
	s.t.Helper()
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.url+path, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if edit != nil {
		edit(req)
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	out := call{status: resp.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	if cs := resp.Cookies(); len(cs) > 0 {
		out.cookie = cs[0]
	}
	return out
}

// fromPage marks a request as the server's own page sends it.
func (s *testServer) fromPage(r *http.Request) { r.Header.Set("Origin", s.url) }

// signIn posts a sign-in from the server's own page.
func (s *testServer) signIn(body map[string]string) call {
	s.t.Helper()
	return s.send(http.MethodPost, "/v1/browser-sessions", body, s.fromPage)
}

// cookieBrowser signs a browser in with a login code that key asks for.
func (s *testServer) cookieBrowser(key string) *browser {
	s.t.Helper()
	return s.browserFrom(s.signIn(map[string]string{"code": s.loginCode(key)}))
}

func (s *testServer) browserFrom(c call) *browser {
	s.t.Helper()
	if c.status != http.StatusCreated || c.cookie == nil {
		s.t.Fatalf("signing in: %d %s", c.status, c.raw)
	}
	csrf, _ := c.body["csrf_token"].(string)
	id, _ := c.body["id"].(string)
	return &browser{s: s, cookie: c.cookie, csrf: csrf, id: id}
}

// length is how many items a decoded JSON array has, 0 when it isn't one.
func length(v any) int {
	a, _ := v.([]any)
	return len(a)
}

// jsonAt reads a value from a decoded JSON object by its path of keys, nil when absent.
func jsonAt(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// do makes a request as the page does: the cookie, the page's Origin and, for a write,
// the CSRF token.
func (b *browser) do(method, path string, body any) call {
	b.s.t.Helper()
	return b.s.send(method, path, body, func(r *http.Request) {
		r.AddCookie(b.cookie)
		b.s.fromPage(r)
		if method != http.MethodGet {
			r.Header.Set("X-Aboard-CSRF", b.csrf)
		}
	})
}

// works checks whether the browser's cookie still reads /v1/me.
func (b *browser) works(want bool) {
	b.s.t.Helper()
	got := b.do(http.MethodGet, "/v1/me", nil)
	if want && got.status != http.StatusOK || !want && got.status != http.StatusUnauthorized {
		b.s.t.Fatalf("GET /v1/me with the session cookie: %d %s, want it to work: %v", got.status, got.raw, want)
	}
}

func TestSessionCookieIsHttpOnlyLaxAndHostOnly(t *testing.T) {
	s := newTestServer(t)
	c := s.signIn(map[string]string{"code": s.loginCode(s.owner)})
	if c.status != http.StatusCreated {
		t.Fatalf("signing in: %d %s", c.status, c.raw)
	}
	if strings.Contains(c.raw, "abb_") {
		t.Fatalf("the response body holds the session's secret: %s", c.raw)
	}
	k := c.cookie
	_, port, _ := strings.Cut(strings.TrimPrefix(s.url, "http://"), ":")
	if k.Name != "aboard_session_"+port || !strings.HasPrefix(k.Value, "abb_") || !k.HttpOnly || k.SameSite != http.SameSiteLaxMode ||
		k.Path != "/" || k.Domain != "" || k.Secure {
		t.Fatalf("the loopback cookie: %+v", k)
	}
	if k.MaxAge != int((30 * 24 * time.Hour).Seconds()) {
		t.Fatalf("the cookie lasts %d seconds, want 30 days", k.MaxAge)
	}
	if c.body["started_with"] != "login_code" || jsonAt(c.body, "key", "name") != "laptop" || jsonAt(c.body, "person", "handle") != "alex" {
		t.Fatalf("the session: %s", c.raw)
	}

	// Over HTTPS, and on any host that isn't this machine, the cookie is Secure and
	// __Host-, which browsers keep to this exact host.
	h := s.srv.Config.Handler
	for _, target := range []string{"https://team.example.com/v1/browser-sessions", "http://team.example.com/v1/browser-sessions"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target,
			strings.NewReader(`{"code":"`+s.loginCode(s.owner)+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://team.example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		set := w.Header().Get("Set-Cookie")
		if w.Code != http.StatusCreated || !strings.HasPrefix(set, "__Host-aboard_session=abb_") ||
			!strings.Contains(set, "; Secure") || !strings.Contains(set, "; HttpOnly") || !strings.Contains(set, "; SameSite=Lax") ||
			!strings.Contains(set, "; Path=/") || strings.Contains(set, "Domain") {
			t.Fatalf("%s: %d, Set-Cookie %q, %s", target, w.Code, set, w.Body)
		}
	}
}

func TestCookieWritesNeedTheOriginAndTheCSRFToken(t *testing.T) {
	s := newTestServer(t)
	boardName := s.newBoard()
	b := s.cookieBrowser(s.owner)
	other := s.cookieBrowser(s.owner)
	post := "/v1/boards/" + boardName + "/messages"
	msg := map[string]any{"to": []string{"all"}, "body": "hello"}

	// Reads need neither.
	if got := s.send(http.MethodGet, "/v1/me", nil, func(r *http.Request) { r.AddCookie(b.cookie) }); got.status != http.StatusOK {
		t.Fatalf("a read with the cookie alone: %d %s", got.status, got.raw)
	}
	for _, tt := range []struct {
		name, origin, csrf, want string
	}{
		{"no Origin", "", b.csrf, "origin_not_allowed"},
		{"another site", "https://evil.example", b.csrf, "origin_not_allowed"},
		{"another port", "http://127.0.0.1:1", b.csrf, "origin_not_allowed"},
		{"no token", s.url, "", "csrf_token_invalid"},
		{"another session's token", s.url, other.csrf, "csrf_token_invalid"},
	} {
		got := s.send(http.MethodPost, post, msg, func(r *http.Request) {
			r.AddCookie(b.cookie)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.csrf != "" {
				r.Header.Set("X-Aboard-CSRF", tt.csrf)
			}
		})
		if got.status != http.StatusForbidden || got.code() != tt.want {
			t.Errorf("%s: %d %s, want 403 %s", tt.name, got.status, got.raw, tt.want)
		}
	}
	if got := b.do(http.MethodGet, post, nil); length(got.body["messages"]) != 0 {
		t.Fatalf("a refused write posted: %s", got.raw)
	}
	if got := b.do(http.MethodPost, post, msg); got.status != http.StatusCreated {
		t.Fatalf("a write from the page: %d %s", got.status, got.raw)
	}

	// A bearer token is the whole credential: no Origin or CSRF token, and a cookie sent
	// alongside is ignored.
	got := s.send(http.MethodPost, post, msg, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.owner)
		r.AddCookie(&http.Cookie{Name: b.cookie.Name, Value: "abb_wrong"}) //nolint:gosec // a cookie a client sends
	})
	if got.status != http.StatusCreated {
		t.Fatalf("a write with a bearer key: %d %s", got.status, got.raw)
	}
}

func TestSigningInNeedsTheServersOwnPage(t *testing.T) {
	s := newTestServer(t)
	code := s.loginCode(s.owner)
	for _, origin := range []string{"", "https://evil.example", "null"} {
		got := s.send(http.MethodPost, "/v1/browser-sessions", map[string]string{"code": code}, func(r *http.Request) {
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
		})
		if got.status != http.StatusForbidden || got.code() != "origin_not_allowed" || got.cookie != nil {
			t.Fatalf("signing in from origin %q: %d %s", origin, got.status, got.raw)
		}
	}
	// A refused sign-in doesn't use the code up.
	s.cookieBrowser(s.owner).works(true)
	s.browserFrom(s.signIn(map[string]string{"code": code})).works(true)

	for _, body := range []map[string]string{{}, {"code": code, "key": s.owner}} {
		if got := s.signIn(body); got.status != http.StatusBadRequest || got.code() != "invalid_request" {
			t.Fatalf("signing in with %v: %d %s", body, got.status, got.raw)
		}
	}
}

func TestACookieSessionCantManageKeysOrSessions(t *testing.T) {
	s := newTestServer(t)
	b := s.cookieBrowser(s.owner)
	keyID := s.keys(s.owner, "").JSON200.CurrentKeyId
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/keys", nil},
		{http.MethodPost, "/v1/keys", map[string]any{"name": "phone"}},
		{http.MethodDelete, "/v1/keys/" + keyID, nil},
		{http.MethodPost, "/v1/login-codes", nil},
		{http.MethodGet, "/v1/browser-sessions", nil},
		{http.MethodDelete, "/v1/browser-sessions/" + b.id, nil},
		{http.MethodDelete, "/v1/browser-tokens", nil},
		{http.MethodPost, "/v1/invites", map[string]any{}},
		{http.MethodPost, "/v1/machine-requests/approve", map[string]any{"code": "7Q4-K2M"}},
		{http.MethodPost, "/v1/machine-requests/refuse", map[string]any{"code": "7Q4-K2M"}},
	} {
		if got := b.do(req.method, req.path, req.body); got.status != http.StatusForbidden || got.code() != "human_token_required" {
			t.Errorf("%s %s with the cookie: %d %s", req.method, req.path, got.status, got.raw)
		}
	}
	b.works(true)
	s.works(s.owner, true)
}

func TestAPastedKeySignsInAndIsNeverKept(t *testing.T) {
	var logs bytes.Buffer
	s := newTestServer(t, func(o *api.Options) { o.Log = slog.New(slog.NewTextHandler(&logs, nil)) })
	id, key := s.newKey(s.owner, "phone")
	c := s.signIn(map[string]string{"key": key})
	b := s.browserFrom(c)
	if c.body["started_with"] != "access_key" || jsonAt(c.body, "key", "id") != id || strings.Contains(c.raw, key) {
		t.Fatalf("the session from a pasted key: %s", c.raw)
	}
	b.works(true)
	if n := len(s.keys(s.owner, "").JSON200.Keys); n != 2 {
		t.Fatalf("signing in made a key: %d keys", n)
	}

	for _, bad := range []string{"abh_wrong", "not-a-key", s.browserToken(s.owner)} {
		if got := s.signIn(map[string]string{"key": bad}); got.status != http.StatusUnauthorized || got.code() != "access_key_invalid" || got.cookie != nil {
			t.Fatalf("signing in with %q: %d %s", bad[:4], got.status, got.raw)
		}
	}
	s.revokeKey(s.owner, id)
	if got := s.signIn(map[string]string{"key": key}); got.code() != "access_key_invalid" {
		t.Fatalf("signing in with a revoked key: %d %s", got.status, got.raw)
	}
	b.works(false)

	// Neither the key nor the session's secret is in the database or the log.
	dir := filepath.Dir(s.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // the test's own folder
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{key, strings.TrimPrefix(key, "abh_"), strings.TrimPrefix(b.cookie.Value, "abb_")} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s holds a secret in plain text", e.Name())
			}
		}
	}
	if strings.Contains(logs.String(), strings.TrimPrefix(key, "abh_")) {
		t.Errorf("the log holds the pasted key:\n%s", logs.String())
	}
}

func TestASessionEndsNoLaterThanItsKey(t *testing.T) {
	s := newTestServer(t)
	r := s.createKey(s.owner, "short", 2*24*3600)
	mustStatus(t, r, nil, 201)
	c := s.signIn(map[string]string{"key": r.JSON201.Token})
	b := s.browserFrom(c)
	if got, err := time.Parse(time.RFC3339, fmt.Sprint(c.body["expires_at"])); err != nil || !got.Equal(*r.JSON201.ExpiresAt) {
		t.Fatalf("the session ends at %v, want its key's expiry %v", c.body["expires_at"], r.JSON201.ExpiresAt)
	}
	if b.cookie.MaxAge != 2*24*3600 {
		t.Fatalf("the cookie lasts %d seconds, want 2 days", b.cookie.MaxAge)
	}
	s.clock.Advance(2 * 24 * time.Hour)
	b.works(false)
}

func TestRevokingTheKeyEndsTheCookieSessionAndItsStream(t *testing.T) {
	s := newTestServer(t)
	s.newBoard()
	id, key := s.newKey(s.owner, "phone")
	b := s.browserFrom(s.signIn(map[string]string{"key": key}))
	st := s.openStreamWith(func(r *http.Request) { r.AddCookie(b.cookie) })
	st.head()
	s.revokeKey(s.owner, id)
	st.ends()
	b.works(false)
}

func TestSignInIsRateLimitedPerAddressAndAcrossTheServer(t *testing.T) {
	s := newTestServer(t, func(o *api.Options) { o.SignInFailures = api.Limits{PerAddr: 3, Server: 5} })
	h := s.srv.Config.Handler
	try := func(addr string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, s.url+"/v1/browser-sessions", strings.NewReader(`{"key":"abh_guess"}`))
		req.RemoteAddr = addr + ":5000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.url)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Fatal("a refused sign-in has no Retry-After")
		}
		return w.Code
	}
	for range 3 {
		if got := try("192.0.2.1"); got != http.StatusUnauthorized {
			t.Fatalf("an attempt within the limit: %d", got)
		}
	}
	if got := try("192.0.2.1"); got != http.StatusTooManyRequests {
		t.Fatalf("the fourth attempt from one address: %d", got)
	}
	// The server-wide limit counts every failure, from any address; a refused attempt
	// isn't one.
	for _, addr := range []string{"192.0.2.2", "192.0.2.3"} {
		if got := try(addr); got != http.StatusUnauthorized {
			t.Fatalf("failures four and five across the server: %d", got)
		}
	}
	if got := try("192.0.2.4"); got != http.StatusTooManyRequests {
		t.Fatalf("an attempt after five failures across the server: %d", got)
	}
	s.clock.Advance(time.Minute)
	if got := try("192.0.2.2"); got != http.StatusUnauthorized {
		t.Fatalf("an attempt a minute later: %d", got)
	}
}

func TestAStoredBrowserTokenMovesIntoTheCookie(t *testing.T) {
	s := newTestServer(t)
	token := s.browserToken(s.owner)
	b := s.browserFrom(s.signIn(map[string]string{"token": token}))
	if b.cookie.Value != token {
		t.Fatal("the cookie holds a different session from the stored token's")
	}
	b.works(true)
	list := s.send(http.MethodGet, "/v1/browser-sessions", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.owner) })
	if n := length(list.body["sessions"]); n != 1 {
		t.Fatalf("moving the token into the cookie started another session: %s", list.raw)
	}
	if got := s.signIn(map[string]string{"token": "abb_ended"}); got.status != http.StatusUnauthorized || got.code() != "unauthorized" {
		t.Fatalf("an ended token: %d %s", got.status, got.raw)
	}
}

func TestSigningOutEndsOnlyThatSession(t *testing.T) {
	s := newTestServer(t)
	s.newBoard()
	b, other := s.cookieBrowser(s.owner), s.cookieBrowser(s.owner)
	st := s.openStreamWith(func(r *http.Request) { r.AddCookie(b.cookie) })
	st.head()
	if got := b.do(http.MethodGet, "/v1/me/browser-session", nil); got.status != http.StatusOK || got.body["csrf_token"] != b.csrf {
		t.Fatalf("the current session: %d %s", got.status, got.raw)
	}
	// A forged sign-out is refused like any forged write.
	forged := s.send(http.MethodDelete, "/v1/me/browser-session", nil, func(r *http.Request) {
		r.AddCookie(b.cookie)
		r.Header.Set("Origin", "https://evil.example")
	})
	if forged.status != http.StatusForbidden {
		t.Fatalf("a sign-out from another site: %d %s", forged.status, forged.raw)
	}
	got := b.do(http.MethodDelete, "/v1/me/browser-session", nil)
	if got.status != http.StatusOK || got.body["id"] != b.id || got.cookie == nil || got.cookie.MaxAge >= 0 || got.cookie.Name != b.cookie.Name {
		t.Fatalf("signing out: %d %s, cookie %+v", got.status, got.raw, got.cookie)
	}
	st.ends()
	b.works(false)
	other.works(true)
	s.works(s.owner, true)
	if got := s.send(http.MethodGet, "/v1/me/browser-session", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.owner) }); got.code() != "browser_session_required" {
		t.Fatalf("a key asking for its browser session: %d %s", got.status, got.raw)
	}
}

func TestAPersonListsAndEndsTheirSessionsOneAtATime(t *testing.T) {
	s := newTestServer(t)
	phoneID, phone := s.newKey(s.owner, "phone")
	laptop := s.cookieBrowser(s.owner)
	s.clock.Advance(time.Minute)
	mobile := s.browserFrom(s.signIn(map[string]string{"key": phone}))
	maya := s.addHuman("maya")
	mayas := s.cookieBrowser(maya)
	asKey := func(token string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	}

	all := s.send(http.MethodGet, "/v1/browser-sessions", nil, asKey(s.owner))
	sessions, _ := all.body["sessions"].([]any)
	if all.status != http.StatusOK || len(sessions) != 2 || jsonAt(sessions[0], "id") != mobile.id ||
		jsonAt(sessions[0], "key", "name") != "phone" {
		t.Fatalf("alex's sessions, newest first: %d %s", all.status, all.raw)
	}
	one := s.send(http.MethodGet, "/v1/browser-sessions?key="+phoneID, nil, asKey(s.owner))
	if n := length(one.body["sessions"]); n != 1 {
		t.Fatalf("the phone key's sessions: %s", one.raw)
	}
	if got := s.send(http.MethodGet, "/v1/browser-sessions?key=key_00000000000000000000000000", nil, asKey(s.owner)); got.code() != "key_not_found" {
		t.Fatalf("another key's sessions: %d %s", got.status, got.raw)
	}

	if got := s.send(http.MethodDelete, "/v1/browser-sessions/"+mayas.id, nil, asKey(s.owner)); got.code() != "browser_session_not_found" {
		t.Fatalf("ending someone else's session: %d %s", got.status, got.raw)
	}
	mayas.works(true)
	ended := s.send(http.MethodDelete, "/v1/browser-sessions/"+mobile.id, nil, asKey(s.owner))
	if ended.status != http.StatusOK || ended.body["id"] != mobile.id {
		t.Fatalf("ending one session: %d %s", ended.status, ended.raw)
	}
	mobile.works(false)
	laptop.works(true)
	if got := s.send(http.MethodDelete, "/v1/browser-sessions/"+mobile.id, nil, asKey(s.owner)); got.code() != "browser_session_not_found" {
		t.Fatalf("ending it again: %d %s", got.status, got.raw)
	}
	s.works(phone, true)
}

// No GET changes anything: no operation in the API reads a body on GET, the UI's files
// answer only GET and HEAD, and reading the session or the sign-in route leaves the
// sessions and codes as they were.
func TestNoGetChangesState(t *testing.T) {
	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths.Map() {
		if item.Get != nil && item.Get.RequestBody != nil {
			t.Errorf("GET %s takes a body", path)
		}
	}
	s := newTestServer(t)
	b := s.cookieBrowser(s.owner)
	code := s.loginCode(s.owner)
	for range 2 {
		b.do(http.MethodGet, "/v1/me/browser-session", nil)
		s.send(http.MethodGet, "/v1/browser-sessions?code="+code, nil, func(r *http.Request) { r.AddCookie(b.cookie) })
	}
	list := s.send(http.MethodGet, "/v1/browser-sessions", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.owner) })
	if n := length(list.body["sessions"]); n != 1 {
		t.Fatalf("GETs changed the sessions: %s", list.raw)
	}
	s.browserFrom(s.signIn(map[string]string{"code": code}))
}

// The server sends no CORS headers, so no other site can read a response, even one a
// signed-in browser's cookie would allow.
func TestNoOtherSiteCanReadResponses(t *testing.T) {
	s := newTestServer(t)
	b := s.cookieBrowser(s.owner)
	h := s.srv.Config.Handler
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		req := httptest.NewRequestWithContext(context.Background(), method, s.url+"/v1/me/browser-session", http.NoBody)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "DELETE")
		req.AddCookie(b.cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		for k := range w.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s from another site got %s: %q", method, k, w.Header().Get(k))
			}
		}
	}
}

func TestPagesAndResponsesCarryAStrictPolicy(t *testing.T) {
	script := `self.__next_f.push([1,"x"])`
	page := `<!doctype html><script src="/_next/a.js"></script><script>` + script + `</script><p>the board list</p>`
	s := newTestServer(t, func(o *api.Options) { o.UI = fstest.MapFS{"index.html": {Data: []byte(page)}} })
	sum := sha256.Sum256([]byte(script))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	resp, err := http.Get(s.url + "/") //nolint:noctx // a test against its own server
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	scripts := ""
	for _, d := range strings.Split(csp, ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
			scripts = d
		}
	}
	if scripts != "script-src 'self' "+hash || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") ||
		!strings.Contains(csp, "base-uri 'none'") {
		t.Fatalf("the page's policy: %q", csp)
	}
	info, err := http.Get(s.url + "/v1/info") //nolint:noctx // a test against its own server
	if err != nil {
		t.Fatal(err)
	}
	_ = info.Body.Close()
	for _, r := range []*http.Response{resp, info} {
		if r.Header.Get("Referrer-Policy") != "no-referrer" || r.Header.Get("X-Content-Type-Options") != "nosniff" ||
			r.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing a security header: %v", r.Request.URL.Path, r.Header)
		}
	}
	if got := info.Header.Get("Content-Security-Policy"); !strings.HasPrefix(got, "default-src 'none'") {
		t.Errorf("an API response's policy: %q", got)
	}
}

// Away from this computer the server's own origin is https, so a page served over plain
// HTTP there can't sign in: its Origin doesn't match. The key it sent is refused.
func TestAPlainHTTPPageAwayFromThisComputerCantSignIn(t *testing.T) {
	s := newTestServer(t)
	_, key := s.newKey(s.owner, "phone")
	for _, path := range []string{"/v1/browser-sessions", "/v1/login-codes/preview"} {
		body := `{"key":"` + key + `"}`
		if path == "/v1/login-codes/preview" {
			body = `{"code":"` + s.loginCode(s.owner) + `"}`
		}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://team.example.com"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://team.example.com")
		w := httptest.NewRecorder()
		s.srv.Config.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "origin_not_allowed") || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("%s from http://team.example.com: %d %s", path, w.Code, w.Body)
		}
	}
}

// signedInAs checks whose session the browser has.
func (b *browser) signedInAs(t *testing.T, handle string) {
	t.Helper()
	got := b.do(http.MethodGet, "/v1/me/browser-session", nil)
	if got.status != http.StatusOK || jsonAt(got.body, "person", "handle") != handle {
		t.Fatalf("the browser's session: %d %s, want %s", got.status, got.raw, handle)
	}
}

func (s *testServer) preview(code string, edit func(*http.Request)) call {
	s.t.Helper()
	return s.send(http.MethodPost, "/v1/login-codes/preview", map[string]string{"code": code}, edit)
}

// A page asks who a code would sign it in as before it signs in: the answer names the
// person and key, and leaves the code working.
func TestPreviewingALoginCodeNamesItsPersonAndKeepsIt(t *testing.T) {
	s := newTestServer(t)
	maya := s.addHuman("maya")
	code := s.loginCode(maya)
	got := s.preview(code, s.fromPage)
	if got.status != http.StatusOK || jsonAt(got.body, "person", "handle") != "maya" || jsonAt(got.body, "key", "name") != "laptop" {
		t.Fatalf("previewing maya's code: %d %s", got.status, got.raw)
	}
	if got.cookie != nil || strings.Contains(got.raw, "abb_") {
		t.Fatalf("previewing a code started a session: %s", got.raw)
	}
	// The code still signs in, once.
	s.browserFrom(s.signIn(map[string]string{"code": code})).signedInAs(t, "maya")
	if again := s.preview(code, s.fromPage); again.code() != "login_code_invalid" {
		t.Fatalf("previewing a used code: %d %s", again.status, again.raw)
	}
	if forged := s.preview(s.loginCode(maya), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); forged.code() != "origin_not_allowed" {
		t.Fatalf("previewing from another site: %d %s", forged.status, forged.raw)
	}
}

// Attempts that work don't reach the failure limit, but a higher limit on every attempt
// still bounds them, previews and sign-ins together.
func TestEveryAttemptMeetsTheHigherLimit(t *testing.T) {
	s := newTestServer(t, func(o *api.Options) {
		o.SignInFailures = api.Limits{PerAddr: 1, Server: 1}
		o.SignInAttempts = api.Limits{PerAddr: 3, Server: 100}
	})
	code := s.loginCode(s.owner)
	for range 2 {
		if got := s.preview(code, s.fromPage); got.status != http.StatusOK {
			t.Fatalf("a preview within the limits: %d %s", got.status, got.raw)
		}
	}
	s.browserFrom(s.signIn(map[string]string{"code": code}))
	if got := s.preview(s.loginCode(s.owner), s.fromPage); got.status != http.StatusTooManyRequests {
		t.Fatalf("a fourth attempt in the minute: %d %s", got.status, got.raw)
	}
	s.clock.Advance(time.Minute)
	s.cookieBrowser(s.owner).works(true)
}

// Guesses are what the limit is for: a wrong code, previewed or used, counts; a preview or
// sign-in that works doesn't.
func TestFailedPreviewsAndSignInsShareTheLimit(t *testing.T) {
	s := newTestServer(t, func(o *api.Options) { o.SignInFailures = api.Limits{PerAddr: 2, Server: 100} })
	code := s.loginCode(s.owner)
	for range 5 {
		if got := s.preview(code, s.fromPage); got.status != http.StatusOK {
			t.Fatalf("a preview that works: %d %s", got.status, got.raw)
		}
	}
	s.browserFrom(s.signIn(map[string]string{"code": code})).works(true)
	if got := s.preview("abl_guess", s.fromPage); got.code() != "login_code_invalid" {
		t.Fatalf("a wrong code: %d %s", got.status, got.raw)
	}
	if got := s.signIn(map[string]string{"code": "abl_guess"}); got.code() != "login_code_invalid" {
		t.Fatalf("a second wrong code: %d %s", got.status, got.raw)
	}
	fresh := s.loginCode(s.owner)
	if got := s.preview(fresh, s.fromPage); got.status != http.StatusTooManyRequests {
		t.Fatalf("a preview after two guesses: %d %s", got.status, got.raw)
	}
	s.clock.Advance(time.Minute)
	s.cookieBrowser(s.owner).works(true)
}

// A sign-in that would switch a browser from one person's session to another's needs
// confirm_switch, which a page sends only after its person clicked; a refused code stays
// usable. Signing in again as the same person needs nothing.
func TestSwitchingABrowserToAnotherPersonNeedsConfirmation(t *testing.T) {
	s := newTestServer(t)
	maya := s.addHuman("maya")
	alex := s.cookieBrowser(s.owner)
	withCookie := func(body map[string]any) call {
		return s.send(http.MethodPost, "/v1/browser-sessions", body, func(r *http.Request) {
			r.AddCookie(alex.cookie)
			s.fromPage(r)
		})
	}
	code := s.loginCode(maya)
	_, mayaPhone := s.newKey(maya, "phone")
	for _, body := range []map[string]any{{"code": code}, {"key": mayaPhone}, {"token": s.browserToken(maya)}} {
		if got := withCookie(body); got.status != http.StatusConflict || got.code() != "browser_session_switch_unconfirmed" || got.cookie != nil {
			t.Fatalf("switching to maya with %v: %d %s", body, got.status, got.raw)
		}
	}
	alex.signedInAs(t, "alex")
	if got := withCookie(map[string]any{"code": s.loginCode(s.owner)}); got.status != http.StatusCreated {
		t.Fatalf("signing in again as alex: %d %s", got.status, got.raw)
	}
	got := withCookie(map[string]any{"code": code, "confirm_switch": true})
	if got.status != http.StatusCreated || jsonAt(got.body, "person", "handle") != "maya" {
		t.Fatalf("a confirmed switch with the refused code: %d %s", got.status, got.raw)
	}
}
