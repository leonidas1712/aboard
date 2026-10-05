//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a buffer a running command writes to while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waiting is aboard running in the background, without a terminal.
type waiting struct {
	t        *testing.T
	args     []string
	out, err *syncBuffer
	done     chan struct{}
	code     int
}

// start runs aboard with args in the background.
func (e *env) start(args ...string) *waiting {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = e.dir
	cmd.Env = append([]string{}, e.vars...)
	w := &waiting{t: e.t, args: args, out: &syncBuffer{}, err: &syncBuffer{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = w.out, w.err
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	go func() {
		var exit *exec.ExitError
		if err := cmd.Wait(); errors.As(err, &exit) {
			w.code = exit.ExitCode()
		}
		close(w.done)
	}()
	e.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-w.done
	})
	return w
}

var approveCode = regexp.MustCompile(`aboard approve ([0-9A-HJKMNP-TV-Z]{3}-[0-9A-HJKMNP-TV-Z]{3})`)

// shownCode waits for the command to show the code to approve, on standard output or, with
// --json, standard error.
func (w *waiting) shownCode() string {
	w.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if m := approveCode.FindStringSubmatch(w.out.String() + w.err.String()); m != nil {
			return m[1]
		}
		select {
		case <-w.done:
			w.t.Fatalf("aboard %v exited before showing a code:\n%s", w.args, w.result())
		case <-time.After(20 * time.Millisecond):
		}
	}
	w.t.Fatalf("aboard %v showed no code:\n%s", w.args, w.result())
	return ""
}

// wait waits for the command to exit.
func (w *waiting) wait() result {
	w.t.Helper()
	select {
	case <-w.done:
		return w.result()
	case <-time.After(20 * time.Second):
		w.t.Fatalf("aboard %v still running:\n%s", w.args, w.result())
		return result{}
	}
}

func (w *waiting) result() result {
	return result{args: w.args, stdout: w.out.String(), stderr: w.err.String(), code: w.code}
}

// host is the server's host and port, as aboard approve names it.
func (tm *team) host() string { return strings.TrimPrefix(tm.url(), "http://") }

