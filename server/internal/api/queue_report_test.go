package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestQueueReportFencesOldDaemonsAndExpiresWithoutReceiptChanges(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	b := s.newBoard()
	name, token := s.joinAs(s.owner, b, "writer", "codex")
	ctx := context.Background()
	posted, err := s.client(s.owner).PostMessageWithResponse(ctx, b, nil, api.PostMessageRequest{Body: "waiting for this turn", To: &[]string{"@" + name}})
	mustStatus(t, posted, err, 201)
	m := *posted.JSON201
	zero, one, two := int64(0), int64(1), int64(2)
	claim := api.DeliveryQueueReport{Session: "codex:thread", Boot: "first", ExpectedEpoch: &zero}
	ck := api.IdempotencyKey("first-claim")
	first, err := s.client(token).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &ck}, claim)
	mustStatus(t, first, err, 200)
	if first.JSON200.Epoch != 1 || first.JSON200.ExpiresAt != nil {
		t.Fatalf("claim: %s", first.Body)
	}
	entries := []api.QueuedMessageIdentity{{MessageId: m.Id, Seq: m.Seq}}
	update := api.DeliveryQueueReport{Session: claim.Session, Boot: claim.Boot, Epoch: &one, Revision: &one, Messages: &entries}
	uk := api.IdempotencyKey("first-report")
	reported, err := s.client(token).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &uk}, update)
	mustStatus(t, reported, err, 200)
	receipts := func() api.Receipt {
		t.Helper()
		r, e := s.client(s.owner).GetReceiptsWithResponse(ctx, b, m.Seq)
		mustStatus(t, r, e, 200)
		if len(r.JSON200.Recipients) != 1 {
			t.Fatalf("receipts: %s", r.Body)
		}
		return r.JSON200.Recipients[0]
	}
	if r := receipts(); r.State != "pending" || r.Queued == nil {
		t.Fatalf("queued state: %+v", r)
	}
	s.clock.Advance(45 * time.Second)
	if r := receipts(); r.Queued != nil || r.State != "pending" {
		t.Fatalf("expiry: %+v", r)
	}
	replay, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &uk}, update)
	mustStatus(t, replay, e, 200)
	if r := receipts(); r.Queued != nil {
		t.Fatal("replay renewed expired queue")
	}
	next := api.DeliveryQueueReport{Session: claim.Session, Boot: "replacement", ExpectedEpoch: &one}
	replaced, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, next)
	mustStatus(t, replaced, e, 200)
	if replaced.JSON200.Epoch != two {
		t.Fatalf("replacement epoch: %s", replaced.Body)
	}
	for _, key := range []*api.ReportDeliveryQueueParams{nil, {IdempotencyKey: &uk}} {
		stale, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, key, update)
		if errorCode(t, stale, e, 409) != "queue_report_conflict" {
			t.Fatal("old reporter accepted")
		}
	}
	oldClaim, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &ck}, claim)
	if errorCode(t, oldClaim, e, 409) != "queue_report_conflict" {
		t.Fatal("old claim replay accepted")
	}
	current, e := s.client(token).GetDeliveryQueueWithResponse(ctx)
	mustStatus(t, current, e, 200)
	if current.JSON200.Epoch != two || current.JSON200.ExpiresAt != nil {
		t.Fatalf("fence: %s", current.Body)
	}
	inbox, e := s.client(token).GetInboxWithResponse(ctx, nil)
	mustStatus(t, inbox, e, 200)
	if len(inbox.JSON200.Messages) != 1 {
		t.Fatalf("report acknowledged: %s", inbox.Body)
	}
}

