package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// harnesses are the harnesses the mapping is checked with, fixed so the cases don't
// change when a harness is added.
var harnesses = []string{"claude-code", "codex", "omp"}

func TestMappingPicksWhatAChangeTouches(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		want  string
	}{
		{"no change", nil, "none"},
		{"docs, design, UI and Markdown", []string{"docs/quickstart.mdx", "design/DECISIONS.md", "web/app/page.tsx", "README.md", "e2e/live/PROOFS.md"}, "none"},
		{"the results a live run writes", []string{"e2e/live/support.json"}, "none"},
		{"this selector", []string{"e2e/live/affected/main.go"}, "none"},
		{"a unit test", []string{"server/internal/delivery/daemon_test.go"}, "none"},
		{"an e2e test and its fakes", []string{"e2e/conformance_test.go", "e2e/fakeharness/main.go"}, "none"},
		{"one adapter", []string{"adapters/omp/aboard.ts"}, "omp"},
		{"one harness package, named without its dash", []string{"server/internal/harness/claudecode/claudecode.go"}, "claude-code"},
		{"two harnesses", []string{"adapters/omp/profile.yaml", "adapters/codex/profile.yaml", "docs/harnesses/codex.mdx"}, "codex,omp"},
		{"every harness one by one", []string{"adapters/omp/aboard.ts", "adapters/codex/profile.yaml", "adapters/claude-code/profile.yaml"}, "all"},
		{"the skill, though it is Markdown", []string{"skills/aboard/SKILL.md"}, "all"},
		{"delivery", []string{"server/internal/delivery/daemon.go"}, "all"},
		{"the control socket", []string{"server/internal/delivery/control/control.go"}, "all"},
		{"a hook command", []string{"server/internal/cli/hook.go"}, "all"},
		{"the server's API", []string{"server/internal/api/messages.go"}, "all"},
		{"the store", []string{"server/internal/store/sqlite/store.go"}, "all"},
		{"the harness registry, which is no harness", []string{"server/internal/harness/registry/registry.go"}, "all"},
		{"the live suite", []string{"e2e/live/scenarios_test.go"}, "all"},
		{"the support code the live suite uses", []string{"e2e/support/profiles.go"}, "all"},
		{"a file no rule names", []string{"go.mod"}, "all"},
		{"a file directly in adapters", []string{"adapters/embed.go"}, "all"},
		{"a harness and the server", []string{"adapters/omp/aboard.ts", "server/internal/api/messages.go"}, "all"},
		{"an unknown adapter folder", []string{"adapters/someharness/profile.yaml"}, "all"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := decide(c.files, harnesses)
			if got := p.output(); got != c.want {
				t.Errorf("decide(%q) = %q, want %q\n%s", c.files, got, c.want, p.summary())
			}
		})
	}
}

func TestSummaryNamesWhatRunsAndWhy(t *testing.T) {
	for _, c := range []struct {
		files []string
		want  string
	}{
		{[]string{"adapters/omp/aboard.ts"}, "runs omp + cross pairs, because adapters/omp/aboard.ts changed"},
		{[]string{"docs/quickstart.mdx", "README.md"}, "runs nothing: the 2 changed files are only the docs, Markdown"},
		{[]string{"server/internal/delivery/daemon.go", "docs/x.mdx"}, "runs every harness, because server/internal/delivery/daemon.go changed"},
		{nil, "runs nothing: no file changed"},
	} {
		if got := decide(c.files, harnesses).summary(); got != c.want {
			t.Errorf("summary for %q:\n got %q\nwant %q", c.files, got, c.want)
		}
	}
}

// Every harness's folders must map to it: its adapters/<h>/ folder, and its package in
// server/internal/harness, whose name is the harness's without dashes.
func TestEveryHarnessFolderMapsToItsHarness(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	known, err := knownHarnesses(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range known {
		if got := decide([]string{"adapters/" + h + "/profile.yaml"}, known).output(); got != h && len(known) > 1 {
			t.Errorf("a change in adapters/%s runs %q, want %q", h, got, h)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "server", "internal", "harness"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "registry" {
			continue
		}
		file := "server/internal/harness/" + e.Name() + "/" + e.Name() + ".go"
		p := decide([]string{file}, known)
		if len(p.harnesses) != 1 || strings.ReplaceAll(p.harnesses[0], "-", "") != e.Name() {
			t.Errorf("a change in server/internal/harness/%s runs %q; want the harness it is for. Name the package after its harness, without dashes", e.Name(), p.output())
		}
	}
}
