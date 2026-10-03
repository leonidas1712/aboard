//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// aboard status shows what the agent's session is doing, as its delivery daemon reports
// it: idle while the session waits, working during a turn, and disconnected once it ends.
func TestStatusShowsTheAgentsPresence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)
	presence := func() string {
		p, _ := field(t, e.run("status", "--as", "reviewer", "--json").json(t), "presence").(string)
		return p
	}
	becomes := func(want string) {
		t.Helper()
		eventually(t, 5*time.Second, "the reviewer to be "+want, func() bool { return presence() == want })
	}

	becomes("idle")
	reviewer.hook("prompt", `"prompt":"work on something"`)
	becomes("working")
	stop := reviewer.startHook("stop")
	becomes("idle")
	if r := e.run("status", "--as", "reviewer"); !strings.Contains(r.stdout, "Agent:  reviewer (from --as); delivery auto; idle\n") {
		t.Fatalf("the Agent line doesn't show idle:\n%s", r)
	}
	reviewer.hook("end", "")
	stop.wait(5 * time.Second)
	becomes("no_session")
	if r := e.run("status", "--as", "reviewer"); !strings.Contains(r.stdout, "; delivery auto; disconnected\n") {
		t.Fatalf("the Agent line doesn't say disconnected:\n%s", r)
	}
}
