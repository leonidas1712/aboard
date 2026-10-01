package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/store"
)

// idempotent replays the saved response when a write is retried with the same
// Idempotency-Key and body, and refuses the same key with a different body. Responses
// are saved per caller unless the server failed (5xx), so a retry after a crash runs
// again.
func idempotent(o Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || r.Method == http.MethodGet {
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

		var saved store.SavedResponse
		err = o.Store.Read(r.Context(), func(tx *store.Tx) error {
			var err error
			saved, err = tx.SavedResponse(scope, key)
			return err
		})
		switch {
		case err == nil && saved.RequestHash == reqHash:
			w.Header().Set("Content-Type", saved.ContentType)
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(saved.Status)
			_, _ = w.Write(saved.Body)
			return
		case err == nil:
			writeError(w, o.Log, apierr.New(http.StatusUnprocessableEntity, "idempotency_conflict",
				"This Idempotency-Key was already used for a different request.", "Use a new Idempotency-Key for a new request."))
			return
		case !errors.Is(err, store.ErrNotFound):
			writeError(w, o.Log, err)
			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 500 {
			return
		}
		err = o.Store.Tx(r.Context(), func(tx *store.Tx) error {
			return tx.SaveResponse(scope, key, store.SavedResponse{
				RequestHash: reqHash, Status: rec.status, ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes(),
			}, o.Clock.Now().UTC().Format(time.RFC3339))
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
