//go:build live

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Codex asks with aboard say --wait-reply and gets the reply inside the same command,
// from inside its sandbox: the reply is shown by the command, never queued for a later
// turn.
func TestCodexWaitsForReplyInItsTurn(t *testing.T) {
	only(t, "codex")
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	reviewer := l.startCodex("asker", l.project("project", "codex"))
	reviewer.bind("reviewer")

	start := time.Now()
	reviewer.submit(`Run: aboard say --to @writer --wait-reply 120 "PING W". When it prints the reply, run: aboard say "GOT" followed by the reply's text.`)
	ping := l.waitMessage("reviewer", start, "PING W", 3*time.Minute)
	pong := l.say("writer", "--reply", strconv.Itoa(ping.Seq), "PONG W")
	got := l.waitMessage("reviewer", pong.At, "GOT", 3*time.Minute)
	reviewer.waitIdle(2 * time.Minute)
	at, how, ok := l.reached(pong.Seq)
	t.Logf("measured: the reply reached Codex %s after it was posted (%s); GOT %s after", at.Sub(pong.At), how, got.At.Sub(pong.At))
	if !ok || how != "claimed by a command" {
		t.Fatalf("the reply reached Codex by %q; want shown by aboard say --wait-reply", how)
	}
	if !strings.Contains(got.Body, "PONG W") {
		t.Errorf("Codex reported %q; want the reply's text", got.Body)
	}
	for _, h := range l.logged("bundle handed") {
		if slices.Contains(h.Seqs, pong.Seq) {
			t.Errorf("the reply was also queued for Codex at %s", h.Time)
		}
	}
	l.waitFor(30*time.Second, "the reply to be acknowledged", func() bool { return len(l.inbox("reviewer")) == 0 })
}

// Codex's sandbox blocks network access by default, so without the allow rule an aboard
// command Codex runs can't reach the running server or daemon, and says so; with the
// rule aboard init --allow-commands adds, Codex runs it outside the sandbox and it
// reaches both. The outcome is read from the command's own output in Codex's event
// stream, never from what the model writes.
func TestCodexSandboxNeedsTheAllowRule(t *testing.T) {
	only(t, "codex")
	setup := requireCodex(t)
	t.Parallel()
	l := newLab(t)
	record(t, l.driverFor("codex"), "SandboxNeedsTheAllowRule")
	home := l.codexHome(setup)
	l.run("up")
	dir := filepath.Join(l.dir, "project")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Trusted so Codex reads the project's rules; no network_access setting, so the
	// sandbox keeps Codex's default and blocks the network.
	appendFile(t, filepath.Join(home, "config.toml"), fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir))
	const prompt = "Run the shell command `aboard status` exactly once, then reply DONE. Don't run anything else."

	blocked := l.codexExec(dir, prompt)
	if !strings.Contains(blocked, "can't be reached from Codex's sandbox") || strings.Contains(blocked, "not running") {
		t.Fatalf("without the rule, aboard status inside Codex should name the sandbox:\n%s", blocked)
	}
	var doctor struct {
		Checks []struct {
			Name string  `json:"name"`
			Code *string `json:"code"`
		} `json:"checks"`
	}
	// Doctor exits 3 here, since Claude Code isn't set up in the lab.
	if r := l.exec(t.Context(), dir, "doctor", "--json"); json.Unmarshal([]byte(r.stdout), &doctor) != nil {
		t.Fatalf("doctor:\n%s", r)
	}
	warned := false
	for _, c := range doctor.Checks {
		warned = warned || (c.Name == "codex_allow" && c.Code != nil && *c.Code == "codex_aboard_not_allowed")
	}
	if !warned {
		t.Fatalf("doctor should warn that aboard isn't allowed: %+v", doctor.Checks)
	}

	l.project("project", "codex") // aboard init --yes --scope project --allow-commands
	env := slices.Clone(l.vars)
	l.scopeCodexHooks(dir, env)
	l.trustCodexHooks(dir, home, env)
	allowed := l.codexExec(dir, prompt)
	if !strings.Contains(allowed, "Server: http://"+l.addr+" running") || !strings.Contains(allowed, "Daemon: running") {
		t.Fatalf("with the rule, aboard status inside Codex should reach the server and daemon:\n%s", allowed)
	}
}

// codexExec runs one Codex turn with codex exec in dir, in Codex's workspace-write
// sandbox, and returns the output of the aboard commands it ran, from its event stream.
func (l *lab) codexExec(dir, prompt string) string {
	l.t.Helper()
	l.typed++
	ctx, cancel := context.WithTimeout(l.t.Context(), 4*time.Minute)
	defer cancel()
	cmd := command(ctx, "codex", "exec", "--json", "--skip-git-repo-check", "-m", codexModel(), "-s", "workspace-write", "-C", dir, prompt)
	cmd.Dir, cmd.Env = dir, l.vars
	raw, err := cmd.Output()
	if err != nil {
		l.t.Fatalf("codex exec: %v\n%s", err, raw)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		var ev struct {
			Item struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Output  string `json:"aggregated_output"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Item.Type == "command_execution" && strings.Contains(ev.Item.Command, "aboard") {
			out.WriteString(ev.Item.Output)
		}
	}
	if out.Len() == 0 {
		l.t.Fatalf("Codex ran no aboard command:\n%s", raw)
	}
	return out.String()
}
