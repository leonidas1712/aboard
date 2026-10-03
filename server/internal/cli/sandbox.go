package cli

// sandboxMarkers are the variables a harness sets for commands it runs in its sandbox,
// and the harness each one means. noNetwork marks the variable a harness sets when its
// sandbox blocks network access: then neither the server's address nor the daemon's
// socket can be reached from there, running or not.
var sandboxMarkers = []struct {
	env, harness, fix string
	noNetwork         bool
}{
	{"CODEX_SANDBOX", "Codex", "trust Aboard's hooks in Codex (/hooks)", false},
	{"CODEX_SANDBOX_NETWORK_DISABLED", "Codex", "trust Aboard's hooks in Codex (/hooks)", true},
	{"SANDBOX_RUNTIME", "Claude Code", "install Aboard's hooks with aboard init", false},
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

// networkBlocked reports the harness whose sandbox this command runs in when that
// sandbox blocks network access, as Codex's does by default.
func (a *app) networkBlocked() (harness string, ok bool) {
	for _, m := range sandboxMarkers {
		if m.noNetwork && a.env.Getenv(m.env) != "" {
			return m.harness, true
		}
	}
	return "", false
}

// allowFix is how to let a harness run aboard commands outside its sandbox.
const allowFix = "aboard init --yes --allow-commands"

// sandboxBlocksNetwork is the error for a command that got no answer from the server
// or the daemon inside a sandbox that blocks network access. Whether they run can't be
// told from there, so it names the sandbox rather than saying they stopped.
func sandboxBlocksNetwork(harness, what string) *Error {
	return &Error{
		Code: "sandbox_blocks_network",
		Message: "This command runs inside " + harness + "'s sandbox, which blocks network access, so it can't reach " +
			what + ".",
		Hint: "Run " + allowFix + " in a terminal, which lets " + harness + " run aboard commands outside its sandbox, " +
			"or approve running this command outside the sandbox. Then run it again.",
	}
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

// sandboxDaemonError is the error for a sandboxed command that found no daemon, or nil
// outside a sandbox: the sandbox may hide a running daemon, or none runs and it mustn't
// start there.
func (a *app) sandboxDaemonError() *Error {
	if harness, ok := a.networkBlocked(); ok {
		return sandboxBlocksNetwork(harness, "the Aboard delivery daemon")
	}
	if harness, fix, ok := a.sandboxed(); ok {
		return daemonInSandbox(harness, fix)
	}
	return nil
}
