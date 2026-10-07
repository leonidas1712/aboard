//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// terminal is aboard running in a pseudo-terminal, as a person at a terminal runs it:
// standard input, output and error are all the terminal.
type terminal struct {
	t    *testing.T
	args []string
	pty  *os.File
	cmd  *exec.Cmd
	mu   sync.Mutex
	out  bytes.Buffer
	done chan struct{}
	code int
}

// startTerminal runs aboard with args and extra environment variables in a terminal 100
// columns wide. TERM is xterm-256color unless extra sets it.
func (e *env) startTerminal(extra []string, args ...string) *terminal {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = e.dir
	cmd.Env = append(append([]string{"TERM=xterm-256color"}, e.vars...), extra...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 100})
	if err != nil {
		e.t.Fatalf("start aboard %v in a terminal: %v", args, err)
	}
	term := &terminal{t: e.t, args: args, pty: f, cmd: cmd, done: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			term.mu.Lock()
			term.out.Write(buf[:n])
			term.mu.Unlock()
			if err != nil {
				break
			}
		}
		err := cmd.Wait()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			term.code = exit.ExitCode()
		}
		close(term.done)
	}()
	e.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-term.done
		_ = f.Close()
	})
	return term
}

// ansi matches the terminal control sequences a screen interprets: colors, cursor
// moves and the like.
var ansi = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)

// text is everything shown so far, without control sequences.
func (term *terminal) text() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return ansi.ReplaceAllString(term.out.String(), "")
}

// raw is everything written so far, control sequences included.
func (term *terminal) raw() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.out.String()
}

