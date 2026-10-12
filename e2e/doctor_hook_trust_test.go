//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestDoctorKeepsHookReviewVisibleUntilCodexReportsTrust(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("init", "--yes", "--harness", "codex", "--allow-commands")
	for _, tc := range []struct{ trust, code string }{
		{"untrusted", "codex_hooks_need_review"},
		{"modified", "codex_hooks_need_review"},
		{"unknown", "codex_hooks_review_unavailable"},
		{"trusted", ""},
	} {
		r := e.exec([]string{"FAKE_CODEX_HOOK_TRUST=" + tc.trust}, "", "doctor", "--json")
		seen := false
		for _, raw := range field(t, r.json(t), "checks").([]any) {
			check := raw.(map[string]any)
			if check["code"] != "codex_hooks_need_review" && check["code"] != "codex_hooks_review_unavailable" {
				continue
			}
			seen = true
			if check["code"] != tc.code {
				t.Fatalf("wrong approval diagnostic: %v", check)
			}
			if !strings.Contains(check["fix"].(string), "/hooks") || !strings.Contains(check["fix"].(string), "codex resume --last") {
				t.Fatalf("review warning lacks approval and resume steps: %v", check)
			}
		}
		if seen != (tc.code != "") {
			t.Fatalf("doctor review with trust %s: %s", tc.trust, r)
		}
	}
}
