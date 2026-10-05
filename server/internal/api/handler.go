// Package api serves Aboard's HTTP API, exactly as spec/openapi.yaml describes it. Code
// generated from the spec decodes requests and encodes responses; this package adds
// request validation, authentication, idempotent writes, rate limiting and the single
// place where errors become HTTP responses.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Options configures the HTTP API.
type Options struct {
	Service   *board.Service
	Responses Responses // keeps answers to writes sent with an Idempotency-Key
	Clock     clock.Clock
	Log       *slog.Logger
	Version   string
	// Commit and CommitTime name the Git commit the server was built from; empty and
	// zero when unknown. GET /v1/info reports them with Version.
	Commit     string
	CommitTime time.Time
	// JoinsPerMinute limits POST /v1/join per client address.
	JoinsPerMinute int
	// ConnectsPerMinute limits POST /v1/connect per client address, and
	// ConnectsPerMinuteServer across every address. Zero means no limit.
	ConnectsPerMinute       int
	ConnectsPerMinuteServer int
	// SignInsPerMinute limits failed browser sign-ins (POST /v1/browser-sessions,
	// POST /v1/browser-tokens and POST /v1/login-codes/preview) per client address, and
	// SignInsPerMinuteServer across every address: failures are what guessing makes.
	// SignInAttemptsPerMinute and SignInAttemptsPerMinuteServer cap every attempt, failed
	// or not, higher, to bound the work they cost. Zero means no limit.
	SignInsPerMinute              int
	SignInsPerMinuteServer        int
	SignInAttemptsPerMinute       int
	SignInAttemptsPerMinuteServer int
	// Shutdown is done when the server starts shutting down. Open event streams on
	// GET /v1/stream end then; without it they end only when their clients disconnect.
	Shutdown context.Context
	// Hosts are the Host headers the server answers; empty allows every host.
	Hosts []string
	// UI holds the web UI's built files, served at /. Nil serves a page saying the UI
	// wasn't built.
	UI fs.FS
}

// NewHandler returns the API's http.Handler.
func NewHandler(o Options) (http.Handler, error) {
	spec, err := GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("load embedded spec: %w", err)
	}
	spec.Servers = nil // validate paths only; the API is served at any host

	strict := NewStrictHandlerWithOptions(&handlers{svc: o.Service, version: o.Version, commit: o.Commit, commitTime: o.CommitTime, clk: o.Clock, log: o.Log, shutdown: o.Shutdown}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, o.Log, apierr.New(http.StatusBadRequest, "invalid_request", "The request body is not valid JSON: "+err.Error(),
				"Send a JSON body as described in the API reference."))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) { writeError(w, o.Log, err) },
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeError(w, o.Log, notFound(r)) })
	routes := HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, o.Log, apierr.New(http.StatusBadRequest, "invalid_request", err.Error(), "Check the request's parameters."))
		},
	})

	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options:               openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		SilenceServersWarning: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts middleware.ErrorHandlerOpts) {
			if opts.StatusCode == http.StatusNotFound || opts.StatusCode == http.StatusMethodNotAllowed {
				writeError(w, o.Log, notFound(r))
				return
			}
			writeError(w, o.Log, apierr.New(http.StatusBadRequest, "invalid_request", firstLine(err.Error()),
				"Check the request against the API reference."))
		},
	})
	limiter := newRateLimiter(o.Clock, o.JoinsPerMinute)
	connects := connectLimits{perAddr: newRateLimiter(o.Clock, o.ConnectsPerMinute), all: newRateLimiter(o.Clock, o.ConnectsPerMinuteServer)}
	signIns := signInLimits{
		failed:   connectLimits{perAddr: newRateLimiter(o.Clock, o.SignInsPerMinute), all: newRateLimiter(o.Clock, o.SignInsPerMinuteServer)},
		attempts: connectLimits{perAddr: newRateLimiter(o.Clock, o.SignInAttemptsPerMinute), all: newRateLimiter(o.Clock, o.SignInAttemptsPerMinuteServer)},
	}
	apiChain := validate(authenticate(o, limiter, connects, signIns, idempotent(o, routes)))
	ui, err := serveUI(o.UI)
	if err != nil {
		return nil, err
	}
	outer := http.NewServeMux()
	outer.Handle("/v1/", apiHeaders(apiChain))
	outer.Handle("/", ui)
	return recoverPanics(o.Log, securityHeaders(checkHost(o.Log, o.Hosts, outer))), nil
}

