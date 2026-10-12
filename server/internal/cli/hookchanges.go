package cli

import (
	"slices"
	"strings"
)

func changedHookHarnesses(setups []harnessSetup) []string {
	var changed []string
	for _, setup := range setups {
		if slices.ContainsFunc(setup.Changes, func(change fileChange) bool { return change.Kind == "hooks" && change.Action != actionUnchanged }) {
			changed = append(changed, setup.Name)
		}
	}
	return changed
}

func hookChangesText(names []string) string {
	var text strings.Builder
	for _, name := range names {
		switch name {
		case "codex":
			text.WriteString("Codex: Aboard's hooks changed. In each open Codex session, approve them in /hooks, then restart (codex resume --last).\n")
		case "claude-code":
			text.WriteString("Claude Code: Aboard's hooks changed. In each open Claude Code session, approve them in /hooks, then restart (claude --continue).\n")
		}
	}
	return text.String()
}
