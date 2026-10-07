package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Responses stores the answers to idempotent writes.
type Responses interface {
	// SavedResponse returns the response saved for key in a caller's scope, and whether
	// there is one within its 24-hour lifetime.
	SavedResponse(ctx context.Context, scope, key string) (SavedResponse, bool, error)
	// SaveResponse saves the response for key in a caller's scope. If one is already
	// saved for the key and has not expired, the first one is kept.
	SaveResponse(ctx context.Context, scope, key string, r SavedResponse) error
}

// SavedResponse is the answer to an idempotent write, kept so a retry gets it again.
type SavedResponse struct {
	RequestHash string // digest of the method, path and body the key was first used with
	Status      int
	ContentType string
	Body        []byte
}

// creationRequestHash keeps the incoming bytes before validation applies defaults.
func creationRequestHash(o Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/delegations/boards" {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			if err != nil {
				writeError(w, o.Log, apierr.New(http.StatusBadRequest, "invalid_request", "The request body could not be read.", "Send the request again."))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
			r = r.WithContext(context.WithValue(r.Context(), requestHashKey{}, hex.EncodeToString(sum[:])))
		}
		next.ServeHTTP(w, r)
	})
}

// idempotent replays the saved response when a write is retried with the same
// Idempotency-Key and body, and refuses the same key with a different body. Responses
// are saved per caller unless the server failed (5xx), so a retry after a crash runs
// again. The responses that carry a join code, a login code, a browser token, a server invite, an
// access key, a guest's key and agent token, a machine request's secrets, a machine's
// delegation, or a delegated join's agent token are never saved, so none is ever
// written to disk: those writes ignore the key. A join with a person's key or a code
// keeps its replay, since each makes a new agent, but its stored answer is returned only
// after the service rechecks that the caller may still have it.
// Delegated board creation uses its transactional receipt instead of this cache.
func idempotent(o Options, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		secret := r.URL.Path == "/v1/login-codes" || r.URL.Path == "/v1/invites" || r.URL.Path == "/v1/connect" || r.URL.Path == "/v1/guest-join" ||
			(r.URL.Path == "/v1/browser-tokens" && r.Method == http.MethodPost) ||
			(r.URL.Path == "/v1/browser-sessions" && r.Method == http.MethodPost) ||
			(r.URL.Path == "/v1/keys" && r.Method == http.MethodPost) ||
			(r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/boards/") &&
				strings.HasSuffix(r.URL.Path, "/join-codes") && strings.Count(r.URL.Path, "/") == 4) ||
			r.URL.Path == "/v1/machine-requests" || r.URL.Path == "/v1/machine-requests/collect" ||
			r.URL.Path == "/v1/delegations" ||
			// Creation keeps its answer in the board transaction, with one expiry.
			r.URL.Path == "/v1/delegations/boards" ||
			// A delegated join's answer holds a token and is never kept: a repeat is a new
			// call, which the server answers by finding the same seat.
			(r.URL.Path == "/v1/join" && principal(r.Context()).Delegation != nil)
		if key == "" || r.Method == http.MethodGet || secret {
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
		if rawHash, ok := r.Context().Value(requestHashKey{}).(string); ok {
			reqHash = rawHash
		}
		ctx := context.WithValue(r.Context(), requestHashKey{}, reqHash)
		r = r.WithContext(ctx)

		saved, found, err := o.Responses.SavedResponse(ctx, scope, key)
		switch {
		case err != nil:
			writeError(w, o.Log, err)
			return
		case found && saved.RequestHash == reqHash:
			if r.URL.Path == "/v1/join" {
				// A stored join answer holds the agent's token: it is returned only
				// while the caller may still have it.
				if err := checkJoinReplay(ctx, o.Service, body, saved); err != nil {
					writeError(w, o.Log, err)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
			} else if err := checkBoardReplay(ctx, o.Service, r.Method, r.URL.Path, body, saved); err != nil {
				writeError(w, o.Log, err)
				return
			}
			saved.Body, err = withheldNames(ctx, o.Service, r.Method, r.URL.Path, saved)
			if err != nil {
				writeError(w, o.Log, err)
				return
			}
			w.Header().Set("Content-Type", saved.ContentType)
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(saved.Status)
			_, _ = w.Write(saved.Body) //nolint:gosec // a JSON answer this server stored, sent as application/json
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
		err = o.Responses.SaveResponse(ctx, scope, key, SavedResponse{
			RequestHash: reqHash, Status: rec.status, ContentType: rec.Header().Get("Content-Type"), Body: rec.body.Bytes(),
		})
		if err != nil {
			o.Log.Error("save idempotent response", "error", err)
		}
	})
}

// checkJoinReplay rechecks a stored answer to a join with a person's key or a code
// before it is returned again: the service decides from the request and the seat and
// token the answer holds. A stored refusal still needs current board access.
func checkJoinReplay(ctx context.Context, svc *board.Service, request []byte, saved SavedResponse) error {
	var in struct {
		Code  string `json:"code"`
		Board string `json:"board"`
		Role  string `json:"role"`
	}
	var answer struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(request, &in); err != nil {
		if saved.Status >= http.StatusBadRequest {
			return svc.CheckBoardReplay(ctx, principal(ctx), board.Replay{})
		}
		return fmt.Errorf("decode the stored join's request: %w", err)
	}
	if saved.Status != http.StatusCreated {
		return svc.CheckBoardReplay(ctx, principal(ctx), board.Replay{Name: in.Board, JoinCode: in.Code})
	}
	if err := json.Unmarshal(saved.Body, &answer); err != nil {
		return fmt.Errorf("decode the stored join's answer: %w", err)
	}
	return svc.CheckJoinReplay(ctx, principal(ctx), board.JoinInput{Code: in.Code, Board: in.Board, Role: in.Role}, answer.Agent.ID, answer.Token)
}

// checkBoardReplay identifies cached board data without trusting the cached response
// as authority. The service checks the current credential and access in one read.
func checkBoardReplay(ctx context.Context, svc *board.Service, method, path string, request []byte, saved SavedResponse) error {
	in := board.Replay{}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case method == http.MethodPost && strings.HasPrefix(path, "/v1/people/") && strings.HasSuffix(path, "/rename"):
		var result struct {
			Person struct {
				ID string `json:"id"`
			} `json:"person"`
		}
		if saved.Status == http.StatusOK {
			if err := json.Unmarshal(saved.Body, &result); err != nil {
				return err
			}
			if result.Person.ID == "" {
				return fmt.Errorf("stored rename has no person id")
			}
		}
		return svc.CheckRenameReplay(ctx, principal(ctx), result.Person.ID)
	case path == "/v1/boards" && method == http.MethodPost:
		var requested struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(request, &requested); err != nil {
			if saved.Status >= http.StatusBadRequest {
				return svc.CheckBoardReplay(ctx, principal(ctx), in)
			}
			return fmt.Errorf("decode the stored board creation request: %w", err)
		}
		if saved.Status == http.StatusCreated {
			var result struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(saved.Body, &result); err != nil {
				return fmt.Errorf("decode the stored board creation response: %w", err)
			}
			in.Name, in.ID = requested.Name, result.ID
		} else {
			var result Error
			if err := json.Unmarshal(saved.Body, &result); err != nil {
				return fmt.Errorf("decode the stored board creation refusal: %w", err)
			}
			if result.Error.Code == "board_name_taken" {
				in.Name = requested.Name
			}
		}
	case len(parts) >= 3 && parts[0] == "v1" && parts[1] == "boards":
		in.Name = parts[2]
		in.Tasks = len(parts) >= 4 && parts[3] == "tasks"
		if len(parts) == 4 && parts[3] == "people" && method == http.MethodPost {
			var add struct {
				Handle string `json:"handle"`
			}
			if err := json.Unmarshal(request, &add); err != nil {
				return err
			}
			in.AddPeople, in.Handle = true, add.Handle
		}
		if len(parts) == 5 && parts[3] == "members" && method == http.MethodDelete {
			in.AgentRemoval = true
		}
		if len(parts) == 4 && (parts[3] == "archive" || parts[3] == "restore" || parts[3] == "delete") {
			in.Lifecycle = parts[3]
			in.DeleteDone = parts[3] == "delete" && saved.Status == http.StatusOK
		}
		if len(parts) == 4 && parts[3] == "messages" && saved.Status == http.StatusCreated {
			var result struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(saved.Body, &result); err != nil {
				return fmt.Errorf("decode the stored message response: %w", err)
			}
			in.MessageID = result.ID
		}
	case len(parts) >= 3 && parts[0] == "v1" && parts[1] == "messages":
		in.MessageID = parts[2]
	case path == "/v1/me/inbox/ack" || path == "/v1/me/presence":
		in.OwnSeat = true
	case path == "/v1/agents/prune":
		// The answer names only the caller's own agents, or for an admin's prune across
		// the server what it removed: the caller's credential and role are checked again.
		var requested struct {
			All bool `json:"all"`
		}
		_ = json.Unmarshal(request, &requested)
		in.PruneAll = requested.All
	case path == "/v1/me/leave":
		// The answer is about the caller's own seat, which ended with it.
		return nil
	default:
		return nil
	}
	return svc.CheckBoardReplay(ctx, principal(ctx), in)
}