// The second machine: Maya's desktop asks to connect with the server's address alone and
// shows a code; on her laptop, signed in, she sees the request and approves it; the
// desktop collects a key of its own. Revoking the laptop's key leaves the desktop's
// working.
func TestApprovingASecondMachine(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	laptop := tm.person("maya")
	desktop := newPersonHome(t, "maya")

	connecting := desktop.start("connect", tm.url(), "--name", "maya-desktop")
	code := connecting.shownCode()
	if got := connecting.out.String(); !strings.HasPrefix(got,
		`Approve this machine ("maya-desktop") from one where you're signed in to `+tm.url()+":\n"+
			"  aboard approve "+code+"     (expires in 5 minutes)\n"+
			"Or paste a key with: aboard login "+tm.url()+"\n") {
		t.Fatalf("connect while waiting:\n%s", got)
	}

	term := laptop.startTerminal(nil, "approve", code)
	term.waitFor(`A machine calling itself "maya-desktop" asked from 127.0.0.1 just now. That name is only its own claim.`)
	term.answer(`Approve "maya-desktop" connecting to `+tm.host()+` as maya?`, "y")
	term.waitFor(`Approved. "maya-desktop" receives a key of its own, as maya, and aboard keys lists it once it has.`)
	if code := term.exit(); code != 0 {
		t.Fatalf("approve exited %d:\n%s", code, term.text())
	}

	done := connecting.wait()
	if done.code != 0 || !strings.HasSuffix(done.stdout, "Connected to "+tm.url()+` as maya (member). This machine's key, "maya-desktop", is saved.`+"\n") {
		t.Fatalf("connect:\n%s", done)
	}
	desktopKey, laptopKey := tm.key(desktop), tm.key(laptop)
	if desktopKey == laptopKey || tm.me(desktopKey)["id"] != tm.me(laptopKey)["id"] {
		t.Fatal("the desktop's key isn't a key of its own for maya")
	}
	info, err := os.Stat(filepath.Join(desktop.configDir(), "servers.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("servers.json: %v %v", info, err)
	}

	list := desktop.run("keys", "--server", tm.url(), "--json").json(t)
	var laptopID string
	for _, k := range field(t, list, "keys").([]any) {
		if field(t, k, "id") != field(t, list, "current_key_id") {
			laptopID = field(t, k, "id").(string)
		} else if field(t, k, "name") != "maya-desktop" || field(t, k, "idle_expiry_seconds") != 7776000.0 {
			t.Fatalf("the desktop's key: %v", k)
		}
	}
	desktop.run("keys", "revoke", laptopID, "--server", tm.url())
	tm.works(laptopKey, false)
	tm.works(desktopKey, true)
}

// Whoever approves is who the new machine becomes: a stranger approving another
// person's code is asked plainly whether to sign the machine in as themselves, and the
// machine is signed in as them. Connect with --json keeps standard output to its result.
func TestApprovalNamesWhoTheMachineBecomes(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	sam := tm.person("sam")
	desktop := newPersonHome(t, "maya")

	connecting := desktop.start("connect", tm.url(), "--name", "maya-desktop", "--json")
	code := connecting.shownCode()
	if connecting.out.String() != "" {
		t.Fatalf("connect --json wrote to standard output while waiting:\n%s", connecting.out.String())
	}
	term := sam.startTerminal(nil, "approve", code)
	term.answer(`Approve "maya-desktop" connecting to `+tm.host()+` as sam?`, "y")
	term.waitFor("Approved.")
	term.exit()

	done := connecting.wait()
	if done.code != 0 {
		t.Fatalf("connect:\n%s", done)
	}
	out := done.json(t)
	matchesCLISpec(t, "ConnectOutput", out)
	if field(t, out, "person.handle") != "sam" || field(t, out, "key.name") != "maya-desktop" {
		t.Fatalf("connect --json: %v", out)
	}
	if _, ok := out["key"].(map[string]any)["token"]; ok {
		t.Fatalf("connect printed the key: %v", out)
	}
}

// A request turned down is refused on the new machine, which saves nothing; the code
// then works no more.
func TestARefusedMachineSavesNothing(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	desktop := newPersonHome(t, "maya")

	connecting := desktop.start("connect", tm.url(), "--name", "maya-desktop", "--json")
	code := connecting.shownCode()
	out := maya.run("approve", code, "--refuse", "--json").json(t)
	matchesCLISpec(t, "ApproveOutput", out)
	if field(t, out, "approved") != false || field(t, out, "request.state") != "refused" {
		t.Fatalf("approve --refuse: %v", out)
	}
	done := connecting.wait()
	if done.code != 1 || errorCode(t, done.json(t)) != "machine_request_refused" {
		t.Fatalf("connect after the refusal:\n%s", done)
	}
	if _, err := os.Stat(filepath.Join(desktop.configDir(), "servers.json")); err == nil {
		t.Fatal("a refused machine saved a key")
	}
	r := maya.runExit("approve", code, "--yes", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "machine_request_invalid" {
		t.Fatalf("approving a refused code:\n%s", r)
	}
}

// Approving is a person's own act with their own key: inside an agent's session the
// command refuses before it sends anything; without a terminal it needs --yes; and on
// the API an agent's token and a browser are refused. None of it approves the request.
func TestOnlyAPersonApprovesAMachine(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	desktop := newPersonHome(t, "maya")
	connecting := desktop.start("connect", tm.url(), "--name", "maya-desktop")
	code := connecting.shownCode()

	s := tm.admin.claudeSession("s-approve")
	if r := s.e.exec(s.vars, "", "approve", code, "--yes", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
		t.Fatalf("approve in an agent's session:\n%s", r)
	}
	r := tm.admin.runExit("approve", code, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" || !strings.Contains(r.stdout, "approve "+code+" --yes") {
		t.Fatalf("approve without a terminal or --yes:\n%s", r)
	}
	line := field(t, tm.admin.run("pair", "--json").json(t), "join.line").(string)
	tm.admin.run("join", line, "--name", "helper")
	for name, token := range map[string]string{"agent": agentToken(t, tm.admin, "helper"), "browser": tm.admin.browserLogin()} {
		for _, path := range []string{"/v1/machine-requests/lookup", "/v1/machine-requests/approve", "/v1/machine-requests/refuse"} {
			status, v := tm.call("POST", path, token, map[string]any{"code": code})
			if status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
				t.Fatalf("%s: POST %s: %d %v", name, path, status, v)
			}
		}
	}

	// The request is still waiting, and a person's own yes approves it.
	tm.admin.run("approve", code, "--yes")
	if done := connecting.wait(); done.code != 0 {
		t.Fatalf("connect:\n%s", done)
	}
}

// Connecting by approval checks the server first, keeps the flags that only an invite
// can use for an invite, and needs the approving machine to name its server when it has
// several.
func TestConnectByApprovalRefusesWhatItCantDo(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	home := newPersonHome(t, "maya")
	for _, tt := range []struct {
		args []string
		exit int
		code string
	}{
		{[]string{"connect", "http://example.com", "--json"}, 1, "insecure_server"},
		{[]string{"connect", tm.url(), "--handle", "maya", "--json"}, 2, "usage"},
		{[]string{"connect", "http://" + home.addr, "--json"}, 1, "already_connected"},
	} {
		r := home.runExit(tt.args...)
		if r.code != tt.exit || (tt.exit == 1 && errorCode(t, r.json(t)) != tt.code) {
			t.Fatalf("%v:\n%s", tt.args, r)
		}
	}
	if r := maya.runExit("connect", tm.url(), "--json"); r.code != 1 || errorCode(t, r.json(t)) != "already_connected" {
		t.Fatalf("connecting a machine already connected:\n%s", r)
	}

	// A machine connected to two servers is asked which one a code is for.
	other := newTeam(t)
	maya.run("connect", other.invite())
	r := maya.runExit("approve", "AAA-AAA", "--yes", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_not_selected" {
		t.Fatalf("approve with two servers:\n%s", r)
	}
	r = maya.runExit("approve", "AAA-AAA", "--yes", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "machine_request_invalid" {
		t.Fatalf("approve a wrong code:\n%s", r)
	}
}
