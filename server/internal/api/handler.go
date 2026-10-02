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
	// Shutdown is done when the server starts shutting down. Open event streams on
	// GET /v1/stream end then; without it they end only when their clients disconnect.
	Shutdown context.Context
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
	return recoverPanics(o.Log, validate(authenticate(o, limiter, idempotent(o, routes)))), nil
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

// authenticate resolves the bearer token for every route except GET /v1/info, and rate
// limits join attempts by client address.
func authenticate(o Options, limiter *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type scopeKey struct{}

// scopeOf names the caller for idempotency keys without storing the token itself.
func scopeOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var errNoScope = errors.New("idempotent request without an authenticated caller")
