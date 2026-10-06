package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

type creationTestTokens struct{ key string }

func (c creationTestTokens) HumanToken(string) (string, error) { return c.key, nil }
func (c creationTestTokens) AgentToken(delivery.AgentRef) (string, error) {
	return "", delivery.ErrUnauthorized
}

func TestCreationDoesNotReplayThroughAReplacementDaemon(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "state.db"), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := board.New(st, notify.NewInProcess(), clk, ids.New(rand.Reader), []byte("test digest"), board.Config{Mode: "local", JoinHost: "localhost"}, log)
	owner, err := svc.BootstrapOwner(ctx, "alex", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(api.Options{Service: svc, Responses: st, Clock: clk, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Rand: rand.Reader, Getenv: func(k string) string { return map[string]string{"HOME": home, "ABOARD_HOME": home}[k] }}, daemonChecked: true}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	oldListener, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	oldDaemon := apiserver.NewDelegated(srv.URL, "original-daemon", creationTestTokens{owner})
	replacementDaemon := apiserver.NewDelegated(srv.URL, "replacement-daemon", creationTestTokens{owner})
	oldDone := make(chan struct{})
	replaced := make(chan struct{})
	replacementDone := make(chan struct{})
	var replacementListener *control.Listener
	go func() {
		defer close(oldDone)
		conn, err := oldListener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req delivery.Request
		if err := delivery.ReadFrame(bufio.NewReader(conn), &req); err != nil {
			t.Error(err)
			return
		}
		create := delivery.SeatCreateRequest{BoardCreateOptions: *req.Create, Harness: req.Harness, Session: req.Key().String(), IdempotencyKey: req.IdempotencyKey}
		if _, err := oldDaemon.Create(ctx, create); err != nil {
			t.Error(err)
			return
		}
		// Commit succeeded, but its server response did not reach the command. Swap the
		// daemon socket before reporting that uncertainty, so any redial reaches a new scope.
		_ = oldListener.Close()
		replacementListener, err = control.Listen(p.socket())
		if err != nil {
			t.Error(err)
			return
		}
		close(replaced)
		go func() {
			defer close(replacementDone)
			conn, err := replacementListener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			var req delivery.Request
			if err := delivery.ReadFrame(bufio.NewReader(conn), &req); err != nil {
				t.Error(err)
				return
			}
			_, err = replacementDaemon.Create(ctx, delivery.SeatCreateRequest{BoardCreateOptions: *req.Create, Harness: req.Harness, Session: req.Key().String(), IdempotencyKey: req.IdempotencyKey})
			if err != nil {
				t.Error(err)
			}
			_ = delivery.WriteFrame(conn, delivery.Response{V: delivery.ProtocolVersion, Error: &delivery.WireError{Code: "server_unreachable", Message: "uncertain", Hint: "discover"}})
		}()
		_ = delivery.WriteFrame(conn, delivery.Response{V: delivery.ProtocolVersion, Error: &delivery.WireError{Code: "server_unreachable", Message: "lost answer", Hint: "discover"}})
	}()
	t.Cleanup(func() {
		_ = oldListener.Close()
		<-oldDone
		select {
		case <-replaced:
			_ = replacementListener.Close()
			<-replacementDone
		default:
		}
	})
	_, _, err = a.createSessionBoard(ctx, delivery.SessionKey{Harness: "codex", ID: "s1"}, serverRef{URL: srv.URL}, delivery.BoardCreateOptions{Template: "general"}, "member", "")
	if asError(err).Code != "server_unreachable" {
		t.Fatalf("unexpected result: %v", err)
	}
	<-oldDone
	client, err := api.NewClientWithResponses(srv.URL, api.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+owner)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListBoardsWithResponse(ctx, nil)
	if err != nil || list.JSON200 == nil {
		t.Fatalf("couldn't read created boards: %v", err)
	}
	if len(list.JSON200.Boards) != 1 {
		t.Fatalf("uncertain creation crossed daemon scope and created %d boards", len(list.JSON200.Boards))
	}
	members, err := client.ListMembersWithResponse(ctx, list.JSON200.Boards[0].Name)
	if err != nil || members.JSON200 == nil {
		t.Fatalf("couldn't read created members: %v", err)
	}
	agents := 0
	for _, m := range members.JSON200.Members {
		if m.Kind == api.MemberKindAgent {
			agents++
		}
	}
	if agents != 1 {
		t.Fatalf("creation made %d agent seats", agents)
	}
}
