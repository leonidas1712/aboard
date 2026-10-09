package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/blobs/disk"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// testServer is a real server in-process: real SQLite, fake clock.
type testServer struct {
	t     *testing.T
	url   string
	srv   *httptest.Server
	clock *clock.Fake
	st    *sqlite.Store
	path  string // the database file
	key   []byte
	owner string // the first human's token
	// shutdown tells the handler the server is shutting down, which ends event streams.
	shutdown context.CancelFunc
}

// newTestServer starts the server; opts change its API options.
func newTestServer(t *testing.T, opts ...func(*api.Options)) *testServer {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "aboard.db")
	st, err := sqlite.Open(ctx, path, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key := []byte("test digest key")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	blobs, err := disk.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	svc := board.New(st, notify.NewInProcess(), clk, ids.New(rand.Reader), key, board.Config{Blobs: blobs, ServerID: "srv_01M3W33B00TESTSERVER000000", Mode: "local", JoinHost: "localhost", IssuerURL: "http://127.0.0.1:7400"}, log)
	owner, err := svc.BootstrapOwner(ctx, "alex", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	shutdown, startShutdown := context.WithCancel(ctx)
	t.Cleanup(startShutdown)
	o := api.Options{Service: svc, Responses: st, Clock: clk, Log: log, Version: "test", JoinsPerMinute: 5, Shutdown: shutdown}
	for _, opt := range opts {
		opt(&o)
	}
	h, err := api.NewHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testServer{t: t, url: srv.URL, srv: srv, clock: clk, st: st, path: path, key: key, owner: owner, shutdown: startShutdown}
}

// addHuman brings another person onto the server the way people join it, through an
// invite from the first person, an admin, and returns their access key.
func (s *testServer) addHuman(name string) string {
	s.t.Helper()
	ctx := context.Background()
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{})
	if err != nil || inv.JSON201 == nil {
		s.t.Fatalf("invite %s: %v %s", name, err, inv.Body)
	}
	c, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: name, KeyName: "laptop"})
	if err != nil || c.JSON201 == nil {
		s.t.Fatalf("connect %s: %v %s", name, err, c.Body)
	}
	return c.JSON201.Key.Token
}

// joinBoard has the board's owner add the person whose key is token to the board, then
// joins an agent of theirs in role with a pairing code they make themselves, as a
// teammate's own sessions join.
func (s *testServer) joinBoard(token, boardName, role string, harness *string) *api.JoinResponse {
	s.t.Helper()
	ctx := context.Background()
	me, err := s.client(token).GetMeWithResponse(ctx)
	mustStatus(s.t, me, err, 200)
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: me.JSON200.Name})
	mustStatus(s.t, added, err, 201)
	code, err := s.client(token).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: role})
	mustStatus(s.t, code, err, 201)
	j, err := s.client(token).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code, Harness: harness})
	mustStatus(s.t, j, err, 201)
	return j
}

// client returns an API client for token whose every response is checked against the
// spec, so each test is also a conformance test.
func (s *testServer) client(token string) *api.ClientWithResponses {
	s.t.Helper()
	c, err := api.NewClientWithResponses(s.url,
		api.WithHTTPClient(s.httpClient()),
		api.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			return nil
		}))
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// httpClient returns an HTTP client whose every response is checked against the spec.
func (s *testServer) httpClient() *http.Client {
	s.t.Helper()
	router, err := specRouter()
	if err != nil {
		s.t.Fatal(err)
	}
	return &http.Client{Transport: conformance{t: s.t, router: router}}
}

// specRouter parses the OpenAPI spec and builds its router once for the whole package:
// parsing it took most of each request's time, and the router is only read.
var specRouter = sync.OnceValues(func() (routers.Router, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil
	return gorillamux.NewRouter(spec)
})

type conformance struct {
	t      *testing.T
	router routers.Router
}