// withheldNames returns a stored answer to an agent removal or a prune as it may be
// sent again: the board and agent names of every board the caller can no longer see
// are withheld, as they would be in a new answer. Every other answer is sent as stored.
func withheldNames(ctx context.Context, svc *board.Service, method, path string, saved SavedResponse) ([]byte, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	removal := method == http.MethodDelete && len(parts) == 5 && parts[1] == "boards" && parts[3] == "members"
	if saved.Status != http.StatusOK || (path != "/v1/agents/prune" && !removal) {
		return saved.Body, nil
	}
	var answer map[string]any
	if err := json.Unmarshal(saved.Body, &answer); err != nil {
		return nil, fmt.Errorf("decode the stored answer: %w", err)
	}
	rows := []map[string]any{answer}
	if !removal {
		rows = nil
		list, _ := answer["agents"].([]any)
		for _, v := range list {
			if m, ok := v.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
	}
	var ids []string
	for _, m := range rows {
		if id, ok := m["board_id"].(string); ok {
			ids = append(ids, id)
		}
	}
	hidden, err := svc.HiddenBoards(ctx, principal(ctx), ids)
	if err != nil {
		return nil, err
	}
	changed := false
	for _, m := range rows {
		if id, _ := m["board_id"].(string); hidden[id] && (m["board"] != nil || m["name"] != nil) {
			m["board"], m["name"], changed = nil, nil, true
		}
	}
	if !changed {
		return saved.Body, nil
	}
	return json.Marshal(answer)
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

// window starts a new minute once the current one is over. Call it with mu held.
func (l *rateLimiter) window() {
	if now := l.clk.Now(); now.Sub(l.start) >= time.Minute {
		l.start, l.count = now, map[string]int{}
	}
}

// allow counts an attempt from addr and reports whether it is within the limit.
func (l *rateLimiter) allow(addr string) bool {
	if l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.window()
	l.count[addr]++
	return l.count[addr] <= l.limit
}

// full reports, without counting anything, whether addr already reached the limit this
// minute. With fail it limits only what failed: check full before an attempt, call fail
// after one that failed.
func (l *rateLimiter) full(addr string) bool {
	if l.limit <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.window()
	return l.count[addr] >= l.limit
}

// fail counts a failed attempt from addr in the minute it failed in.
func (l *rateLimiter) fail(addr string) {
	if l.limit <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.window()
	l.count[addr]++
}
