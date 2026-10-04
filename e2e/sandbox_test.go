//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// sandboxScript runs scripts/sandbox with a home directory of the test's own, so it
// never sees the person's Aboard, Claude Code or Codex setup.
func sandboxScript(t *testing.T, home string, extra []string, args ...string) string {
	t.Helper()
	out, err := sandboxCmd(home, extra, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("scripts/sandbox %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func sandboxCmd(home string, extra []string, args ...string) *exec.Cmd {
	cmd := exec.Command(filepath.Join("..", "scripts", "sandbox"), args...)
	cmd.Env = append([]string{
		"HOME=" + home,
		"USER=alex",
		"SHELL=/bin/sh",
		"PATH=" + fakeBin + string(os.PathListSeparator) + systemPath,
		"SANDBOX_ABOARD=" + binary,
		exitWithVar + "=" + strconv.Itoa(os.Getpid()),
	}, extra...)
	return cmd
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// make sandbox opens a shell whose aboard, Claude Code and Codex all use the sandbox's
// own folders, with the dev build first on the PATH and the project set up; make
// sandbox-clean stops what it started and removes it.
func TestSandboxIsolatesAndCleansUp(t *testing.T) {
	t.Parallel()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte(`{"fake":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := filepath.Join(home, ".aboard-sandboxes", "qa")
	t.Cleanup(func() { _ = sandboxCmd(home, nil, "clean", "qa").Run() })

	// Run from inside a harness session on another Aboard: none of that may leak in.
	outer := []string{
		"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CODEX_THREAD_ID=thread-1",
		"ABOARD_AGENT=writer", "ABOARD_LOCAL_ADDR=127.0.0.1:7400", "CLAUDE_CODE_OAUTH_TOKEN=token-for-test",
		"SANDBOX_CMD=env > " + filepath.Join(home, "env.txt") + "; pwd > " + filepath.Join(home, "pwd.txt") +
			"; command -v aboard > " + filepath.Join(home, "which.txt"),
	}
	banner := sandboxScript(t, home, outer, "open", "qa")
	for _, want := range []string{"Aboard sandbox qa: " + sb, "Claude Code: logged in with CLAUDE_CODE_OAUTH_TOKEN.", "Codex: logged in with " + auth} {
		if !strings.Contains(banner, want) {
			t.Fatalf("banner lacks %q:\n%s", want, banner)
		}
	}
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(read("env.txt"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	for k, want := range map[string]string{
		"ABOARD_HOME":             filepath.Join(sb, "aboard-home"),
		"CLAUDE_CONFIG_DIR":       filepath.Join(sb, "claude-config"),
		"CODEX_HOME":              filepath.Join(sb, "codex-home"),
		"DISABLE_AUTOUPDATER":     "1",
		"CLAUDE_CODE_OAUTH_TOKEN": "token-for-test",
	} {
		if vars[k] != want {
			t.Errorf("%s=%q, want %q", k, vars[k], want)
		}
	}
	for _, k := range []string{"CLAUDECODE", "CODEX_THREAD_ID", "ABOARD_AGENT", "ABOARD_LOCAL_ADDR"} {
		if v, ok := vars[k]; ok {
			t.Errorf("%s=%q leaked into the sandbox", k, v)
		}
	}
	if !strings.HasPrefix(vars["PATH"], filepath.Join(sb, "bin")+":") || strings.TrimSpace(read("which.txt")) != filepath.Join(sb, "bin", "aboard") {
		t.Errorf("the dev build is not first on the PATH: PATH=%s, aboard is %s", vars["PATH"], read("which.txt"))
	}
	project := filepath.Join(sb, "project")
	if got := strings.TrimSpace(read("pwd.txt")); got != project {
		t.Errorf("shell started in %s, want %s", got, project)
	}
	if link, err := os.Readlink(filepath.Join(sb, "codex-home", "auth.json")); err != nil || link != auth {
		t.Errorf("Codex login: link to %q (%v), want a link to %s", link, err, auth)
	}
	settings, err := os.ReadFile(filepath.Join(project, ".claude", "settings.local.json"))
	if err != nil || !strings.Contains(string(settings), "ABOARD_HOME="+filepath.Join(sb, "aboard-home")) {
		t.Errorf("project setup: hooks don't name the sandbox's ABOARD_HOME (%v):\n%s", err, settings)
	}
	for _, f := range []string{".codex/hooks.json", ".agents/skills/aboard/SKILL.md", ".git"} {
		if _, err := os.Stat(filepath.Join(project, f)); err != nil {
			t.Errorf("project setup: %v", err)
		}
	}
	daemon := pidIn(filepath.Join(sb, "aboard-home", "state", "daemon.pid"))
	if !alive(daemon) {
		t.Fatalf("the sandbox's delivery daemon isn't running (pid %d)", daemon)
	}

	// Opening it again keeps what is there.
	sandboxScript(t, home, []string{"SANDBOX_CMD=true"}, "open", "qa")
	if got := pidIn(filepath.Join(sb, "aboard-home", "state", "daemon.pid")); got != daemon {
		t.Fatalf("reopening replaced the daemon: pid %d, was %d", got, daemon)
	}

	sandboxScript(t, home, nil, "clean", "qa")
	if _, err := os.Stat(sb); !os.IsNotExist(err) {
		t.Fatalf("sandbox folder still there: %v", err)
	}
	if _, err := os.Stat(auth); err != nil {
		t.Fatalf("cleaning removed the person's Codex login: %v", err)
	}
	eventually(t, 10*time.Second, "the sandbox's daemon to stop", func() bool { return !alive(daemon) })
}
