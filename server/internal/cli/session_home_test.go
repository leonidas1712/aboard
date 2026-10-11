package cli

import (
	"path/filepath"
	"slices"
	"strings"
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

func TestCustomCodexHooksKeepTheCodexHome(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "CODEX_HOME": filepath.Join(t.TempDir(), "custom home")}
	a := &app{env: Env{Getenv: func(k string) string { return env[k] }}}
	for _, h := range a.withHome(hooksOf(t, "codex", "/bin/aboard")) {
		if !strings.HasPrefix(h.Handler.Command, "CODEX_HOME="+shellWord(env["CODEX_HOME"])+" ") {
			t.Fatalf("custom home missing from hook: %s", h.Handler.Command)
		}
	}
}
