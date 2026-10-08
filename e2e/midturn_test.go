//go:build e2e

package e2e

import "testing"

func TestMidturnPreferenceFromTheCLI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)
	check := func(out map[string]any, policy, source string) {
		t.Helper()
		if field(t, out, "policy") != policy || field(t, out, "source") != source {
			t.Fatalf("mid-turn preference: %v", out)
		}
	}
	check(e.run("delivery", "midturn", "owner-only", "--json").json(t), "owner-only", "person_default")
	check(reviewer.run("delivery", "midturn", "--json").json(t), "owner-only", "person_default")
	check(e.run("delivery", "midturn", "my-agents", "--as", "reviewer", "--json").json(t), "my-agents", "agent_override")
	check(reviewer.run("delivery", "midturn", "--json").json(t), "my-agents", "agent_override")
	check(e.run("delivery", "midturn", "--inherit", "--as", "reviewer", "--json").json(t), "owner-only", "person_default")
	check(reviewer.run("delivery", "midturn", "--json").json(t), "owner-only", "person_default")
	check(e.run("delivery", "midturn", "my-agents", "--as", "reviewer", "--board", "writer-reviewer", "--server", "http://"+e.addr, "--json").json(t), "my-agents", "agent_override")
}