func TestQueueReportUsesOnlyOwnVisibleExactIdentitiesAndAllowsAdmissionsBehindCursor(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	b := s.newBoard()
	name, token := s.joinAs(s.owner, b, "writer", "codex")
	posted, e := s.client(s.owner).PostMessageWithResponse(ctx, b, nil, api.PostMessageRequest{Body: "admitted before model sees it", To: &[]string{"@" + name}})
	mustStatus(t, posted, e, 201)
	m := *posted.JSON201
	zero, one, two := int64(0), int64(1), int64(2)
	claim := api.DeliveryQueueReport{Session: "thread", Boot: "boot", ExpectedEpoch: &zero}
	for _, key := range []string{s.owner, s.addHuman("other")} {
		refused, e := s.client(key).ReportDeliveryQueueWithResponse(ctx, nil, claim)
		if errorCode(t, refused, e, 403) != "agent_token_required" {
			t.Fatal("person reported an agent queue")
		}
	}
	first, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, claim)
	mustStatus(t, first, e, 200)
	otherBoard := s.newBoard()
	foreign := s.say(s.owner, otherBoard, "other board's record")
	ids := []api.QueuedMessageIdentity{{MessageId: foreign.Id, Seq: m.Seq}}
	update := api.DeliveryQueueReport{Session: claim.Session, Boot: claim.Boot, Epoch: &one, Revision: &one, Messages: &ids}
	wrong, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, update)
	if errorCode(t, wrong, e, 404) != "message_not_found" {
		t.Fatal("cross-board identity accepted")
	}
	current, e := s.client(token).GetDeliveryQueueWithResponse(ctx)
	mustStatus(t, current, e, 200)
	if current.JSON200.Revision != 0 {
		t.Fatal("refusal consumed revision")
	}
	mustStatus(t, s.ackBoard(token, b, m.Seq), nil, 200)
	ids = []api.QueuedMessageIdentity{{MessageId: m.Id, Seq: m.Seq}}
	reported, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, update)
	mustStatus(t, reported, e, 200)
	r, e := s.client(s.owner).GetReceiptsWithResponse(ctx, b, m.Seq)
	mustStatus(t, r, e, 200)
	if rc := r.JSON200.Recipients[0]; rc.State != "received" || rc.Queued == nil {
		t.Fatalf("admitted queue lost or receipt changed: %s", r.Body)
	}
	duplicate, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, update)
	if errorCode(t, duplicate, e, 409) != "queue_report_conflict" {
		t.Fatal("repeated revision renewed queue")
	}
	ids = []api.QueuedMessageIdentity{}
	update.Revision = &two
	cleared, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, update)
	mustStatus(t, cleared, e, 200)
	r, e = s.client(s.owner).GetReceiptsWithResponse(ctx, b, m.Seq)
	mustStatus(t, r, e, 200)
	if r.JSON200.Recipients[0].Queued != nil {
		t.Fatal("empty report didn't clear queue")
	}
}

func TestQueueReportRotationHidesOldObservationAndPreservesFence(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	b := s.newBoard()
	dlg := s.delegate(s.owner)
	joined := s.joinSession(dlg, b, "codex:queue-rotation", nil)
	s.want(joined, 201, "")
	token, name := joined.str("token"), joined.str("agent", "name")
	posted, e := s.client(s.owner).PostMessageWithResponse(ctx, b, nil, api.PostMessageRequest{Body: "queued", To: &[]string{"@" + name}})
	mustStatus(t, posted, e, 201)
	m := *posted.JSON201
	zero, one := int64(0), int64(1)
	claim := api.DeliveryQueueReport{Session: "codex:queue-rotation", Boot: "before", ExpectedEpoch: &zero}
	first, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, claim)
	mustStatus(t, first, e, 200)
	ids := []api.QueuedMessageIdentity{{MessageId: m.Id, Seq: m.Seq}}
	reported, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, api.DeliveryQueueReport{Session: claim.Session, Boot: claim.Boot, Epoch: &one, Revision: &one, Messages: &ids})
	mustStatus(t, reported, e, 200)
	rotated := s.joinSession(dlg, b, claim.Session, nil)
	s.want(rotated, 200, "")
	if rotated.str("agent", "id") != joined.str("agent", "id") {
		t.Fatal("rotation changed seat")
	}
	r, e := s.client(s.owner).GetReceiptsWithResponse(ctx, b, m.Seq)
	mustStatus(t, r, e, 200)
	if r.JSON200.Recipients[0].Queued != nil {
		t.Fatal("credential rotation retained old queue observation")
	}
	current, e := s.client(rotated.str("token")).GetDeliveryQueueWithResponse(ctx)
	mustStatus(t, current, e, 200)
	if current.JSON200.Epoch != one || current.JSON200.ExpiresAt != nil {
		t.Fatalf("rotation reset fence: %s", current.Body)
	}
	refused, e := s.client(token).GetDeliveryQueueWithResponse(ctx)
	if errorCode(t, refused, e, 401) != "unauthorized" {
		t.Fatal("old token retained queue access")
	}
}

