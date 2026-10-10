package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

// lifecycleBoard is one board the lifecycle fake keeps.
type lifecycleBoard struct {
	id, name, lifecycle, visibility string
	// readable says the person's key may read the board; an outside admin's may not.
	readable bool
}

// lifecycleServer is a server shaped by spec/openapi.yaml's board lifecycle contract,
// for the CLI's side of it: listing by lifecycle with archived_count, and archive,
// restore and delete by name or id. The person's key is abh_person; an agent's token
// is aba_agent, which may archive and restore its own board but never delete.
type lifecycleServer struct {
	*httptest.Server
	mu       sync.Mutex
	boards   []*lifecycleBoard
	requests []string
}

func newLifecycleServer(t *testing.T, boards ...*lifecycleBoard) *lifecycleServer {
	t.Helper()
	s := &lifecycleServer{boards: boards}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *lifecycleServer) find(sel string) *lifecycleBoard {
	for _, b := range s.boards {
		if (b.name == sel && b.readable) || b.id == sel {
			return b
		}
	}
	return nil
}

func (s *lifecycleServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	line := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		line += "?" + r.URL.RawQuery
	}
	s.requests = append(s.requests, line)
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	fail := func(status int, code, hint string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": "refused: " + code, "hint": hint}})
	}
	if r.Method == http.MethodPost && r.Header.Get("Idempotency-Key") == "" {
		fail(http.StatusBadRequest, "invalid_request", "send an Idempotency-Key")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	switch {
	case r.URL.Path == "/v1/info":
		_ = json.NewEncoder(w).Encode(map[string]string{"version": version, "mode": "team", "name": "team", "server_id": "srv_test"})
	case r.URL.Path == "/v1/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "alex", "kind": "human"})
	case r.URL.Path == "/v1/boards":
		want := r.URL.Query().Get("lifecycle")
		if want == "" {
			want = "active"
		}
		list, archived := []map[string]any{}, 0
		for _, b := range s.boards {
			if !b.readable {
				continue
			}
			if b.lifecycle == "archived" {
				archived++
			}
			if want == "all" || want == b.lifecycle {
				list = append(list, map[string]any{"id": b.id, "name": b.name, "title": nil, "visibility": b.visibility, "on_board": true, "lifecycle": b.lifecycle})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"boards": list, "archived_count": archived})
	case len(parts) == 3 && parts[0] == "boards" && r.Method == http.MethodPost:
		b := s.find(parts[1])
		if b == nil || b.lifecycle == "deleted" {
			fail(http.StatusNotFound, "board_not_found", "Check the board's name.")
			return
		}
		changed := false
		switch parts[2] {
		case "archive":
			changed = b.lifecycle == "active"
			b.lifecycle = "archived"
		case "restore":
			changed = b.lifecycle == "archived"
			b.lifecycle = "active"
		case "delete":
			if strings.HasPrefix(auth, "aba_") {
				fail(http.StatusForbidden, "human_token_required", "A person deletes a board.")
				return
			}
			if b.lifecycle != "archived" {
				fail(http.StatusConflict, "board_not_archived", "Archive it first.")
				return
			}
			changed, b.lifecycle = true, "deleted"
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": b.id, "lifecycle": b.lifecycle, "changed": changed})
	case len(parts) == 3 && parts[0] == "boards" && parts[2] == "people":
		_ = json.NewEncoder(w).Encode(map[string]any{"board": parts[1], "visibility": "open", "people": []map[string]any{{"handle": "alex", "board_role": "owner", "board": parts[1]}}})
	case len(parts) == 3 && parts[0] == "boards" && parts[2] == "members":
		_ = json.NewEncoder(w).Encode(map[string]any{"members": []any{}})
	case len(parts) == 2 && parts[0] == "boards" && r.Method == http.MethodGet:
		b := s.find(parts[1])
		if b == nil || !b.readable || b.lifecycle == "deleted" {
			fail(http.StatusNotFound, "board_not_found", "Check the board's name.")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": b.id, "name": b.name, "visibility": b.visibility, "lifecycle": b.lifecycle, "on_board": true})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// sent returns the requests the server got, as "METHOD /path?query".
func (s *lifecycleServer) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// lifecycleRun is one aboard command's run against a lifecycle server.
type lifecycleRun struct {
	code           int
	stdout, stderr string
}

// lifecycleEnv is a person's machine, logged in to srv, whose directory's board is
// payments-design. env adds to its environment; stdin, when set, is a terminal's input.
type lifecycleEnv struct {
	t     *testing.T
	home  string
	dir   string
	env   map[string]string
	stdin string
}

func newLifecycleEnv(t *testing.T, srv *lifecycleServer) *lifecycleEnv {
	t.Helper()
	return lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1W", Token: "aba_agent"})
}

// lifecycleMachine is a person's machine logged in to url with key, holding the agent
// seat cred, whose directory's board is cred's board.
func lifecycleMachine(t *testing.T, url, key string, cred agentCredential) *lifecycleEnv {
	t.Helper()
	e := &lifecycleEnv{t: t, home: t.TempDir(), dir: t.TempDir(), env: map[string]string{}}
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	if err := a.writeProject(projectFile{Server: serverRef{URL: url}, Board: cred.Board}); err != nil {
		t.Fatal(err)
	}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.servers(), serverLogins{Default: url, Servers: []serverLogin{{URL: url, Handle: "alex", Key: key}}}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *lifecycleEnv) getenv(k string) string {
	if v, ok := e.env[k]; ok {
		return v
	}
	switch k {
	case "HOME", "ABOARD_HOME":
		return e.home
	case "ABOARD_LOCAL_ADDR":
		return "127.0.0.1:1"
	}
	return ""
}

func (e *lifecycleEnv) app(stdout, stderr *bytes.Buffer) *app {
	return &app{env: e.environment(stdout, stderr)}
}

func (e *lifecycleEnv) environment(stdout, stderr *bytes.Buffer) Env {
	env := Env{
		Stdin: strings.NewReader(e.stdin), Stdout: stdout, Stderr: stderr, Getenv: e.getenv, Dir: e.dir,
		Executable: os.Executable, Rand: rand.Reader,
	}
	if e.stdin != "" {
		env.Terminal = true
	}
	return env
}

func (e *lifecycleEnv) run(args ...string) lifecycleRun {
	e.t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, e.environment(&stdout, &stderr))
	return lifecycleRun{code, stdout.String(), stderr.String()}
}

// errorCode reads a --json error's code and hint.
func (r lifecycleRun) errorCode(t *testing.T) (code, hint string) {
	t.Helper()
	var w wireError
	if err := json.Unmarshal([]byte(r.stdout), &w); err != nil {
		t.Fatalf("not a JSON error: %q %q", r.stdout, r.stderr)
	}
	return w.Error.Code, w.Error.Hint
}

func paymentsDesign() *lifecycleBoard {
	return &lifecycleBoard{id: "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7N", name: "payments-design", lifecycle: "active", visibility: "open", readable: true}
}

// A person archives the directory's board, which says how to restore it, and restores
// it; a repeat says it changed nothing. Both send an Idempotency-Key.
func TestBoardArchiveAndRestore(t *testing.T) {
	srv := newLifecycleServer(t, paymentsDesign())
	e := newLifecycleEnv(t, srv)
	r := e.run("board", "archive", "--board", "payments-design")
	if r.code != 0 || r.stdout != "Archived payments-design. It's read-only now; restore with: aboard board restore payments-design.\n" {
		t.Fatalf("archive: %d %q %q", r.code, r.stdout, r.stderr)
	}
	r = e.run("board", "archive", "payments-design", "--json")
	var out struct {
		Server    serverRef `json:"server"`
		Board     string    `json:"board"`
		ID        string    `json:"id"`
		Lifecycle string    `json:"lifecycle"`
		Changed   *bool     `json:"changed"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 {
		t.Fatalf("archive --json: %d %q %v", r.code, r.stdout, err)
	}
	if out.Server.URL != srv.URL || out.Board != "payments-design" || out.ID != "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7N" || out.Lifecycle != "archived" || out.Changed == nil || *out.Changed {
		t.Fatalf("archive repeat: %+v", out)
	}
	if r := e.run("board", "archive", "--board", "payments-design"); r.stdout != "payments-design is already archived; restore with: aboard board restore payments-design.\n" {
		t.Errorf("archive repeat, text: %q", r.stdout)
	}
	if r := e.run("board", "restore", "--board", "payments-design"); r.code != 0 || r.stdout != "Restored payments-design. New messages and joins work again.\n" {
		t.Fatalf("restore: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if r := e.run("board", "restore", "--board", "payments-design"); r.stdout != "payments-design isn't archived; nothing changed.\n" {
		t.Errorf("restore repeat: %q", r.stdout)
	}
}

// An agent archives and restores its own board with its own token, never the person's key.
func TestAnAgentArchivesItsOwnBoard(t *testing.T) {
	srv := newLifecycleServer(t, paymentsDesign())
	e := newLifecycleEnv(t, srv)
	r := e.run("board", "archive", "--as", "claude")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "Archived payments-design.") {
		t.Fatalf("archive --as: %d %q %q", r.code, r.stdout, r.stderr)
	}
	e.env["ABOARD_AGENT"] = "claude"
	if r := e.run("board", "restore", "--board", "payments-design"); r.code != 0 || !strings.HasPrefix(r.stdout, "Restored payments-design.") {
		t.Fatalf("restore as ABOARD_AGENT: %d %q %q", r.code, r.stdout, r.stderr)
	}
	for _, req := range srv.sent() {
		if strings.Contains(req, "abh_") {
			t.Errorf("the person's key was used: %s", req)
		}
	}
}

// Delete is a person's: an agent selected any way is refused before any request, and
// is given the command to hand its person.
func TestBoardDeleteIsForAPerson(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"--as", nil, []string{"--as", "claude"}},
		{"ABOARD_AGENT", map[string]string{"ABOARD_AGENT": "claude"}, nil},
		{"a session", map[string]string{"ABOARD_SESSION": "claude-code:s1"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLifecycleServer(t, paymentsDesign())
			e := newLifecycleEnv(t, srv)
			for k, v := range tc.env {
				e.env[k] = v
			}
			r := e.run(append([]string{"board", "delete", "payments-design", "--json"}, tc.args...)...)
			code, hint := r.errorCode(t)
			if r.code != 1 || code != "human_command_in_session" || !strings.Contains(hint, "aboard board delete payments-design") {
				t.Fatalf("%d %s %q", r.code, code, hint)
			}
			if got := srv.sent(); len(got) != 0 {
				t.Errorf("requests before the refusal: %v", got)
			}
		})
	}
}

// Without a terminal, and always with --json, delete needs --yes, and asks nothing of
// the server first.
func TestBoardDeleteNeedsAYesWithoutATerminal(t *testing.T) {
	b := paymentsDesign()
	b.lifecycle = "archived"
	srv := newLifecycleServer(t, b)
	e := newLifecycleEnv(t, srv)
	r := e.run("board", "delete", "--board", "payments-design", "--json")
	code, hint := r.errorCode(t)
	if r.code != 1 || code != "confirmation_required" || !strings.Contains(hint, "aboard board delete payments-design --yes") {
		t.Fatalf("no --yes: %d %s %q", r.code, code, hint)
	}
	if got := srv.sent(); len(got) != 0 {
		t.Fatalf("requests without a yes: %v", got)
	}
	r = e.run("board", "delete", "--board", "payments-design", "--yes")
	if r.code != 0 || r.stdout != "Deleted payments-design. Its record is kept; nobody can open it again.\n" {
		t.Fatalf("delete --yes: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if b.lifecycle != "deleted" {
		t.Errorf("lifecycle after delete: %s", b.lifecycle)
	}
}

// In a terminal, delete asks for the board's name typed exactly; anything else
// changes nothing.
func TestBoardDeleteAsksForTheName(t *testing.T) {
	b := paymentsDesign()
	b.lifecycle = "archived"
	srv := newLifecycleServer(t, b)
	e := newLifecycleEnv(t, srv)
	e.env["ACCESSIBLE"] = "1"
	e.stdin = "payments\n"
	r := e.run("board", "delete", "--board", "payments-design")
	if r.code != 0 || !strings.Contains(r.stdout, "Nothing changed") || b.lifecycle != "archived" {
		t.Fatalf("a wrong name: %d %q %q %s", r.code, r.stdout, r.stderr, b.lifecycle)
	}
	e.stdin = "payments-design\n"
	r = e.run("board", "delete", "--board", "payments-design")
	if r.code != 0 || !strings.Contains(r.stdout, "Type the board's name") || !strings.HasSuffix(r.stdout, "Deleted payments-design. Its record is kept; nobody can open it again.\n") || b.lifecycle != "deleted" {
		t.Fatalf("the right name: %d %q %q %s", r.code, r.stdout, r.stderr, b.lifecycle)
	}
}

// The typed confirmation takes as long as the person needs: each request has its own
// timeout, and none runs while the question waits.
func TestBoardDeleteWaitsForASlowConfirmation(t *testing.T) {
	b := paymentsDesign()
	b.lifecycle = "archived"
	srv := newLifecycleServer(t, b)
	e := newLifecycleEnv(t, srv)
	e.stdin = "unused\n"
	var out bytes.Buffer
	a := e.app(&out, &out)
	// Every request context the command makes is kept; while the person types, each one
	// made so far runs out, as its deadline would on a slow person.
	var (
		mu      sync.Mutex
		expire  []context.CancelFunc
		expired []context.Context
	)
	a.deadline = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancelCause(ctx)
		mu.Lock()
		defer mu.Unlock()
		expire = append(expire, func() { cancel(context.DeadlineExceeded) })
		expired = append(expired, ctx)
		return ctx, func() { cancel(context.Canceled) }
	}
	a.askLine = func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(expire) == 0 {
			t.Fatal("no request was made before the question")
		}
		for i, run := range expire {
			run()
			<-expired[i].Done()
		}
		return "payments-design", nil
	}
	if err := runBoard(context.Background(), a, []string{"delete", "--board", "payments-design"}); err != nil {
		t.Fatalf("delete after a slow confirmation: %v; output %q", err, out.String())
	}
	if b.lifecycle != "deleted" || !strings.HasSuffix(out.String(), "Deleted payments-design. Its record is kept; nobody can open it again.\n") {
		t.Fatalf("lifecycle %s, output %q", b.lifecycle, out.String())
	}
}

// An outside admin deletes a private board they can't read by its id: they type the
// id to confirm, and the command never reads the board, so it never learns its name.
func TestBoardDeleteByIDNeverReadsTheBoard(t *testing.T) {
	hidden := &lifecycleBoard{id: "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7A", name: "secret-plans", lifecycle: "archived", visibility: "private"}
	srv := newLifecycleServer(t, hidden)
	e := newLifecycleEnv(t, srv)
	e.env["ACCESSIBLE"] = "1"
	e.stdin = hidden.id + "\n"
	r := e.run("board", "delete", hidden.id)
	if r.code != 0 || !strings.Contains(r.stdout, "Type the board's id") || hidden.lifecycle != "deleted" {
		t.Fatalf("delete by id: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stdout, "secret-plans") {
		t.Errorf("the private name was shown: %q", r.stdout)
	}
	for _, req := range srv.sent() {
		if strings.HasPrefix(req, "GET /v1/boards/") {
			t.Errorf("the board was read: %s", req)
		}
	}
}

// Delete on a board that isn't archived says so before asking anything.
func TestBoardDeleteOnAnActiveBoard(t *testing.T) {
	srv := newLifecycleServer(t, paymentsDesign())
	e := newLifecycleEnv(t, srv)
	e.env["ACCESSIBLE"] = "1"
	e.stdin = "payments-design\n"
	r := e.run("board", "delete", "--board", "payments-design")
	if r.code != 1 || !strings.Contains(r.stderr, "board_not_archived") || !strings.Contains(r.stderr, "aboard board archive payments-design") || strings.Contains(r.stdout, "Type") {
		t.Fatalf("%d %q %q", r.code, r.stdout, r.stderr)
	}
}

// boards lists active boards with one quiet line about archived ones, and --archived
// lists only those.
func TestBoardsArchived(t *testing.T) {
	old := &lifecycleBoard{id: "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7B", name: "old-plans", lifecycle: "archived", visibility: "open", readable: true}
	older := &lifecycleBoard{id: "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7C", name: "older-plans", lifecycle: "archived", visibility: "open", readable: true}
	srv := newLifecycleServer(t, paymentsDesign(), old, older)
	e := newLifecycleEnv(t, srv)
	r := e.run("boards")
	if r.code != 0 || !strings.Contains(r.stdout, "payments-design") || strings.Contains(r.stdout, "old-plans") || !strings.HasSuffix(r.stdout, "2 archived boards: aboard boards --archived\n") {
		t.Fatalf("boards: %d %q %q", r.code, r.stdout, r.stderr)
	}
	r = e.run("boards", "--archived", "--json")
	var out struct {
		Lifecycle     string `json:"lifecycle"`
		ArchivedCount *int   `json:"archived_count"`
		Boards        []struct {
			Name      string `json:"name"`
			Lifecycle string `json:"lifecycle"`
		} `json:"boards"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 {
		t.Fatalf("boards --archived --json: %d %q %v", r.code, r.stdout, err)
	}
	if out.Lifecycle != "archived" || out.ArchivedCount == nil || *out.ArchivedCount != 2 || len(out.Boards) != 2 || out.Boards[0].Lifecycle != "archived" {
		t.Fatalf("archived boards: %+v", out)
	}
	if r := e.run("boards", "--archived"); !strings.HasPrefix(r.stdout, "Your archived boards on ") || strings.Contains(r.stdout, "aboard boards --archived") || strings.Contains(r.stdout, "join with") {
		t.Errorf("boards --archived text: %q", r.stdout)
	}
	found := false
	for _, req := range srv.sent() {
		found = found || (strings.HasPrefix(req, "GET /v1/boards?") && strings.Contains(req, "lifecycle=archived"))
	}
	if !found {
		t.Errorf("no archived listing asked for: %v", srv.sent())
	}
}

// In a session, boards --archived asks the daemon for archived boards and shows its count.
func TestSessionBoardsArchived(t *testing.T) {
	srv := newLifecycleServer(t, paymentsDesign())
	e := newLifecycleEnv(t, srv)
	e.env["ABOARD_SESSION"] = "claude-code:s1"
	var got []delivery.Request
	var mu sync.Mutex
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	one := 1
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		resp := delivery.Response{V: delivery.ProtocolVersion}
		if req.Op == delivery.OpBoards {
			resp.Server, resp.ArchivedCount = srv.URL, &one
			if req.Lifecycle == "archived" {
				resp.Boards = []json.RawMessage{json.RawMessage(`{"name":"old-plans","visibility":"open","on_board":true,"lifecycle":"archived"}`)}
			} else {
				resp.Boards = []json.RawMessage{json.RawMessage(`{"name":"payments-design","visibility":"open","on_board":true}`)}
			}
		}
		return resp
	})
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		a.env.Stdout, a.env.Stderr = &out, &out
		if err := runBoards(context.Background(), a, args); err != nil {
			t.Fatalf("boards %v: %v", args, err)
		}
		return out.String()
	}
	if out := run(); !strings.HasSuffix(out, "1 archived board: aboard boards --archived\n") {
		t.Fatalf("session boards: %q", out)
	}
	if out := run("--archived"); !strings.Contains(out, "old-plans") || strings.Contains(out, "join with") || strings.Contains(out, "aboard boards --archived") {
		t.Fatalf("session boards --archived: %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	var lifecycles []string
	for _, req := range got {
		if req.Op == delivery.OpBoards {
			lifecycles = append(lifecycles, req.Lifecycle)
		}
	}
	if strings.Join(lifecycles, ",") != ",archived" {
		t.Errorf("filters sent to the daemon: %q", lifecycles)
	}
}

// fakeDaemonAnswering answers the control socket of a's home with answer.
func fakeDaemonAnswering(t *testing.T, a *app, answer func(delivery.Request) delivery.Response) {
	t.Helper()
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	l, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				var req delivery.Request
				if delivery.ReadFrame(bufio.NewReader(c), &req) != nil {
					return
				}
				_ = delivery.WriteFrame(c, answer(req))
			}()
		}
	}()
	a.daemonChecked = true
}

