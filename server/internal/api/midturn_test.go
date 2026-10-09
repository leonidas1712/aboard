package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestMidturnPolicyBelongsOnlyToThePersonAndOwnActiveAgents(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	b, name, token := s.boardWithMayasAgent(maya, sam)
	agent := s.memberNamed(maya, b, name)
	get := func(key string) api.MidturnPolicyView {
		t.Helper()
		r, err := s.client(key).GetMidturnPolicyWithResponse(context.Background())
		mustStatus(t, r, err, 200)
		return *r.JSON200
	}
	put := func(key string, member *string, policy *api.MidturnPolicy) *api.SetMidturnPolicyResponse {
		t.Helper()
		r, err := s.client(key).SetMidturnPolicyWithResponse(context.Background(), nil, api.SetMidturnPolicyJSONRequestBody{MemberId: member, Policy: policy})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := get(token); got.Policy != "my-agents" || got.Overrides != nil {
		t.Fatalf("agent default/authority: %+v", got)
	}
	ownerOnly, peers := api.MidturnPolicy("owner-only"), api.MidturnPolicy("my-agents")
	if r := put(maya, nil, &ownerOnly); r.StatusCode() != 200 {
		t.Fatalf("person default: %d %s", r.StatusCode(), r.Body)
	}
	if got := get(token); got.Policy != ownerOnly || got.Source != "person_default" {
		t.Fatalf("agent didn't inherit own person's policy: %+v", got)
	}
	inherited, err := s.client(token).GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, inherited, err, 200)
	if inherited.JSON200.MidturnPolicy == nil || *inherited.JSON200.MidturnPolicy != ownerOnly {
		t.Fatalf("fresh inbox omitted effective owner default: %s", inherited.Body)
	}
	if r := put(maya, &agent.Id, &peers); r.StatusCode() != 200 {
		t.Fatalf("own override: %d %s", r.StatusCode(), r.Body)
	}
	if got := get(token); got.Policy != peers || got.Source != "agent_override" {
		t.Fatalf("agent override: %+v", got)
	}
	overridden, err := s.client(token).GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, overridden, err, 200)
	if overridden.JSON200.MidturnPolicy == nil || *overridden.JSON200.MidturnPolicy != peers {
		t.Fatalf("fresh inbox omitted current override: %s", overridden.Body)
	}
	for _, key := range []string{sam, s.owner} {
		if r := put(key, &agent.Id, &ownerOnly); errorCode(t, r, nil, 403) != "agent_owner_required" {
			t.Fatal("foreign person changed policy")
		}
	}
	if r := put(token, &agent.Id, &ownerOnly); errorCode(t, r, nil, 403) != "human_token_required" {
		t.Fatal("agent changed own policy")
	}
	if r := put(maya, nil, nil); errorCode(t, r, nil, 422) != "invalid_request" {
		t.Fatal("null default was accepted")
	}
	if r := put(maya, &agent.Id, nil); r.StatusCode() != 200 {
		t.Fatalf("clear override: %d %s", r.StatusCode(), r.Body)
	}
	if got := get(token); got.Policy != ownerOnly || got.Source != "person_default" {
		t.Fatalf("clear did not restore person default: %+v", got)
	}
	if got := get(sam); got.Policy != peers {
		t.Fatalf("another person's default changed: %+v", got)
	}
}

func TestMidturnReplayRechecksRemovedTargetsAndNeverReappliesThePreference(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	b, name, _ := s.boardWithMayasAgent(maya, sam)
	agent := s.memberNamed(maya, b, name)
	ctx := context.Background()
	ownerOnly, peers := api.MidturnPolicy("owner-only"), api.MidturnPolicy("my-agents")
	key := api.IdempotencyKey("midturn-default")
	input := api.SetMidturnPolicyJSONRequestBody{Policy: &ownerOnly}
	first, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, &api.SetMidturnPolicyParams{IdempotencyKey: &key}, input)
	mustStatus(t, first, err, 200)
	if first.JSON200.Overrides != nil {
		t.Fatal("write receipt retained an override inventory")
	}
	changed, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, nil, api.SetMidturnPolicyJSONRequestBody{Policy: &peers})
	mustStatus(t, changed, err, 200)
	replay, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, &api.SetMidturnPolicyParams{IdempotencyKey: &key}, input)
	mustStatus(t, replay, err, 200)
	if !bytes.Equal(first.Body, replay.Body) {
		t.Fatalf("receipt changed: %s / %s", first.Body, replay.Body)
	}
	current, err := s.client(maya).GetMidturnPolicyWithResponse(ctx)
	mustStatus(t, current, err, 200)
	if current.JSON200.Policy != peers {
		t.Fatal("replay reapplied an old policy")
	}
	agentKey := api.IdempotencyKey("midturn-agent")
	agentInput := api.SetMidturnPolicyJSONRequestBody{MemberId: &agent.Id, Policy: &ownerOnly}
	own, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, &api.SetMidturnPolicyParams{IdempotencyKey: &agentKey}, agentInput)
	mustStatus(t, own, err, 200)
	removed, err := s.client(maya).RemoveAgentWithResponse(ctx, b, agent.Id, nil)
	mustStatus(t, removed, err, 200)
	refused, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, &api.SetMidturnPolicyParams{IdempotencyKey: &agentKey}, agentInput)
	if code := errorCode(t, refused, err, 404); code != "member_not_found" {
		t.Fatalf("removed target replay: %s", code)
	}
}

