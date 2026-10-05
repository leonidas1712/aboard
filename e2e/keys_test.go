//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keyID returns the id of the key called name in a keys --json listing.
func keyID(t *testing.T, list map[string]any, name string) string {
	t.Helper()
	for _, k := range field(t, list, "keys").([]any) {
		if field(t, k, "name") == name {
			return field(t, k, "id").(string)
		}
	}
	t.Fatalf("no key %s in %v", name, list)
	return ""
}

// agentToken returns the token a home keeps for one of its agents.
func agentToken(t *testing.T, e *env, name string) string {
	t.Helper()
	var creds struct {
		Agents []struct{ Name, Token string } `json:"agents"`
	}
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatal(err)
	}
	for _, a := range creds.Agents {
		if a.Name == name {
			return a.Token
		}
	}
	t.Fatalf("%s holds no token for %s", e.home, name)
	return ""
}

// teamBrowser logs a browser in with key on the team's server and returns its token.
func (tm *team) teamBrowser(key string) string {
	tm.t.Helper()
	status, code := tm.call("POST", "/v1/login-codes", key, nil)
	if status != http.StatusCreated {
		tm.t.Fatalf("login code: %d %v", status, code)
	}
	status, got := tm.call("POST", "/v1/browser-tokens", "", map[string]any{"code": code["code"]})
	if status != http.StatusCreated {
		tm.t.Fatalf("browser token: %d %v", status, got)
	}
	return got["token"].(string)
}

func (tm *team) works(token string, want bool) {
	tm.t.Helper()
	status, v := tm.call("GET", "/v1/me", token, nil)
	if (status == http.StatusOK) != want {
		tm.t.Fatalf("GET /v1/me: %d %v, want working=%v", status, v, want)
	}
}

