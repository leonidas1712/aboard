//go:build live

package live

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// ompSetup is how the suite runs omp on this machine: always with a scratch home folder,
// so omp's ~/.omp (its agent.db with logins, its settings and sessions) is the test's
// own, logged in to Anthropic through ANTHROPIC_OAUTH_TOKEN from the environment. The
// person's own ~/.omp is never read or written.
type ompSetup struct {
	// skip says why omp can't run here: it isn't installed.
	skip string
	// fail says why the suite can't run omp without the person's own login: there is no
	// token, or omp is too old for Aboard's extension.
	fail string
}

var (
	ompOnce  sync.Once
	ompSetUp ompSetup
)

// requireOmp skips the test unless omp is installed, and fails it unless omp is new
// enough and CLAUDE_CODE_OAUTH_TOKEN can log it in with a scratch home folder.
func requireOmp(t *testing.T, p support.Profile) {
	t.Helper()
	ompOnce.Do(func() { ompSetUp = detectOmp(p) })
	if ompSetUp.skip != "" {
		t.Skip(ompSetUp.skip)
	}
	if ompSetUp.fail != "" {
		t.Fatal(ompSetUp.fail)
	}
}

func detectOmp(p support.Profile) ompSetup {
	if _, err := exec.LookPath("omp"); err != nil {
		return ompSetup{skip: "omp is not installed: no omp on the PATH"}
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		return ompSetup{fail: "CLAUDE_CODE_OAUTH_TOKEN is not set. Run claude setup-token once and export the token it prints as " +
			"CLAUDE_CODE_OAUTH_TOKEN: the suite logs omp in to Anthropic with it (ANTHROPIC_OAUTH_TOKEN) in a scratch home folder, " +
			"and never uses your own ~/.omp."}
	}
	dir, err := os.MkdirTemp("", "aboard-live-omp-")
	if err != nil {
		return ompSetup{fail: "create a scratch home folder: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := command(ctx, "omp", "--version")
	cmd.Env = append(cleanEnv(), "PATH="+os.Getenv("PATH"), "HOME="+dir)
	out, err := cmd.Output()
	if err != nil {
		return ompSetup{fail: "omp --version failed with a scratch home folder: " + err.Error()}
	}
	if v := strings.TrimSpace(string(out)); !versionAtLeast(v, p.Checks.MinVersion) {
		return ompSetup{fail: v + " is older than " + p.Checks.MinVersion + ", the first omp Aboard's extension works with; run omp update"}
	}
	return ompSetup{}
}

// ompHome is the scratch home folder omp runs with in this lab, set up on first use:
// past omp's first-run setup, and with update checks off, since an update offered in the
// middle of a test would change the person's own install.
func (l *lab) ompHome() string {
	l.t.Helper()
	home := filepath.Join(l.dir, "omp-home")
	cfg := filepath.Join(home, ".omp", "agent", "config.yml")
	if _, err := os.Stat(cfg); err == nil {
		return home
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("startup:\n  setupWizard: false\n  checkUpdate: false\n"), 0o600); err != nil {
		l.t.Fatal(err)
	}
	return home
}

// ompEnv is the environment omp runs with in this lab: the lab's, with omp's scratch
// home folder and the Anthropic token from CLAUDE_CODE_OAUTH_TOKEN. aboard commands omp
// runs see the lab's ABOARD_HOME and PI_CODING_AGENT_DIR, as everything in the lab does,
// so omp reads its settings and extensions only from the lab.
//
// Images are off. omp takes TERM=tmux-256color to mean a terminal that shows Kitty
// images, but without TMUX (the suite never passes on the person's) it doesn't wrap them
// for tmux, and tmux reads the unwrapped Kitty command omp sends as it starts as a new
// pane title. The title then stays "Ga=d,d=A,q=2", which ompIdle never takes for idle.
func (l *lab) ompEnv() []string {
	return append(slices.Clone(l.vars), "HOME="+l.ompHome(), "ANTHROPIC_OAUTH_TOKEN="+os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		"PI_FORCE_IMAGE_PROTOCOL=off")
}

// ompArgv is how the suite runs omp, followed by args (such as --resume and a session id).
func ompArgv(args ...string) []string {
	return append([]string{"omp", "--model", ompModel()}, args...)
}

// startOmp starts omp in dir and waits until it takes a prompt. Aboard's extension in
// the project's .omp/extensions connects as omp starts. It then checks, from omp's own
// list, that omp runs no extension but that one (checkOmpExtensions).
func (l *lab) startOmp(name, dir string) *pane {
	l.t.Helper()
	p := l.start(name, dir, l.ompEnv(), ompArgv())
	p.waitOmpReady()
	var want []string
	if _, err := os.Stat(filepath.Join(dir, ".omp", "extensions", "aboard.ts")); err == nil {
		want = []string{"aboard"}
	}
	p.checkOmpExtensions(want)
	return p
}

// checkOmpExtensions opens omp's Extension Control Center (/extensions), which lists
// every extension module omp found, reads the list, and closes it again. The test fails
// unless the list is exactly want: an extension from the person's own ~/.omp, or from
// anywhere else outside the lab, would run inside the test and could reach the person's
// own daemon or apps. It runs before the session is bound, so no message arrives in the
// meantime, and it clears the pane's history after, so nothing it showed stays there.
func (p *pane) checkOmpExtensions(want []string) {
	p.l.t.Helper()
	p.tmuxRun("send-keys", "-t", p.target(), "-l", "/extensions")
	p.l.waitFor(5*time.Second, p.name+": omp to take /extensions", func() bool {
		return strings.Contains(ompInput(p.screen()), "/extensions")
	})
	p.keys("Enter")
	var listed []string
	last := ""
	p.l.waitFor(15*time.Second, p.name+": omp to list its extensions", func() bool {
		s := p.screen()
		if !strings.Contains(s, "Extension Control Center") || !strings.Contains(s, "Rules (") {
			return false
		}
		// The list fills in as omp's sources answer; read it once it stops changing.
		stable := s == last
		last = s
		listed = ompExtensionModules(s)
		return stable
	})
	p.keys("Escape")
	p.l.waitFor(10*time.Second, p.name+": omp to close its extension list", func() bool {
		return !strings.Contains(p.screen(), "Extension Control Center") && ompIdle(p)
	})
	p.tmuxRun("clear-history", "-t", p.target())
	if !slices.Equal(listed, want) {
		p.l.t.Fatalf("%s: omp runs the extensions %q, and only %q may run in a test; omp is reading extensions from "+
			"outside the lab (the person's ~/.omp, a configured path or a plugin)", p.name, listed, want)
	}
}

// ompExtensionModules reads the extension modules omp's Extension Control Center lists:
// the names under "Extension Modules (n)" in its left column, a heading omp leaves out
// when there are none.
func ompExtensionModules(screen string) []string {
	var names []string
	in := false
	for _, line := range strings.Split(screen, "\n") {
		cols := strings.Split(line, "│")
		if len(cols) < 3 {
			continue
		}
		left := strings.TrimSpace(cols[1])
		switch {
		case strings.Contains(left, "Extension Modules ("):
			in = true
		// ● marks an extension that runs, ○ one turned off; both count.
		case in && (strings.HasPrefix(left, "●") || strings.HasPrefix(left, "○")):
			if f := strings.Fields(left); len(f) > 1 {
				names = append(names, f[1])
			}
		case in:
			return names
		}
	}
	return names
}

// title is the terminal title the harness in the pane set.
func (p *pane) title() string {
	return strings.TrimSpace(p.tmuxRun("display-message", "-p", "-t", p.target(), "#{pane_title}"))
}

// ompIdle reports whether omp shows its prompt with no turn running. omp's terminal
// title is "π > <folder>" while it waits for the person, an animated spinner in place of
// ">" while a turn runs, and "!" while it waits for an answer (tui.titleState).
func ompIdle(p *pane) bool {
	t := p.title()
	return (t == "π >" || strings.HasPrefix(t, "π > ")) && strings.Contains(p.screen(), "╰─")
}

// ompInput is the text in omp's prompt box: its last line starting with ╰─.
func ompInput(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "╰─") {
			return strings.TrimPrefix(line, "╰─")
		}
	}
	return ""
}