func (c conformance) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	signIn := (req.Method == http.MethodPost && req.URL.Path == "/v1/browser-sessions") ||
		(req.Method == http.MethodDelete && req.URL.Path == "/v1/me/browser-session")
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 && !signIn {
		c.t.Errorf("%s %s set a cookie: %q; only signing a browser in or out sets one", req.Method, req.URL.Path, cookies)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		// An event stream doesn't end, so its body is left for the test to read; the
		// status and content type are still checked against the spec.
		c.checkStream(req, resp)
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		c.t.Errorf("conformance: %s %s is not in the spec: %v", req.Method, req.URL.Path, err)
		return resp, nil
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header,
		Body: io.NopCloser(bytes.NewReader(body)), Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}
	if err := openapi3filter.ValidateResponse(req.Context(), out); err != nil {
		c.t.Errorf("conformance: %s %s → %d does not match the spec: %v\nbody: %s", req.Method, req.URL.Path, resp.StatusCode, err, body)
	}
	return resp, nil
}

// checkStream checks an event stream's status and content type against the spec.
func (c conformance) checkStream(req *http.Request, resp *http.Response) {
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		c.t.Errorf("conformance: %s %s is not in the spec: %v", req.Method, req.URL.Path, err)
		return
	}
	in := &openapi3filter.RequestValidationInput{
		Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
	out := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: resp.StatusCode, Header: resp.Header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true, ExcludeResponseBody: true},
	}
	if err := openapi3filter.ValidateResponse(req.Context(), out); err != nil {
		c.t.Errorf("conformance: %s %s → %d does not match the spec: %v", req.Method, req.URL.Path, resp.StatusCode, err)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if r := route.Operation.Responses.Status(resp.StatusCode); r == nil || r.Value.Content.Get(ct) == nil {
		c.t.Errorf("conformance: %s %s → %d: the spec has no %q response", req.Method, req.URL.Path, resp.StatusCode, ct)
	}
}

// pair creates a board from writer-reviewer on preset and joins a writer and a reviewer.
func (s *testServer) pair(preset string) (boardName, writer, reviewer string) {
	s.t.Helper()
	ctx := context.Background()
	human := s.client(s.owner)
	p := api.PolicyPreset(preset)
	tpl := "writer-reviewer"
	b, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl, Preset: &p})
	mustStatus(s.t, b, err, 201)
	boardName = b.JSON201.Name
	join := func(role string) string {
		r, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &boardName, Role: &role})
		mustStatus(s.t, r, err, 201)
		return r.JSON201.Token
	}
	return boardName, join("writer"), join("reviewer")
}

type response interface {
	StatusCode() int
}

func mustStatus(t *testing.T, r response, err error, want int) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode() != want {
		t.Fatalf("status %d, want %d: %s", r.StatusCode(), want, bodyOf(r))
	}
}

// bodyOf returns the raw body of a generated response, which every response type
// keeps in its Body field.
func bodyOf(r any) string {
	v := reflect.ValueOf(r)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if f := v.FieldByName("Body"); f.IsValid() {
		return string(f.Bytes())
	}
	return fmt.Sprintf("%v", r)
}

// errorCode returns the code of an error response, failing if status differs.
func errorCode(t *testing.T, r response, err error, wantStatus int) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode() != wantStatus {
		t.Fatalf("status %d, want %d: %s", r.StatusCode(), wantStatus, bodyOf(r))
	}
	var e api.Error
	if err := json.Unmarshal([]byte(bodyOf(r)), &e); err != nil {
		t.Fatalf("not an error body (%v): %s", err, bodyOf(r))
	}
	return string(e.Error.Code)
}