func notFound(r *http.Request) *apierr.Error {
	return apierr.New(http.StatusNotFound, "not_found", fmt.Sprintf("There is no %s %s.", r.Method, r.URL.Path),
		"Check the path against the API reference.")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// writeError is the one place an error becomes an HTTP response.
func writeError(w http.ResponseWriter, log *slog.Logger, err error) {
	e, ok := apierr.As(err)
	if !ok {
		log.Error("request failed", "error", err)
		e = apierr.New(http.StatusInternalServerError, "internal", "Something went wrong on the server.",
			"Try again; if it keeps failing, check the server log.")
	}
	body := map[string]any{"code": e.Code, "message": e.Message, "hint": e.Hint}
	if e.Details != nil {
		body["details"] = e.Details
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				writeError(w, log, fmt.Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// authenticate resolves the caller's credential for every route except GET /v1/info,
// the browser sign-in routes and POST /v1/connect, and rate limits join attempts by
// client address, and invite redemptions and browser sign-ins by client address and
// across the server. The credential is the Authorization header's bearer token or,
// without that header, a browser session's cookie, whose writes must also pass the
// Origin and CSRF checks.
func authenticate(o Options, limiter *rateLimiter, connects connectLimits, signIns signInLimits, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie := cookieFor(r)
		if r.URL.Path == "/v1/connect" && r.Method == http.MethodPost {
			if !connects.allow(r) {
				w.Header().Set("Retry-After", "60")
				writeError(w, o.Log, apierr.New(http.StatusTooManyRequests, "rate_limited", "Too many attempts to redeem an invite.",
					"Wait a minute, then try again."))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && (r.URL.Path == "/v1/browser-sessions" || r.URL.Path == "/v1/browser-tokens" || r.URL.Path == "/v1/login-codes/preview") {
			if !signIns.allow(r) {
				w.Header().Set("Retry-After", "60")
				writeError(w, o.Log, apierr.New(http.StatusTooManyRequests, "rate_limited", "Too many attempts to sign a browser in.",
					"Wait a minute, then try again."))
				return
			}
			// Every answer of 400 or more counts as a failure, in the minute it happens.
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			w = rec
			defer func() {
				if rec.status >= 400 {
					signIns.failedAttempt(r)
				}
			}()
			// A forged sign-in would put the victim's browser in someone else's session.
			// Only a session's cookie can be planted that way; the deprecated token
			// exchange sets none.
			if r.URL.Path != "/v1/browser-tokens" {
				if err := sameOrigin(r, cookie); err != nil {
					writeError(w, o.Log, err)
					return
				}
			}
			// The handler sets the cookie this request's host and scheme call for, and
			// refuses to switch the browser from a session it already has to another
			// person's without the person's confirmation.
			session := requestSession{cookie: cookie}
			if c, err := r.Cookie(cookie.name); err == nil && strings.HasPrefix(c.Value, "abb_") && r.URL.Path == "/v1/browser-sessions" {
				if p, err := o.Service.Authenticate(r.Context(), c.Value); err == nil && p.Browser && p.Human != nil {
					session.currentPerson = p.Human.ID
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
			return
		}
		if r.URL.Path == "/v1/info" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/join" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			if !limiter.allow(host) {
				w.Header().Set("Retry-After", "60")
				writeError(w, o.Log, apierr.New(http.StatusTooManyRequests, "rate_limited", "Too many join attempts from this address.",
					"Wait a minute, then try again."))
				return
			}
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(cookie.name); err == nil && c.Value != "" {
				// Only a browser session's secret lives in the cookie.
				if !strings.HasPrefix(c.Value, "abb_") {
					writeError(w, o.Log, apierr.Unauthorized())
					return
				}
				// Checked before the session is looked up, so a forged write records no
				// use of the key.
				if err := checkCSRF(r, o.Service, cookie, c.Value); err != nil {
					writeError(w, o.Log, err)
					return
				}
				token, ok = c.Value, true
			}
		}
		if !ok || token == "" {
			writeError(w, o.Log, apierr.Unauthorized())
			return
		}
		p, err := o.Service.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, o.Log, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, p)
		ctx = context.WithValue(ctx, scopeKey{}, scopeOf(token))
		session := requestSession{cookie: cookie}
		if p.Browser {
			session.token = token
		}
		ctx = context.WithValue(ctx, sessionKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// connectLimits are two limits on attempts with a secret (redeeming invites, signing a
// browser in): per client address, and across the server.
type connectLimits struct{ perAddr, all *rateLimiter }

// signInLimits are the limits on signing a browser in: on failed attempts, which is
// what guessing a code or key makes, and a higher one on every attempt, which bounds the
// work sign-ins cost.
type signInLimits struct{ failed, attempts connectLimits }

// allow counts r against the limit on attempts and checks the one on failures, without
// counting r as a failure: failed records that once r has failed.
func (l signInLimits) allow(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	full := l.failed.perAddr.full(host) || l.failed.all.full("")
	return l.attempts.allow(r) && !full
}

// failedAttempt counts a failed attempt from r, in the minute it failed in.
func (l signInLimits) failedAttempt(r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	l.failed.perAddr.fail(host)
	l.failed.all.fail("")
}

// statusRecorder passes a response through and remembers its status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// allow counts an attempt from r against both limits. Both count every attempt, so a
// guess spread over many addresses still meets the server-wide one.
func (l connectLimits) allow(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	perAddr, all := l.perAddr.allow(host), l.all.allow("")
	return perAddr && all
}

type scopeKey struct{}

// scopeOf names the caller for idempotency keys without storing the token itself.
func scopeOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var errNoScope = errors.New("idempotent request without an authenticated caller")
