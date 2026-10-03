//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// browserLogin runs aboard open and trades its link for a browser token, the way the
// web UI does, and returns the token.
func (e *env) browserLogin() string {
	e.t.Helper()
	link, err := url.Parse(e.openUI("true").json(e.t)["url"].(string))
	if err != nil {
		e.t.Fatal(err)
	}
	r := e.exchange(link)
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(r.body), &got); r.status != http.StatusCreated || err != nil || !strings.HasPrefix(got.Token, "abb_") {
		e.t.Fatalf("exchanging the login code: %d %s", r.status, r.body)
	}
	return got.Token
}

// expectLoggedIn checks whether token still reads the board list, and that a token
// that doesn't is told to run aboard open.
func (e *env) expectLoggedIn(token string, want bool) {
	e.t.Helper()
	r := e.get("http://"+e.addr+"/v1/boards", token, "")
	switch {
	case want && r.status != http.StatusOK:
		e.t.Fatalf("the browser was logged out: %d %s\nserver log:\n%s", r.status, r.body, e.serverLog())
	case !want && (r.status != http.StatusUnauthorized || !strings.Contains(r.body, "aboard open")):
		e.t.Fatalf("want 401 saying to run aboard open, got %d %s", r.status, r.body)
	}
}

// A browser login outlives the server: aboard down and up, and a newer aboard replacing
// an older local server, both keep the browser logged in. The database holds only a
// digest of the token.
func TestBrowserLoginSurvivesARestartAndAnUpgrade(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = oldBinary
	e.run("pair", "writer-reviewer")
	token := e.browserLogin()
	e.expectLoggedIn(token, true)

	e.run("down")
	e.run("up")
	e.expectLoggedIn(token, true)

	oldPID := e.serverPID()
	e.bin = binary
	e.run("up")
	if pid := e.serverPID(); pid == oldPID || e.serverVersion() == oldVersion {
		t.Fatalf("the older server (pid %d) wasn't replaced", oldPID)
	}
	e.expectLoggedIn(token, true)

	secret := strings.TrimPrefix(token, "abb_")
	err := filepath.WalkDir(e.aboardHome(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSocket != 0 {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // the test's own home
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), secret) {
			t.Errorf("%s holds the browser token in plain text", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLogoutBrowsersEndsEveryBrowserLogin(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer")
	first, second := e.browserLogin(), e.browserLogin()

	if r := e.runExit("logout"); r.code != 2 || !strings.Contains(r.stderr, "--browsers") {
		t.Fatalf("aboard logout without --browsers:\n%s", r)
	}
	inSession := e.exec([]string{"CLAUDECODE=1"}, "", "logout", "--browsers", "--json")
	if inSession.code != 1 || field(t, inSession.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("aboard logout --browsers inside a session:\n%s", inSession)
	}
	e.expectLoggedIn(first, true)

	out := e.run("logout", "--browsers", "--json").json(t)
	if out["ended"] != float64(2) || out["server_started"] != false {
		t.Fatalf("aboard logout --browsers: %v", out)
	}
	e.expectLoggedIn(first, false)
	e.expectLoggedIn(second, false)

	expectLines(t, e.run("logout", "--browsers"), "No browser was logged in.")
	third := e.browserLogin()
	e.expectLoggedIn(third, true)
	expectLines(t, e.run("logout", "--browsers"), "Logged out 1 browser. Run aboard open to log in again.")
	e.expectLoggedIn(third, false)
}