// A person makes a key for their phone, sees it shown once and listed without its
// secret, and revokes it; the key stops working and their machine's key doesn't.
func TestKeysCreateListAndRevoke(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")

	text := maya.run("keys", "create", "phone", "--expires", "90d", "--server", tm.url())
	lines := text.lines()
	if len(lines) < 3 || !strings.HasPrefix(lines[0], `Key "phone" (shown once, then never again): abh_`) ||
		lines[1] != "Save it in your password manager. Anyone with it can sign in as you until you revoke it." ||
		!strings.Contains(lines[2], "(in 90 days). Revoke it with: aboard keys revoke phone") {
		t.Fatalf("keys create:\n%s", text)
	}
	phone := strings.TrimPrefix(lines[0], `Key "phone" (shown once, then never again): `)
	tm.works(phone, true)

	created := maya.run("keys", "create", "script", "--expires", "12h", "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "KeysCreateOutput", created)
	if !strings.HasPrefix(field(t, created, "key.token").(string), "abh_") {
		t.Fatalf("keys create --json: %v", created)
	}

	listed := maya.run("keys", "--server", tm.url(), "--json")
	list := listed.json(t)
	matchesCLISpec(t, "KeysOutput", list)
	if strings.Contains(listed.stdout, phone) || strings.Contains(listed.stdout, "abh_") {
		t.Fatalf("the listing shows a secret:\n%s", listed)
	}
	if n := len(field(t, list, "keys").([]any)); n != 3 || field(t, list, "current_key_id") != keyID(t, list, machineKeyName(t, list)) {
		t.Fatalf("maya's keys: %v", list)
	}
	plain := maya.run("keys", "--server", tm.url()).stdout
	if !strings.HasPrefix(plain, "Keys of maya on "+tm.url()+":\n") || !strings.Contains(plain, "(this machine)") ||
		!strings.Contains(plain, "expires when unused for") {
		t.Fatalf("keys:\n%s", plain)
	}

	revoked := maya.run("keys", "revoke", "phone", "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "KeysRevokeOutput", revoked)
	if field(t, revoked, "key.state") != "revoked" || field(t, revoked, "this_machine") != false {
		t.Fatalf("keys revoke: %v", revoked)
	}
	tm.works(phone, false)
	tm.works(tm.key(maya), true)
	r := maya.runExit("keys", "revoke", "nothing", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "key_not_selected" {
		t.Fatalf("revoking a key nobody has:\n%s", r)
	}
	r = maya.runExit("keys", "create", "x", "--expires", "400d", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "invalid_request" {
		t.Fatalf("a key for 400 days:\n%s", r)
	}
}

// machineKeyName is the name of the one key in a listing that expires once unused: the
// key aboard connect gave the machine.
func machineKeyName(t *testing.T, list map[string]any) string {
	t.Helper()
	for _, k := range field(t, list, "keys").([]any) {
		if field(t, k, "idle_expiry_seconds") != nil {
			return field(t, k, "name").(string)
		}
	}
	t.Fatalf("no machine key in %v", list)
	return ""
}

// The lost laptop: from her desktop, signed in with a key of its own, Maya revokes the
// laptop's key. The laptop's key, its browser and its agent stop; the desktop, her other
// key and her history don't.
func TestRevokingALostLaptopsKeyEndsWhatItStarted(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	laptop := tm.person("maya")
	line := field(t, tm.admin.run("pair", "--name", "writer", "--json").json(t), "join.line").(string)
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	joined := laptop.claudeSession("s-laptop").run("join", line, "--json").json(t)
	laptopAgent := agentToken(t, laptop, field(t, joined, "agent.name").(string))
	laptopBrowser := tm.teamBrowser(tm.key(laptop))
	tm.admin.run("say", "--as", "writer", "Before the laptop was lost.")

	desktopKey := field(t, laptop.run("keys", "create", "desktop", "--server", tm.url(), "--json").json(t), "key.token").(string)
	phone := field(t, laptop.run("keys", "create", "phone", "--server", tm.url(), "--json").json(t), "key.token").(string)
	desktop := newPersonHome(t, "maya")
	logged := desktop.exec(nil, desktopKey+"\n", "login", tm.url(), "--json")
	if logged.code != 0 {
		t.Fatalf("login:\n%s", logged)
	}
	desktopAgent := field(t, desktop.claudeSession("s-desktop").run("join", line, "--json").json(t), "agent.name").(string)

	list := desktop.run("keys", "--server", tm.url(), "--json").json(t)
	laptopKey := machineKeyName(t, list)
	for _, k := range field(t, list, "keys").([]any) {
		if field(t, k, "name") == laptopKey && (field(t, k, "browser_sessions") != 1.0 || field(t, k, "agent_seats") != 1.0) {
			t.Fatalf("the laptop's key before it is revoked: %v", k)
		}
	}
	out := desktop.run("keys", "revoke", laptopKey, "--server", tm.url())
	expectLines(t, out, "Revoked "+laptopKey+". Its 1 browser session and 1 agent stopped working with it.")

	for name, token := range map[string]string{"key": tm.key(laptop), "browser": laptopBrowser, "agent": laptopAgent} {
		status, v := tm.call("GET", "/v1/me", token, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("the laptop's %s after its key was revoked: %d %v", name, status, v)
		}
	}
	tm.works(tm.key(desktop), true)
	tm.works(phone, true)
	tm.works(agentToken(t, desktop, desktopAgent), true)
	status, v := tm.call("GET", "/v1/boards/"+board+"/messages", tm.key(desktop), nil)
	if status != http.StatusOK || !strings.Contains(strings.Join(bodies(v), "\n"), "Before the laptop was lost.") {
		t.Fatalf("maya's history from the desktop: %d %v", status, v)
	}
	// The laptop's own commands say its key no longer works and how to sign in again.
	r := laptop.runExit("keys", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "unauthorized" || !strings.Contains(r.stdout, "aboard login "+tm.url()) {
		t.Fatalf("keys on the laptop after its key was revoked:\n%s", r)
	}
}

func bodies(v map[string]any) []string {
	var out []string
	msgs, _ := v["messages"].([]any)
	for _, m := range msgs {
		if b, ok := m.(map[string]any)["body"].(string); ok {
			out = append(out, b)
		}
	}
	return out
}

// An admin lists and revokes a member's keys but has no way to create one for them; a
// member can't see or revoke anyone else's.
func TestAdminRevokesAMembersKeyButCantCreateOne(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	sam := tm.person("sam")

	list := tm.admin.run("keys", "--person", "maya", "--server", tm.url(), "--json").json(t)
	if field(t, list, "person.handle") != "maya" {
		t.Fatalf("the admin listing maya's keys: %v", list)
	}
	r := tm.admin.runExit("keys", "create", "for-maya", "--person", "maya", "--server", tm.url())
	if r.code != 2 {
		t.Fatalf("keys create --person:\n%s", r)
	}
	for _, args := range [][]string{
		{"keys", "--person", "alex", "--server", tm.url(), "--json"},
		{"keys", "--person", "sam", "--server", tm.url(), "--json"},
		{"keys", "revoke", "x", "--person", "sam", "--server", tm.url(), "--json"},
	} {
		r := maya.runExit(args...)
		if r.code != 1 || errorCode(t, r.json(t)) != "server_admin_required" {
			t.Fatalf("maya running %v:\n%s", args, r)
		}
	}
	samKey := keyID(t, sam.run("keys", "--server", tm.url(), "--json").json(t), machineKeyName(t, sam.run("keys", "--server", tm.url(), "--json").json(t)))
	if status, v := tm.call("DELETE", "/v1/keys/"+samKey, tm.key(maya), nil); status != http.StatusNotFound || errorCode(t, v) != "key_not_found" {
		t.Fatalf("maya revoking sam's key through the API: %d %v", status, v)
	}
	tm.works(tm.key(sam), true)

	revoked := tm.admin.run("keys", "revoke", machineKeyName(t, list), "--person", "maya", "--server", tm.url(), "--json").json(t)
	if field(t, revoked, "person") != "maya" || field(t, revoked, "this_machine") != false {
		t.Fatalf("the admin revoking maya's key: %v", revoked)
	}
	tm.works(tm.key(maya), false)
	tm.works(tm.key(sam), true)
	tm.works(tm.key(tm.admin), true)
}

// Keys are a person's own: in an agent's session the commands refuse and never print a
// key, and on the API an agent's token and a browser can't list, create or revoke one.
func TestAgentsAndBrowsersCantManageKeys(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	s := tm.admin.claudeSession("s-keys")
	for _, args := range [][]string{{"keys", "--json"}, {"keys", "create", "x", "--json"}, {"keys", "revoke", "x", "--json"}} {
		r := s.e.exec(s.vars, "", args...)
		if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" || strings.Contains(r.stdout, "abh_") {
			t.Fatalf("%v in an agent's session:\n%s", args, r)
		}
	}
	if r := s.e.exec(s.vars, tm.key(tm.admin)+"\n", "login", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
		t.Fatalf("login in an agent's session:\n%s", r)
	}

	line := field(t, tm.admin.run("pair", "--json").json(t), "join.line").(string)
	tm.admin.run("join", line, "--name", "helper")
	keyList := tm.admin.run("keys", "--json").json(t)
	own := field(t, keyList, "current_key_id").(string)
	for name, token := range map[string]string{"agent": agentToken(t, tm.admin, "helper"), "browser": tm.admin.browserLogin()} {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/v1/keys", nil},
			{"POST", "/v1/keys", map[string]any{"name": "stolen"}},
			{"DELETE", "/v1/keys/" + own, nil},
		} {
			status, v := tm.call(req.method, req.path, token, req.body)
			if status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
				t.Fatalf("%s: %s %s: %d %v", name, req.method, req.path, status, v)
			}
		}
	}
	if n := len(field(t, tm.admin.run("keys", "--json").json(t), "keys").([]any)); n != 1 {
		t.Fatalf("keys after the refusals: %d", n)
	}
}

// aboard login on a second home saves the key for that server alone: the home's own
// commands without a server go to its own local server, with its own key. A bad key is
// refused and nothing is saved.
func TestLoginOnASecondHomeUsesTheKeyOnlyForItsServer(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	key := field(t, maya.run("keys", "create", "desktop", "--server", tm.url(), "--json").json(t), "key.token").(string)
	tm.works(key, true)

	second := newPersonHome(t, "maya")
	r := second.exec(nil, "abh_wrong\n", "login", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "key_invalid" {
		t.Fatalf("login with a wrong key:\n%s", r)
	}
	r = second.exec(nil, "", "login", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "key_invalid" {
		t.Fatalf("login with no key:\n%s", r)
	}
	r = second.exec(nil, key+"\n", "login", "http://example.com", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "insecure_server" {
		t.Fatalf("login over http to another machine:\n%s", r)
	}
	if _, err := os.Stat(filepath.Join(second.configDir(), "servers.json")); err == nil {
		t.Fatal("a refused login saved a key")
	}

	text := second.exec(nil, key+"\n", "login", tm.url())
	if text.code != 0 || !strings.HasPrefix(text.stdout, "Logged in to "+tm.url()+` as maya with the key "desktop", saved for that server only.`) ||
		!strings.Contains(text.stdout, "A key used in two places is revoked in both at once.") {
		t.Fatalf("login:\n%s", text)
	}
	again := second.exec(nil, key+"\n", "login", tm.url(), "--json")
	if again.code != 0 {
		t.Fatalf("login again:\n%s", again)
	}
	out := again.json(t)
	matchesCLISpec(t, "LoginOutput", out)
	if field(t, out, "replaced") != true || field(t, out, "previous_use") == nil || field(t, out, "key.name") != "desktop" {
		t.Fatalf("logging in again: %v", out)
	}
	info, err := os.Stat(filepath.Join(second.configDir(), "servers.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("servers.json: %v %v", info, err)
	}
	if tm.key(second) != key {
		t.Fatal("login saved another key")
	}

	teamMaya := field(t, second.run("keys", "--server", tm.url(), "--json").json(t), "person.id")
	localMaya := field(t, second.run("keys", "--json").json(t), "person.id")
	if teamMaya != tm.me(key)["id"] || localMaya == teamMaya {
		t.Fatalf("the second home's people: on the team server %v, on its own %v", teamMaya, localMaya)
	}
	if status, _ := callAPI(t, "http://"+second.addr, "GET", "/v1/me", key, nil); status != http.StatusUnauthorized {
		t.Fatalf("the team key on the second home's own server: %d", status)
	}
}

// When the key a swarm's seats came from is revoked, swarm ps and show say the seats
// ended instead of showing sessions that silently can't act; signing in again with
// another key doesn't revive them.
func TestSwarmSaysWhenTheKeyItsSeatsCameFromEnds(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: keyed\nagents:\n  - {name: worker, harness: claude-code, launcher: headless}\n")
	s.run("swarm", "up", "--json")
	if ag := agentsByName(t, s.run("swarm", "ps", "--json").json(t))["worker"]; ag["seat_credential"] != "works" {
		t.Fatalf("the worker's seat after swarm up: %v", ag)
	}
	spare := field(t, s.run("keys", "create", "spare", "--json").json(t), "key.token").(string)
	own := field(t, s.run("keys", "--json").json(t), "current_key_id").(string)
	r := s.runExit("keys", "revoke", own, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" {
		t.Fatalf("revoking this machine's key without --yes:\n%s", r)
	}
	revoked := s.run("keys", "revoke", own, "--yes", "--json").json(t)
	if field(t, revoked, "this_machine") != true || field(t, revoked, "key.agent_seats") != 1.0 {
		t.Fatalf("revoking this machine's key: %v", revoked)
	}

	for _, signedIn := range []bool{false, true} {
		if signedIn {
			if r := s.exec(nil, spare+"\n", "login", "--json"); r.code != 0 {
				t.Fatalf("login with the spare key:\n%s", r)
			}
		}
		out := s.run("swarm", "ps", "--json").json(t)
		matchesCLISpec(t, "SwarmPsOutput", out)
		if ag := agentsByName(t, out)["worker"]; ag["seat_credential"] != "ended" {
			t.Fatalf("the worker's seat after its key was revoked (signed in again: %v): %v", signedIn, ag)
		}
		ps := s.run("swarm", "ps").stdout
		if !strings.Contains(ps, "worker can't act on keyed any more: the access key its seat came from was revoked or has expired.") {
			t.Fatalf("swarm ps after the key was revoked:\n%s", ps)
		}
		show := s.run("swarm", "show").stdout
		if !strings.Contains(show, "ended: the access key it came from was revoked or has expired") {
			t.Fatalf("swarm show after the key was revoked:\n%s", show)
		}
	}

	// Signed in again with the board file unchanged, swarm up refuses the ended seat at
	// once instead of starting a session that could never take it.
	s.run("swarm", "down")
	started := time.Now()
	r = s.runExit("swarm", "up", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "seat_ended" || !strings.Contains(r.stdout, "new name in the board file") {
		t.Fatalf("swarm up with an ended seat:\n%s", r)
	}
	if time.Since(started) > 20*time.Second {
		t.Fatalf("swarm up took %s to refuse the ended seat", time.Since(started))
	}
	if ag := agentsByName(t, s.run("swarm", "ps", "--json").json(t))["worker"]; ag["state"] == "running" {
		t.Fatalf("swarm up started the ended seat's session: %v", ag)
	}
}

// aboard login checks where a key would go before it reads or sends one, wherever the
// server came from: a project's .aboard naming a server over plain http, or an address
// that isn't a server's, is refused without a request.
func TestLoginRefusesAnUnsafeServerFromTheProject(t *testing.T) {
	t.Parallel()
	home := newPersonHome(t, "maya")
	for url, code := range map[string]string{
		"http://team.example.com":      "insecure_server",
		"https://team.example.com/x":   "invalid_request",
		"https://u@team.example.com":   "invalid_request",
		"ftp://team.example.com":       "invalid_request",
		"https://team.example.com?a=b": "invalid_request",
	} {
		project := `{"server":{"name":"` + url + `","url":"` + url + `"},"board":"none"}`
		if err := os.WriteFile(filepath.Join(home.dir, ".aboard"), []byte(project), 0o600); err != nil {
			t.Fatal(err)
		}
		r := home.exec(nil, "abh_"+strings.Repeat("A", 43)+"\n", "login", "--json")
		if r.code != 1 || errorCode(t, r.json(t)) != code {
			t.Fatalf("login with .aboard naming %s:\n%s", url, r)
		}
	}
	if _, err := os.Stat(filepath.Join(home.configDir(), "servers.json")); err == nil {
		t.Fatal("a refused login saved a key")
	}
}
