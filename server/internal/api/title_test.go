package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// titledEvents returns the data of a board's board.titled events, oldest first.
func titledEvents(t *testing.T, s *testServer, boardName string) []map[string]any {
	t.Helper()
	ev, err := s.client(s.owner).ListEventsWithResponse(context.Background(), boardName, nil)
	mustStatus(t, ev, err, 200)
	var page struct {
		Events []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(ev.Body, &page); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range page.Events {
		if e.Type == "board.titled" {
			out = append(out, e.Data)
		}
	}
	return out
}

// A board made with a title keeps it beside its name, and the record says so.
func TestBoardCreatedWithATitle(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	human := s.client(s.owner)
	title := "Payments retry design"
	b, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Title: &title})
	mustStatus(t, b, err, 201)
	if b.JSON201.Title == nil || *b.JSON201.Title != title || b.JSON201.Name != "board" {
		t.Fatalf("created board: name %q, title %v", b.JSON201.Name, b.JSON201.Title)
	}
	g, err := human.GetBoardWithResponse(ctx, "board")
	mustStatus(t, g, err, 200)
	if g.JSON200.Title == nil || *g.JSON200.Title != title {
		t.Fatalf("GET board title = %v", g.JSON200.Title)
	}
	ev, err := human.ListEventsWithResponse(ctx, "board", nil)
	mustStatus(t, ev, err, 200)
	if !strings.Contains(string(ev.Body), `"title":"Payments retry design"`) {
		t.Fatalf("board.created doesn't record the title: %s", ev.Body)
	}

	// Without a title the board has none, and board.created doesn't mention one.
	plain, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{})
	mustStatus(t, plain, err, 201)
	if plain.JSON201.Title != nil {
		t.Fatalf("untitled board has title %q", *plain.JSON201.Title)
	}
}

// An admin changes and removes a board's title; each change is an event, and a change
// to the same title writes nothing.
func TestAdminChangesTheTitle(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	human := s.client(s.owner)

	set := func(title string) *api.UpdateBoardResponse {
		u, err := human.UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Title: &title})
		mustStatus(t, u, err, 200)
		return u
	}
	if u := set("  Docs review  "); u.JSON200.Title == nil || *u.JSON200.Title != "Docs review" {
		t.Fatalf("title after set = %v", u.JSON200.Title)
	}
	set("Docs review")
	if u := set(""); u.JSON200.Title != nil {
		t.Fatalf("title after removing = %q", *u.JSON200.Title)
	}
	got := titledEvents(t, s, boardName)
	if len(got) != 2 {
		t.Fatalf("board.titled events = %v, want 2 (set, removed)", got)
	}
	if got[0]["before"] != nil || got[0]["after"] != "Docs review" || got[1]["before"] != "Docs review" || got[1]["after"] != nil {
		t.Fatalf("board.titled events = %v", got)
	}

	// Title and policy change together in one request.
	title, preset := "Both", api.PolicyPreset("recommended")
	u, err := human.UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Title: &title, Policy: &api.PolicyChange{Preset: &preset}})
	mustStatus(t, u, err, 200)
	if *u.JSON200.Title != "Both" || u.JSON200.Policy.Preset != "recommended" {
		t.Fatalf("after both: title %v, preset %s", u.JSON200.Title, u.JSON200.Policy.Preset)
	}
}

// Only a board's admins change its title: a member and an agent are refused, and a
// title must be one line of at most 80 characters.
func TestOnlyAdminsChangeTheTitle(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	code, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, code, err, 201)
	priya := s.addHuman("priya")
	j, err := s.client(priya).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(t, j, err, 201)

	title := "Mine now"
	change := api.UpdateBoardRequest{Title: &title}
	u, err := s.client(priya).UpdateBoardWithResponse(ctx, boardName, nil, change)
	if c := errorCode(t, u, err, 403); c != "admin_required" {
		t.Fatalf("title change by a member: %s", c)
	}
	u, err = s.client(writer).UpdateBoardWithResponse(ctx, boardName, nil, change)
	if c := errorCode(t, u, err, 403); c != "human_token_required" {
		t.Fatalf("title change by an agent: %s", c)
	}
	for bad, status := range map[string]int{strings.Repeat("x", 81): 400, "two\nlines": 422} {
		u, err = s.client(s.owner).UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Title: &bad})
		if c := errorCode(t, u, err, status); c != "invalid_request" {
			t.Fatalf("title %q: %s", bad, c)
		}
	}
	if got := titledEvents(t, s, boardName); len(got) != 0 {
		t.Fatalf("refused changes wrote events: %v", got)
	}
	u, err = s.client(s.owner).UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{})
	if c := errorCode(t, u, err, 400); c != "invalid_request" {
		t.Fatalf("empty change: %s", c)
	}
}

// GET /v1/me says who a token acts as: a person, their browser, or an agent with its
// board and owner.
func TestMeNamesTheCaller(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")

	me, err := s.client(s.owner).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	if me.JSON200.Kind != api.MeKindHuman || me.JSON200.Name != "alex" || me.JSON200.Board != nil || me.JSON200.Owner != nil || me.JSON200.Browser {
		t.Fatalf("person: %s", me.Body)
	}
	me, err = s.client(s.browserToken(s.owner)).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	if me.JSON200.Kind != api.MeKindHuman || me.JSON200.Name != "alex" || !me.JSON200.Browser {
		t.Fatalf("browser: %s", me.Body)
	}
	me, err = s.client(writer).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	if me.JSON200.Kind != api.MeKindAgent || me.JSON200.Name != "writer" || me.JSON200.Board == nil || *me.JSON200.Board != boardName ||
		me.JSON200.Owner == nil || *me.JSON200.Owner != "alex" {
		t.Fatalf("agent: %s", me.Body)
	}
	me, err = s.client("abh_" + strings.Repeat("z", 43)).GetMeWithResponse(ctx)
	if c := errorCode(t, me, err, 401); c != "unauthorized" {
		t.Fatalf("unknown token: %s", c)
	}
}
