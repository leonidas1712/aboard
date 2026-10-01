package cli

// sandboxMarkers are the variables a harness sets for commands it runs in its sandbox,
// and the harness each one means.
var sandboxMarkers = []struct{ env, harness, fix string }{
	{"CODEX_SANDBOX", "Codex", "trust Aboard's hooks in Codex (/hooks)"},
	{"CODEX_SANDBOX_NETWORK_DISABLED", "Codex", "trust Aboard's hooks in Codex (/hooks)"},
	{"SANDBOX_RUNTIME", "Claude Code", "install Aboard's hooks with aboard init"},
}

// sandboxed reports the harness whose sandbox this command runs in, if any. A daemon
// started there would inherit the sandbox and couldn't reach the harness.
func (a *app) sandboxed() (harness, fix string, ok bool) {
	for _, m := range sandboxMarkers {
		if a.env.Getenv(m.env) != "" {
			return m.harness, m.fix, true
		}
	}
	return "", "", false
}

// daemonInSandbox is the error for a command that would have to start the delivery
// daemon inside a harness's sandbox.
func daemonInSandbox(harness, fix string) *Error {
	return &Error{
		Code: "daemon_in_sandbox",
		Message: "The delivery daemon isn't running, and this command runs inside " + harness +
			"'s sandbox, where a daemon couldn't reach " + harness + ".",
		Hint: "Either " + fix + ", so its session-start hook starts the daemon outside the sandbox, " +
			"or run aboard daemon start in a normal terminal. Then run this command again.",
	}
}
