//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeliveryWarningIsOwnSeatBookkeepingAndClears(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	token := tm.agentToken(tm.admin, board)
	report := map[string]any{"presence": "idle", "delivery_warning": "codex_no_daemon_orca"}
	status, out := tm.call("PUT", "/v1/me/presence", token, report)
	if status != http.StatusOK {
		t.Fatalf("own-seat warning report: %d %v", status, out)
	}
	status, out = tm.call("GET", "/v1/boards/"+board+"/members", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("members: %d %v", status, out)
	}
	found := false
	for _, raw := range field(t, out, "members").([]any) {
		member := raw.(map[string]any)
		if member["kind"] == "agent" && member["delivery_warning"] == "codex_no_daemon_orca" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warning absent from members: %v", out)
	}
	status, out = tm.call("PUT", "/v1/me/presence", tm.key(tm.admin), report)
	if status != http.StatusForbidden {
		t.Fatalf("person reported a seat warning: %d %v", status, out)
	}
	status, out = tm.call("PUT", "/v1/me/presence", token, map[string]any{"presence": "idle", "delivery_warning": "unexpected"})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown warning code accepted: %d %v", status, out)
	}
	status, out = tm.call("PUT", "/v1/me/presence", token, map[string]any{"presence": "idle"})
	if status != http.StatusOK {
		t.Fatalf("clear: %d %v", status, out)
	}
	status, out = tm.call("GET", "/v1/boards/"+board+"/members", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	for _, raw := range field(t, out, "members").([]any) {
		if warning := raw.(map[string]any)["delivery_warning"]; warning != nil {
			t.Fatalf("warning not cleared: %v", out)
		}
	}
}

func TestCodexParentWarningReachesStatusDoctorAndClearsOnResume(t *testing.T) {
	t.Parallel()
	e := newSwarmEnv(t)
	writer := e.claudeSession("warning-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	home := filepath.Join(e.home, "codex home")
	control := filepath.Join(home, "app-server-control", "app-server-control.sock")
	if err := os.MkdirAll(filepath.Dir(control), 0o700); err != nil {
		t.Fatal(err)
	}
	// The fixture only checks whether the control socket path exists; no connection
	// goes through it. A healthy phase has it, even when its prompt mentions the flag.
	if err := os.WriteFile(control, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id := "019a0000-0000-7000-8000-0000000000e3"
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	for i, phase := range []struct{ flag, want string }{
		{"--no-daemon", "codex_no_daemon_orca"},
		{"", ""},
		{"", "codex_no_daemon_orca"},
	} {
		if i == 2 {
			if err := os.Remove(control); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(e.home, "warning-status-"+string(rune('0'+i))+".json")
		command := "aboard join " + quote(line) + " --name reviewer"
		if i > 0 {
			command = "aboard resume reviewer"
		}
		prompt := "This prompt mentions --no-daemon as text.\nrun: " + command + "\nrun: aboard status --json > " + quote(path)
		args := []string{"resume", id}
		if phase.flag != "" {
			args = append(args, phase.flag)
		}
		args = append(args, prompt)
		cmd := exec.Command(filepath.Join(filepath.Dir(binary), "fakeagents", "codex"), args...)
		cmd.Dir, cmd.Env = e.dir, append(append([]string{}, e.vars...), "CODEX_HOME="+home, "ORCA_WARNING_TEST=1", "FAKE_CODEX_HOOKS=untrusted")
		log, err := os.Create(path + ".log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = log.Close() }
		t.Cleanup(stop)
		var out map[string]any
		eventually(t, 15*time.Second, "status from the Codex parent", func() bool {
			raw, err := os.ReadFile(path)
			return err == nil && json.Unmarshal(raw, &out) == nil
		})
		matchesCLISpec(t, "StatusOutput", out)
		if got, _ := out["delivery_warning"].(string); got != phase.want {
			t.Fatalf("parent warning: %v, want %q", out, phase.want)
		}
		checks := e.doctorChecks()
		seen := false
		for _, check := range checks {
			if check["code"] == "codex_no_daemon_orca" {
				seen = true
				if !strings.Contains(check["message"].(string), "export ORCA_CODEX_ISOLATE=0") {
					t.Fatalf("missing Orca fix: %v", check)
				}
			}
		}
		if seen != (phase.want != "") {
			raw, _ := os.ReadFile(path + ".log")
			t.Fatalf("doctor warning after phase %d: %v; status: %v; harness: %s", i, checks, out, raw)
		}
		stop()
	}
}
