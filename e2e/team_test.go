//go:build e2e

package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// team is one server with several people on one machine. The first person's home runs
// the server, and they are its admin; every other person has a home of their own (their
// own HOME, Aboard files, keys and delivery daemon) connected to the server with an
// invite, exactly as a person on another machine would be.
type team struct {
	t     *testing.T
	admin *env
}

// newTeam starts the first person's local server, which every other person joins.
func newTeam(t *testing.T) *team {
	t.Helper()
	admin := newEnv(t)
	admin.run("up")
	return &team{t: t, admin: admin}
}

// url is the server's address, as people connect to it.
func (tm *team) url() string { return "http://" + tm.admin.addr }

// invite makes a server invite as the admin and returns its link.
func (tm *team) invite() string {
	tm.t.Helper()
	return field(tm.t, tm.admin.run("invite", "--server", "--json").json(tm.t), "link").(string)
}

// newPersonHome is a fresh home for a person whose system user name is user, connected to
// nothing yet.
func newPersonHome(t *testing.T, user string) *env {
	t.Helper()
	e := newEnv(t)
	for i, v := range e.vars {
		if strings.HasPrefix(v, "USER=") {
			e.vars[i] = "USER=" + user
		}
	}
	return e
}

// person brings a new person onto the server: a home of their own that connects with a
// fresh invite under handle.
func (tm *team) person(handle string) *env {
	tm.t.Helper()
	e := newPersonHome(tm.t, handle)
	e.run("connect", tm.invite())
	return e
}

// key returns the access key a home keeps for the team's server, or its own local
// owner key when it runs the server.
func (tm *team) key(e *env) string {
	tm.t.Helper()
	if e == tm.admin {
		raw, err := os.ReadFile(filepath.Join(e.configDir(), "local-owner-token"))
		if err != nil {
			tm.t.Fatal(err)
		}
		return strings.TrimSpace(string(raw))
	}
	var logins struct {
		Servers []struct{ URL, Key string } `json:"servers"`
	}
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "servers.json"))
	if err != nil {
		tm.t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &logins); err != nil {
		tm.t.Fatal(err)
	}
	for _, s := range logins.Servers {
		if s.URL == tm.url() {
			return s.Key
		}
	}
	tm.t.Fatalf("%s has no key for %s:\n%s", e.home, tm.url(), raw)
	return ""
}

// call makes a request to the team's server with token (none when empty) and returns
// the status and the decoded body.
func (tm *team) call(method, path, token string, body any) (int, map[string]any) {
	tm.t.Helper()
	return callAPI(tm.t, tm.url(), method, path, token, body)
}

