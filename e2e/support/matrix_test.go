package support

import (
	"strings"
	"testing"
)

// A capability the profile declares counts as supported only once a live scenario that
// measures it passed; a failure in the latest run makes it partial, and a capability
// the profile doesn't declare stays unsupported whatever ran.
func TestMatrixChecksDeclaredCapabilitiesAgainstLiveResults(t *testing.T) {
	var p Profile
	p.Harness, p.Identity.Kind = "h", "env"
	p.Delivery.Capabilities = []string{"queue"}
	cell := func(r *HarnessResults, id string) Cell {
		t.Helper()
		for _, c := range Matrix(p, r) {
			if c.Capability.ID == id {
				return c
			}
		}
		t.Fatalf("no capability %s", id)
		return Cell{}
	}
	tests := []struct {
		name       string
		results    map[string]Result
		id         string
		want       Status
		wantInNote string
	}{
		{"declared, never run live", nil, "idle_delivery", Partial, "not proven by the live kit"},
		{"declared and passed", map[string]Result{"WakesAndReplies": {Result: "pass"}}, "idle_delivery", Supported, ""},
		{"one scenario of several passed", map[string]Result{"RepliesReachPromptly": {Result: "pass"}}, "idle_delivery", Supported, ""},
		{"passed once, failed in another", map[string]Result{
			"WakesAndReplies": {Result: "pass"}, "RestartsLoseNothing": {Result: "fail", Date: "2026-10-04"},
		}, "idle_delivery", Partial, "failed live: RestartsLoseNothing 2026-10-04"},
		{"n/a doesn't count as proof", map[string]Result{"WakesAndReplies": {Result: "n/a"}}, "idle_delivery", Partial, "not proven"},
		{"not declared", map[string]Result{"OwnerReachesBusy": {Result: "pass"}}, "owner_mid_turn", Unsupported, ""},
		{"no live scenario applies", nil, "sandbox", NotApplicable, ""},
	}
	for _, tt := range tests {
		got := cell(&HarnessResults{Scenarios: tt.results}, tt.id)
		if got.Status != tt.want || !strings.Contains(got.Note, tt.wantInNote) {
			t.Errorf("%s: %s is %s (%q), want %s with %q", tt.name, tt.id, got.Status, got.Note, tt.want, tt.wantInNote)
		}
	}
}

// Under the table, each harness that ran live names the versions and days its
// capabilities were proven on, and how long a delivery typically took.
func TestEvidenceNamesVersionsDaysAndHandover(t *testing.T) {
	p := Profile{Name: "Claude Code"}
	h := &HarnessResults{
		Version: "2.1.288 (Claude Code)",
		Scenarios: map[string]Result{
			"WakesAndReplies":  {Result: "pass", Date: "2026-10-04", Version: "2.1.288 (Claude Code)"},
			"PingPong":         {Result: "pass", Date: "2026-10-03", Version: "2.1.286 (Claude Code)"},
			"OwnerReachesBusy": {Result: "fail", Date: "2026-10-05", Version: "2.1.290 (Claude Code)"},
			"ResumeReconnects": {Result: "pass", Date: "2026-10-04"},
		},
		Handover: &Handover{AboardMS: 120, HarnessMS: 2340, Deliveries: 14, Date: "2026-10-04"},
	}
	want := "Claude Code: proven on 2.1.286, 2.1.288, 2026-10-03 to 2026-10-04; a delivery typically began 0.1 s after its " +
		"message was posted, and Claude Code confirmed it 2.3 s later (median of 14, 2026-10-04)."
	if got := Evidence(p, h); got != want {
		t.Errorf("Evidence:\n got %s\nwant %s", got, want)
	}
	if got := Evidence(p, &HarnessResults{Scenarios: map[string]Result{"PingPong": {Result: "fail"}}}); got != "" {
		t.Errorf("a harness never proven live: %q, want nothing", got)
	}
}
