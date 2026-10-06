package apiserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/server"
)

// tokens is a fixed set of logins.
type tokens struct {
	human  map[string]string
	agents map[delivery.AgentRef]string
}

func (k tokens) AgentToken(a delivery.AgentRef) (string, error) {
	if t, ok := k.agents[a]; ok {
		return t, nil
	}
	return "", delivery.ErrUnauthorized
}

func (k tokens) HumanToken(url string) (string, error) {
	if t, ok := k.human[url]; ok {
		return t, nil
	}
	return "", delivery.ErrLoginMissing
}

// localServer runs a real Aboard server in this process and returns its URL and owner
// token.
func localServer(t *testing.T) (url, owner string) {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The server takes the listener itself: a port closed again before it listens could
	// go to another test's server, which knows none of this one's tokens.
	addr := l.Addr().String()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- server.Run(ctx, server.Options{
			Listener: l, DataDir: filepath.Join(dir, "data"), OwnerName: "alex",
			OwnerTokenPath: filepath.Join(dir, "owner-token"), Version: "test",
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	url = "http://" + addr
	ready, stopWaiting := context.WithTimeout(ctx, 10*time.Second)
	defer stopWaiting()
	owner, err = awaitOwnerReady(ready, url, filepath.Join(dir, "owner-token"), done)
	if err != nil {
		t.Fatal(err)
	}
	return url, owner
}

func apiClient(t *testing.T, url, token string) *api.ClientWithResponses {
	t.Helper()
	c, err := api.NewClientWithResponses(url, api.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pairedAgents makes a board with a writer and a reviewer and returns their tokens.
func pairedAgents(t *testing.T, url, owner string) (board, writerToken, reviewerToken string) {
	t.Helper()
	ctx := context.Background()
	c := apiClient(t, url, owner)
	tmpl := "writer-reviewer"
	created, err := c.CreateBoardWithResponse(ctx, &api.CreateBoardParams{}, api.CreateBoardRequest{Template: &tmpl})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create board: %v %s", err, created.Body)
	}
	board = created.JSON201.Name
	join := func(role string) string {
		r, err := c.JoinWithResponse(ctx, &api.JoinParams{}, api.JoinRequest{Board: &board, Role: &role})
		if err != nil || r.JSON201 == nil {
			t.Fatalf("join as %s: %v %s", role, err, r.Body)
		}
		return r.JSON201.Token
	}
	return board, join("writer"), join("reviewer")
}

// removedPersonsAgent brings a second person, sam, onto the server and the owner's
// board, joins an agent of sam's and removes sam from the board, and returns the board
// and the agent's token, which still signs in.
func removedPersonsAgent(t *testing.T, url, owner string) (board, token string) {
	t.Helper()
	ctx := context.Background()
	c := apiClient(t, url, owner)
	board, _, _ = pairedAgents(t, url, owner)
	inv, err := c.CreateServerInviteWithResponse(ctx, &api.CreateServerInviteParams{}, api.CreateInviteRequest{})
	if err != nil || inv.JSON201 == nil {
		t.Fatalf("invite: %v %s", err, inv.Body)
	}
	conn, err := apiClient(t, url, "").ConnectWithResponse(ctx, &api.ConnectParams{},
		api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "sam", KeyName: "sam-laptop"})
	if err != nil || conn.JSON201 == nil {
		t.Fatalf("connect: %v %s", err, conn.Body)
	}
	if r, err := c.AddPersonWithResponse(ctx, board, &api.AddPersonParams{}, api.AddPersonRequest{Handle: "sam"}); err != nil || r.JSON201 == nil {
		t.Fatalf("add sam: %v %s", err, r.Body)
	}
	role := "reviewer"
	joined, err := apiClient(t, url, conn.JSON201.Key.Token).JoinWithResponse(ctx, &api.JoinParams{}, api.JoinRequest{Board: &board, Role: &role})
	if err != nil || joined.JSON201 == nil {
		t.Fatalf("sam's agent joins: %v %s", err, joined.Body)
	}
	if r, err := c.RemovePersonWithResponse(ctx, board, "sam", &api.RemovePersonParams{}); err != nil || r.JSON200 == nil {
		t.Fatalf("remove sam: %v %s", err, r.Body)
	}
	return board, joined.JSON201.Token
}

// The adapter passes the server contract against a real Aboard server, stream included.
func TestAPIServerPassesTheServerContract(t *testing.T) {
	deliverytest.RunServer(t, deliverytest.ServerFixture{WithModes: func(t *testing.T) (delivery.Server, delivery.AgentRef, func(delivery.Mode) int) {
		url, owner := localServer(t)
		board, _, reviewerToken := pairedAgents(t, url, owner)
		to := delivery.AgentRef{Server: url, Board: board, Name: "reviewer"}
		srv := New(url, tokens{human: map[string]string{url: owner}, agents: map[delivery.AgentRef]string{to: reviewerToken}}, rand.Reader)
		person := apiClient(t, url, owner)
		set := func(m delivery.Mode) int {
			r, err := person.SetDeliveryModeWithResponse(context.Background(), board, "reviewer", &api.SetDeliveryModeParams{},
				api.SetDeliveryModeJSONRequestBody{Mode: api.DeliveryModeSetting(m)})
			if err != nil || r.JSON200 == nil {
				t.Fatalf("set the delivery mode: %v %s", err, r.Body)
			}
			return r.JSON200.Revision
		}
		return srv, to, set
	}, Gone: func(t *testing.T) (delivery.Server, delivery.AgentRef) {
		url, owner := localServer(t)
		board, token := removedPersonsAgent(t, url, owner)
		agent := delivery.AgentRef{Server: url, Board: board, Name: "sam-reviewer"}
		return New(url, tokens{agents: map[delivery.AgentRef]string{agent: token}}, rand.Reader), agent
	}, New: func(t *testing.T) (delivery.Server, delivery.AgentRef, func(string, bool) int) {
		url, owner := localServer(t)
		board, writerToken, reviewerToken := pairedAgents(t, url, owner)
		to := delivery.AgentRef{Server: url, Board: board, Name: "reviewer"}
		srv := New(url, tokens{human: map[string]string{url: owner}, agents: map[delivery.AgentRef]string{to: reviewerToken}}, rand.Reader)
		writer := apiClient(t, url, writerToken)
		post := func(body string, urgent bool) int {
			to := []string{"@reviewer"}
			r, err := writer.PostMessageWithResponse(context.Background(), board, &api.PostMessageParams{},
				api.PostMessageRequest{Body: body, To: &to, Urgent: &urgent})
			if err != nil || r.JSON201 == nil {
				t.Fatalf("post: %v %s", err, r.Body)
			}
			return r.JSON201.Seq
		}
		return srv, to, post
	}})
}

func TestRejectedAgentTokenIsUnauthorized(t *testing.T) {
	url, owner := localServer(t)
	board, _, _ := pairedAgents(t, url, owner)
	to := delivery.AgentRef{Server: url, Board: board, Name: "reviewer"}
	srv := New(url, tokens{agents: map[delivery.AgentRef]string{to: "aba_not-a-real-token"}}, rand.Reader)
	if _, _, _, err := srv.Inbox(context.Background(), to); !errors.Is(err, delivery.ErrUnauthorized) {
		t.Fatalf("Inbox with a bad token = %v, want ErrUnauthorized", err)
	}
	if err := srv.Ack(context.Background(), to, 1); !errors.Is(err, delivery.ErrUnauthorized) {
		t.Fatalf("Ack with a bad token = %v, want ErrUnauthorized", err)
	}
	if err := srv.SetPresence(context.Background(), to, delivery.PresenceIdle, delivery.ModeAuto); !errors.Is(err, delivery.ErrUnauthorized) {
		t.Fatalf("SetPresence with a bad token = %v, want ErrUnauthorized", err)
	}
}

// A presence the daemon reports is what the board's members see.
func TestReportedPresenceShowsOnTheBoard(t *testing.T) {
	ctx := context.Background()
	url, owner := localServer(t)
	board, _, reviewerToken := pairedAgents(t, url, owner)
	to := delivery.AgentRef{Server: url, Board: board, Name: "reviewer"}
	srv := New(url, tokens{agents: map[delivery.AgentRef]string{to: reviewerToken}}, rand.Reader)
	if err := srv.SetPresence(ctx, to, delivery.PresenceWorking, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	r, err := apiClient(t, url, owner).ListMembersWithResponse(ctx, board, nil)
	if err != nil || r.JSON200 == nil {
		t.Fatalf("members: %v %s", err, r.Body)
	}
	for _, m := range r.JSON200.Members {
		if m.Name == "reviewer" && (m.Presence == nil || *m.Presence != api.MemberPresenceWorking) {
			t.Fatalf("reviewer's presence: %v", m.Presence)
		}
	}
}

func TestFollowWithoutALoginSendsNothing(t *testing.T) {
	hit := false
	ts := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true })}
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ts.Serve(l) }()
	t.Cleanup(func() { _ = ts.Close() })
	srv := New("http://"+l.Addr().String(), tokens{}, rand.Reader)
	err = srv.Follow(context.Background(), func() {}, func(delivery.Head) {})
	if !errors.Is(err, delivery.ErrLoginMissing) || hit {
		t.Fatalf("Follow = %v (server contacted: %v), want ErrLoginMissing without a request", err, hit)
	}
}

