package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

// sessionCookie is the cookie that holds a browser login.
const sessionCookie = "aboard_session"

func (h *handlers) CreateLoginCode(ctx context.Context, _ CreateLoginCodeRequestObject) (CreateLoginCodeResponseObject, error) {
	c, err := h.svc.CreateLoginCode(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return convert[CreateLoginCode201JSONResponse](map[string]string{"code": c.Code, "expires_at": c.ExpiresAt})
}

// loginFailedPage is what a browser shows when its login link doesn't work.
const loginFailedPage = `<!doctype html><meta charset="utf-8"><title>Aboard</title>` +
	`<p>This login link doesn't work: it is wrong, expired or already used. Run <code>aboard open</code> in a terminal to get a new one.</p>`

// Login exchanges a login code for the browser login cookie and sends the browser on to
// the web UI.
func (h *handlers) Login(ctx context.Context, req LoginRequestObject) (LoginResponseObject, error) {
	var code string
	if req.Params.Code != nil {
		code = *req.Params.Code
	}
	token, expires, err := h.svc.RedeemLoginCode(ctx, code)
	if e, ok := apierr.As(err); ok && e.Code == board.LoginCodeInvalid().Code {
		return Login404TexthtmlResponse{Body: strings.NewReader(loginFailedPage), ContentLength: int64(len(loginFailedPage))}, nil
	}
	if err != nil {
		return nil, err
	}
	// Not Secure: the local server speaks plain HTTP on 127.0.0.1, where some browsers
	// drop Secure cookies.
	cookie := (&http.Cookie{ //nolint:gosec // see above
		Name: sessionCookie, Value: token, Path: "/", Expires: expires.UTC(),
		MaxAge: int(board.BrowserLoginTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}).String()
	location := "/"
	if req.Params.Board != nil && *req.Params.Board != "" {
		location = "/?board=" + url.QueryEscape(*req.Params.Board)
	}
	return Login303Response{Headers: Login303ResponseHeaders{Location: &location, SetCookie: &cookie}}, nil
}

// browserReadOnly is the error for a write sent with only the browser login.
func browserReadOnly() *apierr.Error {
	return apierr.New(http.StatusForbidden, "browser_read_only",
		"The browser login can only read; writes need a bearer token.",
		"Use the aboard command, or send Authorization: Bearer <token>.")
}

// checkHost refuses requests whose Host header isn't one of hosts, so a web page that
// points its own domain name at this address (DNS rebinding) can't use the browser's
// login. An empty hosts list allows every host.
func checkHost(log *slog.Logger, hosts []string, next http.Handler) http.Handler {
	if len(hosts) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(hosts, strings.ToLower(r.Host)) {
			writeError(w, log, apierr.New(http.StatusMisdirectedRequest, "host_not_allowed",
				"This server only answers requests addressed to "+strings.Join(hosts, " or ")+".",
				"Open http://"+hosts[0]+"/ instead."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LocalHosts returns the Host headers a server listening on addr answers to: the
// address itself, and 127.0.0.1 and localhost with its port.
func LocalHosts(addr string) []string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{strings.ToLower(addr)}
	}
	hosts := []string{"127.0.0.1:" + port, "localhost:" + port}
	if a := strings.ToLower(addr); !slices.Contains(hosts, a) {
		hosts = append(hosts, a)
	}
	return hosts
}

// noUIPage is served at / by a binary built without the web UI.
const noUIPage = `<!doctype html><meta charset="utf-8"><title>Aboard</title>` +
	`<p>This aboard was built without its web UI. Install a release of aboard, or build it from source with <code>make install</code>.</p>`

// serveUI serves the web UI's files for GET requests. With no files, it serves a page
// at / saying how to get them.
func serveUI(files fs.FS) http.Handler {
	var static http.Handler
	if files != nil {
		static = http.FileServerFS(files)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		switch {
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		case static != nil:
			static.ServeHTTP(w, r)
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(noUIPage))
		default:
			http.NotFound(w, r)
		}
	})
}
