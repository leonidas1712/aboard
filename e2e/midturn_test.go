//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

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

func TestUrgentPeerFeedbackExplainsEligibilityAndOwnerPolicy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"work"`)
	e.presenceIs("writer-reviewer", "reviewer", "working", "focused")
	first := writer.run("say", "--to", "@reviewer", "--urgent", "peer step", "--json").json(t)
	recipients, ok := first["recipients"].([]any)
	if !ok || len(recipients) != 1 || recipients[0].(map[string]any)["outcome"] != "next_step_if_supported" {
		t.Fatalf("peer eligibility: %v", first)
	}
	e.run("delivery", "midturn", "owner-only")
	second := writer.run("say", "--to", "@reviewer", "--urgent", "wait until end")
	if !strings.Contains(second.stdout, "its owner allows mid-turn input only from their person") {
		t.Fatalf("owner policy feedback: %s", second.stdout)
	}
	e.run("delivery", "off", "--as", "reviewer")
	quiet := writer.run("say", "--to", "@reviewer", "--urgent", "do not wake", "--json").json(t)
	if field(t, quiet, "recipients").([]any)[0].(map[string]any)["outcome"] != "not_woken" {
		t.Fatalf("policy hint overrode delivery off: %v", quiet)
	}
}