func say(s *testServer, token, boardName string, to []string, body string) *api.PostMessageResponse {
	s.t.Helper()
	req := api.PostMessageRequest{Body: body}
	if to != nil {
		targets := make([]api.Target, len(to))
		copy(targets, to)
		req.To = &targets
	}
	r, err := s.client(token).PostMessageWithResponse(context.Background(), boardName, nil, req)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func TestAgentCannotBroadcastWhenRoleLacksPermission(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	// The writer-reviewer roles grant broadcast, so use a board whose agents only have the
	// default member role.
	ctx := context.Background()
	human := s.client(s.owner)
	p := api.PolicyPreset("recommended")
	b, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Preset: &p})
	mustStatus(t, b, err, 201)
	role := "member"
	j, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &b.JSON201.Name, Role: &role})
	mustStatus(t, j, err, 201)

	r := say(s, j.JSON201.Token, b.JSON201.Name, nil, "hello everyone")
	if code := errorCode(t, r, nil, 403); code != "broadcast_not_allowed" {
		t.Fatalf("code %s", code)
	}
	// Addressing someone is still allowed, and humans may always broadcast.
	mustStatus(t, say(s, j.JSON201.Token, b.JSON201.Name, []string{"@alex"}, "hi"), nil, 201)
	mustStatus(t, say(s, s.owner, b.JSON201.Name, []string{"all"}, "hi all"), nil, 201)
}

func TestStarterPolicyLetsAnyRoleBroadcast(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	human := s.client(s.owner)
	b, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{})
	mustStatus(t, b, err, 201)
	if b.JSON201.Policy.Preset != "starter" {
		t.Fatalf("default preset %s", b.JSON201.Policy.Preset)
	}
	role := "member"
	j, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &b.JSON201.Name, Role: &role})
	mustStatus(t, j, err, 201)
	mustStatus(t, say(s, j.JSON201.Token, b.JSON201.Name, nil, "hello everyone"), nil, 201)
}

func TestUrgentNeedsPermissionUnderRecommended(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	human := s.client(s.owner)
	p := api.PolicyPreset("recommended")
	b, err := human.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Preset: &p})
	mustStatus(t, b, err, 201)
	role := "member"
	j, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &b.JSON201.Name, Role: &role})
	mustStatus(t, j, err, 201)

	urgent := true
	r, err := s.client(j.JSON201.Token).PostMessageWithResponse(ctx, b.JSON201.Name, nil,
		api.PostMessageRequest{Body: "now!", To: &[]api.Target{"@alex"}, Urgent: &urgent})
	if code := errorCode(t, r, err, 403); code != "urgent_not_allowed" {
		t.Fatalf("code %s", code)
	}
	r, err = human.PostMessageWithResponse(ctx, b.JSON201.Name, nil, api.PostMessageRequest{Body: "now!", Urgent: &urgent})
	mustStatus(t, r, err, 201)
	if !r.JSON201.Urgent {
		t.Fatal("urgent flag not stored")
	}
}

func TestAddressedVisibilityHidesOtherAgentsMessages(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("recommended")
	human := s.client(s.owner)
	role := "member"
	third, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &boardName, Role: &role})
	mustStatus(t, third, err, 201)

	mustStatus(t, say(s, writer, boardName, []string{"@reviewer"}, "private note for the reviewer"), nil, 201)

	timeline := func(token string) int {
		r, err := s.client(token).ListMessagesWithResponse(ctx, boardName, nil)
		mustStatus(t, r, err, 200)
		return len(r.JSON200.Messages)
	}
	if n := timeline(reviewer); n != 1 {
		t.Fatalf("recipient sees %d messages", n)
	}
	if n := timeline(third.JSON201.Token); n != 0 {
		t.Fatalf("bystander sees %d messages", n)
	}
	if n := timeline(s.owner); n != 1 {
		t.Fatalf("human sees %d messages", n)
	}

	// The bystander gets the event without its payload, and the chain still verifies.
	r, err := s.client(third.JSON201.Token).ListEventsWithResponse(ctx, boardName, nil)
	mustStatus(t, r, err, 200)
	var page struct {
		Events []events.Event `json:"events"`
	}
	if err := json.Unmarshal(r.Body, &page); err != nil {
		t.Fatal(err)
	}
	v := events.NewVerifier()
	if p := v.Add(page.Events); p != nil {
		t.Fatalf("chain problem %+v", p)
	}
	if v.Withheld != 1 {
		t.Fatalf("withheld %d events, want 1", v.Withheld)
	}
}

