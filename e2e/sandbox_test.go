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

// make sandbox opens a shell whose aboard, Claude Code, Codex and omp all use the
// sandbox's own folders, each named by its profile's config_dir.env, with the dev build first on the PATH and the project set up; make
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
		"OMPCODE=1", "PI_CODING_AGENT_DIR=" + filepath.Join(home, ".omp", "agent"),
		"ABOARD_AGENT=writer", "ABOARD_LOCAL_ADDR=127.0.0.1:7400", "CLAUDE_CODE_OAUTH_TOKEN=token-for-test",
		"SANDBOX_CMD=env > " + filepath.Join(home, "env.txt") + "; pwd > " + filepath.Join(home, "pwd.txt") +
			"; command -v aboard > " + filepath.Join(home, "which.txt"),
	}
	banner := sandboxScript(t, home, outer, "open", "qa")
	for _, want := range []string{"Aboard sandbox qa: " + sb, "claude-code: logged in with CLAUDE_CODE_OAUTH_TOKEN.", "codex: logged in with " + auth, "omp: logged in to Anthropic", "Board view:  http://"} {
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
		"CLAUDE_CONFIG_DIR":       filepath.Join(sb, "claude-code"),
		"CODEX_HOME":              filepath.Join(sb, "codex"),
		"PI_CODING_AGENT_DIR":     filepath.Join(sb, "omp"),
		"ANTHROPIC_OAUTH_TOKEN":   "token-for-test",
		"DISABLE_AUTOUPDATER":     "1",
		"CLAUDE_CODE_OAUTH_TOKEN": "token-for-test",
	} {
		if vars[k] != want {
			t.Errorf("%s=%q, want %q", k, vars[k], want)
		}
	}
	for _, k := range []string{"CLAUDECODE", "CODEX_THREAD_ID", "OMPCODE", "ABOARD_AGENT", "ABOARD_LOCAL_ADDR"} {
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
	if link, err := os.Readlink(filepath.Join(sb, "codex", "auth.json")); err != nil || link != auth {
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

	// Opening it again gives another shell in it: nothing is wiped, the running server
	// and daemon stay, and the banner says so and names the board view.
	marker := filepath.Join(sb, "aboard-home", "kept.txt")
	if err := os.WriteFile(marker, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	again := sandboxScript(t, home, []string{"SANDBOX_CMD=true"}, "open", "qa")
	if got := pidIn(filepath.Join(sb, "aboard-home", "state", "daemon.pid")); got != daemon {
		t.Fatalf("reopening replaced the daemon: pid %d, was %d", got, daemon)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("reopening wiped the sandbox: %v", err)
	}
	for _, want := range []string{"Reopened sandbox qa", "Board view:  http://"} {
		if !strings.Contains(again, want) {
			t.Fatalf("reopen banner lacks %q:\n%s", want, again)
		}
	}

	// Updating restarts the server and daemon on the build now linked, keeping the data.
	upd := sandboxScript(t, home, nil, "update", "qa")
	if !strings.Contains(upd, "its boards and data kept") || !strings.Contains(upd, "Board view: http://") {
		t.Fatalf("update output:\n%s", upd)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("updating wiped the sandbox: %v", err)
	}
	eventually(t, 10*time.Second, "the old daemon to stop", func() bool { return !alive(daemon) })
	daemon = pidIn(filepath.Join(sb, "aboard-home", "state", "daemon.pid"))
	if !alive(daemon) {
		t.Fatalf("update left no delivery daemon running (pid %d)", daemon)
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

// Updating a sandbox that does not exist says how to open one.
func TestSandboxUpdateNeedsASandbox(t *testing.T) {
	t.Parallel()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, err := sandboxCmd(home, nil, "update", "nope").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "There is no sandbox named nope") || !strings.Contains(string(out), "make sandbox NAME=nope") {
		t.Fatalf("update of a missing sandbox: %v\n%s", err, out)
	}
}

// The person's own rc files run in the sandbox's shell, and may export ABOARD_HOME or a
// harness's config folder to the real paths: the sandbox's values must win, and the
// session markers an rc file sets must be dropped again.
func TestSandboxShellKeepsIsolationAgainstRcFiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ shell, rc string }{{"bash", ".bashrc"}, {"zsh", ".zshrc"}} {
		t.Run(tc.shell, func(t *testing.T) {
			t.Parallel()
			path, err := exec.LookPath(tc.shell)
			if err != nil {
				t.Skipf("no %s on this machine", tc.shell)
			}
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(home, "real")
			rc := "export ABOARD_HOME=" + outside + "/aboard-home\n" +
				"export CLAUDE_CONFIG_DIR=" + outside + "/claude\n" +
				"export CODEX_HOME=" + outside + "/codex\n" +
				"export PI_CODING_AGENT_DIR=" + outside + "/omp\n" +
				"export ABOARD_SANDBOX_NAME=elsewhere\n" +
				"export CODEX_THREAD_ID=thread-from-rc\n" +
				"export ABOARD_AGENT=agent-from-rc\n" +
				"export PATH=/usr/bin:/bin\n"
			if err := os.WriteFile(filepath.Join(home, tc.rc), []byte(rc), 0o600); err != nil {
				t.Fatal(err)
			}
			sb := filepath.Join(home, ".aboard-sandboxes", "rc")
			t.Cleanup(func() { _ = sandboxCmd(home, nil, "clean", "rc").Run() })

			envFile := filepath.Join(home, "env.txt")
			cmd := sandboxCmd(home, []string{"SHELL=" + path}, "open", "rc")
			cmd.Stdin = strings.NewReader("env > " + envFile + "\nexit\n")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("scripts/sandbox open rc: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(envFile)
			if err != nil {
				t.Fatal(err)
			}
			vars := map[string]string{}
			for _, line := range strings.Split(string(raw), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					vars[k] = v
				}
			}
			for k, want := range map[string]string{
				"ABOARD_HOME":         filepath.Join(sb, "aboard-home"),
				"CLAUDE_CONFIG_DIR":   filepath.Join(sb, "claude-code"),
				"CODEX_HOME":          filepath.Join(sb, "codex"),
				"PI_CODING_AGENT_DIR": filepath.Join(sb, "omp"),
				"ABOARD_SANDBOX_NAME": "rc",
			} {
				if vars[k] != want {
					t.Errorf("%s=%q, want %q", k, vars[k], want)
				}
			}
			if !strings.HasPrefix(vars["PATH"], filepath.Join(sb, "bin")+":") {
				t.Errorf("the dev build is not first on the PATH: %s", vars["PATH"])
			}
			for _, k := range []string{"CODEX_THREAD_ID", "ABOARD_AGENT"} {
				if v, ok := vars[k]; ok {
					t.Errorf("%s=%q set by an rc file survived in the sandbox", k, v)
				}
			}
		})
	}
}

// Updating stops the sandbox's server and daemon first. When aboard down fails the
// update fails too, saying so, instead of starting a second server beside the old one.
func TestSandboxUpdateFailsWhenStoppingFails(t *testing.T) {
	t.Parallel()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sb := filepath.Join(home, ".aboard-sandboxes", "stuck")
	for _, d := range []string{"bin", "aboard-home"} {
		if err := os.MkdirAll(filepath.Join(sb, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(sb, "bin", "aboard")
	script := "#!/bin/sh\nif [ \"$1\" = down ]; then echo 'down: cannot stop' >&2; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { //nolint:gosec // an executable stand-in for aboard
		t.Fatal(err)
	}
	out, err := sandboxCmd(home, nil, "update", "stuck").CombinedOutput()
	if err == nil {
		t.Fatalf("update succeeded although aboard down failed:\n%s", out)
	}
	for _, want := range []string{"Could not stop the server and daemon of sandbox stuck", "make sandbox-update NAME=stuck"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("update output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "now runs") {
		t.Errorf("update printed success:\n%s", out)
	}
	if info, err := os.Lstat(fake); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("update relinked the build although it could not stop the old one: %v", err)
	}
}
