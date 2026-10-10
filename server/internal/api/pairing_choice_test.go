package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestBrowserChoosesOwnPairingSeatWithoutEndpointAuthority(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name := s.newBoard()
	sender, senderToken := s.joinAs(s.owner, name, "writer", "")
	senderMember := s.memberNamed(senderToken, name, sender)
	maya := s.addHuman("maya")
	joined := s.joinBoard(maya, name, "reviewer", nil)
	chosen := joined.JSON201.Agent
	me, err := s.client(maya).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	created, err := s.client(senderToken).CreatePairingRequestWithResponse(ctx, nil, api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: senderMember.Id, Work: "Review together."})
	mustStatus(t, created, err, 201)
	path := "/v1/pairing-requests/" + created.JSON201.Id + "/choose"
	body := map[string]any{"agent_id": chosen.Id, "generation": 1}
	browser := s.cookieBrowser(maya)
	forged := s.send(http.MethodPost, path, body, func(r *http.Request) { r.AddCookie(browser.cookie); s.fromPage(r) })
	if forged.status != 403 || forged.code() != "csrf_token_invalid" {
		t.Fatalf("CSRF: %d %s", forged.status, forged.raw)
	}
	wrongPerson := s.cookieBrowser(s.owner).do(http.MethodPost, path, body)
	if wrongPerson.status != 403 {
		t.Fatalf("inviter selected recipient: %d %s", wrongPerson.status, wrongPerson.raw)
	}
	foreign := browser.do(http.MethodPost, path, map[string]any{"agent_id": senderMember.Id, "generation": 1})
	if foreign.status != 404 {
		t.Fatalf("foreign seat: %d %s", foreign.status, foreign.raw)
	}
	stale := browser.do(http.MethodPost, path, map[string]any{"agent_id": chosen.Id, "generation": 2})
	if stale.status != 409 {
		t.Fatalf("stale selection: %d %s", stale.status, stale.raw)
	}
	choose := func() call {
		return s.send(http.MethodPost, path, body, func(r *http.Request) {
			r.AddCookie(browser.cookie)
			s.fromPage(r)
			r.Header.Set("X-Aboard-CSRF", browser.csrf)
			r.Header.Set("Idempotency-Key", "browser-choice")
		})
	}
	good := choose()
	if good.status != 200 || !strings.Contains(string(good.raw), `"chosen_recipient_agent_id":"`+chosen.Id+`"`) {
		t.Fatalf("choose: %d %s", good.status, good.raw)
	}
	repeated := choose()
	if repeated.status != 200 {
		t.Fatalf("repeat: %d %s", repeated.status, repeated.raw)
	}
	inbox, err := s.client(joined.JSON201.Token).GetInboxWithResponse(ctx, nil)
	mustStatus(t, inbox, err, 200)
	if len(inbox.JSON200.Messages) != 1 || !strings.Contains(inbox.JSON200.Messages[0].Body, "aboard pairing accept "+created.JSON201.Id) || inbox.JSON200.Messages[0].From.Name != "maya" {
		t.Fatalf("actionable notice: %s", inbox.Body)
	}
	current, err := s.client(maya).GetPairingRequestWithResponse(ctx, created.JSON201.Id)
	mustStatus(t, current, err, 200)
	if !strings.Contains(string(current.Body), `"recipient_handle":"maya"`) || !strings.Contains(string(current.Body), `"recipient_agent_name":"`+chosen.Name+`"`) {
		t.Fatalf("recipient labels: %s", current.Body)
	}
	if current.JSON200.Recipient != nil {
		t.Fatal("browser bound a runtime endpoint")
	}
	otherName, otherToken := s.joinAs(maya, name, "writer", "")
	other := s.memberNamed(otherToken, name, otherName)
	wrongToken := "abp_" + strings.Repeat("z", 43)
	wrongMint, err := s.client(maya).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: created.JSON201.Id, Side: "recipient", AgentId: other.Id, Generation: 1, SessionBinding: "sha256:" + strings.Repeat("c", 64), ClientToken: &wrongToken})
	wantCode(t, wrongMint, 409, "pairing_changed")
	if err != nil {
		t.Fatal(err)
	}
	token := "abp_" + strings.Repeat("q", 43)
	mint, err := s.client(maya).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: created.JSON201.Id, Side: "recipient", AgentId: chosen.Id, Generation: 1, SessionBinding: "sha256:" + strings.Repeat("b", 64), ClientToken: &token})
	mustStatus(t, mint, err, 200)
	accepted, err := s.client(token).AcceptPairingRequestWithResponse(ctx, created.JSON201.Id, nil, api.AcceptPairingRequest{AgentId: chosen.Id, Generation: 1})
	mustStatus(t, accepted, err, 200)
	removed, err := s.client(maya).RemoveAgentWithResponse(ctx, name, chosen.Id, nil)
	mustStatus(t, removed, err, 200)
	revoked := choose()
	if revoked.status != 404 {
		t.Fatalf("removed-seat cached choice: %d %s", revoked.status, revoked.raw)
	}
}