func TestReadEventsParsesTheStream(t *testing.T) {
	stream := ": keepalive\n\n" +
		"event: head\ndata: {\"board\":\"docs\",\"board_id\":\"brd_1\",\"seq\":7}\n\n" +
		"data: not a head\n\n" +
		"event: head\ndata: {\"board\":\"docs\",\n" + "data: \"seq\":8}\n\n" +
		"event: head\ndata: {\"board\":\"other\",\"seq\":2}\n"
	var got []string
	lines := 0
	err := readEvents(strings.NewReader(stream), func() { lines++ }, func(event, data string) {
		got = append(got, event+" "+data)
	})
	if !errors.Is(err, errStreamEnded) {
		t.Fatalf("err = %v", err)
	}
	want := []string{
		`head {"board":"docs","board_id":"brd_1","seq":7}`,
		"message not a head",
		"head {\"board\":\"docs\",\n\"seq\":8}",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events:\n%q\nwant:\n%q", got, want)
	}
	if lines != 13 {
		t.Fatalf("saw %d lines, want 13", lines)
	}
}

// awaitOwnerReady waits for bootstrap to publish a credential usable by the server.
func awaitOwnerReady(ctx context.Context, url, path string, stopped <-chan error) (string, error) {
	for {
		raw, err := os.ReadFile(filepath.Clean(path))
		if err == nil {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/v1/me", http.NoBody)
			if err != nil {
				return "", err
			}
			candidate := strings.TrimSpace(string(raw))
			req.Header.Set("Authorization", "Bearer "+candidate)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK && candidate != "" {
					return candidate, nil
				}
			}
		}
		select {
		case err := <-stopped:
			if err != nil {
				return "", fmt.Errorf("the test server stopped: %w", err)
			}
			return "", errors.New("the test server stopped")
		case <-ctx.Done():
			return "", fmt.Errorf("the test server didn't become ready: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestOwnerReadinessDoesNotReturnAnIncompleteBootstrapCredential(t *testing.T) {
	for _, initial := range []string{"", "fixture"} {
		t.Run(fmt.Sprintf("initial-bytes-%d", len(initial)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "owner-token")
			if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}
			const complete = "fixture-owner-token"
			var once sync.Once
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The read before this first request necessarily saw incomplete file bytes.
				// Publish the completed bootstrap only when that stale read has been observed.
				once.Do(func() {
					if err := os.WriteFile(path, []byte(complete), 0o600); err != nil {
						t.Error(err)
					}
				})
				if r.URL.Path == "/v1/info" {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.URL.Path != "/v1/me" {
					t.Errorf("unexpected readiness route %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer "+complete {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			got, err := awaitOwnerReady(ctx, srv.URL, path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != complete {
				t.Fatalf("readiness returned %d incomplete credential bytes", len(got))
			}
		})
	}
}
