package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestPairingSelectsOnlyOwnExactEndpointsAndReplacementEndsOldProof(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name := s.newBoard()
	a, at := s.joinAs(s.owner, name, "writer", "")
	b, bt := s.joinAs(s.owner, name, "reviewer", "")
	am, bm := s.memberNamed(at, name, a), s.memberNamed(bt, name, b)
	me, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	board, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, board, err, 200)
	created, err := s.client(at).CreatePairingRequestWithResponse(ctx, nil, api.CreatePairingRequest{BoardId: board.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: am.Id, Work: "Review the change together."})
	mustStatus(t, created, err, 201)
	request := created.JSON201
	mint := func(side api.CreatePairingCredentialSide, agent, token, binding string, gen int, replace bool) *api.CreatePairingCredentialResponse {
		t.Helper()
		r, e := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: request.Id, Side: side, AgentId: agent, ClientToken: &token, SessionBinding: binding, Generation: gen, Replace: &replace})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	firstToken := "abp_" + strings.Repeat("a", 43)
	secondToken := "abp_" + strings.Repeat("b", 43)
	binding := "sha256:" + strings.Repeat("a", 64)
	first := mint("initiator", am.Id, firstToken, binding, 1, false)
	mustStatus(t, first, nil, 200)
	second := mint("recipient", bm.Id, secondToken, binding, 1, false)
	mustStatus(t, second, nil, 200)
	if strings.Contains(string(first.Body), firstToken) {
		t.Fatal("endpoint secret returned")
	}
	ordinary, err := s.client(bt).AcceptPairingRequestWithResponse(ctx, request.Id, nil, api.AcceptPairingRequest{AgentId: bm.Id, Generation: 1})
	wantCode(t, ordinary, 403, "forbidden")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.client(secondToken).AcceptPairingRequestWithResponse(ctx, request.Id, nil, api.AcceptPairingRequest{AgentId: bm.Id, Generation: 1})
	mustStatus(t, accepted, err, 200)
	if accepted.JSON200.State != "verifying" {
		t.Fatalf("accepted: %s", accepted.Body)
	}
	unrelated, err := s.client(firstToken).GetMeWithResponse(ctx)
	wantCode(t, unrelated, 403, "forbidden")
	if err != nil {
		t.Fatal(err)
	}
	replaced := mint("initiator", am.Id, "abp_"+strings.Repeat("c", 43), "sha256:"+strings.Repeat("b", 64), 1, true)
	mustStatus(t, replaced, nil, 200)
	retry := mint("initiator", am.Id, "abp_"+strings.Repeat("c", 43), "sha256:"+strings.Repeat("b", 64), 1, true)
	mustStatus(t, retry, nil, 200)
	if retry.JSON200.Id != replaced.JSON200.Id || retry.JSON200.Request.Generation != 2 {
		t.Fatal("lost replacement response selected another generation")
	}
	if replaced.JSON200.Request.Generation != 2 {
		t.Fatalf("replacement: %s", replaced.Body)
	}
	ended, err := s.client(firstToken).GetPairingRequestWithResponse(ctx, request.Id)
	mustStatus(t, ended, err, 401)
	ended, err = s.client(secondToken).GetPairingRequestWithResponse(ctx, request.Id)
	mustStatus(t, ended, err, 401)
}

type pairingFixture struct {
	s                            *testServer
	boardName                    string
	request                      api.PairingRequest
	names, seats, tokens, proofs [2]string
}

func newPairingFixture(t *testing.T) *pairingFixture {
	t.Helper()
	s := newTestServer(t)
	f := &pairingFixture{s: s, boardName: s.newBoard()}
	ctx := context.Background()
	f.names[0], f.tokens[0] = s.joinAs(s.owner, f.boardName, "writer", "")
	f.names[1], f.tokens[1] = s.joinAs(s.owner, f.boardName, "reviewer", "")
	for i := range 2 {
		f.seats[i] = s.memberNamed(f.tokens[i], f.boardName, f.names[i]).Id
		f.proofs[i] = "abp_" + strings.Repeat(string(rune('x'+i)), 43)
	}
	me, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, f.boardName)
	mustStatus(t, b, err, 200)
	r, err := s.client(f.tokens[0]).CreatePairingRequestWithResponse(ctx, nil, api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: f.seats[0], Work: "Check the implementation."})
	mustStatus(t, r, err, 201)
	f.request = *r.JSON201
	for i, side := range []api.CreatePairingCredentialSide{"initiator", "recipient"} {
		token := f.proofs[i]
		m, e := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: f.request.Id, Side: side, AgentId: f.seats[i], SessionBinding: "sha256:" + strings.Repeat(string(rune('a'+i)), 64), Generation: 1, ClientToken: &token})
		mustStatus(t, m, e, 200)
	}
	a, err := s.client(f.proofs[1]).AcceptPairingRequestWithResponse(ctx, f.request.Id, nil, api.AcceptPairingRequest{AgentId: f.seats[1], Generation: 1})
	mustStatus(t, a, err, 200)
	return f
}

