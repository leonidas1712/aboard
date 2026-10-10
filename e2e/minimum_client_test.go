//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestInvitationsAdvertiseTheClientNeededForSetup(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	status, info := tm.call("GET", "/v1/info", "", nil)
	if status != http.StatusOK || info["min_client_version"] != "0.1.4" {
		t.Fatalf("server omitted its minimum setup client: %d %v", status, info)
	}
	invite := tm.admin.run("invite", "--person", "--server", tm.url(), "--handle", "maya", "--json").json(t)
	link := field(t, invite, "link").(string)
	secret := strings.SplitN(link, "#", 2)[1]
	status, preview := tm.call("POST", "/v1/invites/preview", "", map[string]any{"invite": secret})
	if status != http.StatusOK || preview["min_client_version"] != info["min_client_version"] || preview["prompt"] != invite["prompt"] {
		t.Fatalf("preview and issued prompt disagree: %d %v", status, preview)
	}
	if !strings.Contains(field(t, invite, "prompt").(string), "If aboard version is older than 0.1.4, run aboard upgrade first.") {
		t.Fatalf("invitation omitted upgrade guidance: %v", invite["prompt"])
	}
}

func TestUnknownCommandsTellOlderClientsHowToUpgrade(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, prefix := range [][]string{{"future-command"}, {"help", "future-command"}} {
		for _, jsonOutput := range []bool{false, true} {
			args := append([]string(nil), prefix...)
			if jsonOutput {
				args = append(args, "--json")
			}
			r := e.runExit(args...)
			if r.code != 2 {
				t.Fatalf("unknown command exit changed: %s", r)
			}
			hint := r.stderr
			if jsonOutput {
				out := r.json(t)
				if errorCode(t, out) != "invalid_request" {
					t.Fatalf("unknown command error changed: %s", r)
				}
				hint = field(t, out, "error.hint").(string)
			}
			if !strings.Contains(hint, "this aboard may be older than the server; run aboard upgrade") || !strings.Contains(hint, "aboard help") {
				t.Fatalf("unknown command lacks upgrade/help guidance: %s", r)
			}
		}
	}
}
