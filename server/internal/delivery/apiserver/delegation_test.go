package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	boards, err := d.Boards(ctx)
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
	if _, err := d.Boards(ctx); err != nil {
		t.Fatal(err)
	}
	// Another delegation with the same key and name ends the one d holds.
	post(t, "POST", url+"/v1/delegations", owner, map[string]any{"name": "laptop"})
	if _, err := d.Boards(ctx); err != nil {
		t.Fatalf("after the delegation was replaced: %v", err)
	}
	// With the key revoked, nothing works.
	keys := post(t, "GET", url+"/v1/keys", owner, nil)
	id, _ := keys["current_key_id"].(string)
	post(t, "DELETE", url+"/v1/keys/"+id, owner, nil)
	_, err := d.Boards(ctx)
	var refused *delivery.WireError
	if !errors.As(err, &refused) || refused.Code != "delegation_revoked" {
		t.Fatalf("after the key was revoked: %v", err)
	}
}

// Without a key, or a server to reach, nothing is made.
func TestADelegationNeedsAKeyAndAServer(t *testing.T) {
	url, _ := localServer(t)
	ctx := context.Background()
	if _, err := NewDelegated(url, "laptop", tokens{}).Boards(ctx); !errors.Is(err, delivery.ErrLoginMissing) {
		t.Errorf("no key: %v", err)
	}
	gone := "http://127.0.0.1:1"
	if _, err := NewDelegated(gone, "laptop", tokens{human: map[string]string{gone: "abh_x"}}).Join(ctx, delivery.SeatRequest{Board: "b", Session: "codex:1"}); !errors.Is(err, delivery.ErrServerUnreachable) {
		t.Errorf("no server: %v", err)
	}
}
