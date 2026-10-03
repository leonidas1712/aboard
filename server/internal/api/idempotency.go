package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Responses stores the answers to idempotent writes.
type Responses interface {
	// SavedResponse returns the response saved for key in a caller's scope, and whether
	// there was one.
	SavedResponse(ctx context.Context, scope, key string) (SavedResponse, bool, error)
	// SaveResponse saves the response for key in a caller's scope. If one is already
	// saved for the key, the first one is kept.
	SaveResponse(ctx context.Context, scope, key string, r SavedResponse) error
}

// SavedResponse is the answer to an idempotent write, kept so a retry gets it again.
type SavedResponse struct {
	RequestHash string // digest of the method, path and body the key was first used with
	Status      int
	ContentType string
	Body        []byte
}

// idempotent replays the saved response when a write is retried with the same
// Idempotency-Key and body, and refuses the same key with a different body. Responses
// are saved per caller unless the server failed (5xx), so a retry after a crash runs
// again. The responses that carry a login code or a browser token are never saved, so
// neither is ever written to disk: those writes ignore the key.
func idempotent(o Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || r.Method == http.MethodGet || r.URL.Path == "/v1/login-codes" || (r.URL.Path == "/v1/browser-tokens" && r.Method == http.MethodPost) {
			next.ServeHTTP(w, r)
			return
		}
		scope, ok := r.Context().Value(scopeKey{}).(string)
		if !ok {
			writeError(w, o.Log, errNoScope)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, o.Log, apierr.New(http.StatusBadRequest, "invalid_request", "The request body could not be read.", "Send the request again."))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
		reqHash := hex.EncodeToString(sum[:])

		saved, found, err := o.Responses.SavedResponse(r.Context(), scope, key)
		switch {
		case err != nil:
			writeError(w, o.Log, err)
			return
		case found && saved.RequestHash == reqHash:
			w.Header().Set("Content-Type", saved.ContentType)
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(saved.Status)
			_, _ = w.Write(saved.Body)
			return
		case found:
			writeError(w, o.Log, apierr.New(http.StatusUnprocessableEntity, "idempotency_conflict",
				"This Idempotency-Key was already used for a different request.", "Use a new Idempotency-Key for a new request."))
			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 500 {
			return
		}
		err = o.Responses.SaveResponse(r.Context(), scope, key, SavedResponse{
			RequestHash: reqHash, Status: rec.status, ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes(),
		})
		if err != nil {
			o.Log.Error("save idempotent response", "error", err)
		}
	})
}

// recorder passes a response through while keeping a copy of it.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// rateLimiter allows a fixed number of requests per client address per minute.
type rateLimiter struct {
	clk   clock.Clock
	limit int
	mu    sync.Mutex
	start time.Time
	count map[string]int
}

func newRateLimiter(clk clock.Clock, perMinute int) *rateLimiter {
	return &rateLimiter{clk: clk, limit: perMinute, count: map[string]int{}}
}

func (l *rateLimiter) allow(addr string) bool {
	if l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	if now.Sub(l.start) >= time.Minute {
		l.start, l.count = now, map[string]int{}
	}
	l.count[addr]++
	return l.count[addr] <= l.limit
}