func TestTimelineFiltersPageBothWays(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	post := func(token string, to []string, body string) int {
		r := say(s, token, boardName, to, body)
		mustStatus(t, r, nil, 201)
		return r.JSON201.Seq
	}
	a := post(writer, []string{"@reviewer"}, "a")
	b := post(reviewer, nil, "b")
	c := post(writer, nil, "c")
	d := post(writer, nil, "d")

	type want struct {
		seqs             []int
		prevBefore, next *int
	}
	page := func(token string, p api.ListMessagesParams, w want) {
		t.Helper()
		r, err := s.client(token).ListMessagesWithResponse(ctx, boardName, &p)
		mustStatus(t, r, err, 200)
		got := []int{}
		for _, m := range r.JSON200.Messages {
			got = append(got, m.Seq)
		}
		if !reflect.DeepEqual(got, w.seqs) || !reflect.DeepEqual(r.JSON200.PrevBefore, w.prevBefore) || !reflect.DeepEqual(r.JSON200.NextAfter, w.next) {
			t.Errorf("seqs %v prev_before %v next_after %v, want %v %v %v", got, r.JSON200.PrevBefore, r.JSON200.NextAfter, w.seqs, w.prevBefore, w.next)
		}
	}
	yes, two, from := true, 2, "writer"
	one, beforeC, afterA, afterD := 1, c, a, d
	page(reviewer, api.ListMessagesParams{From: &from, Newest: &yes, Limit: &two}, want{[]int{c, d}, &c, nil})
	page(reviewer, api.ListMessagesParams{From: &from, Newest: &yes, Limit: &two, Before: &beforeC}, want{[]int{a}, nil, &a})
	page(reviewer, api.ListMessagesParams{From: &from, After: &afterA, Limit: &one}, want{[]int{c}, &c, &c})
	page(reviewer, api.ListMessagesParams{From: &from, After: &afterD}, want{[]int{}, nil, nil})
	role := "reviewer"
	page(writer, api.ListMessagesParams{Role: &role}, want{[]int{b}, nil, nil})
	page(reviewer, api.ListMessagesParams{ToMe: &yes}, want{[]int{a, c, d}, nil, nil})

	nobody := "nobody"
	r, err := s.client(reviewer).ListMessagesWithResponse(ctx, boardName, &api.ListMessagesParams{From: &nobody})
	if code := errorCode(t, r, err, 404); code != "member_not_found" {
		t.Fatalf("unknown from: code %s", code)
	}
	r, err = s.client(reviewer).ListMessagesWithResponse(ctx, boardName, &api.ListMessagesParams{Role: &nobody})
	if code := errorCode(t, r, err, 404); code != "role_not_found" {
		t.Fatalf("unknown role: code %s", code)
	}
}

