package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// raw makes a request with token through the conformance-checking client and returns
// the status and body.
func (s *testServer) raw(method, path, token string, body any) (status int, text string) {
	s.t.Helper()
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.url+path, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// privateBoard has maya create a private board, post on it and add a join code, and
// returns its name.
func (s *testServer) privateBoard(maya string) string {
	s.t.Helper()
	ctx := context.Background()
	vis := api.BoardVisibility("private")
	tpl := "general"
	b, err := s.client(maya).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl, Visibility: &vis})
	mustStatus(s.t, b, err, 201)
	if b.JSON201.Visibility != "private" || !b.JSON201.OnBoard {
		s.t.Fatalf("new private board: %s", b.Body)
	}
	name := b.JSON201.Name
	if st, body := s.raw("POST", "/v1/boards/"+name+"/messages", maya, map[string]any{"body": "secret plans"}); st != 201 {
		s.t.Fatalf("post: %d %s", st, body)
	}
	return name
}

// Every path to a private board a person isn't on answers exactly as for a board that
// doesn't exist: same status, same code, and the same message with only the name in it.
func TestAPrivateBoardLooksLikeNoBoardToOutsiders(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	name := s.privateBoard(maya)
	const ghost = "no-such-board"
	paths := []struct{ method, path string }{
		{"GET", "/v1/boards/%s"},
		{"GET", "/v1/boards/%s/people"},
		{"GET", "/v1/boards/%s/members"},
		{"GET", "/v1/boards/%s/messages"},
		{"GET", "/v1/boards/%s/events"},
		{"GET", "/v1/boards/%s/threads"},
		{"POST", "/v1/boards/%s/messages"},
		{"POST", "/v1/boards/%s/people"},
		{"DELETE", "/v1/boards/%s/people/maya"},
		{"POST", "/v1/boards/%s/leave"},
		{"POST", "/v1/boards/%s/owners"},
		{"POST", "/v1/boards/%s/visibility"},
		{"POST", "/v1/boards/%s/join-codes"},
		{"PATCH", "/v1/boards/%s"},
		{"PUT", "/v1/boards/%s/members/claude/delivery"},
		{"DELETE", "/v1/boards/%s/members/claude"},
	}
	bodies := map[string]any{
		"/v1/boards/%s/messages":                map[string]any{"body": "hi"},
		"/v1/boards/%s/people":                  map[string]any{"handle": "sam"},
		"/v1/boards/%s/owners":                  map[string]any{"handle": "sam"},
		"/v1/boards/%s/visibility":              map[string]any{"visibility": "open"},
		"/v1/boards/%s/join-codes":              map[string]any{"role": "member"},
		"/v1/boards/%s":                         map[string]any{"title": "x"},
		"/v1/boards/%s/members/claude/delivery": map[string]any{"mode": "off"},
	}
	// sam is a member of the server; alex, its admin, isn't on the board either.
	for who, token := range map[string]string{"a member": sam, "the admin": s.owner} {
		for _, p := range paths {
			var body any
			if p.method != "GET" {
				body = bodies[p.path]
			}
			hidden, hiddenBody := s.raw(p.method, strings.Replace(p.path, "%s", name, 1), token, body)
			missing, missingBody := s.raw(p.method, strings.Replace(p.path, "%s", ghost, 1), token, body)
			if hidden != http.StatusNotFound || hidden != missing ||
				strings.ReplaceAll(hiddenBody, name, ghost) != missingBody {
				t.Errorf("%s %s %s as %s: hidden %d %s, missing %d %s", p.method, p.path, name, who, hidden, hiddenBody, missing, missingBody)
			}
		}
	}
	// Joining by board and role, and a message id on the board, say nothing either.
	st, body := s.raw("POST", "/v1/join", sam, map[string]any{"board": name, "role": "member"})
	if st != 404 || !strings.Contains(body, "board_not_found") {
		t.Errorf("join a private board: %d %s", st, body)
	}
	l, err := s.client(sam).ListBoardsWithResponse(context.Background(), &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, l, err, 200)
	if strings.Contains(string(l.Body), name) {
		t.Errorf("sam's list of every board names the private one: %s", l.Body)
	}
}

