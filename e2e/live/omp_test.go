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
// runs see the lab's ABOARD_HOME and PI_CODING_AGENT_DIR, as everything in the lab does.
func (l *lab) ompEnv() []string {
	return append(slices.Clone(l.vars), "HOME="+l.ompHome(), "ANTHROPIC_OAUTH_TOKEN="+os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"))
}

// ompArgv is how the suite runs omp, followed by args (such as --resume and a session id).
func ompArgv(args ...string) []string {
	return append([]string{"omp", "--model", ompModel()}, args...)
}

// startOmp starts omp in dir and waits until it takes a prompt. Aboard's extension in
// the project's .omp/extensions connects as omp starts.
func (l *lab) startOmp(name, dir string) *pane {
	l.t.Helper()
	p := l.start(name, dir, l.ompEnv(), ompArgv())
	p.waitOmpReady()
	return p
}

// title is the terminal title the harness in the pane set.
func (p *pane) title() string {
	return strings.TrimSpace(p.l.tmuxRun("display-message", "-p", "-t", p.target(), "#{pane_title}"))
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
