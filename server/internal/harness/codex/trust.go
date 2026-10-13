package codex

import (
	"context"
	"path/filepath"
	"time"

	deliverycodex "github.com/leonidas1712/aboard/server/internal/delivery/codex"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// HookTrustChecks reports required approval for Aboard's commands only.
func (h *Harness) HookTrustChecks(ctx context.Context, e harness.Env, commands, files []string) []harness.CheckResult {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	adapter := &deliverycodex.Adapter{}
	hooks, err := adapter.ListHookTrust(ctx, h.ConfigDir(e), e.Dir)
	fix := "Approve Aboard's hooks in /hooks in each open Codex session, then restart with codex resume --last."
	unknown := func() []harness.CheckResult {
		return []harness.CheckResult{{Name: "codex_hooks_review", Level: harness.LevelWarning, Code: "codex_hooks_review_unavailable", Message: "codex: hook approval could not be checked", Fix: fix}}
	}
	if err != nil {
		return unknown()
	}
	type hookSource struct{ command, path string }
	expected := make(map[hookSource]bool)
	for _, file := range files {
		for _, command := range commands {
			expected[hookSource{command, hookPath(file)}] = false
		}
	}
	for _, hook := range hooks {
		key := hookSource{hook.Command, hookPath(hook.SourcePath)}
		if _, ok := expected[key]; !ok {
			continue
		}
		switch hook.TrustStatus {
		case "trusted", "managed":
			expected[key] = true
		case "untrusted", "modified":
			return []harness.CheckResult{{Name: "codex_hooks_review", Level: harness.LevelWarning, Code: "codex_hooks_need_review", Message: "codex: Aboard's hooks need approval before they can run", Fix: fix}}
		default:
			return unknown()
		}
	}
	if len(expected) == 0 {
		return unknown()
	}
	for _, trusted := range expected {
		if !trusted {
			return unknown()
		}
	}
	return nil
}

func hookPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
