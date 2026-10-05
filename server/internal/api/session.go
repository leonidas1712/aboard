package api

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

// csrfHeader carries a browser session's CSRF token on every write it makes.
const csrfHeader = "X-Aboard-CSRF"

// sessionCookie is how this server names and marks a browser session's cookie for one
// request, and the origin a page it serves has.
type sessionCookie struct {
	name   string
	secure bool
	origin string
}

// cookieFor works out the session cookie for r. Over HTTPS, or on any host that isn't a
// loopback address (a server behind a proxy that ends HTTPS), the cookie is
// __Host-aboard_session and Secure, so browsers send it only over HTTPS and only to this
// exact host. A loopback address over plain HTTP, as the local server runs, can't keep a
// Secure cookie, so it gets aboard_session_<port>: browsers share cookies between the
// ports of one host, and the port keeps two local servers from sharing one.
func cookieFor(r *http.Request) sessionCookie {
	host := strings.ToLower(r.Host)
	if r.TLS == nil && loopback(host) {
		name := "aboard_session"
		if _, port, err := net.SplitHostPort(host); err == nil && port != "" {
			name += "_" + port
		}
		return sessionCookie{name: name, origin: "http://" + host}
	}
	return sessionCookie{name: "__Host-aboard_session", secure: true, origin: "https://" + host}
}

// loopback reports whether host (with or without a port) names this machine.
func loopback(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// cookie returns the Set-Cookie value that keeps token in the browser until expires, or,
// with an empty token, that clears the cookie.
func (c sessionCookie) cookie(token string, expires time.Time, now time.Time) string {
	k := http.Cookie{Name: c.name, Value: token, Path: "/", HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode}
	if token == "" {
		k.MaxAge = -1
	} else {
		k.MaxAge = int(expires.Sub(now).Seconds())
	}
	return k.String()
}

func originNotAllowed() *apierr.Error {
	return apierr.New(http.StatusForbidden, "origin_not_allowed",
		"This request didn't come from this server's own page.",
		"Use the board view this server serves, or send an access key as a bearer token from a script.")
}

// sameOrigin checks that r's Origin header is exactly the origin of the page this server
// serves to it. Browsers send Origin with every request that isn't a GET or HEAD, and
// another site can't forge it.
func sameOrigin(r *http.Request, c sessionCookie) error {
	if r.Header.Get("Origin") != c.origin {
		return originNotAllowed()
	}
	return nil
}

// checkCSRF refuses a write made with a session cookie that lacks the session's CSRF
// token or comes from another origin. Reads (GET, HEAD) never change anything, so they
// need neither.
func checkCSRF(r *http.Request, svc *board.Service, c sessionCookie, token string) error {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil
	}
	if err := sameOrigin(r, c); err != nil {
		return err
	}
	got := r.Header.Get(csrfHeader)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(svc.CSRFToken(token))) != 1 {
		return apierr.New(http.StatusForbidden, "csrf_token_invalid",
			"This write didn't carry this browser session's CSRF token.",
			"Reload the board view; it reads the token again.")
	}
	return nil
}

// sessionKey holds, for a request, its session cookie's name and marks, and the browser
// session's secret when the request came with one.
type sessionKey struct{}

type requestSession struct {
	cookie sessionCookie
	token  string // the browser session's secret, when the caller is a browser
}

func sessionOf(ctx context.Context) requestSession {
	s, _ := ctx.Value(sessionKey{}).(requestSession)
	return s
}

func sessionBody(s board.BrowserSession) BrowserSession {
	created, _ := time.Parse(time.RFC3339, s.CreatedAt)
	expires, _ := time.Parse(time.RFC3339, s.ExpiresAt)
	return BrowserSession{
		Id: s.ID, Key: BrowserSessionKey{Id: s.KeyID, Name: s.KeyName}, StartedWith: BrowserSessionStart(s.StartedWith),
		CreatedAt: created, ExpiresAt: expires,
	}
}

func (h *handlers) currentBody(s board.BrowserSession, person board.Human, token string) (CurrentBrowserSession, error) {
	b := sessionBody(s)
	p, err := convert[Person](personOf(person))
	if err != nil {
		return CurrentBrowserSession{}, err
	}
	return CurrentBrowserSession{
		Id: b.Id, Key: b.Key, StartedWith: b.StartedWith, CreatedAt: b.CreatedAt, ExpiresAt: b.ExpiresAt,
		Person: p, CsrfToken: h.svc.CSRFToken(token),
	}, nil
}

// StartBrowserSession signs a browser in and sets its session cookie. The body never
// holds the session's secret.
func (h *handlers) StartBrowserSession(ctx context.Context, req StartBrowserSessionRequestObject) (StartBrowserSessionResponseObject, error) {
	in := board.SessionStart{}
	if req.Body.Code != nil {
		in.Code = *req.Body.Code
	}
	if req.Body.Key != nil {
		in.Key = *req.Body.Key
	}
	if req.Body.Token != nil {
		in.Token = *req.Body.Token
	}
	started, err := h.svc.StartBrowserSession(ctx, in)
	if err != nil {
		return nil, err
	}
	body, err := h.currentBody(started.Session, started.Person, started.Token)
	if err != nil {
		return nil, err
	}
	set := sessionOf(ctx).cookie.cookie(started.Token, body.ExpiresAt, h.clk.Now())
	return StartBrowserSession201JSONResponse{Body: body, Headers: StartBrowserSession201ResponseHeaders{SetCookie: &set}}, nil
}

// GetBrowserSession returns the session the browser is signed in with, and its CSRF
// token.
func (h *handlers) GetBrowserSession(ctx context.Context, _ GetBrowserSessionRequestObject) (GetBrowserSessionResponseObject, error) {
	cur, err := h.svc.CurrentBrowserSession(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	body, err := h.currentBody(cur.Session, cur.Person, sessionOf(ctx).token)
	if err != nil {
		return nil, err
	}
	return GetBrowserSession200JSONResponse(body), nil
}

// SignOut ends the browser's own session and clears its cookie.
func (h *handlers) SignOut(ctx context.Context, _ SignOutRequestObject) (SignOutResponseObject, error) {
	s, err := h.svc.SignOut(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	clear := sessionOf(ctx).cookie.cookie("", time.Time{}, h.clk.Now())
	return SignOut200JSONResponse{Body: sessionBody(s), Headers: SignOut200ResponseHeaders{SetCookie: &clear}}, nil
}

func (h *handlers) ListBrowserSessions(ctx context.Context, req ListBrowserSessionsRequestObject) (ListBrowserSessionsResponseObject, error) {
	key := ""
	if req.Params.Key != nil {
		key = *req.Params.Key
	}
	list, err := h.svc.ListBrowserSessions(ctx, principal(ctx), key)
	if err != nil {
		return nil, err
	}
	p, err := convert[Person](personOf(list.Person))
	if err != nil {
		return nil, err
	}
	out := ListBrowserSessions200JSONResponse{Person: p, Sessions: []BrowserSession{}}
	for _, s := range list.Sessions {
		out.Sessions = append(out.Sessions, sessionBody(s))
	}
	return out, nil
}

func (h *handlers) EndBrowserSession(ctx context.Context, req EndBrowserSessionRequestObject) (EndBrowserSessionResponseObject, error) {
	s, err := h.svc.EndBrowserSession(ctx, principal(ctx), req.Session)
	if err != nil {
		return nil, err
	}
	return EndBrowserSession200JSONResponse(sessionBody(s)), nil
}
