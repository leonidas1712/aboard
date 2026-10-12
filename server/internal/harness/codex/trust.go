package codex

import (
	"context"
	"path/filepath"
	"slices"
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
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = hookPath(file)
	}
	seen := false
	for _, hook := range hooks {
		if !slices.Contains(commands, hook.Command) || !slices.Contains(paths, hookPath(hook.SourcePath)) {
			continue
		}
		seen = true
		switch hook.TrustStatus {
		case "trusted", "managed":
		case "untrusted", "modified":
			return []harness.CheckResult{{Name: "codex_hooks_review", Level: harness.LevelWarning, Code: "codex_hooks_need_review", Message: "codex: Aboard's hooks need approval before they can run", Fix: fix}}
		default:
			return unknown()
		}
	}
	if !seen {
		return unknown()
	}
	return nil
}

func hookPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}
