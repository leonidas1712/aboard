package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestAGuestCodeCannotIssueAnotherKeyForAnExistingPerson(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	firstBoard, _, _ := s.pair("starter")
	firstCode := s.guestCode(s.owner, firstBoard, "kim")
	mustStatus(t, firstCode, nil, 201)
	first := s.guestJoin(*firstCode.JSON201.Code)
	mustStatus(t, first, nil, 201)
	name := "other-board"
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &name, Template: ptr("writer-reviewer")})
	mustStatus(t, b, err, 201)
	next := s.guestCode(s.owner, name, "kim")
	mustStatus(t, next, nil, 201)
	wantCode(t, s.guestJoin(*next.JSON201.Code), 404, "join_code_invalid")
	joined, err := s.client(first.JSON201.Key.Token).JoinWithResponse(ctx, nil, api.JoinRequest{Code: next.JSON201.Code})
	mustStatus(t, joined, err, 201)
	me, err := s.client(first.JSON201.Key.Token).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	if me.JSON200.Name != "kim" {
		t.Fatalf("guest identity changed: %s", bodyOf(me))
	}
}

func TestOutstandingGuestCodesDoNotBecomeIdentityProof(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	firstBoard, _, _ := s.pair("starter")
	name := "other-board"
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &name, Template: ptr("writer-reviewer")})
	mustStatus(t, b, err, 201)
	firstCode := s.guestCode(s.owner, firstBoard, "kim")
	next := s.guestCode(s.owner, name, "kim")
	mustStatus(t, firstCode, nil, 201)
	mustStatus(t, next, nil, 201)
	first := s.guestJoin(*firstCode.JSON201.Code)
	mustStatus(t, first, nil, 201)
	wantCode(t, s.guestJoin(*next.JSON201.Code), 404, "join_code_invalid")
	// It was issued for a new identity, not for the identity the other code created.
	r, err := s.client(first.JSON201.Key.Token).JoinWithResponse(ctx, nil, api.JoinRequest{Code: next.JSON201.Code})
	if code := errorCode(t, r, err, 404); code != "join_code_invalid" {
		t.Fatalf("code: %s", code)
	}
}
