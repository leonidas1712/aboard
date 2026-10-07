package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestOwnerTargetsFreezeTheirAgentRecipients(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("recommended")
	me, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	peer := s.addHuman("peer")
	peerSeat := s.joinBoard(peer, name, "member", nil)
	posted := say(s, s.owner, name, []string{"owner:" + me.JSON200.Name}, "For my current agents")
	mustStatus(t, posted, nil, 201)
	if posted.JSON201.To[0] != "owner:"+me.JSON200.Name {
		t.Fatal(posted.JSON201.To)
	}
	receipt, states := s.receipts(s.owner, name, posted.JSON201.Seq)
	if len(states) != 2 || len(receipt.Recipients) != 2 {
		t.Fatalf("recipients: %+v", receipt.Recipients)
	}
	for _, token := range []string{writer, reviewer} {
		inbox, callErr := s.client(token).GetInboxWithResponse(ctx, nil)
		mustStatus(t, inbox, callErr, 200)
		if len(inbox.JSON200.Messages) != 1 {
			t.Fatalf("own agent inbox: %s", inbox.Body)
		}
	}
	role := "member"
	late, err := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &name, Role: &role})
	mustStatus(t, late, err, 201)
	for _, token := range []string{peerSeat.JSON201.Token, late.JSON201.Token} {
		inbox, callErr := s.client(token).GetInboxWithResponse(ctx, nil)
		mustStatus(t, inbox, callErr, 200)
		if len(inbox.JSON200.Messages) != 0 {
			t.Fatalf("unaddressed inbox: %s", inbox.Body)
		}
		timeline, callErr := s.client(token).ListMessagesWithResponse(ctx, name, nil)
		mustStatus(t, timeline, callErr, 200)
		if len(timeline.JSON200.Messages) != 0 {
			t.Fatalf("hidden owner message: %s", timeline.Body)
		}
		message, callErr := s.client(token).GetReceiptsWithResponse(ctx, name, posted.JSON201.Seq)
		mustStatus(t, message, callErr, 404)
		log, callErr := s.client(token).ListEventsWithResponse(ctx, name, nil)
		mustStatus(t, log, callErr, 200)
		if bytes.Contains(log.Body, []byte("For my current agents")) {
			t.Fatalf("hidden event payload: %s", log.Body)
		}
		reaction, callErr := s.client(token).AddReactionWithResponse(ctx, posted.JSON201.Id, "thumbsup", nil)
		mustStatus(t, reaction, callErr, 404)
	}
	_, states = s.receipts(s.owner, name, posted.JSON201.Seq)
	if len(states) != 2 {
		t.Fatal(states)
	}
	missing := say(s, writer, name, []string{"owner:unknown"}, "No discovery")
	mustStatus(t, missing, nil, 422)
}

func TestGuestOwnerTargetsStayOnTheirBoard(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	code := s.guestCode(s.owner, name, "kim")
	mustStatus(t, code, nil, 201)
	guest := s.guestJoin(*code.JSON201.Code)
	mustStatus(t, guest, nil, 201)
	own := say(s, guest.JSON201.Token, name, []string{"owner:kim"}, "Own guest seat")
	mustStatus(t, own, nil, 201)
	receipt, states := s.receipts(s.owner, name, own.JSON201.Seq)
	if len(states) != 0 || len(receipt.Recipients) != 0 {
		t.Fatal(receipt.Recipients)
	}
	allowed := say(s, guest.JSON201.Token, name, []string{"owner:alex"}, "Same board only")
	mustStatus(t, allowed, nil, 201)
	blocked := say(s, guest.JSON201.Token, "hidden-other-board", []string{"owner:alex"}, "No outside access")
	mustStatus(t, blocked, nil, 404)
}
