//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureTokens are the secrets of the server in testdata/schema10: a local server made
// by aboard before it had people and access keys, with a paired board, two agents, two
// messages, an acknowledged inbox and a browser login.
type fixtureTokens struct {
	Owner   string            `json:"owner"`
	Browser string            `json:"browser"`
	Agents  map[string]string `json:"agents"`
}

// installSchema10 puts the fixture's database, owner key and agent tokens into e's home,
// as an existing install would have them.
func installSchema10(t *testing.T, e *env) fixtureTokens {
	t.Helper()
	var tok fixtureTokens
	raw, err := os.ReadFile("testdata/schema10/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		t.Fatal(err)
	}
	db, err := os.ReadFile("testdata/schema10/aboard.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{e.dataDir(), e.configDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	creds := map[string]any{"agents": []map[string]string{
		{"server": "http://" + e.addr, "board": "writer-reviewer", "name": "writer", "token": tok.Agents["writer"]},
		{"server": "http://" + e.addr, "board": "writer-reviewer", "name": "reviewer", "token": tok.Agents["reviewer"]},
	}}
	credsJSON, _ := json.Marshal(creds)
	for path, content := range map[string][]byte{
		filepath.Join(e.dataDir(), "aboard.db"):           db,
		filepath.Join(e.configDir(), "local-owner-token"): []byte(tok.Owner + "\n"),
		filepath.Join(e.configDir(), "credentials.json"):  credsJSON,
		filepath.Join(e.dir, ".aboard"):                   []byte(`{"server":{"name":"local","url":"http://` + e.addr + `"},"board":"writer-reviewer"}`),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tok
}

// A local server from before people and access keys keeps working after the upgrade,
// with nothing for its person to do: its owner becomes the server's first admin with
// the same key, the browser login and the agents' tokens still work, every read position
// and the hash chain are kept, and a backup of the old database is kept beside it.
func TestUpgradeKeepsAServerFromBeforePeople(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tok := installSchema10(t, e)
	e.run("up")

	me := func(token string) map[string]any {
		status, v := callAPI(t, "http://"+e.addr, "GET", "/v1/me", token, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /v1/me: %d %v\nserver log:\n%s", status, v, e.serverLog())
		}
		return v
	}
	owner := me(tok.Owner)
	if owner["id"] != "hum_01M456DFRK2C2KFJCP5N62N8BT" || owner["server_role"] != "admin" || owner["name"] != "alex" {
		t.Fatalf("the owner after the upgrade: %v", owner)
	}
	if b := me(tok.Browser); b["browser"] != true || b["id"] != owner["id"] {
		t.Fatalf("the browser login after the upgrade: %v", b)
	}

	// The writer's position is before the reviewer's reply; the reviewer read everything.
	writer := e.run("inbox", "--as", "writer", "--peek", "--json").json(t)
	if msgs := field(t, writer, "messages").([]any); len(msgs) != 1 || field(t, msgs[0], "body") != "Reading it now." {
		t.Fatalf("writer's inbox after the upgrade: %v", writer)
	}
	if msgs := field(t, e.run("inbox", "--as", "reviewer", "--json").json(t), "messages").([]any); len(msgs) != 0 {
		t.Fatalf("reviewer's inbox after the upgrade: %v", msgs)
	}
	e.run("say", "--as", "reviewer", "--to", "@writer", "Done.")
	if r := e.run("audit", "verify"); !strings.HasPrefix(r.stdout, "OK: 8 events on writer-reviewer verified") {
		t.Fatalf("audit verify after the upgrade:\n%s", r)
	}

	// The first person is the server's admin, so they can invite people.
	e.run("invite", "--server")

	backups, err := filepath.Glob(filepath.Join(e.dataDir(), "backups", "aboard-*-schema-10.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}
	if info, err := os.Stat(backups[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup: %v %v", info, err)
	}
}