func callAPI(t *testing.T, base, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

// me returns who a token is on the team's server.
func (tm *team) me(token string) map[string]any {
	tm.t.Helper()
	status, v := tm.call("GET", "/v1/me", token, nil)
	if status != http.StatusOK {
		tm.t.Fatalf("GET /v1/me: %d %v", status, v)
	}
	return v
}

// errorCode returns the error code in a decoded error body.
func errorCode(t *testing.T, v map[string]any) string {
	t.Helper()
	code, _ := field(t, v, "error.code").(string)
	return code
}

// treeSums returns the sha256 of every file under dir, by path.
func treeSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		sums[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

// The first person on a server is its admin, through the key the server wrote for them.
func TestFirstPersonIsTheServersAdmin(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	me := tm.me(tm.key(tm.admin))
	if me["kind"] != "human" || me["server_role"] != "admin" || me["name"] != "alex" {
		t.Fatalf("the first person: %v", me)
	}
}

// An admin's invite makes a new person a member, with a key of their own saved only in
// their own home; they authenticate with it and keep their id across a restart.
func TestInviteConnectsASecondPerson(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	inv := tm.admin.run("invite", "--server")
	link := strings.TrimSpace(inv.lines()[1])
	link = strings.TrimPrefix(link, "aboard connect ")
	if !strings.HasPrefix(link, tm.url()+"/join#abi_") {
		t.Fatalf("invite output:\n%s", inv)
	}
	expectLines(t, inv, "Invite for "+tm.url()+": one person, as a member, once, within 168 hours. On their machine, run:", "  aboard connect "+link,
		"", "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh, read aboard skill, then run aboard setup "+link+" --handle <name you'd like teammates to see>. Verify you can exchange messages with the inviting agent.")

	maya := newPersonHome(t, "maya")
	out := maya.run("connect", link, "--display-name", "Maya Chen", "--json").json(t)
	matchesCLISpec(t, "ConnectOutput", out)
	if field(t, out, "person.handle") != "maya" || field(t, out, "person.server_role") != "member" || field(t, out, "person.display_name") != "Maya Chen" {
		t.Fatalf("connect output: %v", out)
	}
	if _, ok := out["key"].(map[string]any)["token"]; ok {
		t.Fatalf("connect printed the key: %v", out)
	}
	info, err := os.Stat(filepath.Join(maya.configDir(), "servers.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("servers.json: %v %v", info, err)
	}

	key := tm.key(maya)
	me := tm.me(key)
	id := me["id"].(string)
	if me["kind"] != "human" || me["name"] != "maya" || me["server_role"] != "member" || !strings.HasPrefix(id, "hum_") || id == tm.me(tm.key(tm.admin))["id"] {
		t.Fatalf("maya's key: %v", me)
	}

	tm.admin.run("down")
	tm.admin.run("up")
	if again := tm.me(key)["id"]; again != id {
		t.Fatalf("maya's id changed across a restart: %v, was %s", again, id)
	}
}

// An invite works once: a second home can't redeem it again, and neither can the API.
func TestServerInviteWorksOnce(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	link := tm.invite()
	newPersonHome(t, "maya").run("connect", link)

	sam := newPersonHome(t, "sam")
	r := sam.runExit("connect", link, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "invite_invalid" {
		t.Fatalf("a used invite:\n%s", r)
	}
	if _, err := os.Stat(filepath.Join(sam.configDir(), "servers.json")); err == nil {
		t.Fatal("a refused connect saved a key")
	}
	invite := link[strings.Index(link, "#")+1:]
	status, v := tm.call("POST", "/v1/connect", "", map[string]any{"invite": invite, "handle": "sam", "key_name": "x"})
	if status != http.StatusNotFound || errorCode(t, v) != "invite_invalid" {
		t.Fatalf("replaying a used invite: %d %v", status, v)
	}
	// A guessed invite is refused the same way.
	status, v = tm.call("POST", "/v1/connect", "", map[string]any{"invite": "abi_" + strings.Repeat("A", 43), "handle": "sam", "key_name": "x"})
	if status != http.StatusNotFound || errorCode(t, v) != "invite_invalid" {
		t.Fatalf("a guessed invite: %d %v", status, v)
	}
	// Another kind of secret is never taken for an invite.
	status, v = tm.call("POST", "/v1/connect", "", map[string]any{"invite": tm.key(tm.admin), "handle": "sam", "key_name": "x"})
	if status != http.StatusNotFound || errorCode(t, v) != "invite_invalid" {
		t.Fatalf("an access key as an invite: %d %v", status, v)
	}
}

// Handles are unique: a taken one is refused, never signs in as its person, and leaves
// the invite for another try.
func TestHandlesAreUnique(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.person("maya")
	link := tm.invite()
	other := newPersonHome(t, "maya")
	for _, handle := range []string{"maya", "alex"} {
		r := other.runExit("connect", link, "--handle", handle, "--json")
		if r.code != 1 || errorCode(t, r.json(t)) != "handle_taken" {
			t.Fatalf("taking %s:\n%s", handle, r)
		}
	}
	r := other.runExit("connect", link, "--handle", "Maya B", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "handle_invalid" {
		t.Fatalf("an invalid handle:\n%s", r)
	}
	out := other.run("connect", link, "--handle", "maya-b", "--json").json(t)
	if field(t, out, "person.handle") != "maya-b" {
		t.Fatalf("the invite after a taken handle: %v", out)
	}
}

// Only an admin's own key makes server invites: a member's, an agent's and a browser's
// are refused, through the CLI and the API alike, and the agent never sees a link.
func TestOnlyAnAdminInvitesPeople(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	project := `{"server":{"name":"` + tm.url() + `","url":"` + tm.url() + `"},"board":"none"}`
	if err := os.WriteFile(filepath.Join(maya.dir, ".aboard"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	r := maya.runExit("invite", "--server", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_admin_required" {
		t.Fatalf("a member's invite:\n%s", r)
	}
	if status, v := tm.call("POST", "/v1/invites", tm.key(maya), map[string]any{}); status != http.StatusForbidden || errorCode(t, v) != "server_admin_required" {
		t.Fatalf("a member calling the API: %d %v", status, v)
	}

	line := field(t, tm.admin.run("pair", "--json").json(t), "join.line").(string)
	tm.admin.run("join", line, "--name", "helper")
	var agentToken string
	var creds struct {
		Agents []struct{ Name, Token string } `json:"agents"`
	}
	raw, _ := os.ReadFile(filepath.Join(tm.admin.configDir(), "credentials.json"))
	_ = json.Unmarshal(raw, &creds)
	for _, a := range creds.Agents {
		if a.Name == "helper" {
			agentToken = a.Token
		}
	}
	if status, v := tm.call("POST", "/v1/invites", agentToken, map[string]any{}); status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
		t.Fatalf("an admin's agent calling the API: %d %v", status, v)
	}
	browser := tm.admin.browserLogin()
	if status, v := tm.call("POST", "/v1/invites", browser, map[string]any{}); status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
		t.Fatalf("an admin's browser calling the API: %d %v", status, v)
	}
	s := tm.admin.claudeSession("s-invite")
	if r := s.e.exec(s.vars, "", "invite", "--person", "--server", tm.url(), "--json"); r.code != 1 || errorCode(t, r.json(t)) != "agent_session_required" || field(t, r.json(t), "error.next.command") != "aboard boards --server '"+tm.url()+"'" || strings.Contains(r.stdout, "abi_") {
		t.Fatalf("invite --server in an agent's session:\n%s", r)
	}
}

// A key works only on the server that issued it, and the CLI never sends it anywhere
// else: not to the person's own local server, and not to a server a project names.
func TestAKeyWorksOnlyOnItsServer(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	maya.run("up")
	if status, v := callAPI(t, "http://"+maya.addr, "GET", "/v1/me", tm.key(maya), nil); status != http.StatusUnauthorized {
		t.Fatalf("maya's team key on her own local server: %d %v", status, v)
	}
	if status, v := tm.call("GET", "/v1/me", tm.key(tm.admin)+"x", nil); status != http.StatusUnauthorized {
		t.Fatalf("a wrong key: %d %v", status, v)
	}
	r := maya.runExit("connect", tm.invite(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "already_connected" {
		t.Fatalf("connecting twice to one server:\n%s", r)
	}
	r = maya.runExit("connect", "http://example.com/join#abi_x", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "insecure_server" {
		t.Fatalf("an http invite to another machine:\n%s", r)
	}
}

// Two people's agents on one board exchange messages, each labelled as someone else's
// agent; the second person's agent runs in a session in her own home, woken by her own
// daemon. A person's own message is labelled as from the agent's owner, another
// person's as from another person.
func TestTwoPeoplesAgentsTalk(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	adminSums := treeSums(t, tm.admin.configDir())

	tm.admin.run("pair", "--name", "writer")
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	tm.admin.run("board", "add", "@maya")
	adminSums = treeSums(t, tm.admin.configDir())

	// maya, on the board now, brings her agent with a join line of her own.
	tm.link(maya, board)
	line := field(t, maya.run("invite", "--json").json(t), "join_line").(string)
	s := maya.claudeSession("s-maya")
	joined := s.run("join", line, "--json").json(t)
	if field(t, joined, "server.url") != tm.url() || field(t, joined, "agent.owner") != "maya" {
		t.Fatalf("maya's agent joined: %v", joined)
	}
	agent := field(t, joined, "agent.name").(string)

	stop := s.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatalf("stop hook returned without a message\n%s", stop.wait(time.Second))
	}
	tm.admin.run("say", "--as", "writer", "--to", "@"+agent, "Your draft, please.")
	woke := stop.wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `sender="other_agent"`) || !strings.Contains(woke.stderr, "Your draft, please.") {
		t.Fatalf("maya's session should wake with the other person's agent's message\n%s", woke)
	}

	s.run("say", "--to", "@writer", "Here it is.")
	in := tm.admin.run("inbox", "--as", "writer", "--json").json(t)
	if field(t, in, "messages.0.body") != "Here it is." || field(t, in, "messages.0.sender") != "other_agent" {
		t.Fatalf("writer's inbox: %v", in)
	}

	for _, post := range []struct {
		who        *env
		body, want string
	}{
		{tm.admin, "From alex.", "other_person"},
		{maya, "From maya.", "owner"},
	} {
		status, v := tm.call("POST", "/v1/boards/"+board+"/messages", tm.key(post.who), map[string]any{"body": post.body, "to": []string{"@" + agent}})
		if status != http.StatusCreated {
			t.Fatalf("posting as a person: %d %v", status, v)
		}
		got := s.run("inbox", "--json").json(t)
		msgs := field(t, got, "messages").([]any)
		last := msgs[len(msgs)-1]
		if field(t, last, "body") != post.body || field(t, last, "sender") != post.want {
			t.Fatalf("maya's agent reading %q: %v", post.body, got)
		}
	}

	// Nothing maya did touched the first person's home.
	after := treeSums(t, tm.admin.configDir())
	for path, sum := range after {
		if adminSums[path] != sum {
			t.Fatalf("maya's commands changed %s in the first person's home", path)
		}
	}
	if len(after) != len(adminSums) {
		t.Fatalf("maya's commands added files to the first person's home: %v", after)
	}
}