func ptr[T any](v T) *T { return &v }

// An admin who isn't on a private board sees that it exists, who made it, when, and how
// many people are on it, and nothing else; and can't add anyone to it, themselves
// included. An admin's agent sees no more than any agent.
func TestAnAdminSeesOnlyThatAPrivateBoardExists(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name := s.privateBoard(maya)
	ctx := context.Background()
	l, err := s.client(s.owner).ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, l, err, 200)
	if l.JSON200.HiddenBoards == nil || len(*l.JSON200.HiddenBoards) != 1 {
		t.Fatalf("admin's hidden boards: %s", l.Body)
	}
	h := (*l.JSON200.HiddenBoards)[0]
	if h.CreatedBy.Handle != "maya" || h.People != 1 || h.Visibility != "private" {
		t.Errorf("hidden board: %+v", h)
	}
	for _, leak := range []string{name, "secret plans", "general"} {
		if strings.Contains(string(l.Body), leak) {
			t.Errorf("admin's list leaks %q: %s", leak, l.Body)
		}
	}
	for _, handle := range []string{"alex", "maya"} {
		if st, body := s.raw("POST", "/v1/boards/"+name+"/people", s.owner, map[string]any{"handle": handle}); st != 404 || !strings.Contains(body, "board_not_found") {
			t.Errorf("admin adding %s: %d %s", handle, st, body)
		}
	}
	// Without all, and for a member, there is no hidden list.
	plain, err := s.client(s.owner).ListBoardsWithResponse(ctx, nil)
	mustStatus(t, plain, err, 200)
	if plain.JSON200.HiddenBoards != nil {
		t.Errorf("hidden boards without all: %s", plain.Body)
	}
	member, err := s.client(maya).ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, member, err, 200)
	if member.JSON200.HiddenBoards == nil || len(*member.JSON200.HiddenBoards) != 0 {
		t.Errorf("a member's hidden boards: %s", member.Body)
	}
	// The admin's agent, on a board of the admin's, gets only its own board.
	_, writer, _ := s.pair("starter")
	agent, err := s.client(writer).ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, agent, err, 200)
	if len(agent.JSON200.Boards) != 1 || (agent.JSON200.HiddenBoards != nil && len(*agent.JSON200.HiddenBoards) != 0) {
		t.Errorf("the admin's agent's list: %s", agent.Body)
	}
}

