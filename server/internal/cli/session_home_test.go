package cli

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSessionRequestsUseTheirOwnCodexHome(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "CODEX_HOME": t.TempDir()}
	a := &app{env: Env{Getenv: func(k string) string { return env[k] }}}
	req := delivery.Request{Harness: "codex", Session: "thread"}
	if got := a.withSessionHome(req).CodexHome; got != env["CODEX_HOME"] {
		t.Fatalf("session home = %q", got)
	}
	delete(env, "CODEX_HOME")
	if got := a.withSessionHome(req).CodexHome; got != filepath.Join(env["HOME"], ".codex") {
		t.Fatalf("default home = %q", got)
	}
}

func TestDaemonDropsHarnessHomesAndOrcaEnvironment(t *testing.T) {
	keep := []string{"HOME=/scratch", "ABOARD_HOME=/scratch/aboard", "PATH=/bin", "ORCALESS=keep"}
	env := append(slices.Clone(keep), "CODEX_HOME=/wrong", "CLAUDE_CONFIG_DIR=/wrong", "ORCA_SESSION_ID=old", "ORCA_FOO=old")
	if got := daemonEnvironment(env); !slices.Equal(got, keep) {
		t.Fatalf("daemon environment = %v, want %v", got, keep)
	}
}

func TestCustomCodexHomeDoesNotChangeInstalledHookCommands(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "CODEX_HOME": filepath.Join(t.TempDir(), "custom home")}
	a := &app{env: Env{Getenv: func(k string) string { return env[k] }}}
	custom := a.withHome(hooksOf(t, "codex", "/bin/aboard"))
	delete(env, "CODEX_HOME")
	original := a.withHome(hooksOf(t, "codex", "/bin/aboard"))
	for i, h := range custom {
		if h.Handler.Command != original[i].Handler.Command {
			t.Fatalf("custom home changed trusted hook: %s; want %s", h.Handler.Command, original[i].Handler.Command)
		}
	}
}

func TestCodexHookRequestUsesRuntimeHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "runtime home")
	a := &app{env: Env{Getenv: func(name string) string {
		if name == "CODEX_HOME" {
			return home
		}
		return ""
	}}}
	h := hookCall{a: a, harness: "codex"}
	h.in.SessionID = "runtime-thread"
	if got := h.request(delivery.OpTurnStart).CodexHome; got != home {
		t.Fatalf("hook request home = %q, want %q", got, home)
	}
}