func TestMidturnBrowserWriteRequiresCSRF(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	page := s.cookieBrowser(s.owner)
	path := "/v1/me/midturn"
	forged := s.send(http.MethodPut, path, map[string]any{"policy": "owner-only"}, func(r *http.Request) {
		r.AddCookie(page.cookie)
		s.fromPage(r)
	})
	if forged.status != http.StatusForbidden || forged.code() != "csrf_token_invalid" {
		t.Fatalf("missing CSRF accepted: %d %s", forged.status, forged.raw)
	}
	if got := page.do(http.MethodPut, path, map[string]any{"policy": "owner-only"}); got.status != 200 || got.body["policy"] != "owner-only" {
		t.Fatalf("browser policy: %d %s", got.status, got.raw)
	}
}

func TestMidturnTargetsHidePrivateAgentsAndRequireCurrentOwnerMembership(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam, outsider := s.addHuman("maya"), s.addHuman("sam"), s.addHuman("outsider")
	b, name, _ := s.boardWithMayasAgent(maya, sam)
	agent := s.memberNamed(maya, b, name)
	ctx := context.Background()
	private, err := s.client(s.owner).SetVisibilityWithResponse(ctx, b, nil, api.SetVisibilityRequest{Visibility: "private"})
	mustStatus(t, private, err, 200)
	policy := api.MidturnPolicy("owner-only")
	input := api.SetMidturnPolicyJSONRequestBody{MemberId: &agent.Id, Policy: &policy}
	hidden, err := s.client(outsider).SetMidturnPolicyWithResponse(ctx, nil, input)
	if code := errorCode(t, hidden, err, 404); code != "member_not_found" {
		t.Fatalf("private agent existence leaked: %s", code)
	}
	key := api.IdempotencyKey("before-owner-removal")
	set, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, &api.SetMidturnPolicyParams{IdempotencyKey: &key}, input)
	mustStatus(t, set, err, 200)
	removed, err := s.client(s.owner).RemovePersonWithResponse(ctx, b, "maya", nil)
	mustStatus(t, removed, err, 200)
	for _, params := range []*api.SetMidturnPolicyParams{nil, {IdempotencyKey: &key}} {
		refused, err := s.client(maya).SetMidturnPolicyWithResponse(ctx, params, input)
		if code := errorCode(t, refused, err, 404); code != "member_not_found" {
			t.Fatalf("removed owner retained preference authority: %s", code)
		}
	}
}

func TestPeerReceiptGuidanceIsCurrentAndOnlyForItsSender(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	b, _, from := s.boardWithMayasAgent(maya, sam)
	target, to := s.joinAs(maya, b, "member", "codex")
	ctx := context.Background()
	urgent := true
	posted, err := s.client(from).PostMessageWithResponse(ctx, b, nil, api.PostMessageRequest{Body: "step", To: ptr([]api.Target{api.Target("@" + target)}), Urgent: &urgent})
	mustStatus(t, posted, err, 201)
	get := func(key string) *api.Receipt {
		t.Helper()
		r, e := s.client(key).GetReceiptsWithResponse(ctx, b, posted.JSON201.Seq)
		mustStatus(t, r, e, 200)
		if len(r.JSON200.Recipients) != 1 {
			t.Fatalf("receipts: %s", r.Body)
		}
		return &r.JSON200.Recipients[0]
	}
	if hint := get(from).MidturnHint; hint == nil || *hint != "peer_if_supported" {
		t.Fatalf("missing eligible guidance: %v", hint)
	}
	for _, key := range []string{to, maya, sam} {
		if get(key).MidturnHint != nil {
			t.Fatal("non-sender learned peer preference")
		}
	}
	policy := api.MidturnPolicy("owner-only")
	set, e := s.client(maya).SetMidturnPolicyWithResponse(ctx, nil, api.SetMidturnPolicyJSONRequestBody{Policy: &policy})
	mustStatus(t, set, e, 200)
	if hint := get(from).MidturnHint; hint == nil || *hint != "owner_only" {
		t.Fatalf("stale policy guidance: %v", hint)
	}
	foreign, _ := s.joinAs(sam, b, "member", "codex")
	for _, input := range []api.PostMessageRequest{
		{Body: "ordinary", To: ptr([]api.Target{api.Target("@" + target)})},
		{Body: "role urgent", To: ptr([]api.Target{"role:member"}), Urgent: &urgent},
		{Body: "foreign urgent", To: ptr([]api.Target{api.Target("@" + foreign)}), Urgent: &urgent},
	} {
		post, e := s.client(from).PostMessageWithResponse(ctx, b, nil, input)
		mustStatus(t, post, e, 201)
		receipts, e := s.client(from).GetReceiptsWithResponse(ctx, b, post.JSON201.Seq)
		mustStatus(t, receipts, e, 200)
		for _, rc := range receipts.JSON200.Recipients {
			if rc.MidturnHint != nil {
				t.Fatalf("ineligible message got preference: %s", receipts.Body)
			}
		}
	}
}