func TestIdempotentPostIsReplayedNotDuplicated(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	c := s.client(writer)
	key := "retry-1"
	post := func(body string) *api.PostMessageResponse {
		r, err := c.PostMessageWithResponse(ctx, boardName, &api.PostMessageParams{IdempotencyKey: &key}, api.PostMessageRequest{Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first, second := post("once"), post("once")
	mustStatus(t, first, nil, 201)
	mustStatus(t, second, nil, 201)
	if first.JSON201.Id != second.JSON201.Id || second.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry was not replayed: %s vs %s", first.JSON201.Id, second.JSON201.Id)
	}
	tl, err := c.ListMessagesWithResponse(ctx, boardName, nil)
	mustStatus(t, tl, err, 200)
	if len(tl.JSON200.Messages) != 1 {
		t.Fatalf("%d messages stored, want 1", len(tl.JSON200.Messages))
	}
	if code := errorCode(t, post("different"), nil, 422); code != "idempotency_conflict" {
		t.Fatalf("code %s", code)
	}
}

func TestJoinCodesExpireAndCanBeRevoked(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	human := s.client(s.owner)
	newCode := func() *api.CreateJoinCodeResponse {
		r, err := human.CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
		mustStatus(t, r, err, 201)
		return r
	}
	join := func(code string) (*api.JoinResponse, error) {
		return human.JoinWithResponse(ctx, nil, api.JoinRequest{Code: &code})
	}

	multi := newCode()
	for range 2 {
		r, err := join(*multi.JSON201.Code)
		mustStatus(t, r, err, 201)
	}

	revoked := newCode()
	rv, err := human.RevokeJoinCodeWithResponse(ctx, boardName, revoked.JSON201.Id, nil)
	mustStatus(t, rv, err, 200)
	r, err := join(*revoked.JSON201.Code)
	if code := errorCode(t, r, err, 404); code != "join_code_invalid" {
		t.Fatalf("revoked code: %s", code)
	}

	s.clock.Advance(24*time.Hour + time.Second)
	r, err = join(*multi.JSON201.Code)
	if code := errorCode(t, r, err, 404); code != "join_code_invalid" {
		t.Fatalf("expired code: %s", code)
	}
}

// A pairing code lets in only its maker's own sessions: another person on the server
// can't redeem it, whether or not they are on the board, and nothing is written; once
// on the board, they join with a code of their own.
func TestAPairingCodeAdmitsOnlyItsMakersSessions(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	code, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, code, err, 201)
	if code.JSON201.Kind != api.JoinCodeKindPairing || code.JSON201.Guest != nil {
		t.Fatalf("a code without guest: %+v", code.JSON201)
	}

	sam := s.addHuman("sam")
	head := s.boardHead(s.owner, boardName).Seq
	j, err := s.client(sam).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	if got := errorCode(t, j, err, 403); got != "join_code_not_yours" || !strings.Contains(j.JSON403.Error.Hint, "aboard board add @sam --board "+boardName) {
		t.Fatalf("sam redeeming alex's code: %s", bodyOf(j))
	}
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: "sam"})
	mustStatus(t, added, err, 201)
	j, err = s.client(sam).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	if got := errorCode(t, j, err, 403); got != "join_code_not_yours" {
		t.Fatalf("sam, on the board, redeeming alex's code: %s", got)
	}
	if after := s.boardHead(s.owner, boardName).Seq; after != head+1 {
		t.Fatalf("refused joins wrote to the record: head %d, then %d with only person.added between", head, after)
	}

	// The code still works for alex's own sessions. (A minute passes, past the join limit.)
	s.clock.Advance(time.Minute)
	mine, err := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(t, mine, err, 201)
	own, err := s.client(sam).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, own, err, 201)
	j, err = s.client(sam).JoinWithResponse(ctx, nil, api.JoinRequest{Code: own.JSON201.Code})
	mustStatus(t, j, err, 201)
	if j.JSON201.Agent.Owner == nil || *j.JSON201.Agent.Owner != "sam" {
		t.Fatalf("sam's agent %+v", j.JSON201.Agent)
	}
}

func TestInboxWaitWakesWhenAMessageArrives(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	wait := 30
	got := make(chan *api.GetInboxResponse, 1)
	go func() {
		r, err := s.client(reviewer).GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	mustStatus(t, say(s, writer, boardName, []string{"role:reviewer"}, "ping"), nil, 201)
	r := <-got
	mustStatus(t, r, nil, 200)
	if len(r.JSON200.Messages) != 1 || r.JSON200.Messages[0].Body != "ping" {
		t.Fatalf("inbox %+v", r.JSON200.Messages)
	}
}

func TestInboxWaitReturnsEmptyAtTimeout(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, _, reviewer := s.pair("starter")
	wait := 30
	got := make(chan *api.GetInboxResponse, 1)
	go func() {
		r, err := s.client(reviewer).GetInboxWithResponse(context.Background(), &api.GetInboxParams{Wait: &wait})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	// Keep moving the fake clock until the waiting request has registered its timer and
	// returned.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.clock.Advance(31 * time.Second)
		select {
		case r := <-got:
			mustStatus(t, r, nil, 200)
			if len(r.JSON200.Messages) != 0 {
				t.Fatalf("got %d messages", len(r.JSON200.Messages))
			}
			return
		case <-time.After(10 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("inbox wait never returned")
			}
		}
	}
}

func TestInboxHoldsOnlyMessagesAddressedToTheAgentSinceItJoined(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	mustStatus(t, say(s, writer, boardName, []string{"@writer"}, "note to self"), nil, 201)
	mustStatus(t, say(s, writer, boardName, nil, "before tester joined"), nil, 201)
	role := "member"
	tester, err := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &boardName, Role: &role})
	mustStatus(t, tester, err, 201)
	mustStatus(t, say(s, writer, boardName, nil, "after tester joined"), nil, 201)

	bodies := func(token string) []string {
		r, err := s.client(token).GetInboxWithResponse(ctx, nil)
		mustStatus(t, r, err, 200)
		var out []string
		for _, m := range r.JSON200.Messages {
			out = append(out, m.Body)
		}
		return out
	}
	if got := strings.Join(bodies(reviewer), "|"); got != "before tester joined|after tester joined" {
		t.Fatalf("reviewer inbox: %s", got)
	}
	if got := strings.Join(bodies(tester.JSON201.Token), "|"); got != "after tester joined" {
		t.Fatalf("new agent inbox: %s", got)
	}
	if got := bodies(writer); len(got) != 0 {
		t.Fatalf("sender sees its own messages: %v", got)
	}
}

