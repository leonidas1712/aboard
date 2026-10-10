//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFlowEvalPreparesFreshPeopleWithoutChangingTheSourceHome(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "aboard-flow-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	helper := filepath.Join(home, "helper")
	if out, err := exec.Command("go", "build", "-o", helper, "../scripts/sandboxteam").CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	original := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(original), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte(`{"test":"source"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(home, "result")
	cmd := exec.Command("python3", "../scripts/flow_eval.py", "--prepare-only", "--output", outDir)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + fakeBin + string(os.PathListSeparator) + systemPath}, "SANDBOX_ABOARD="+binary, "SANDBOX_TEAM_HELPER="+helper)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare: %v %s", err, out)
	}
	var report struct {
		Fresh           bool   `json:"fresh"`
		Project         string `json:"project"`
		SourceUnchanged bool   `json:"source_unchanged"`
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "prepare.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Fresh || !report.SourceUnchanged {
		t.Fatalf("isolation: %+v", report)
	}
	entries, err := os.ReadDir(report.Project)
	if err != nil || len(entries) != 0 {
		t.Fatalf("fresh project: %v %v", entries, err)
	}
	raw, err = os.ReadFile(original)
	if err != nil || string(raw) != `{"test":"source"}` {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".aboard")); !os.IsNotExist(err) {
		t.Fatalf("source aboard written: %v", err)
	}
}
