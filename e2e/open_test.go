//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
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

// request makes a request to the local server, sending token as a bearer token when
// it is set, without following redirects.
func (e *env) request(method, rawURL, token, host, body string) reply {
	e.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
	if c := resp.Header.Values("Set-Cookie"); len(c) > 0 {
		e.t.Fatalf("%s %s set a cookie: %q", method, rawURL, c)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// get makes a GET request to the local server, the way a browser would.
func (e *env) get(rawURL, token, host string) reply {
	e.t.Helper()
	return e.request(http.MethodGet, rawURL, token, host, "")
}

// exchange trades the code in a login link's fragment for a browser token, the way the
// web UI does.
func (e *env) exchange(link *url.URL) reply {
	e.t.Helper()
	frag, err := url.ParseQuery(link.Fragment)
	if err != nil {
		e.t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"code": frag.Get("code")})
	if err != nil {
		e.t.Fatal(err)
	}
	return e.request(http.MethodPost, "http://"+link.Host+"/v1/browser-tokens", "", "", string(body))
}

func TestOpenLogsTheBrowserInOnceAsItsPerson(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	e.run("join", line)
	board := "writer-reviewer"
	e.sayAs("writer", "--to", "@reviewer", "Draft is in notes.md.")

	out := e.openUI("true").json(t)
	if out["opened"] != true || out["board"] != board || out["server_started"] != false {
		t.Fatalf("aboard open: %v", out)
	}
	link, err := url.Parse(out["url"].(string))
	if err != nil || link.Host != e.addr || link.Path != "/" || link.RawQuery != "" {
		t.Fatalf("login link %v: %v", out["url"], err)
	}
	frag, err := url.ParseQuery(link.Fragment)
	if err != nil || frag.Get("board") != board || !strings.HasPrefix(frag.Get("code"), "abl_") {
		t.Fatalf("login link's fragment %q: %v", link.Fragment, err)
	}

	first := e.exchange(link)
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(first.body), &got); first.status != http.StatusCreated || err != nil || !strings.HasPrefix(got.Token, "abb_") {
		t.Fatalf("exchanging the code: %d %s", first.status, first.body)
	}
	token := got.Token
	if again := e.exchange(link); again.status != http.StatusNotFound || !strings.Contains(again.body, "login_code_invalid") || !strings.Contains(again.body, "aboard open") {
		t.Fatalf("second use of the code: %d %s", again.status, again.body)
	}

	base := "http://" + e.addr
	if r := e.get(base+"/v1/boards", token, ""); r.status != http.StatusOK || !strings.Contains(r.body, `"name":"`+board+`"`) {
		t.Fatalf("boards with the browser token: %d %s", r.status, r.body)
	}
	if r := e.get(base+"/v1/boards/"+board+"/messages", token, ""); r.status != http.StatusOK || !strings.Contains(r.body, "Draft is in notes.md.") {
		t.Fatalf("messages with the browser token: %d %s", r.status, r.body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/stream", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	event, err := bufio.NewReader(stream.Body).ReadString('\n')
	_ = stream.Body.Close()
	if stream.StatusCode != http.StatusOK || err != nil || event != "event: head\n" {
		t.Fatalf("stream with the browser token: %d %q %v", stream.StatusCode, event, err)
	}

	// The browser acts as the person who ran aboard open, so it posts as them.
	post := e.request(http.MethodPost, base+"/v1/boards/"+board+"/messages", token, "", `{"body":"from the browser"}`)
	if post.status != http.StatusCreated || !strings.Contains(post.body, `"kind":"human"`) {
		t.Fatalf("post with the browser token: %d %s", post.status, post.body)
	}
	if r := e.run("read", "--as", "writer", "--json"); !strings.Contains(r.stdout, "from the browser") {
		t.Fatalf("the writer doesn't see the browser's post:\n%s", r)
	}
	code := e.request(http.MethodPost, base+"/v1/login-codes", token, "", "")
	if code.status != http.StatusForbidden || !strings.Contains(code.body, "human_token_required") {
		t.Fatalf("login code with the browser token: %d %s", code.status, code.body)
	}
}

func TestLocalServerRefusesOtherHosts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	r := e.get("http://"+e.addr+"/v1/info", "", "rebind.example:"+e.port())
	if r.status != http.StatusMisdirectedRequest || !strings.Contains(r.body, "host_not_allowed") {
		t.Fatalf("other host: %d %s", r.status, r.body)
	}
	if r := e.get("http://"+e.addr+"/v1/info", "", "localhost:"+e.port()); r.status != http.StatusOK {
		t.Fatalf("localhost: %d", r.status)
	}
}

func TestBinaryWithoutTheUIServesAPageSayingHowToGetIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	r := e.get("http://"+e.addr+"/", "", "")
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
	want := "http://" + e.addr + "/#code="
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
	writeProgram(e.t, browser, []byte(script))
	return browser, saved
}

func TestOpenInsideASessionNeverShowsTheLoginLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer")
	browser, saved := e.recordingBrowser()
	for _, session := range []string{"CLAUDECODE=1", "ABOARD_SESSION=claude-code:5f1c", "ABOARD_AGENT=writer"} {
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
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.exchange(u); got.status != http.StatusCreated {
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
