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
