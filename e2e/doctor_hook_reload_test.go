//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorKeepsChangedHooksVisibleUntilTheSessionReloads(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.vars = append(e.vars, "FAKE_CLAUDE_VERSION=2.1.100")
			e.run("init", "--yes", "--scope", scope, "--harness", "claude-code", "--allow-commands")
			e.run("daemon", "start")
			cwd := e.dir
			if scope == "project" {
				cwd = filepath.Join(e.home, "project-alias")
				if err := os.Symlink(e.dir, cwd); err != nil {
					t.Fatal(err)
				}
			}
			register := func(boot string) {
				t.Helper()
				conn, err := net.Dial("unix", e.socketPath())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				req := map[string]any{"v": 1, "op": "register", "harness": "claude-code", "session": "reload-session", "boot": boot, "runtime_hook": true, "cwd": cwd}
				if err := json.NewEncoder(conn).Encode(req); err != nil {
					t.Fatal(err)
				}
				var reply map[string]any
				if err := json.NewDecoder(conn).Decode(&reply); err != nil || reply["error"] != nil {
					t.Fatalf("register: %v, %v", reply, err)
				}
			}
			register("old")
			e.vars = append(e.vars, "FAKE_CLAUDE_VERSION=2.1.288")
			e.run("init", "--yes", "--scope", scope, "--harness", "claude-code", "--allow-commands")
			assertReload := func(want bool) {
				t.Helper()
				check, seen := e.doctorChecks()["claude_hooks_reload"]
				if seen != want {
					t.Fatalf("reload warning present %v, want %v: %v", seen, want, check)
				}
				if seen && (check["code"] != "claude_hooks_reload_required" || !strings.Contains(check["fix"].(string), "claude --continue")) {
					t.Fatalf("reload fix: %v", check)
				}
			}
			assertReload(true)
			e.run("init", "--yes", "--scope", scope, "--harness", "claude-code", "--allow-commands")
			killDaemon(t, e)
			e.run("daemon", "start")
			register("old")
			assertReload(true)
			register("new")
			assertReload(false)
		})
	}
}

func TestInitLeavesFilesUntouchedWhenHookRecoveryCannotBeRecorded(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.vars = append(e.vars, "FAKE_CLAUDE_VERSION=2.1.100")
	e.run("init", "--yes", "--harness", "claude-code", "--allow-commands")
	path := filepath.Join(e.home, ".claude", "settings.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.stateDir(), "delivery.db"), []byte("unreadable isolated journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.vars = append(e.vars, "FAKE_CLAUDE_VERSION=2.1.288")
	r := e.runExit("init", "--yes", "--harness", "claude-code", "--allow-commands", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "hook_recovery_unavailable" {
		t.Fatalf("unavailable checkpoint: %s", r)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed checkpoint changed hooks: %v", err)
	}
}
