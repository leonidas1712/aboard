package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

func (h *handlers) CreateLoginCode(ctx context.Context, _ CreateLoginCodeRequestObject) (CreateLoginCodeResponseObject, error) {
	c, err := h.svc.CreateLoginCode(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return convert[CreateLoginCode201JSONResponse](map[string]string{"code": c.Code, "expires_at": c.ExpiresAt})
}

// CreateBrowserToken exchanges a login code for a browser token that acts as the human
// who asked for the code. It needs no token: the code is the proof.
func (h *handlers) CreateBrowserToken(ctx context.Context, req CreateBrowserTokenRequestObject) (CreateBrowserTokenResponseObject, error) {
	token, expires, err := h.svc.CreateBrowserToken(ctx, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return CreateBrowserToken201JSONResponse{Token: token, ExpiresAt: expires.UTC()}, nil
}

// EndBrowserTokens logs every browser of the calling person out.
func (h *handlers) EndBrowserTokens(ctx context.Context, _ EndBrowserTokensRequestObject) (EndBrowserTokensResponseObject, error) {
	n, err := h.svc.EndBrowserLogins(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return EndBrowserTokens200JSONResponse{Ended: n}, nil
}

// checkHost refuses requests whose Host header isn't one of hosts, so a web page that
// points its own domain name at this address (DNS rebinding) can't reach the server
// through the browser. An empty hosts list allows every host.
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
func serveUI(files fs.FS) (http.Handler, error) {
	var static http.Handler
	if files != nil {
		static = http.FileServerFS(files)
	}
	csp, err := uiPolicy(files)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
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
	}), nil
}

// inlineScript matches a script element with no src attribute and its text.
var inlineScript = regexp.MustCompile(`(?is)<script((?:\s[^>]*)?)>(.*?)</script>`)

// uiPolicy is the Content-Security-Policy for the UI's pages. Scripts run only from this
// server's own files and the inline scripts the built pages hold, each allowed by its
// SHA-256 hash, read from the files when the server starts: the page's next build
// changes them, and nothing else can add one. So text that reaches the page (a message,
// a note, a file name) can't run as a script even if it got into the page as HTML.
// Styles may be inline, because the UI's components position menus and tooltips with
// style attributes and tags; a style can't run code. Nothing loads from another origin,
// and no other site may frame the page.
func uiPolicy(files fs.FS) (string, error) {
	hashes := map[string]bool{}
	if files != nil {
		err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
				return err
			}
			page, err := fs.ReadFile(files, path)
			if err != nil {
				return err
			}
			for _, m := range inlineScript.FindAllSubmatch(page, -1) {
				if bytes.Contains(bytes.ToLower(m[1]), []byte("src=")) || len(m[2]) == 0 {
					continue
				}
				sum := sha256.Sum256(m[2])
				hashes["'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'"] = true
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("read the UI's pages: %w", err)
		}
	}
	scripts := []string{"'self'"}
	for _, h := range slices.Sorted(maps.Keys(hashes)) {
		scripts = append(scripts, h)
	}
	return "default-src 'self'; script-src " + strings.Join(scripts, " ") +
		"; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'" +
		"; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'", nil
}

// apiPolicy is the Content-Security-Policy for API responses, which are data, never
// pages: a response opened in a browser tab loads and runs nothing.
const apiPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// apiHeaders marks every API response as data that no page may run or frame.
func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", apiPolicy)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets what every response carries: no page of another site may frame
// this server's, browsers don't guess content types, and no address of this server (a
// board name in a query, say) is sent to another site as a referrer.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