func TestQueueReportsNeverExposeHiddenMessagesAndRemovalEndsReplayAuthority(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	b, writer, reviewer := s.pair("recommended")
	role := "member"
	third, e := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &b, Role: &role})
	mustStatus(t, third, e, 201)
	posted := say(s, writer, b, []string{"@reviewer"}, "private addressed record")
	mustStatus(t, posted, nil, 201)
	m := *posted.JSON201
	zero, one := int64(0), int64(1)
	claim := api.DeliveryQueueReport{Session: "viewer", Boot: "boot", ExpectedEpoch: &zero}
	ids := []api.QueuedMessageIdentity{{MessageId: m.Id, Seq: m.Seq}}
	update := api.DeliveryQueueReport{Session: claim.Session, Boot: claim.Boot, Epoch: &one, Revision: &one, Messages: &ids}
	for _, token := range []string{third.JSON201.Token, reviewer} {
		r, e := s.client(token).ReportDeliveryQueueWithResponse(ctx, nil, claim)
		mustStatus(t, r, e, 200)
	}
	hidden, e := s.client(third.JSON201.Token).ReportDeliveryQueueWithResponse(ctx, nil, update)
	if errorCode(t, hidden, e, 404) != "message_not_found" {
		t.Fatal("hidden message accepted into queue observation")
	}
	key := api.IdempotencyKey("before-removal")
	queued, e := s.client(reviewer).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &key}, update)
	mustStatus(t, queued, e, 200)
	unseen, e := s.client(third.JSON201.Token).GetReceiptsWithResponse(ctx, b, m.Seq)
	if errorCode(t, unseen, e, 404) != "message_not_found" {
		t.Fatal("queue metadata exposed hidden message")
	}
	removed := s.removeAgent(s.owner, b, "reviewer", "remove-queued")
	s.want(removed, 200, "")
	replay, e := s.client(reviewer).ReportDeliveryQueueWithResponse(ctx, &api.ReportDeliveryQueueParams{IdempotencyKey: &key}, update)
	if errorCode(t, replay, e, 403) != "agent_removed" {
		t.Fatal("removed agent retained report authority")
	}
	receipts, e := s.client(writer).GetReceiptsWithResponse(ctx, b, m.Seq)
	mustStatus(t, receipts, e, 200)
	if len(receipts.JSON200.Recipients) != 0 {
		t.Fatal("removed recipient remained visible")
	}
}

func TestOnlyOneQueueReporterCanClaimAnObservedEpoch(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	b := s.newBoard()
	_, token := s.joinAs(s.owner, b, "writer", "codex")
	zero := int64(0)
	type result struct {
		response *api.ReportDeliveryQueueResponse
		err      error
	}
	out := make(chan result, 2)
	for _, boot := range []string{"first", "second"} {
		go func() {
			r, e := s.client(token).ReportDeliveryQueueWithResponse(context.Background(), nil, api.DeliveryQueueReport{Session: "same-session", Boot: boot, ExpectedEpoch: &zero})
			out <- result{r, e}
		}()
	}
	won, conflicted := 0, 0
	for range 2 {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch r.response.StatusCode() {
		case 200:
			won++
		case 409:
			conflicted++
		default:
			t.Fatalf("claim: %d %s", r.response.StatusCode(), r.response.Body)
		}
	}
	if won != 1 || conflicted != 1 {
		t.Fatalf("claims won=%d conflicted=%d", won, conflicted)
	}
}