// waitFor waits until the screen has shown want.
func (term *terminal) waitFor(want string) {
	term.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(term.text(), want) {
		select {
		case <-term.done:
			if strings.Contains(term.text(), want) {
				return
			}
			term.t.Fatalf("aboard %v exited before showing %q:\n%s", term.args, want, term.text())
		default:
		}
		if time.Now().After(deadline) {
			term.t.Fatalf("aboard %v didn't show %q:\n%s", term.args, want, term.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// press types keys.
func (term *terminal) press(keys string) {
	term.t.Helper()
	if _, err := io.WriteString(term.pty, keys); err != nil {
		term.t.Fatalf("type into aboard %v: %v", term.args, err)
	}
}

// answer waits for a question, then answers it with keys.
func (term *terminal) answer(question, keys string) {
	term.t.Helper()
	term.waitFor(question)
	term.press(keys)
}

// exit waits for aboard to exit and returns its exit code.
func (term *terminal) exit() int {
	term.t.Helper()
	select {
	case <-term.done:
		return term.code
	case <-time.After(15 * time.Second):
		term.t.Fatalf("aboard %v didn't exit:\n%s", term.args, term.text())
		return -1
	}
}

const (
	enter  = "\r"
	down   = "\x1b[B"
	toggle = "x"
	ctrlC  = "\x03"
)

// In a terminal, aboard init shows that nothing is set up, asks with selectors and
// confirms before writing; Enter takes each suggestion, which for Codex includes the
// allow rule. It ends with what to do next.
func TestInitGuidesAPersonThroughSetup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	term := e.startTerminal(nil, "init")
	term.waitFor("Aboard setup on this machine")
	term.waitFor("Claude Code  not set up")
	term.answer("Set up which harnesses?", enter)
	term.answer("Install where?", enter)
	term.answer("Delivery for agents on this machine", enter)
	term.answer("Let agents run aboard commands without a permission prompt?", enter)
	term.waitFor("create    ~/.codex/rules/aboard.rules (permissions)")
	term.answer("Make these changes?", enter)
	term.waitFor("Done. Claude Code and Codex ask you to trust new hooks")
	term.waitFor("run aboard pair.")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if !strings.Contains(term.raw(), "\x1b[") {
		t.Fatalf("a terminal got no color:\n%q", term.raw())
	}
	for _, f := range []string{".claude/settings.json", ".claude/skills/aboard/SKILL.md", ".codex/hooks.json", ".codex/rules/aboard.rules", ".agents/skills/aboard/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(e.home, f)); err != nil {
			t.Fatalf("init didn't write %s: %v", f, err)
		}
	}
	settings, err := os.ReadFile(filepath.Join(e.home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), `"Bash(aboard *)"`) {
		t.Fatalf("settings.json lacks the allow rule (%v):\n%s", err, settings)
	}
	// What the person chose is what init --json now finds unchanged.
	v := e.run("init", "--allow-commands", "--json").json(t)
	matchesCLISpec(t, "InitOutput", v)
	for _, h := range field(t, v, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if a := c.(map[string]any)["action"]; a != "unchanged" {
				t.Fatalf("after the guided setup, init --json still has a change: %v", c)
			}
		}
	}
}

// Run again, the guided aboard init shows what is set up and says it is current;
// leaving it as it is changes nothing. Once a file is edited, it shows that, and Ctrl-C
// stops without changing anything.
func TestInitShowsWhatIsAlreadySetUp(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes", "--allow-commands")
	before := filesWithContent(t, e.home)

	term := e.startTerminal(nil, "init")
	term.waitFor("Claude Code  everywhere: hooks current, skill current, aboard commands allowed")
	term.waitFor("Codex        everywhere: hooks current, skill current, aboard commands allowed")
	term.waitFor("Everything is set up and current.")
	term.answer("Leave it as it is", enter)
	term.waitFor("Nothing changed.")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if after := filesWithContent(t, e.home); !mapsEqual(before, after) {
		t.Fatal("leaving the setup as it is changed files")
	}

	skill := filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")
	if err := os.WriteFile(skill, []byte("my own notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	term = e.startTerminal(nil, "init")
	term.waitFor("skill edited since aboard ")
	term.answer("Set up which harnesses?", ctrlC)
	term.waitFor("Nothing changed.")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if raw, _ := os.ReadFile(skill); string(raw) != "my own notes\n" {
		t.Fatalf("Ctrl-C still replaced the edited skill: %q", raw)
	}

	// Choosing to change the setup, and picking only Claude Code, updates only its skill.
	term = e.startTerminal(nil, "init")
	term.answer("Set up which harnesses?", down+toggle+enter)
	term.answer("Install where?", enter)
	term.answer("Delivery for agents on this machine", enter)
	term.answer("Let agents run aboard commands without a permission prompt?", enter)
	term.waitFor("update    ~/.claude/skills/aboard/SKILL.md (skill; edited since aboard ")
	term.waitFor("codex: not chosen")
	term.answer("Make these changes?", enter)
	term.waitFor("Done.")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if raw, _ := os.ReadFile(skill); string(raw) == "my own notes\n" {
		t.Fatal("the guided init didn't update the skill it listed")
	}
}

// Where an agent runs it (inside a harness session, which a harness may give a terminal
// too) or with --json, aboard init never asks: it lists the changes and exits.
func TestInitNeverAsksAnAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	for _, extra := range [][]string{{"CLAUDECODE=1"}, {"ABOARD_SESSION=s1"}, {"CODEX_THREAD_ID=t1"}, {"CODEX_SANDBOX=seatbelt"}} {
		term := e.startTerminal(extra, "init")
		term.waitFor("Run aboard init --yes to make these changes.")
		if code := term.exit(); code != 0 {
			t.Fatalf("%v: exit %d:\n%s", extra, code, term.text())
		}
		if text := term.text(); strings.Contains(text, "?") || strings.Contains(term.raw(), "\x1b[3") {
			t.Fatalf("%v: init asked or colored its output:\n%q", extra, term.raw())
		}
	}
	term := e.startTerminal(nil, "init", "--json")
	if code := term.exit(); code != 0 {
		t.Fatalf("init --json in a terminal: exit %d:\n%s", code, term.text())
	}
	if !strings.HasPrefix(term.raw(), "{") || strings.Contains(term.raw(), "\x1b") {
		t.Fatalf("init --json in a terminal printed more than JSON:\n%q", term.raw())
	}
	// From a pipe it lists the changes too, as plain text.
	r := e.run("init")
	if !strings.HasSuffix(r.stdout, "Run aboard init --yes to make these changes.\n") || strings.Contains(r.stdout, "\x1b") {
		t.Fatalf("init from a pipe:\n%s", r)
	}
	for _, f := range []string{".claude/settings.json", ".claude/skills", ".codex/hooks.json", ".agents"} {
		if _, err := os.Stat(filepath.Join(e.home, f)); err == nil {
			t.Fatalf("init without --yes wrote %s", f)
		}
	}
}

// Color goes only to a person at a terminal: never with NO_COLOR, TERM=dumb, --json,
// inside a harness session or into a pipe. Errors on a terminal are colored too.
func TestColorOnlyForAPersonAtATerminal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	term := e.startTerminal(nil, "help", "init")
	term.exit()
	if !strings.Contains(term.raw(), "\x1b[") {
		t.Fatalf("help in a terminal has no color:\n%q", term.raw())
	}
	plain := e.run("help", "init").stdout
	if got := strings.ReplaceAll(term.text(), "\r\n", "\n"); got != plain {
		t.Fatalf("the words differ between a terminal and a pipe:\n%s\n---\n%s", got, plain)
	}
	for _, extra := range [][]string{{"NO_COLOR=1"}, {"TERM=dumb"}, {"CLAUDECODE=1"}, {"CODEX_THREAD_ID=t1"}} {
		term := e.startTerminal(extra, "help", "init")
		term.exit()
		if strings.Contains(term.raw(), "\x1b") {
			t.Fatalf("%v: help has control sequences:\n%q", extra, term.raw())
		}
	}
	term = e.startTerminal(nil, "nope")
	if code := term.exit(); code != 2 || !strings.Contains(term.raw(), "\x1b[") || !strings.Contains(term.text(), "Error (invalid_request): \"nope\" is not an aboard command.") {
		t.Fatalf("an error in a terminal: exit %d\n%q", code, term.raw())
	}
}

// Lists of people, keys and servers have a header row and color for a person at a terminal;
// NO_COLOR and --no-color remove the color and leave the same words.
func TestListsAreColoredAtATerminalAndPlainOtherwise(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	for _, args := range [][]string{{"keys"}, {"servers"}, {"people"}} {
		plain := e.run(args...).stdout
		if strings.Contains(plain, "\x1b") || !strings.Contains(plain, "  "+strings.ToUpper(map[string]string{"keys": "key", "servers": "server", "people": "handle"}[args[0]])) {
			t.Fatalf("aboard %v piped:\n%q", args, plain)
		}
		term := e.startTerminal(nil, args...)
		term.exit()
		if !strings.Contains(term.raw(), "\x1b[1m") {
			t.Fatalf("aboard %v at a terminal has no bold header:\n%q", args, term.raw())
		}
		if got := strings.ReplaceAll(term.text(), "\r\n", "\n"); got != plain {
			t.Fatalf("aboard %v: the words differ at a terminal:\n%s\n---\n%s", args, got, plain)
		}
		for _, off := range []struct {
			env  []string
			args []string
		}{{[]string{"NO_COLOR=1"}, args}, {nil, append([]string{"--no-color"}, args...)}} {
			term := e.startTerminal(off.env, off.args...)
			term.exit()
			if strings.Contains(term.raw(), "\x1b") {
				t.Fatalf("%v %v: control sequences:\n%q", off.env, off.args, term.raw())
			}
		}
	}
}

// aboard uninstall --data asks a person at a terminal before deleting data; the answer
// starts on No.
func TestUninstallAsksBeforeDeletingData(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	term := e.startTerminal(nil, "uninstall", "--data")
	term.answer("Delete Aboard's data in ", enter)
	term.waitFor("Nothing changed.")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if _, err := os.Stat(e.dataDir()); err != nil {
		t.Fatalf("declining deleted the data: %v", err)
	}
}

// filesWithContent maps each file under dir to its content.
func filesWithContent(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range filesUnder(t, dir, "data", "project") {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue // a socket or a file that went away
		}
		out[f] = string(raw)
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