func TestAckMovesForwardOnlyAndNotPastTheHead(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	mustStatus(t, say(s, writer, boardName, nil, "one"), nil, 201)
	m := say(s, writer, boardName, nil, "two")
	mustStatus(t, m, nil, 201)
	c := s.client(reviewer)
	ack := func(upTo int) *api.AckInboxResponse {
		r, err := c.AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: upTo})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := ack(m.JSON201.Seq)
	mustStatus(t, r, nil, 200)
	if r.JSON200.Cursor != m.JSON201.Seq {
		t.Fatalf("cursor %d", r.JSON200.Cursor)
	}
	r = ack(1)
	mustStatus(t, r, nil, 200)
	if r.JSON200.Cursor != m.JSON201.Seq {
		t.Fatalf("cursor moved back to %d", r.JSON200.Cursor)
	}
	if code := errorCode(t, ack(m.JSON201.Seq+10), nil, 422); code != "ack_out_of_range" {
		t.Fatalf("code %s", code)
	}
	in, err := c.GetInboxWithResponse(ctx, nil)
	mustStatus(t, in, err, 200)
	if len(in.JSON200.Messages) != 0 {
		t.Fatalf("acknowledged messages still in inbox: %d", len(in.JSON200.Messages))
	}
}

func TestAgentsCannotSeeOtherBoardsOrDoHumanOnlyThings(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	first, writer, _ := s.pair("starter")
	second, _, _ := s.pair("starter")
	if first == second {
		t.Fatal("boards share a name")
	}
	c := s.client(writer)
	r, err := c.GetBoardWithResponse(ctx, second)
	if code := errorCode(t, r, err, 404); code != "board_not_found" {
		t.Fatalf("other board: %s", code)
	}
	preset := api.PolicyPreset("recommended")
	u, err := c.UpdateBoardWithResponse(ctx, first, nil, api.UpdateBoardRequest{Policy: &api.PolicyChange{Preset: &preset}})
	if code := errorCode(t, u, err, 403); code != "human_token_required" {
		t.Fatalf("policy change by agent: %s", code)
	}
	role := "writer"
	j, err := c.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &first, Role: &role})
	if code := errorCode(t, j, err, 403); code != "human_token_required" {
		t.Fatalf("join by agent: %s", code)
	}
	i, err := s.client(s.owner).GetInboxWithResponse(ctx, nil)
	if code := errorCode(t, i, err, 403); code != "agent_token_required" {
		t.Fatalf("inbox by human: %s", code)
	}
}

func TestPolicyChangeIsRecordedAndApplied(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, _, _ := s.pair("starter")
	human := s.client(s.owner)
	preset := api.PolicyPreset("recommended")
	u, err := human.UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Policy: &api.PolicyChange{Preset: &preset}})
	mustStatus(t, u, err, 200)
	if u.JSON200.Policy.Visibility != "addressed" || u.JSON200.Policy.Broadcast != "granted" || u.JSON200.Policy.Urgent != "granted" {
		t.Fatalf("policy %+v", u.JSON200.Policy)
	}
	ev, err := human.ListEventsWithResponse(ctx, boardName, nil)
	mustStatus(t, ev, err, 200)
	if !strings.Contains(string(ev.Body), `"type":"board.policy_changed"`) {
		t.Fatal("no board.policy_changed event")
	}
}

