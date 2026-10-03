package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// olderServer stands in for a server from another aboard build: it reports serverVersion
// in GET /v1/info, answers POST /v1/boards with template_not_found unless the template
// is writer-reviewer (all an early build knew), and has no other operation.
func olderServer(t *testing.T, serverVersion string) *httptest.Server {
	t.Helper()
	writeErr := func(w http.ResponseWriter, status int, code, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg, "hint": "h"}})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/info":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"version": serverVersion, "mode": "local", "name": "local", "server_id": "srv_01JABCDEFGHJKMNPQRSTVWXYZ0",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/boards":
			var body struct{ Template string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeErr(w, http.StatusUnprocessableEntity, "template_not_found", "There is no template "+body.Template+".")
		default:
			writeErr(w, http.StatusNotFound, "not_found", "There is no "+r.Method+" "+r.URL.Path+".")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAServerThatLacksSomethingThisAboardKnows(t *testing.T) {
	older := olderServer(t, "0.0.1")
	same := olderServer(t, version)
	host := func(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }
	tests := []struct {
		name      string
		srv       *httptest.Server
		localAddr string
		call      func(context.Context, *client) error
		wantCode  string
		wantHint  string
	}{
		{"older local server lacks an operation", older, host(older), getBoard, "server_outdated", "Run aboard down"},
		{"older team server lacks an operation", older, "127.0.0.1:1", getBoard, "server_outdated", "Ask whoever runs that server"},
		{"older server lacks a built-in template", older, host(older), createBoard("general"), "server_outdated", "Run aboard down"},
		{"a template no aboard has", older, host(older), createBoard("no-such-template"), "template_not_found", ""},
		{"a server of the same build", same, host(same), getBoard, "not_found", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &app{env: Env{Rand: rand.Reader, Getenv: func(k string) string {
				if k == "ABOARD_LOCAL_ADDR" {
					return tt.localAddr
				}
				return ""
			}}}
			c, err := a.newClient(serverRef{Name: "s", URL: tt.srv.URL}, "", 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			err = tt.call(t.Context(), c)
			var e *Error
			if !errors.As(err, &e) || e.Code != tt.wantCode || !strings.HasPrefix(e.Hint, tt.wantHint) {
				t.Fatalf("error %+v, want code %s with a hint starting %q", err, tt.wantCode, tt.wantHint)
			}
		})
	}
}

func getBoard(ctx context.Context, c *client) error {
	_, err := c.board(ctx, "docs")
	return err
}

func createBoard(template string) func(context.Context, *client) error {
	return func(ctx context.Context, c *client) error {
		r, err := c.api.CreateBoardWithResponse(ctx, &api.CreateBoardParams{}, api.CreateBoardRequest{Template: &template})
		if err != nil {
			return err
		}
		return apiError(r.StatusCode(), r.Body)
	}
}
