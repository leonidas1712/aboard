package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestFreshInboxPeerEligibilityUsesCurrentOwnershipAndDirectTargets(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	me, err := s.client(reviewer).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	sender, err := s.client(writer).GetMeWithResponse(ctx)
	mustStatus(t, sender, err, 200)
	target := "@" + me.JSON200.Name
	urgent := true
	for _, tc := range []struct {
		body   string
		to     []string
		urgent bool
	}{
		{"direct urgent", []string{target}, true},
		{"normal direct", []string{target}, false},
		{"all urgent", []string{"all"}, true},
		{"role urgent", []string{"role:reviewer"}, true},
		{"owner urgent", []string{"owner:alex"}, true},
	} {
		targets := make([]api.Target, len(tc.to))
		for i, v := range tc.to {
			targets[i] = api.Target(v)
		}
		r, callErr := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: tc.body, To: &targets, Urgent: &tc.urgent})
		mustStatus(t, r, callErr, 201)
	}
	peer := s.addHuman("peer")
	peerSeat := s.joinBoard(peer, name, "member", nil)
	to := []api.Target{api.Target(target)}
	r, err := s.client(peerSeat.JSON201.Token).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "foreign urgent", To: &to, Urgent: &urgent})
	mustStatus(t, r, err, 201)
	read := func(wantDirect bool) {
		t.Helper()
		in, callErr := s.client(reviewer).GetInboxWithResponse(ctx, nil)
		mustStatus(t, in, callErr, 200)
		if in.JSON200.BoardId == nil || *in.JSON200.BoardId == "" {
			t.Fatal("missing immutable board id")
		}
		found := false
		for _, m := range in.JSON200.Messages {
			eligible := m.MidturnPeerSenderId != nil
			if m.Body == "direct urgent" {
				found = true
				if eligible != wantDirect {
					t.Fatalf("direct eligibility after membership change: %s", in.Body)
				}
				if eligible && *m.MidturnPeerSenderId != sender.JSON200.Id {
					t.Fatal("cap identity is not sender member id")
				}
			} else if eligible {
				t.Fatalf("nondirect/foreign message eligible: %s", in.Body)
			}
		}
		if !found {
			t.Fatal("missing direct message")
		}
	}
	read(true)
	removed := s.removeAgent(s.owner, name, sender.JSON200.Name, "remove-peer")
	s.want(removed, 200, "")
	read(false)
}