func TestBadTargetsAreRejected(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	boardName, writer, _ := s.pair("starter")
	tests := []struct {
		to   []string
		code string
	}{
		{[]string{"@nobody"}, "unknown_recipient"},
		{[]string{"role:nobody"}, "role_not_found"},
		{[]string{"all", "@reviewer"}, "invalid_target"},
	}
	for _, tt := range tests {
		if code := errorCode(t, say(s, writer, boardName, tt.to, "x"), nil, 422); code != tt.code {
			t.Errorf("to %v: code %s, want %s", tt.to, code, tt.code)
		}
	}
}

func TestInvalidRequestsGetTheErrorShape(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	boardName, writer, _ := s.pair("starter")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, s.url+"/v1/boards/"+boardName+"/messages", strings.NewReader(`{"body": 5}`))
	req.Header.Set("Authorization", "Bearer "+writer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var e api.Error
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 || e.Error.Code != "invalid_request" || e.Error.Hint == "" {
		t.Fatalf("status %d, error %+v", resp.StatusCode, e.Error)
	}
	r, err := s.client("").GetBoardWithResponse(context.Background(), boardName)
	if code := errorCode(t, r, err, 401); code != "unauthorized" {
		t.Fatalf("no token: %s", code)
	}
}

func TestJoinIsRateLimited(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	human := s.client(s.owner)
	code := "AAA-AAA"
	var last *api.JoinResponse
	for range 6 {
		r, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Code: &code})
		if err != nil {
			t.Fatal(err)
		}
		last = r
	}
	if c := errorCode(t, last, nil, 429); c != "rate_limited" {
		t.Fatalf("code %s", c)
	}
	s.clock.Advance(time.Minute)
	r, err := human.JoinWithResponse(ctx, nil, api.JoinRequest{Code: &code})
	if c := errorCode(t, r, err, 404); c != "join_code_invalid" {
		t.Fatalf("after a minute: %s", c)
	}
}

func TestOperationsThisServerDoesNotProvideReturnNotImplemented(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	m := say(s, writer, boardName, nil, "hello")
	mustStatus(t, m, nil, 201)
	c := s.client(writer)

	status, err := c.GetMessageWithResponse(ctx, m.JSON201.Id)
	if code := errorCode(t, status, err, 501); code != "not_implemented" {
		t.Fatalf("message status: %s", code)
	}
}

// An agent that has seen its unread messages can wait for the next one with after,
// without acknowledging the ones before it.
func TestInboxWaitAfterSkipsMessagesAlreadySeen(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	first := say(s, writer, boardName, []string{"role:reviewer"}, "first")
	mustStatus(t, first, nil, 201)
	wait, after := 30, first.JSON201.Seq
	got := make(chan *api.GetInboxResponse, 1)
	go func() {
		r, err := s.client(reviewer).GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait, After: &after})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	mustStatus(t, say(s, writer, boardName, []string{"role:reviewer"}, "second"), nil, 201)
	r := <-got
	mustStatus(t, r, nil, 200)
	if len(r.JSON200.Messages) != 1 || r.JSON200.Messages[0].Body != "second" {
		t.Fatalf("inbox after %d: %+v", after, r.JSON200.Messages)
	}
	if r.JSON200.Cursor >= first.JSON201.Seq {
		t.Fatalf("waiting after a message moved the cursor to %d", r.JSON200.Cursor)
	}
	all, err := s.client(reviewer).GetInboxWithResponse(ctx, nil)
	mustStatus(t, all, err, 200)
	if len(all.JSON200.Messages) != 2 {
		t.Fatalf("the inbox without after holds %d messages, want both", len(all.JSON200.Messages))
	}
}
