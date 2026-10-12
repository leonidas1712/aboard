//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCodexNoDaemonWarningAndHealthyResume(t *testing.T) {
	only(t, "codex")
	setup := requireCodex(t)
	parallel(t)
	l := newLab(t)
	l.codexHome(setup)
	board := l.pairCLI()
	p := l.startCodex("warning-agent", l.project("project", "codex"))
	p.submit(fmt.Sprintf("Run `aboard join --board %s --name warning-agent` now, then reply only OK.", board))
	p.waitIdle(3 * time.Minute)
	p.d.afterBind(p)
	l.waitPresence("warning-agent", "idle", 30*time.Second)
	id := ""
	for _, start := range l.starts() {
		if thread, ok := strings.CutPrefix(start.Session, "codex:"); ok {
			id = thread
		}
	}
	if id == "" {
		t.Fatal("no joined Codex thread in the daemon log")
	}
	p.quit()
	p.d.stopBackground(l)
	args := p.d.resumeArgv(l, id)
	args = append([]string{args[0], "--no-daemon"}, args[1:]...)
	env := append(slices.Clone(p.d.env(l)), "ORCA_WARNING_PROOF=1")
	p.respawn(env, args)
	p.d.ready(p)
	p.submit("Run `aboard status` and `aboard doctor` now, then reply only OK. Do not change any settings.")
	p.waitIdle(3 * time.Minute)
	l.waitFor(30*time.Second, "the Orca no-daemon warning", func() bool {
		return l.codexWarning("warning-agent") == "codex_no_daemon_orca"
	})
	doctor := l.exec(t.Context(), l.human, "doctor", "--json")
	if !strings.Contains(doctor.stdout, "export ORCA_CODEX_ISOLATE=0") {
		t.Fatalf("doctor omitted Orca recovery: %s", doctor.stdout)
	}
	p.quit()
	p.d.stopBackground(l)
	p.respawn(p.d.env(l), p.d.resumeArgv(l, id))
	p.d.ready(p)
	p.submit("Run `aboard resume warning-agent` and `aboard status` now, then reply only OK.")
	p.waitIdle(3 * time.Minute)
	l.waitFor(30*time.Second, "healthy resume to clear the warning", func() bool {
		return l.codexWarning("warning-agent") == ""
	})
	l.waitPresence("warning-agent", "idle", 30*time.Second)
}

func (l *lab) codexWarning(agent string) string {
	r := l.exec(l.t.Context(), l.human, "status", "--as", agent, "--json")
	var out struct {
		DeliveryWarning string `json:"delivery_warning"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil {
		return "unavailable"
	}
	return out.DeliveryWarning
}