// Anyone on a board adds people; only owners remove them, make owners or change the
// visibility; the last owner can't leave; agents manage no one.
func TestWhoMayChangeABoardsPeople(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam, kim := s.addHuman("maya"), s.addHuman("sam"), s.addHuman("kim")
	name := s.privateBoard(maya)
	call := func(method, path, token string, body any, want int, code string) string {
		t.Helper()
		st, b := s.raw(method, "/v1/boards/"+name+path, token, body)
		if st != want || (code != "" && !strings.Contains(b, `"code":"`+code+`"`)) {
			t.Fatalf("%s %s: %d %s, want %d %s", method, path, st, b, want, code)
		}
		return b
	}
	call("POST", "/people", maya, map[string]any{"handle": "sam"}, 201, "")
	call("POST", "/people", maya, map[string]any{"handle": "sam"}, 409, "already_on_board")
	call("POST", "/people", maya, map[string]any{"handle": "nobody"}, 404, "person_not_found")
	// sam, a member, adds kim, but can't remove anyone or change the board.
	call("POST", "/people", sam, map[string]any{"handle": "kim"}, 201, "")
	call("DELETE", "/people/kim", sam, nil, 403, "owner_required")
	call("POST", "/owners", sam, map[string]any{"handle": "sam"}, 403, "owner_required")
	call("POST", "/visibility", sam, map[string]any{"visibility": "open"}, 403, "owner_required")
	// The last owner can't leave, or remove themselves.
	call("POST", "/leave", maya, nil, 409, "last_owner")
	call("DELETE", "/people/maya", maya, nil, 409, "last_owner")
	// An owner removes kim, who then can't see the board.
	call("DELETE", "/people/kim", maya, nil, 200, "")
	call("GET", "/people", kim, nil, 404, "board_not_found")
	call("DELETE", "/people/kim", maya, nil, 404, "person_not_on_board")
	// With a second owner, maya may leave.
	if b := call("POST", "/owners", maya, map[string]any{"handle": "sam"}, 200, ""); !strings.Contains(b, `"board_role":"owner"`) {
		t.Fatalf("sam made owner: %s", b)
	}
	people := call("GET", "/people", sam, nil, 200, "")
	if !strings.Contains(people, `"handle":"maya","display_name":null,"board":"`+name+`","name":"maya","member_id"`) {
		t.Logf("people: %s", people)
	}
	call("POST", "/leave", maya, nil, 200, "")
	call("GET", "/messages", maya, nil, 404, "board_not_found")
	// An agent manages no one.
	_, writer, _ := s.pair("starter")
	_ = writer
	j, err := s.client(sam).JoinWithResponse(context.Background(), nil, api.JoinRequest{Board: &name, Role: ptr("member")})
	mustStatus(t, j, err, 201)
	agent := j.JSON201.Token
	call("POST", "/people", agent, map[string]any{"handle": "kim"}, 403, "human_token_required")
	call("DELETE", "/people/sam", agent, nil, 403, "human_token_required")
	call("POST", "/visibility", agent, map[string]any{"visibility": "open"}, 403, "human_token_required")
	call("GET", "/people", agent, nil, 200, "")
}

