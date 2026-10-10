//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxTeamKeepsPeopleAndRealHomesSeparate(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "aboard-team-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	helper, err := filepath.Abs(filepath.Join(home, "sandbox-team"))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", helper, "../scripts/sandboxteam")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v %s", err, out)
	}
	extra := []string{"SANDBOX_TEAM_HELPER=" + helper}
	originals := map[string]string{".aboard/keep": "aboard", ".claude.json": "claude state", ".claude/keep": "claude", ".codex/auth.json": `{"fake":true}`}
	for path, body := range originals {
		p := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = sandboxCmd(home, extra, "team-clean", "qa").Run() })
	banner := sandboxScript(t, home, extra, "team-start", "qa")
	if !strings.Contains(banner, "(aboard:<person>@qa) shells") {
		t.Fatalf("missing dev-build reminder: %s", banner)
	}
	for _, person := range []string{"leo", "maya"} {
		out := sandboxScript(t, home, append(extra, "TEAM=qa", "SANDBOX_CMD=printf 'HOME=%s\\n' \"$HOME\"; aboard servers --json; aboard people --server \"$ABOARD_SANDBOX_TEAM_URL\" --json || true"), "open", person)
		if !strings.Contains(out, "This sandbox shell uses the dev build. Other terminals may use your installed aboard.") {
			t.Fatalf("missing binary distinction: %s", out)
		}
		if !strings.Contains(out, "HOME="+filepath.Join(home, ".aboard-sandboxes", "teams", "qa", "people", person, "home")) {
			t.Fatalf("person home missing: %s", out)
		}
		if person == "leo" && !strings.Contains(strings.ReplaceAll(out, " ", ""), `"handle":"leo"`) {
			t.Fatalf("admin not signed in: %s", out)
		}
		if person == "maya" && !strings.Contains(out, "login_required") {
			t.Fatalf("new person already signed in: %s", out)
		}
	}
	leoHome := filepath.Join(home, ".aboard-sandboxes", "teams", "qa", "people", "leo", "home")
	sandboxScript(t, home, append(extra, "TEAM=qa", `SANDBOX_CMD=aboard invite --person --server qa --json > "$HOME/invite.json"; printf refreshed > "$CODEX_HOME/auth.json"`), "open", "leo")
	raw, err := os.ReadFile(filepath.Join(leoHome, "invite.json"))
	if err != nil {
		t.Fatal(err)
	}
	var invite struct{ Link string }
	if err = json.Unmarshal(raw, &invite); err != nil || invite.Link == "" {
		t.Fatalf("invite output: %v", err)
	}
	out := sandboxScript(t, home, append(extra, "TEAM=qa", "SANDBOX_CMD=aboard connect '"+invite.Link+"' --handle maya --server-name qa --json; aboard people --server qa --json"), "open", "maya")
	if !strings.Contains(strings.ReplaceAll(out, " ", ""), `"handle":"maya"`) {
		t.Fatalf("real invitation did not connect maya: %s", out)
	}
	sandboxScript(t, home, extra, "team-stop", "qa")
	sandboxScript(t, home, extra, "team-start", "qa")
	reopened := sandboxScript(t, home, append(extra, "TEAM=qa", "SANDBOX_CMD=aboard people --server qa --json"), "open", "maya")
	if !strings.Contains(strings.ReplaceAll(reopened, " ", ""), `"handle":"maya"`) {
		t.Fatalf("restart lost maya login: %s", reopened)
	}
	for path, body := range originals {
		got, err := os.ReadFile(filepath.Join(home, path))
		if err != nil || string(got) != body {
			t.Fatalf("real home changed %s: %v", path, err)
		}
	}
}
