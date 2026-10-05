//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// cookieBrowser is a browser signed in with a session cookie, as the board view is.
type cookieBrowser struct {
	t      *testing.T
	base   string // the server's origin, which the page sends as Origin
	cookie *http.Cookie
	csrf   string
	id     string
}

// signInBrowser posts a sign-in to base's /v1/browser-sessions from base's own page and
// returns the status, the decoded body and the cookie it set.
func signInBrowser(t *testing.T, base string, body map[string]string) (int, map[string]any, *http.Cookie) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/browser-sessions", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	var c *http.Cookie
	if cs := resp.Cookies(); len(cs) > 0 {
		c = cs[0]
	}
	return resp.StatusCode, v, c
}

func mustSignIn(t *testing.T, base string, body map[string]string) *cookieBrowser {
	t.Helper()
	status, v, c := signInBrowser(t, base, body)
	if status != http.StatusCreated || c == nil || !c.HttpOnly {
		t.Fatalf("signing a browser in: %d %v %+v", status, v, c)
	}
	return &cookieBrowser{t: t, base: base, cookie: c, csrf: v["csrf_token"].(string), id: v["id"].(string)}
}

// do makes a request as the board view does: the cookie, its Origin and, for a write,
// the CSRF token.
func (b *cookieBrowser) do(method, path string, body any) (int, map[string]any) {
	b.t.Helper()
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, b.base+path, payload)
	if err != nil {
		b.t.Fatal(err)
	}
	req.AddCookie(b.cookie)
	req.Header.Set("Origin", b.base)
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		req.Header.Set("X-Aboard-CSRF", b.csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

// signedInAs checks who the browser is signed in as, or, with want empty, that it isn't.
func (b *cookieBrowser) signedInAs(want string) {
	b.t.Helper()
	status, v := b.do(http.MethodGet, "/v1/me", nil)
	switch {
	case want != "" && (status != http.StatusOK || v["name"] != want):
		b.t.Fatalf("the browser: %d %v, want it signed in as %s", status, v, want)
	case want == "" && status != http.StatusUnauthorized:
		b.t.Fatalf("the browser: %d %v, want it signed out", status, v)
	}
}

// aboard open signs the browser in with a cookie; the person lists that session with its
// key and signs it out from the terminal, leaving the key working.
func TestOpenSignsABrowserInThatKeysSessionsCanEnd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer")
	link, err := url.Parse(e.openUI("true").json(t)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	frag, err := url.ParseQuery(link.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + link.Host
	b := mustSignIn(t, base, map[string]string{"code": frag.Get("code")})
	b.signedInAs("alex")

	out := e.run("keys", "sessions", "--json").json(t)
	matchesCLISpec(t, "KeysSessionsOutput", out)
	sessions := out["sessions"].([]any)
	if len(sessions) != 1 || field(t, sessions[0], "id") != b.id || field(t, sessions[0], "started_with") != "login_code" {
		t.Fatalf("aboard keys sessions: %v", out)
	}
	keyName := field(t, sessions[0], "key.name").(string)
	text := e.run("keys", "sessions", keyName)
	if !strings.Contains(text.stdout, b.id) || !strings.Contains(text.stdout, "aboard open") || !strings.Contains(text.stdout, "aboard keys sessions end <id>") {
		t.Fatalf("aboard keys sessions %s:\n%s", keyName, text)
	}
	if !strings.Contains(e.run("keys").stdout, "browser sessions: 1") {
		t.Fatal("aboard keys doesn't count the browser session")
	}

	inSession := e.exec([]string{"CLAUDECODE=1"}, "", "keys", "sessions", "end", b.id, "--json")
	if inSession.code != 1 || field(t, inSession.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("aboard keys sessions end inside a session:\n%s", inSession)
	}
	b.signedInAs("alex")

	ended := e.run("keys", "sessions", "end", b.id, "--json").json(t)
	matchesCLISpec(t, "KeysSessionsEndOutput", ended)
	b.signedInAs("")
	expectLines(t, e.run("keys", "sessions"), "No browser is signed in to "+base+" as alex.")
	again := e.runExit("keys", "sessions", "end", b.id, "--json")
	if again.code != 1 || field(t, again.json(t), "error.code") != "browser_session_not_found" {
		t.Fatalf("ending it again:\n%s", again)
	}
}

// On a team server, a person signs a browser in by pasting a key they made for it. The
// session is theirs alone: another person can't end it, and revoking the key ends it.
func TestAPastedKeySignsABrowserInOnATeamServer(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	phone := maya.run("keys", "create", "phone", "--server", tm.url(), "--json").json(t)
	key := field(t, phone, "key.token").(string)

	if status, v, c := signInBrowser(t, tm.url(), map[string]string{"key": key + "x"}); status != http.StatusUnauthorized || c != nil ||
		field(t, v, "error.code") != "access_key_invalid" {
		t.Fatalf("a mistyped key: %d %v", status, v)
	}
	b := mustSignIn(t, tm.url(), map[string]string{"key": key})
	b.signedInAs("maya")
	if status, v := b.do(http.MethodPost, "/v1/keys", map[string]any{"name": "more"}); status != http.StatusForbidden {
		t.Fatalf("the browser making a key: %d %v", status, v)
	}

	out := maya.run("keys", "sessions", "phone", "--server", tm.url(), "--json").json(t)
	if s := out["sessions"].([]any); len(s) != 1 || field(t, s[0], "started_with") != "access_key" || field(t, s[0], "key.name") != "phone" {
		t.Fatalf("maya's phone sessions: %v", out)
	}
	byAdmin := tm.admin.runExit("keys", "sessions", "end", b.id, "--json")
	if byAdmin.code != 1 || field(t, byAdmin.json(t), "error.code") != "browser_session_not_found" {
		t.Fatalf("the admin ending maya's session:\n%s", byAdmin)
	}
	b.signedInAs("maya")

	maya.run("keys", "revoke", "phone", "--server", tm.url())
	b.signedInAs("")
}
