//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// helpCommands returns every command aboard help --json lists, and the ones Aboard runs
// itself, which have help too.
func helpCommands(t *testing.T, e *env) []string {
	t.Helper()
	r := e.run("help", "--json")
	v := r.json(t)
	matchesCLISpec(t, "HelpOutput", v)
	var names []string
	for _, c := range field(t, v, "commands").([]any) {
		names = append(names, c.(map[string]any)["name"].(string))
	}
	return append(names, "serve", "hook")
}

// aboard help lists every command under its group, one line each, as plain text.
func TestHelpListsTheCommandsByGroup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	r := e.run("help")
	for _, want := range []string{"Usage: aboard <command> [flags]", "Get started:", "Talk:", "Board:", "Run:", "Maintain:", "aboard help <command>"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("aboard help lacks %q:\n%s", want, r)
		}
	}
	for _, name := range helpCommands(t, e) {
		if name == "serve" || name == "hook" {
			if strings.Contains(r.stdout, "\n  "+name+" ") {
				t.Fatalf("aboard help lists %s, which Aboard runs itself:\n%s", name, r)
			}
			continue
		}
		if !strings.Contains(r.stdout, "\n  "+name+" ") {
			t.Fatalf("aboard help doesn't list %s:\n%s", name, r)
		}
	}
	if strings.Contains(r.stdout, "\x1b[") || r.stderr != "" {
		t.Fatalf("aboard help into a pipe isn't plain text on stdout:\n%s", r)
	}
	// With no command at all, the list goes to stderr as a usage error.
	if r := e.runExit(); r.code != 2 || !strings.Contains(r.stderr, "Get started:") {
		t.Fatalf("aboard with no command:\n%s", r)
	}
}

// Every command has its own help, the same through aboard help <command>, --help and -h:
// what it does, its usage line, flags and examples, on stdout with exit 0. aboard
// <command> help is not a command, and the error says where the help is.
func TestEveryCommandHasItsOwnHelp(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, name := range helpCommands(t, e) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			commandHelpWorks(t, newEnv(t), name)
		})
	}
	// A usage error names the command's help too.
	r := e.runExit("inbox", "--wait", "soon")
	if r.code != 2 || !strings.Contains(r.stderr, "Hint: Run aboard help inbox for its usage, flags and examples.") ||
		!strings.Contains(r.stderr, "Usage: aboard inbox [--wait SECONDS]") {
		t.Fatalf("usage error:\n%s", r)
	}
	r = e.runExit("help", "nope")
	if r.code != 2 || !strings.Contains(r.stderr, `"nope" is not an aboard command.`) {
		t.Fatalf("aboard help nope:\n%s", r)
	}
	r = e.runExit("nope")
	if r.code != 2 || !strings.Contains(r.stderr, "Hint: Run aboard help to see the commands.") {
		t.Fatalf("aboard nope:\n%s", r)
	}
}

// commandHelpWorks checks one command's help, and the error for aboard <command> help.
func commandHelpWorks(t *testing.T, e *env, name string) {
	t.Helper()
	r := e.run("help", name)
	if !strings.Contains(r.stdout, "Usage:\n  aboard "+name) || r.stderr != "" {
		t.Fatalf("aboard help %s:\n%s", name, r)
	}
	if name != "help" && !strings.Contains(r.stdout, "Flags:") && name != "serve" && name != "hook" {
		t.Fatalf("aboard help %s has no flags:\n%s", name, r)
	}
	for _, flag := range []string{"--help", "-h"} {
		if got := e.run(name, flag); got.stdout != r.stdout {
			t.Fatalf("aboard %s %s differs from aboard help %s:\n%s\n\n%s", name, flag, name, got, r)
		}
	}
	v := e.run("help", name, "--json").json(t)
	matchesCLISpec(t, "HelpOutput", v)
	if got := field(t, v, "commands.0.name"); got != name {
		t.Fatalf("aboard help %s --json names %v", name, got)
	}

	if name == "help" {
		return // aboard help help is the help's own help
	}
	bad := e.runExit(name, "help")
	if bad.code != 2 || !strings.Contains(bad.stderr, "Hint: Run aboard help "+name+" for its usage, flags and examples.") {
		t.Fatalf("aboard %s help:\n%s", name, bad)
	}
	js := e.runExit(name, "help", "--json")
	if js.code != 2 || field(t, js.json(t), "error.code") != "invalid_request" {
		t.Fatalf("aboard %s help --json:\n%s", name, js)
	}
}