func (f *pairingFixture) roundTrip(t *testing.T, side int) (api.Message, api.Message) {
	t.Helper()
	direction := "initiator_to_recipient"
	if side == 1 {
		direction = "recipient_to_initiator"
	}
	marker := fmt.Sprintf("ABOARD-PAIRING %s generation=1 direction=%s", f.request.Id, direction)
	ping := say(f.s, f.tokens[side], f.boardName, []string{"@" + f.names[1-side]}, marker+" kind=ping")
	mustStatus(t, ping, nil, 201)
	recipients := []api.Target{"@" + f.names[side]}
	reply, err := f.s.client(f.tokens[1-side]).PostMessageWithResponse(context.Background(), f.boardName, nil, api.PostMessageRequest{Body: marker + " kind=reply", ReplyTo: &ping.JSON201.Id, To: &recipients})
	mustStatus(t, reply, err, 201)
	return *ping.JSON201, *reply.JSON201
}

func TestPairingNeedsBothCorrelatedRuntimeRoundTrips(t *testing.T) {
	t.Parallel()
	f := newPairingFixture(t)
	ctx := context.Background()
	for i, direction := range []api.PairingRoundTripDirection{"initiator_to_recipient", "recipient_to_initiator"} {
		ping, reply := f.roundTrip(t, i)
		evidence := api.PairingRoundTrip{Direction: direction, Generation: 1, PingSeq: ping.Seq, ReplySeq: reply.Seq, HandoffId: fmt.Sprintf("handoff-%d", i)}
		plain, err := f.s.client(f.tokens[i]).VerifyPairingRoundTripWithResponse(ctx, f.request.Id, nil, evidence)
		wantCode(t, plain, 403, "forbidden")
		if err != nil {
			t.Fatal(err)
		}
		forged := evidence
		forged.PingSeq = reply.Seq
		bad, err := f.s.client(f.proofs[i]).VerifyPairingRoundTripWithResponse(ctx, f.request.Id, nil, forged)
		wantCode(t, bad, 409, "pairing_changed")
		if err != nil {
			t.Fatal(err)
		}
		good, err := f.s.client(f.proofs[i]).VerifyPairingRoundTripWithResponse(ctx, f.request.Id, nil, evidence)
		mustStatus(t, good, err, 200)
		want := api.PairingState("verifying")
		if i == 1 {
			want = "ready"
		}
		if good.JSON200.State != want {
			t.Fatalf("round trip: %s", good.Body)
		}
	}
	ended, err := f.s.client(f.proofs[0]).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, ended, err, 401)
	visible, err := f.s.client(f.s.owner).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, visible, err, 200)
	if visible.JSON200.State != "ready" {
		t.Fatal("ready was not durable")
	}
}

func TestPairingExpiresRenewsWithoutReplacingAndRechecksParentRevocation(t *testing.T) {
	t.Parallel()
	f := newPairingFixture(t)
	ctx := context.Background()
	s := f.s
	s.clock.Advance(10 * time.Minute)
	expired, err := s.client(f.proofs[0]).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, expired, err, 401)
	token := f.proofs[0]
	renewed, err := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: f.request.Id, Side: "initiator", AgentId: f.seats[0], SessionBinding: "sha256:" + strings.Repeat("a", 64), Generation: 1, ClientToken: &token})
	mustStatus(t, renewed, err, 200)
	if renewed.JSON200.Request.Generation != 1 {
		t.Fatal("renewal replaced the endpoints")
	}
	working, err := s.client(token).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, working, err, 200)
	me, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	db, err := sql.Open("sqlite", "file:"+s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "UPDATE access_keys SET revoked_at = ? WHERE human_id = ?", "2026-10-01T16:10:00.000Z", me.JSON200.Id); err != nil {
		t.Fatal(err)
	}
	ended, err := s.client(token).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, ended, err, 401)
}

func TestPairingVisibilityAndCredentialMintAreCurrentOwnerOnly(t *testing.T) {
	t.Parallel()
	f := newPairingFixture(t)
	s := f.s
	ctx := context.Background()
	outsider := s.addHuman("outsider")
	hidden, err := s.client(outsider).GetPairingRequestWithResponse(ctx, f.request.Id)
	wantCode(t, hidden, 404, "pairing_not_found")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.client(outsider).ListPairingRequestsWithResponse(ctx)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Requests) != 0 {
		t.Fatal("foreign requests exposed")
	}
	token := "abp_" + strings.Repeat("q", 43)
	in := api.CreatePairingCredential{RequestId: f.request.Id, Side: "initiator", AgentId: f.seats[0], SessionBinding: "sha256:" + strings.Repeat("a", 64), Generation: 1, ClientToken: &token}
	for _, credential := range []string{f.tokens[0], s.browserToken(s.owner)} {
		r, e := s.client(credential).CreatePairingCredentialWithResponse(ctx, nil, in)
		wantCode(t, r, 403, "human_token_required")
		if e != nil {
			t.Fatal(e)
		}
	}
	private, err := s.client(s.owner).SetVisibilityWithResponse(ctx, f.boardName, nil, api.SetVisibilityRequest{Visibility: "private"})
	mustStatus(t, private, err, 200)
	key := api.IdempotencyKey("pairing-mint")
	first, err := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, &api.CreatePairingCredentialParams{IdempotencyKey: &key}, in)
	mustStatus(t, first, err, 200)
	removed, err := s.client(s.owner).RemoveAgentWithResponse(ctx, f.boardName, f.seats[0], nil)
	mustStatus(t, removed, err, 200)
	replay, err := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, &api.CreatePairingCredentialParams{IdempotencyKey: &key}, in)
	wantCode(t, replay, 404, "pairing_not_found")
	if err != nil {
		t.Fatal(err)
	}
	ended, err := s.client(token).GetPairingRequestWithResponse(ctx, f.request.Id)
	mustStatus(t, ended, err, 401)
}
