//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorExplainsMissingSetupInPlainWords(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "omp"), []byte("#!/bin/sh\necho omp/18.5.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.vars = append(e.vars, "PATH="+bin+string(os.PathListSeparator)+fakeBin+string(os.PathListSeparator)+systemPath)
	e.run("up")
	r := e.runExit("doctor")
	if r.code != 3 {
		t.Fatalf("missing required setup must still exit 3: %s", r)
	}
	for _, want := range []string{
		"✓ Codex 0.159.3 found; it can receive Aboard messages",
		"✓ omp 18.5.1 found",
		"these versions can work together",
		"! claude-code: Aboard isn't set up",
		"! codex: Aboard isn't set up",
		"! omp: Aboard isn't set up",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("doctor lacks %q:\n%s", want, r)
		}
	}
	for _, jargon := range []string{"queue available", "omp/18.5.1", "SessionStart", "PostToolUse", "PreToolUse", "supported version window"} {
		if strings.Contains(r.stdout, jargon) {
			t.Fatalf("doctor still contains %q:\n%s", jargon, r)
		}
	}
	j := e.runExit("doctor", "--json")
	if j.code != 3 {
		t.Fatalf("JSON exit changed: %s", j)
	}
	data := j.json(t)
	matchesCLISpec(t, "DoctorOutput", data)
	wantLevels := map[string]string{"claude_hooks_missing": "error", "codex_hooks_missing": "warning", "omp_extension_missing": "error"}
	for _, value := range field(t, data, "checks").([]any) {
		check := value.(map[string]any)
		code, _ := check["code"].(string)
		if want, ok := wantLevels[code]; ok {
			if check["level"] != want {
				t.Fatalf("%s changed level: %v", code, check)
			}
			delete(wantLevels, code)
		}
	}
	if len(wantLevels) > 0 {
		t.Fatalf("missing JSON checks: %v", wantLevels)
	}
	if err := os.WriteFile(filepath.Join(e.home, ".claude", "settings.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if damaged := e.runExit("doctor"); !strings.Contains(damaged.stdout, "✗ claude-code:") {
		t.Fatalf("damaged configuration must retain its error marker: %s", damaged)
	}
}