// waitOmpReady waits for omp's prompt. Its first-run setup and login screens never show,
// since the scratch home is past them and the token logs it in; if one does, the test
// says why.
func (p *pane) waitOmpReady() {
	p.l.t.Helper()
	p.l.waitFor(90*time.Second, p.name+": omp to show its prompt", func() bool {
		if s := p.screen(); strings.Contains(s, "Sign in to your providers") || strings.Contains(s, "Setup step") {
			p.l.t.Fatalf("%s: omp shows its setup, which the scratch config should have skipped; is CLAUDE_CODE_OAUTH_TOKEN valid?\n%s", p.name, s)
		}
		return ompIdle(p)
	})
}

// ompTranscripts lists every session file omp wrote in the lab's scratch home, its
// subagents' included.
func (l *lab) ompTranscripts() []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(l.dir, "omp-home", ".omp"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// versionNumber is the first dotted version number in a harness's --version output.
var versionNumber = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// versionAtLeast reports whether the first dotted number in text is at least minimum.
func versionAtLeast(text, minimum string) bool {
	found := versionNumber.FindString(text)
	if found == "" || minimum == "" {
		return true
	}
	have, want := strings.Split(found, "."), strings.Split(minimum, ".")
	for i := range want {
		var h, w int
		if i < len(have) {
			h = atoi(have[i])
		}
		w = atoi(want[i])
		if h != w {
			return h > w
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
