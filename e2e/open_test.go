//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openUI runs aboard open --json with BROWSER set to browser, which aboard runs with
// the URL, and returns the result.
func (e *env) openUI(browser string, args ...string) result {
	e.t.Helper()
	return e.exec([]string{"BROWSER=" + browser}, "", append([]string{"open", "--json"}, args...)...)
}

// reply is a finished HTTP response, its body read.
type reply struct {
	status int
	header http.Header
	body   string
}

// get makes a GET request to the local server the way a browser would, without
// following redirects.
func (e *env) get(rawURL string, cookie *http.Cookie, host string) reply {
	e.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		e.t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if host != "" {
		req.Host = host
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

func TestOpenLogsTheBrowserInOnceWithAReadOnlyCookie(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "--json").json(t), "join.line").(string)
	e.run("join", line)
	board := "writer-reviewer"
	e.sayAs("writer", "--to", "@reviewer", "Draft is in notes.md.")

	out := e.openUI("true").json(t)
	if out["opened"] != true || out["board"] != board || out["server_started"] != false {
		t.Fatalf("aboard open: %v", out)
	}
	link, err := url.Parse(out["url"].(string))
	if err != nil || link.Host != e.addr || link.Path != "/login" || link.Query().Get("board") != board || link.Query().Get("code") == "" {
		t.Fatalf("login link %v: %v", out["url"], err)
	}

	first := e.get(link.String(), nil, "")
	if first.status != http.StatusSeeOther || first.header.Get("Location") != "/?board="+board {
		t.Fatalf("first login: %d to %q", first.status, first.header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range (&http.Response{Header: first.header}).Cookies() {
		if c.Name == "aboard_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login cookie: %q", first.header.Get("Set-Cookie"))
	}
	again := e.get(link.String(), nil, "")
	if again.status != http.StatusNotFound || !strings.Contains(again.body, "aboard open") {
		t.Fatalf("second use of the link: %d %s", again.status, again.body)
	}

	base := "http://" + e.addr
	if r := e.get(base+"/v1/boards", cookie, ""); r.status != http.StatusOK || !strings.Contains(r.body, `"name":"`+board+`"`) {
		t.Fatalf("boards with the cookie: %d %s", r.status, r.body)
	}
	if r := e.get(base+"/v1/boards/"+board+"/messages", cookie, ""); r.status != http.StatusOK || !strings.Contains(r.body, "Draft is in notes.md.") {
		t.Fatalf("messages with the cookie: %d %s", r.status, r.body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/stream", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	event, err := bufio.NewReader(stream.Body).ReadString('\n')
	_ = stream.Body.Close()
	if stream.StatusCode != http.StatusOK || err != nil || event != "event: head\n" {
		t.Fatalf("stream with the cookie: %d %q %v", stream.StatusCode, event, err)
	}

	post, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/boards/"+board+"/messages", strings.NewReader(`{"body":"from a web page"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	post.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "browser_read_only") {
		t.Fatalf("post with only the cookie: %d %s %v", resp.StatusCode, body, err)
	}
}

func TestLocalServerRefusesOtherHosts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	r := e.get("http://"+e.addr+"/v1/info", nil, "rebind.example:"+e.port())
	if r.status != http.StatusMisdirectedRequest || !strings.Contains(r.body, "host_not_allowed") {
		t.Fatalf("other host: %d %s", r.status, r.body)
	}
	if r := e.get("http://"+e.addr+"/v1/info", nil, "localhost:"+e.port()); r.status != http.StatusOK {
		t.Fatalf("localhost: %d", r.status)
	}
}

func TestBinaryWithoutTheUIServesAPageSayingHowToGetIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	r := e.get("http://"+e.addr+"/", nil, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, "built without its web UI") || !strings.Contains(r.body, "make install") {
		t.Fatalf("/ without the UI: %d %s", r.status, r.body)
	}
}

func TestOpenWithoutABrowserPrintsTheLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	r := e.exec([]string{"BROWSER=false"}, "", "open")
	if r.code != 0 {
		t.Fatalf("aboard open without a browser:\n%s", r)
	}
	lines := r.lines()
	want := "http://" + e.addr + "/login?code="
	if len(lines) != 3 || lines[0] != "Started local Aboard at http://"+e.addr ||
		lines[1] != "Couldn't open a browser. Open this link in one within 60 seconds; it works once:" ||
		!strings.HasPrefix(lines[2], "  "+want) {
		t.Fatalf("output:\n%s", r)
	}
}

// recordingBrowser writes a stand-in browser that saves the URL it is given to a file,
// and returns the browser's path and the file's.
func (e *env) recordingBrowser() (browser, saved string) {
	e.t.Helper()
	browser = filepath.Join(e.home, "browser")
	saved = filepath.Join(e.home, "browser-url")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > " + saved + "\n"
	if err := os.WriteFile(browser, []byte(script), 0o755); err != nil { //nolint:gosec // the stand-in browser must be executable
		e.t.Fatal(err)
	}
	return browser, saved
}

func TestOpenInsideASessionNeverShowsTheLoginLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair")
	browser, saved := e.recordingBrowser()
	for _, session := range []string{"CLAUDECODE=1", "ABOARD_SESSION=claude-code:5f1c"} {
		r := e.exec([]string{session, "BROWSER=" + browser}, "", "open", "--json")
		if r.code != 0 {
			t.Fatalf("with %s:\n%s", session, r)
		}
		out := r.json(t)
		if out["url"] != nil || out["ui_url"] != "http://"+e.addr+"/?board=writer-reviewer" || strings.Contains(r.stdout, "code=") {
			t.Fatalf("with %s, the output shows the login link or misses the UI address:\n%s", session, r)
		}
		raw, err := os.ReadFile(saved)
		if err != nil {
			t.Fatal(err)
		}
		link := string(raw)
		if strings.Contains(r.stdout+r.stderr, strings.TrimPrefix(link, "http://"+e.addr)) {
			t.Fatalf("the output holds the login code:\n%s", r)
		}
		if got := e.get(link, nil, ""); got.status != http.StatusSeeOther {
			t.Fatalf("the link the browser got doesn't log in: %d %s", got.status, got.body)
		}
	}
	text := e.exec([]string{"CLAUDECODE=1", "BROWSER=" + browser}, "", "open")
	expectLines(t, text, "Opened writer-reviewer in your browser: http://"+e.addr+"/?board=writer-reviewer")

	r := e.exec([]string{"CLAUDECODE=1", "BROWSER=false"}, "", "open", "--json")
	out := r.json(t)
	if r.code != 1 || field(t, out, "error.code") != "browser_unavailable" ||
		field(t, out, "error.hint") != "Give your human this command to run in their own terminal: aboard open --board writer-reviewer" {
		t.Fatalf("want browser_unavailable naming the board\n%s", r)
	}
}

func TestOpenRefusesUnknownBoards(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if r := e.openUI("true", "--board", "nope"); r.code != 1 || field(t, r.json(t), "error.code") != "board_not_found" {
		t.Fatalf("want board_not_found\n%s", r)
	}
}
