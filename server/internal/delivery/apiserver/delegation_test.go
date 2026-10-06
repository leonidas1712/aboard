package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// post makes a request to url with token and returns the decoded answer.
func post(t *testing.T, method, url, token string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// The delegation lists the person's boards and joins a session, which a second join
// from the same session finds again.
func TestADelegationListsAndJoins(t *testing.T) {
	url, owner := localServer(t)
	board, _ := post(t, "POST", url+"/v1/boards", owner, map[string]any{"template": "general"})["name"].(string)
	d := NewDelegated(url, "laptop", tokens{human: map[string]string{url: owner}})
	ctx := context.Background()
	list, err := d.Boards(ctx, "")
	boards := list.Boards
	if err != nil || len(boards) != 1 || boards[0].Name != board {
		t.Fatalf("boards: %v %v", boards, err)
	}
	req := delivery.SeatRequest{Board: board, Harness: "claude-code", Session: "claude-code:s1"}
	first, err := d.Join(ctx, req)
	if err != nil || first.Reused || first.Seat.MemberID == "" || first.Seat.Board != board || first.Token == "" || first.Mode != delivery.ModeFocused {
		t.Fatalf("first join: %+v %v", first, err)
	}
	again, err := d.Join(ctx, req)
	if err != nil || !again.Reused || again.Seat.MemberID != first.Seat.MemberID || again.Token == first.Token {
		t.Fatalf("second join: %+v %v", again, err)
	}
	// A refusal comes back as the server's own.
	_, err = d.Join(ctx, delivery.SeatRequest{Board: "no-such-board", Session: "claude-code:s1"})
	var refused *delivery.WireError
	if !errors.As(err, &refused) || refused.Code != "board_not_found" {
		t.Fatalf("a missing board: %v", err)
	}
}

// A delegation that stopped working is made again once with the key; when the key is
// refused too, the command is told delegation_revoked.
func TestADelegationIsMadeAgainOnce(t *testing.T) {
	url, owner := localServer(t)
	d := NewDelegated(url, "laptop", tokens{human: map[string]string{url: owner}})
	ctx := context.Background()
	if _, err := d.Boards(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// Another delegation with the same key and name ends the one d holds.
	post(t, "POST", url+"/v1/delegations", owner, map[string]any{"name": "laptop"})
	if _, err := d.Boards(ctx, ""); err != nil {
		t.Fatalf("after the delegation was replaced: %v", err)
	}
	// With the key revoked, nothing works.
	keys := post(t, "GET", url+"/v1/keys", owner, nil)
	id, _ := keys["current_key_id"].(string)
	post(t, "DELETE", url+"/v1/keys/"+id, owner, nil)
	_, err := d.Boards(ctx, "")
	var refused *delivery.WireError
	if !errors.As(err, &refused) || refused.Code != "delegation_revoked" {
		t.Fatalf("after the key was revoked: %v", err)
	}
}

// Without a key, or a server to reach, nothing is made.
func TestADelegationNeedsAKeyAndAServer(t *testing.T) {
	url, _ := localServer(t)
	ctx := context.Background()
	if _, err := NewDelegated(url, "laptop", tokens{}).Boards(ctx, ""); !errors.Is(err, delivery.ErrLoginMissing) {
		t.Errorf("no key: %v", err)
	}
	gone := "http://127.0.0.1:1"
	if _, err := NewDelegated(gone, "laptop", tokens{human: map[string]string{gone: "abh_x"}}).Join(ctx, delivery.SeatRequest{Board: "b", Session: "codex:1"}); !errors.Is(err, delivery.ErrServerUnreachable) {
		t.Errorf("no server: %v", err)
	}
}

// Against a real server, an archived board leaves the delegation's default list, is
// counted there, and is listed with the archived filter.
func TestADelegationListsArchivedBoards(t *testing.T) {
	url, owner := localServer(t)
	board, _ := post(t, "POST", url+"/v1/boards", owner, map[string]any{"template": "general"})["name"].(string)
	if got := post(t, "POST", url+"/v1/boards/"+board+"/archive", owner, nil); got["lifecycle"] != "archived" {
		t.Fatalf("archive: %v", got)
	}
	d := NewDelegated(url, "laptop", tokens{human: map[string]string{url: owner}})
	ctx := context.Background()
	active, err := d.Boards(ctx, "")
	if err != nil || len(active.Boards) != 0 || active.ArchivedCount == nil || *active.ArchivedCount != 1 {
		t.Fatalf("active: %+v %v", active, err)
	}
	archived, err := d.Boards(ctx, "archived")
	if err != nil || len(archived.Boards) != 1 || archived.Boards[0].Name != board {
		t.Fatalf("archived: %+v %v", archived, err)
	}
}

// The delegation asks for the lifecycle filter it is given and passes the server's
// archived count back; it sends no filter for the default, and a missing count stays
// missing, never zero.
func TestADelegationListsByLifecycle(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/delegations":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"abd_test"}`))
		case "/v1/boards":
			queries = append(queries, r.URL.RawQuery)
			if r.URL.Query().Get("lifecycle") == "archived" {
				_, _ = w.Write([]byte(`{"boards":[{"name":"old","lifecycle":"archived"}],"archived_count":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"boards":[{"name":"docs"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	d := NewDelegated(srv.URL, "laptop", tokens{human: map[string]string{srv.URL: "abh_x"}})
	ctx := context.Background()
	archived, err := d.Boards(ctx, "archived")
	if err != nil || len(archived.Boards) != 1 || archived.Boards[0].Name != "old" || archived.ArchivedCount == nil || *archived.ArchivedCount != 1 {
		t.Fatalf("archived: %+v %v", archived, err)
	}
	active, err := d.Boards(ctx, "")
	if err != nil || len(active.Boards) != 1 || active.ArchivedCount != nil {
		t.Fatalf("active: %+v %v", active, err)
	}
	if fmt.Sprint(queries) != "[lifecycle=archived ]" {
		t.Errorf("queries: %q", queries)
	}
}

func TestCreateBoardDoesNotReplaceARefusedDelegation(t *testing.T) {
	var issued, creates int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/delegations" {
			issued++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"abd_test"}`))
			return
		}
		creates++
		if r.Header.Get("Idempotency-Key") != "operation-1" {
			t.Error("missing creation key")
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"delegation_revoked","message":"ended","hint":"login"}}`))
	}))
	defer srv.Close()
	d := NewDelegated(srv.URL, "test", tokens{human: map[string]string{srv.URL: "test-key"}})
	_, err := d.Create(context.Background(), delivery.SeatCreateRequest{IdempotencyKey: "operation-1", Harness: "codex", Session: "codex:s1"})
	if err == nil || issued != 1 || creates != 1 {
		t.Fatalf("creation retried: issued=%d creates=%d error=%v", issued, creates, err)
	}
}

func TestCreationRetryKeepsItsCredentialAndBodyAfterDiscoveryRefresh(t *testing.T) {
	var mu sync.Mutex
	issued, created := 0, 0
	var firstBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/delegations":
			issued++
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"token":"abd_test%d"}`, issued)
		case "/v1/boards":
			if r.Header.Get("Authorization") == "Bearer abd_test1" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"delegation_revoked"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"boards":[]}`))
		default:
			created++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if r.Header.Get("Authorization") != "Bearer abd_test1" || r.Header.Get("Idempotency-Key") != "same-operation" {
				t.Error("creation changed replay scope")
			}
			if created == 1 {
				firstBody = body
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if !bytes.Equal(firstBody, body) {
				t.Error("creation body changed")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"board":{"name":"work"},"agent":{"id":"mem_test","board":"work","name":"codex"},"token":"test-seat"}`))
		}
	}))
	defer srv.Close()
	d := NewDelegated(srv.URL, "test", tokens{human: map[string]string{srv.URL: "test-key"}})
	req := delivery.SeatCreateRequest{BoardCreateOptions: delivery.BoardCreateOptions{Name: "work"}, Session: "codex:s1", Harness: "codex", IdempotencyKey: "same-operation"}
	if _, err := d.Create(context.Background(), req); !errors.Is(err, delivery.ErrServerUnreachable) {
		t.Fatalf("first answer: %v", err)
	}
	if _, err := d.Boards(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if grant, err := d.Create(context.Background(), req); err != nil || grant.Seat.MemberID != "mem_test" {
		t.Fatalf("recovery: %+v %v", grant, err)
	}
}
