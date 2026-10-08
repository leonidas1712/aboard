//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// teamServer is aboard serve --team running as its own process, as in a container,
// behind a reverse proxy that ends HTTPS, as an ingress would.
type teamServer struct {
	t      *testing.T
	url    string // the proxy's https address: the public URL
	data   string
	log    *syncBuffer
	certs  string // a PEM file with the proxy's certificate, for SSL_CERT_FILE
	client *http.Client
}

// startTeamServer starts the proxy, then the team server with its configuration in its
// environment, as a container gets it.
func startTeamServer(t *testing.T) *teamServer {
	t.Helper()
	listen := "127.0.0.1:0"
	var backend atomic.Pointer[url.URL]
	proxy := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(backend.Load())
		r.Out.Host = r.In.Host // an ingress keeps the Host the browser sent
		r.SetXForwarded()      // and adds forwarded headers, which the server must ignore
	}, FlushInterval: -1})
	proxy.StartTLS()
	t.Cleanup(proxy.Close)

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &teamServer{t: t, url: proxy.URL, data: filepath.Join(home, "data"), log: &syncBuffer{}, certs: filepath.Join(home, "proxy.pem"), client: proxy.Client()}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw})
	if err := os.WriteFile(s.certs, cert, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, "serve", "--team")
	cmd.Env = []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"),
		"ABOARD_PUBLIC_URL=" + s.url, "ABOARD_DATA=" + s.data, "ABOARD_LISTEN=" + listen, "ABOARD_ADMIN=alex",
		exitWithVar + "=" + strconv.Itoa(os.Getpid()),
	}
	cmd.Stdout, cmd.Stderr = s.log, s.log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	eventually(t, 15*time.Second, "the team server answers through the proxy", func() bool {
		select {
		case <-exited:
			t.Fatalf("aboard serve --team exited:\n%s", s.log)
		default:
		}
		if backend.Load() == nil {
			// Read only this process's serving record, after it has bound its port.
			for _, line := range strings.Split(s.log.String(), "\n") {
				var record struct{ Msg, Addr string }
				if json.Unmarshal([]byte(line), &record) == nil && record.Msg == "serving" && record.Addr != "" {
					backend.Store(&url.URL{Scheme: "http", Host: record.Addr})
					break
				}
			}
			if backend.Load() == nil {
				return false
			}
		}
		req, err := http.NewRequestWithContext(context.Background(), "GET", s.url+"/v1/info", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return s
}

// call makes a request through the proxy with token.
func (s *teamServer) call(method, path, token string, body any) (int, map[string]any) {
	s.t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, s.url+path, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

// person is a fresh home for someone on their own machine, trusting the proxy's
// certificate as a machine trusts a team's own certificate authority.
func (s *teamServer) person(user string) *env {
	s.t.Helper()
	e := newPersonHome(s.t, user)
	e.vars = append(e.vars, "SSL_CERT_FILE="+s.certs)
	return e
}

// savedKey is the key a home keeps for the team server.
func (s *teamServer) savedKey(e *env) string {
	s.t.Helper()
	var logins struct {
		Servers []struct{ URL, Key string } `json:"servers"`
	}
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "servers.json"))
	if err != nil {
		s.t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &logins); err != nil {
		s.t.Fatal(err)
	}
	for _, l := range logins.Servers {
		if l.URL == s.url {
			return l.Key
		}
	}
	s.t.Fatalf("%s has no key for %s:\n%s", e.home, s.url, raw)
	return ""
}

// aboard serve --team refuses a configuration it can't run safely before it opens
// anything: no public URL, one that isn't https or has a path, no data folder or a
// relative one; and the team flags without --team.
func TestServeTeamRefusesABadConfiguration(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	data := filepath.Join(e.home, "team-data")
	for _, c := range []struct {
		args  []string
		extra []string
		want  string
	}{
		{[]string{"serve", "--team", "--data", data}, nil, "needs its public URL"},
		{[]string{"serve", "--team", "--public-url", "http://team.example.com", "--data", data}, nil, "is not an https address"},
		{[]string{"serve", "--team", "--data", data}, []string{"ABOARD_PUBLIC_URL=https://team.example.com/aboard"}, "is not an https address"},
		{[]string{"serve", "--team", "--public-url", "https://team.example.com"}, nil, "needs a folder for its data"},
		{[]string{"serve", "--team", "--public-url", "https://team.example.com", "--data", "data"}, nil, "is not an absolute path"},
		{[]string{"serve", "--public-url", "https://team.example.com"}, nil, "add --team"},
	} {
		r := e.exec(c.extra, "", append(c.args, "--json")...)
		if r.code != 2 || errorCode(t, r.json(t)) != "invalid_request" || !strings.Contains(r.stdout, c.want) {
			t.Errorf("aboard %v with %v:\n%s", c.args, c.extra, r)
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("a refused start made the data folder: %v", err)
	}
}

// starterNoticeLine is the starter policy notice, as a command prints it.
const starterNoticeLine = "Starter policy: every member reads everything. Before adding more agents or people, run: aboard board policy recommended\n"

// board new uses the local server when the machine has no other server. In a session
// it creates the person's board and an ordinary seat for that session.
func TestBoardNewOnTheLocalServerAndInASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	out := e.run("board", "new", "notes", "--title", "Loose ends", "--json").json(t)
	matchesCLISpec(t, "BoardNewOutput", out)
	if field(t, out, "server.name") != "local" || field(t, out, "board.name") != "notes" || field(t, out, "board.title") != "Loose ends" ||
		field(t, out, "board.visibility") != "open" {
		t.Fatalf("board new on the local server: %v", out)
	}
	if r := e.runExit("board", "new", "notes", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_name_taken" {
		t.Fatalf("board new with a taken name:\n%s", r)
	}
	if r := e.runExit("board", "new", "--json"); r.code != 2 {
		t.Fatalf("board new without a name:\n%s", r)
	}
	s := e.claudeSession("s-board-new")
	created := s.run("board", "new", "agent-made", "--json").json(t)
	matchesCLISpec(t, "BoardNewOutput", created)
	if field(t, created, "board.name") != "agent-made" || field(t, created, "board.created_by.kind") != "human" ||
		field(t, created, "board.created_by.name") != "alex" || field(t, created, "agent.owner") != "alex" || field(t, created, "agent.role") != "member" {
		t.Fatal("session creation did not keep the person as creator with an ordinary agent seat")
	}
	tm := &team{t: t, admin: e}
	creatorID := workAssertOwnership(t, tm, e, created)
	status, events := tm.call("GET", "/v1/boards/agent-made/events", tm.key(e), nil)
	workStatus(t, status, http.StatusOK, events)
	first := events["events"].([]any)[0].(map[string]any)
	if first["type"] != "board.created" || field(t, first, "actor.member_id") != creatorID {
		t.Fatal("session creation did not record the person's permanent member id as actor")
	}
}

// A person's board command refused inside an agent's session hands the person the
// command with every flag that was given, quoted for a shell, so it acts on the same
// board on the same server.
func TestRefusedBoardCommandsKeepTheirFlags(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.claudeSession("s-handoff")
	const srv = "https://team.example.com"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"board", "policy", "recommended", "--board", "pay", "--server", srv}, "aboard board policy recommended --board pay --server https://team.example.com"},
		{[]string{"board", "agents-add-people", "off", "--board", "pay", "--server", srv}, "aboard board agents-add-people off --board pay --server https://team.example.com"},
		{[]string{"board", "remove", "@maya", "--board", "pay", "--server", srv}, "aboard board remove @maya --board pay --server https://team.example.com"},
		{[]string{"board", "leave", "--board", "pay", "--server", srv}, "aboard board leave --board pay --server https://team.example.com"},
		{[]string{"board", "owner", "@maya", "--board", "pay", "--server", srv}, "aboard board owner @maya --board pay --server https://team.example.com"},
		{[]string{"board", "visibility", "open", "--yes", "--board", "pay", "--server", srv}, "aboard board visibility open --board pay --server https://team.example.com"},
	} {
		r := s.runExit(append(c.args, "--json")...)
		if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
			t.Errorf("aboard %v in a session:\n%s", c.args, r)
			continue
		}
		hint, _ := field(t, r.json(t), "error.hint").(string)
		if !strings.HasSuffix(hint, ": "+c.want) {
			t.Errorf("aboard %v hands over:\n%s\nwant it to end with:\n%s", c.args, hint, c.want)
		}
	}

	// Outside a session, the command a confirmation asks for keeps --server too.
	local := "http://" + e.addr
	e.run("board", "new", "quiet", "--private", "--server", local)
	r := e.runExit("board", "visibility", "open", "--board", "quiet", "--server", local, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" {
		t.Fatalf("visibility open without --yes:\n%s", r)
	}
	if hint, _ := field(t, r.json(t), "error.hint").(string); hint != "Run aboard board visibility open --board quiet --server "+local+" --yes to make it open." {
		t.Fatalf("the confirmation's hint: %q", hint)
	}
}