// Against a real server: the creator archives and deletes, their agent restores its own
// board, boards counts and lists archives, and delete keeps its refusals.
func TestBoardLifecycleOnARealServer(t *testing.T) {
	url, owner := testServer(t)
	cred := seatOn(t, url, owner)
	e := lifecycleMachine(t, url, owner, cred)
	board := cred.Board
	if r := e.run("board", "archive"); r.code != 0 || r.stdout != "Archived "+board+". It's read-only now; restore with: aboard board restore "+board+".\n" {
		t.Fatalf("archive: %d %q %q", r.code, r.stdout, r.stderr)
	}
	if r := e.run("boards"); r.code != 0 || strings.Contains(r.stdout, board+" ") || !strings.HasSuffix(r.stdout, "1 archived board: aboard boards --archived\n") {
		t.Fatalf("boards: %d %q %q", r.code, r.stdout, r.stderr)
	}
	r := e.run("boards", "--archived", "--json")
	var listed struct {
		Lifecycle     string `json:"lifecycle"`
		ArchivedCount *int   `json:"archived_count"`
		Boards        []struct {
			Name      string `json:"name"`
			Lifecycle string `json:"lifecycle"`
		} `json:"boards"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &listed); err != nil || r.code != 0 {
		t.Fatalf("boards --archived: %d %q %v", r.code, r.stdout, err)
	}
	if listed.Lifecycle != "archived" || listed.ArchivedCount == nil || *listed.ArchivedCount != 1 || len(listed.Boards) != 1 || listed.Boards[0].Name != board || listed.Boards[0].Lifecycle != "archived" {
		t.Fatalf("archived list: %+v", listed)
	}
	r = e.run("board", "delete", "--as", cred.Name, "--json")
	if code, _ := r.errorCode(t); r.code != 1 || code != "human_command_in_session" {
		t.Fatalf("delete as the agent: %d %q", r.code, r.stdout)
	}
	if r := e.run("board", "restore", "--as", cred.Name); r.code != 0 || r.stdout != "Restored "+board+". New messages and joins work again.\n" {
		t.Fatalf("restore as the agent: %d %q %q", r.code, r.stdout, r.stderr)
	}
	r = e.run("board", "delete", "--board", board, "--yes", "--json")
	if code, _ := r.errorCode(t); r.code != 1 || code != "board_not_archived" {
		t.Fatalf("delete an active board: %d %q", r.code, r.stdout)
	}
	if r := e.run("board", "archive", board); r.code != 0 {
		t.Fatalf("archive again: %d %q", r.code, r.stderr)
	}
	r = e.run("board", "delete", "--board", board, "--yes", "--json")
	var out boardLifecycleOutput
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 || out.Lifecycle != "deleted" || !out.Changed || out.Board != board || out.Server.URL != url {
		t.Fatalf("delete: %d %q %v", r.code, r.stdout, err)
	}
	if r := e.run("boards", "--archived"); !strings.Contains(r.stdout, "  none\n") {
		t.Errorf("after delete: %q", r.stdout)
	}
	r = e.run("board", "restore", "--board", board, "--json")
	if code, _ := r.errorCode(t); r.code != 1 || code != "board_not_found" {
		t.Errorf("restore a deleted board: %d %q", r.code, r.stdout)
	}
}