// Turning a board private keeps exactly its people, cancels its working join codes and
// hides it from everyone else; turning it open again shows how much becomes visible,
// and anyone may then join and read its history.
func TestTurningABoardOpenAndPrivate(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	ctx := context.Background()
	tpl := "general"
	b, err := s.client(maya).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl})
	mustStatus(t, b, err, 201)
	name := b.JSON201.Name
	if b.JSON201.Visibility != "open" {
		t.Fatalf("a new board's visibility: %s", b.JSON201.Visibility)
	}
	for _, body := range []string{"one", "two"} {
		if st, out := s.raw("POST", "/v1/boards/"+name+"/messages", maya, map[string]any{"body": body}); st != 201 {
			t.Fatal(out)
		}
	}
	// On the open board, sam sees it and its people but must join to read.
	if g, err := s.client(sam).GetBoardWithResponse(ctx, name); err != nil || g.JSON200 == nil || g.JSON200.OnBoard {
		t.Fatalf("sam getting the open board: %v %s", err, g.Body)
	}
	if st, body := s.raw("GET", "/v1/boards/"+name+"/messages", sam, nil); st != 403 || !strings.Contains(body, "not_on_board") {
		t.Fatalf("sam reading the open board he isn't on: %d %s", st, body)
	}
	if st, body := s.raw("POST", "/v1/boards/"+name+"/people", sam, map[string]any{"handle": "maya"}); st != 403 || !strings.Contains(body, "not_on_board") {
		t.Fatalf("sam adding someone to a board he isn't on: %d %s", st, body)
	}
	jc, err := s.client(maya).CreateJoinCodeWithResponse(ctx, name, nil, api.CreateJoinCodeRequest{Role: "member"})
	mustStatus(t, jc, err, 201)

	dry := true
	p, err := s.client(maya).SetVisibilityWithResponse(ctx, name, nil, api.SetVisibilityRequest{Visibility: "private", DryRun: &dry})
	mustStatus(t, p, err, 200)
	if !p.JSON200.Changed || p.JSON200.JoinCodesCanceled != 1 || p.JSON200.Reveals != nil {
		t.Fatalf("preview of private: %s", p.Body)
	}
	if g, _ := s.client(maya).GetBoardWithResponse(ctx, name); g.JSON200.Visibility != "open" {
		t.Fatal("a preview changed the board")
	}
	p, err = s.client(maya).SetVisibilityWithResponse(ctx, name, nil, api.SetVisibilityRequest{Visibility: "private"})
	mustStatus(t, p, err, 200)
	if st, _ := s.raw("GET", "/v1/boards/"+name, sam, nil); st != 404 {
		t.Fatalf("sam after private: %d", st)
	}
	code := *jc.JSON201.Code
	if st, body := s.raw("POST", "/v1/join", sam, map[string]any{"code": code}); st != 404 || !strings.Contains(body, "join_code_invalid") {
		t.Fatalf("a join code after private: %d %s", st, body)
	}

	p, err = s.client(maya).SetVisibilityWithResponse(ctx, name, nil, api.SetVisibilityRequest{Visibility: "open", DryRun: &dry})
	mustStatus(t, p, err, 200)
	if p.JSON200.Reveals == nil || p.JSON200.Reveals.Messages != 2 || p.JSON200.Reveals.Files != 0 {
		t.Fatalf("preview of open: %s", p.Body)
	}
	p, err = s.client(maya).SetVisibilityWithResponse(ctx, name, nil, api.SetVisibilityRequest{Visibility: "open"})
	mustStatus(t, p, err, 200)
	again, err := s.client(maya).SetVisibilityWithResponse(ctx, name, nil, api.SetVisibilityRequest{Visibility: "open"})
	mustStatus(t, again, err, 200)
	if again.JSON200.Changed {
		t.Fatalf("setting the same visibility: %s", again.Body)
	}
	// sam joins by adding himself and reads the whole history.
	if st, body := s.raw("POST", "/v1/boards/"+name+"/people", sam, map[string]any{"handle": "sam"}); st != 201 {
		t.Fatalf("sam joining the open board: %d %s", st, body)
	}
	if st, body := s.raw("GET", "/v1/boards/"+name+"/messages", sam, nil); st != 200 || !strings.Contains(body, `"one"`) || !strings.Contains(body, `"two"`) {
		t.Fatalf("sam reading history: %d %s", st, body)
	}

	// The record names who did each change.
	ev, err := s.client(maya).ListEventsWithResponse(ctx, name, &api.ListEventsParams{Limit: ptr(200)})
	mustStatus(t, ev, err, 200)
	var page struct {
		Events []struct {
			Type  string         `json:"type"`
			Actor map[string]any `json:"actor"`
			Data  map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(ev.Body, &page); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Events {
		switch e.Type {
		case "board.visibility_changed", "person.added", "joincode.revoked":
			actor, _ := e.Actor["name"].(string)
			what := "-"
			if a, ok := e.Data["after"].(string); ok {
				what = a
			} else if n, ok := e.Data["name"].(string); ok {
				what = n
			}
			got = append(got, e.Type+" by "+actor+" to "+what)
		}
	}
	want := []string{
		"board.visibility_changed by maya to private", "joincode.revoked by maya to -",
		"board.visibility_changed by maya to open", "person.added by sam to sam",
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Fatalf("record:\n got %v\nwant %v", got, want)
	}
}

// Who may create boards is a server setting only an admin's own key changes.
func TestTheBoardCreationSetting(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	ctx := context.Background()
	g, err := s.client(maya).GetSettingsWithResponse(ctx)
	mustStatus(t, g, err, 200)
	if g.JSON200.BoardCreation != "members" {
		t.Fatalf("default: %s", g.Body)
	}
	admins := api.BoardCreation("admins")
	u, err := s.client(maya).UpdateSettingsWithResponse(ctx, nil, api.ServerSettingsChange{BoardCreation: &admins})
	mustStatus(t, u, err, 403)
	u, err = s.client(s.owner).UpdateSettingsWithResponse(ctx, nil, api.ServerSettingsChange{BoardCreation: &admins})
	mustStatus(t, u, err, 200)
	tpl := "general"
	b, err := s.client(maya).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl})
	mustStatus(t, b, err, 403)
	if !strings.Contains(string(b.Body), "board_creation_restricted") {
		t.Fatalf("a member creating a board: %s", b.Body)
	}
	b, err = s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl})
	mustStatus(t, b, err, 201)
}