// A team server in its own process behind a proxy that ends HTTPS: its first start makes
// the admin and writes their key to a file, which the admin pipes into aboard login on
// their machine; the admin invites a colleague, who connects a second machine by
// approval; both join a board on the server from their agents' sessions and exchange a
// message. Join lines name the public host, and the server ignores the proxy's
// forwarded headers.
func TestATeamServerBehindAnHTTPSProxy(t *testing.T) {
	t.Parallel()
	s := startTeamServer(t)
	host := strings.TrimPrefix(s.url, "https://")

	keyFile := filepath.Join(s.data, "admin-key")
	info, err := os.Stat(keyFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the admin key file: %v %v\n%s", info, err, s.log)
	}
	adminKey, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.log.String(), strings.TrimSpace(string(adminKey))) || !strings.Contains(s.log.String(), `"key_file":"`+keyFile+`"`) {
		t.Fatalf("the log shows the key, or doesn't name its file:\n%s", s.log)
	}
	if status, v := s.call("GET", "/v1/info", "", nil); status != http.StatusOK || v["mode"] != "team" {
		t.Fatalf("GET /v1/info: %d %v", status, v)
	}

	alex := s.person("alex")
	if r := alex.exec(nil, string(adminKey), "login", s.url); r.code != 0 {
		t.Fatalf("login with the admin key:\n%s", r)
	}
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	people := alex.run("people", "--server", s.url)
	if !strings.Contains(people.stdout, "@alex   admin") {
		t.Fatalf("aboard people:\n%s", people)
	}

	link := field(t, alex.run("invite", "--server", "--json").json(t), "link").(string)
	if !strings.HasPrefix(link, s.url+"/join#abi_") {
		t.Fatalf("the invite link doesn't name the public URL: %s", link)
	}
	laptop := s.person("maya")
	laptop.run("connect", link)

	desktop := s.person("maya")
	connecting := desktop.start("connect", s.url, "--handle", "maya", "--name", "maya-desktop", "--json")
	code := connecting.shownCode()
	laptop.run("approve", code, "--yes", "--server", s.url)
	if r := connecting.wait(); r.code != 0 {
		t.Fatalf("connect by approval:\n%s", r)
	}
	if s.savedKey(desktop) == s.savedKey(laptop) {
		t.Fatal("the desktop has the laptop's key")
	}

	created := alex.run("board", "new", "payments", "--title", "Payments retry design")
	if want := "Created board payments on " + s.url + ", open to everyone on the server.\n" +
		"Linked this directory to payments, so board commands run here act on it.\n" + starterNoticeLine +
		"Next: from an agent's session, run aboard join --board payments; to bring a person onto it, aboard board add @handle\n"; created.stdout != want {
		t.Fatalf("board new:\n%s\nwant:\n%s", created, want)
	}
	board := "payments"
	alex.run("board", "policy", "recommended")
	if status, v := s.call("GET", "/v1/boards/"+board, s.savedKey(alex), nil); status != http.StatusOK || field(t, v, "policy.preset") != "recommended" {
		t.Fatalf("the board's policy: %d %v", status, v)
	}
	private := desktop.run("board", "new", "maya-notes", "--private", "--server", s.url, "--json").json(t)
	matchesCLISpec(t, "BoardNewOutput", private)
	if field(t, private, "board.visibility") != "private" || field(t, private, "server.url") != s.url ||
		private["join_command"] != "aboard join --board maya-notes" || field(t, private, "policy_notice.preset") != "starter" || private["linked"] != true {
		t.Fatalf("board new --private --json: %v", private)
	}
	// A linked directory keeps its board, and the next step names the new one.
	other := desktop.run("board", "new", "maya-drafts")
	if !strings.HasSuffix(other.stdout, "aboard board add @handle --board maya-drafts\n") || strings.Contains(other.stdout, "Linked") {
		t.Fatalf("board new in a linked directory:\n%s", other)
	}
	desktop.run("board", "add", "@alex", "--board", "maya-notes")

	// A board made on the team server from a folder linked to a local board: the folder
	// keeps its board, and the next steps name the team server, which the board
	// commands take with --server.
	laptop.run("pair")
	local := field(t, laptop.run("status", "--json").json(t), "board").(string)
	linkedBefore, err := os.ReadFile(filepath.Join(laptop.dir, ".aboard"))
	if err != nil {
		t.Fatal(err)
	}
	cross := laptop.run("board", "new", "maya-cross", "--server", s.url)
	localURL := "http://" + laptop.addr
	if want := "Created board maya-cross on " + s.url + ", open to everyone on the server.\n" +
		"This directory stays linked to " + local + " on " + localURL + ", so board commands for maya-cross need --server " + s.url + ".\n" +
		starterNoticeLine +
		"Next: from an agent's session, run aboard join --board maya-cross --server " + s.url +
		"; to bring a person onto it, aboard board add @handle --board maya-cross --server " + s.url + "\n"; cross.stdout != want {
		t.Fatalf("board new across servers:\n%s\nwant:\n%s", cross, want)
	}
	crossJSON := laptop.run("board", "new", "maya-cross-2", "--server", s.url, "--json").json(t)
	matchesCLISpec(t, "BoardNewOutput", crossJSON)
	if crossJSON["linked"] != false || field(t, crossJSON, "stays_linked.board") != local || field(t, crossJSON, "stays_linked.server.url") != localURL ||
		crossJSON["join_command"] != "aboard join --board maya-cross-2 --server "+s.url {
		t.Fatalf("board new across servers --json: %v", crossJSON)
	}
	if after, _ := os.ReadFile(filepath.Join(laptop.dir, ".aboard")); !bytes.Equal(after, linkedBefore) {
		t.Fatalf("the folder's link changed:\n%s\nwas:\n%s", after, linkedBefore)
	}
	laptop.run("board", "policy", "recommended", "--board", "maya-cross", "--server", s.url)
	laptop.run("board", "add", "@alex", "--board", "maya-cross", "--server", s.url)
	if status, v := s.call("GET", "/v1/boards/maya-cross/people", s.savedKey(alex), nil); status != http.StatusOK || !strings.Contains(fmt.Sprint(v), "alex") {
		t.Fatalf("alex on maya-cross: %d %v", status, v)
	}
	if status, v := s.call("GET", "/v1/boards/maya-cross", s.savedKey(alex), nil); status != http.StatusOK || field(t, v, "policy.preset") != "recommended" {
		t.Fatalf("maya-cross's policy: %d %v", status, v)
	}
	status, jc := s.call("POST", "/v1/boards/"+board+"/join-codes", s.savedKey(alex), map[string]any{"role": "member"})
	if line, _ := jc["join_line"].(string); status != http.StatusCreated || !strings.Contains(line, " on "+host+" as member ") {
		t.Fatalf("the join line doesn't name the public host: %d %v", status, jc)
	}

	alexSession := alex.claudeSession("s-team-alex")
	alexSession.run("join", "--board", board, "--server", s.url)
	mayaSession := desktop.claudeSession("s-team-maya")
	mayaSession.run("join", "--board", board, "--server", s.url)
	alexSession.run("say", "Hello from behind the proxy")
	eventually(t, 15*time.Second, "maya's agent reads alex's message", func() bool {
		return strings.Contains(mayaSession.runExit("read").stdout, "Hello from behind the proxy")
	})
}
