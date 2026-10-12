//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReleasedUpgradeWithSessionsOpen(t *testing.T) {
	t.Parallel()
	for _, d := range drivers(t) {
		if !selected(d.p.Harness) || (d.p.Harness != "claude-code" && d.p.Harness != "codex") {
			continue
		}
		t.Run(d.p.Harness, func(t *testing.T) {
			d.require(t)
			parallel(t)
			old, version := previousRelease(t)
			l := newLabWith(t, old)
			d.setUp(l)
			l.pairCLI()
			l.run("init", "--yes", "--scope", "global", "--harness", d.p.Harness, "--allow-commands")
			project := filepath.Join(l.dir, "upgrade-project")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			p := d.startPlain(l, "writer", project)
			p.submit(fmt.Sprintf(bindPrompt, "writer"))
			l.waitPresence("writer", "idle", 3*time.Minute)
			hooks := filepath.Join(l.claudeConfig, "settings.json")
			if d.p.Harness == "codex" {
				hooks = filepath.Join(l.dir, "codex-home", "hooks.json")
			}
			before := readFile(t, hooks)
			oldDaemon := l.daemonPID()
			if got := l.serverVersion(); got != version {
				t.Fatalf("previous release server version: %q, want %q", got, version)
			}
			staged := l.bin + ".new"
			copyFile(t, newBinary, staged)
			if err := os.Rename(staged, l.bin); err != nil {
				t.Fatal(err)
			}
			// Package-manager installs and the install script both replace the binary;
			// upgrade's setup refresh runs this same init against the installed path.
			refresh := l.runIn(l.human, "init", "--yes", "--scope", "global", "--harness", d.p.Harness, "--allow-commands")
			if !bytes.Equal(before, readFile(t, hooks)) {
				t.Fatalf("upgrading from released %s changed %s\n%s", version, hooks, refresh)
			}
			installed := time.Now()
			for i := 1; i <= 2; i++ {
				want := fmt.Sprintf("RELEASE-UPGRADE-%d", i)
				ping := l.say("reviewer", "--to", "@writer", "--expect-reply", "Reply to this message with exactly "+want+".")
				reply := l.waitMessage("writer", ping.At, want, 3*time.Minute)
				t.Logf("released %s: %s answered after %s", version, d.p.Harness, reply.At.Sub(ping.At))
				l.waitPresence("writer", "idle", time.Minute)
			}
			l.waitFor(30*time.Second, "upgrade messages to be acknowledged", func() bool { return len(l.writerInbox()) == 0 })
			if len(l.handedAfter(installed)) != 2 {
				t.Errorf("upgrade messages were not each handed once: %+v", l.handedAfter(installed))
			}
			if pid := l.daemonPID(); pid == 0 || pid == oldDaemon {
				t.Errorf("daemon was not replaced: %d -> %d", oldDaemon, pid)
			}
			if !bytes.Equal(before, readFile(t, hooks)) {
				t.Fatal("message delivery rewrote the trusted hooks")
			}
		})
	}
}

func previousRelease(t *testing.T) (string, string) {
	t.Helper()
	path := os.Getenv("ABOARD_LIVE_PREVIOUS")
	if path == "" {
		var err error
		path, err = exec.LookPath("aboard")
		if err != nil {
			t.Fatal("Set ABOARD_LIVE_PREVIOUS to the previous released aboard binary; the proof never substitutes restamped current source.")
		}
	}
	raw, err := command(t.Context(), path, "version", "--json").Output()
	if err != nil {
		t.Fatalf("previous binary version: %v", err)
	}
	var build struct{ Version, Commit string }
	if json.Unmarshal(raw, &build) != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(build.Version) || build.Commit == "" {
		t.Fatalf("previous binary must report a released version and commit: %s", raw)
	}
	cmd := command(t.Context(), "git", "rev-parse", "--verify", "refs/tags/v"+build.Version+"^{commit}")
	cmd.Dir = repoRoot
	commit, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(commit)) != build.Commit {
		t.Fatalf("previous binary commit %s does not match release tag v%s; fetch that tag and supply its released binary", build.Commit, build.Version)
	}
	return path, build.Version
}
