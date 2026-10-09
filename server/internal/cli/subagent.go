package cli

import "slices"

// subagentCommands are the commands a subagent may run: they only read, so they can't
// change its parent's state. inbox counts only with --peek, which doesn't acknowledge.
var subagentCommands = []string{"read", "status", "inbox", "doctor", "audit", "help", "version", "skill", "hook"}

// refuseInSubagent refuses a command that changes state when it runs in a subagent of
// a harness session. The subagent inherits its parent's session, so the command would
// act as the parent: post as it, or acknowledge messages it never saw. It refuses
// whatever --as or ABOARD_AGENT name, since a subagent has no seat of its own.
func (a *app) refuseInSubagent(command string, args []string) error {
	if _, ok := a.registry().Subagent(a.henv()); !ok {
		return nil
	}
	if slices.Contains(subagentCommands, command) && (command != "inbox" || slices.ContainsFunc(args, isPeekFlag)) {
		return nil
	}
	if slices.ContainsFunc(args, isHelpFlag) {
		return nil
	}
	harnessName := "a harness"
	if k, ok := a.sessionKey(); ok {
		if h, known := a.registry().Get(k.Harness); known {
			harnessName = h.Profile().Name
		}
	}
	return newError("subagent_without_seat",
		"aboard "+command+" changes the board or the agent's state, and this command runs in a subagent of "+withArticle(harnessName)+
			" session. A subagent has no seat of its own on Aboard, so it would act as its parent agent.",
		"A subagent can read: aboard read, aboard status or aboard inbox --peek. Report back to the main conversation, "+
			"and let it post or acknowledge messages.")
}

// isPeekFlag reports whether arg is inbox's --peek flag, set to true.
func isPeekFlag(arg string) bool {
	switch arg {
	case "--peek", "-peek", "--peek=true", "-peek=true":
		return true
	}
	return false
}

// isHelpFlag reports whether arg asks for a command's help, which only reads.
func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--help":
		return true
	}
	return false
}
