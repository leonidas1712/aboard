//go:build live

package live

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func terminalDriverFixture(t *testing.T, script string) (p *pane, stateDir string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- The owner executes this scratch terminal stand-in.
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TERMINAL_DRIVER_STATE", dir)
	return &pane{l: &lab{t: t, dir: dir}, harness: "codex", name: "writer"}, dir
}

func TestCodexPromptUsesBracketedPaste(t *testing.T) {
	p, dir := terminalDriverFixture(t, `shift 2
case "$1" in
  send-keys)
    shift 3
    if [ "$1" = -l ]; then printf '%s' "$2" > "$TERMINAL_DRIVER_STATE/input"; fi
    ;;
  capture-pane) printf '%s\n' 'When reviewer posts a message, run aboard say --to @reviewer "ACK".' ;;
esac
`)
	const prompt = `When reviewer posts a message, run aboard say --to @reviewer "ACK".`
	p.typeInto(prompt)
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "input")))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), "\x1b[200~"+prompt+"\x1b[201~"; got != want {
		t.Fatalf("prompt was typed through autocomplete instead of pasted: %q", got)
	}
}

func TestClaudeTrustWaitsForSelectionBeforeEnter(t *testing.T) {
	p, dir := terminalDriverFixture(t, `shift 2
case "$1" in
  capture-pane)
    state=$(cat "$TERMINAL_DRIVER_STATE/menu" 2>/dev/null || true)
    case "$state" in
      moving)
        printf '%s' selected > "$TERMINAL_DRIVER_STATE/menu"
        printf '%s\n' '> No, exit' '  Yes, I trust this folder'
        ;;
      selected)
        printf '%s' observed > "$TERMINAL_DRIVER_STATE/menu"
        printf '%s\n' '  No, exit' '> Yes, I trust this folder'
        ;;
      observed) printf '%s\n' '  No, exit' '> Yes, I trust this folder' ;;
      ready) printf '%s\n' '────────────────────' '> Ready' '────────────────────' ;;
      *) printf '%s\n' '> No, exit' '  Yes, I trust this folder' ;;
    esac
    ;;
  send-keys)
    shift 3
    case "$1" in
      Down) printf '%s' moving > "$TERMINAL_DRIVER_STATE/menu" ;;
      Enter)
        [ "$(cat "$TERMINAL_DRIVER_STATE/menu")" = observed ] || { printf '%s\n' 'Enter arrived before the Yes selection was observed'; exit 1; }
        printf '%s' ready > "$TERMINAL_DRIVER_STATE/menu"
        ;;
    esac
    ;;
esac
`)
	p.harness = "claude-code"
	p.waitClaudeReady()
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "menu")))
	if err != nil || strings.TrimSpace(string(raw)) != "ready" {
		t.Fatalf("trust selection did not reach the prompt: %q, %v", raw, err)
	}
}
