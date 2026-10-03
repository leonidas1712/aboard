# How we prove things work

A test exists to prove a feature works the way a user or agent would use it. Coverage
numbers are not a goal.

## Three levels, in priority order

### 1. End-to-end tests (`/e2e`)

Build the real `aboard` binary and drive it exactly as a user or agent would: CLI
commands and HTTP calls against a real server, with real SQLite in a temp directory and
a temp `HOME`.

- Every command in `docs/quickstart.mdx` runs in an e2e test, with the same arguments,
  and the test checks the output the page shows.
- Every feature in scope has at least one e2e test showing it works.
- Steps that need a real harness login go in `e2e/RELEASE_CHECKLIST.md` instead.

```go
func TestQuickstartTwoTerminalPair(t *testing.T) {
	env := e2e.NewEnv(t) // temp HOME, built binary, free port
	pair := env.Run("pair", "--json")
	line := pair.JSON("join.line")
	env.Run("join", line)
	env.Run("say", "--as", "writer", "--to", "@reviewer", "Draft is in notes.md.")
	inbox := env.Run("inbox", "--as", "reviewer", "--json")
	if got := inbox.JSON("messages.0.from.name"); got != "writer" {
		t.Fatalf("reviewer's inbox: got message from %q, want writer", got)
	}
	env.Run("audit", "verify").ExitCode(0)
}
```

### 2. Integration tests

Start the server in-process with `httptest.NewServer`, real storage in a temp
directory, and call it through the client generated from `spec/openapi.yaml`. Use these
for API behaviour: permissions, visibility, idempotency, every error code, long-poll
and ack semantics.

### 3. Unit tests

Only for pure logic with real edge cases: the hash chain, secret-redaction patterns,
name allocation, permission checks, rate limits, join-line parsing. Table-driven.

```go
// Do: edge cases that could actually break.
func TestParseJoinLine(t *testing.T) {
	tests := []struct{ name, in, wantCode, wantServer string; wantErr bool }{
		{"local", "Join Aboard board docs on localhost as reviewer with code 7Q4-K2M", "7Q4-K2M", "localhost", false},
		{"lowercase code", "join aboard board docs on localhost as reviewer with code 7q4k2m", "7Q4-K2M", "localhost", false},
		{"surrounding prose", "Please run this: Join Aboard board docs on a.example.com as reviewer with code 7Q4-K2M.", "7Q4-K2M", "a.example.com", false},
		{"no code", "Join Aboard board docs on localhost as reviewer", "", "", true},
	}
	// …
}
```

Don't write unit tests that restate the code, test getters and setters, or assert that
a mock was called.

## Rules

- **Don't mock our own components.** Use the real store, real events, real rules. Fakes
  are allowed only at true outside boundaries: harness processes (a fake harness binary
  for delivery tests), the Jev or LLM API, and the clock.
- **No sleeps.** Wait for a condition with a deadline:
  ```go
  // Do
  e2e.Eventually(t, 5*time.Second, func() bool { return len(env.Inbox("reviewer")) == 1 })
  // Don't
  time.Sleep(2 * time.Second)
  ```
  Use the fake clock to move time (expiry, rate limits) instead of waiting.
- **Always `-race`.** `make check` runs `go test -race ./...`.
- **Conformance.** A test runs the server and checks every response against
  `spec/openapi.yaml`, including error responses.
- **Every bug fix comes with a test** that fails before the fix, at the highest level
  that's practical (e2e if the user saw it, integration if it's API behaviour).
- **Test names describe behaviour** in plain words:
  `TestAgentCannotBroadcastWhenRoleLacksPermission`, not `TestPostMessage2`.
- **Tests are independent.** Each one gets its own temp directory, port and server. No
  shared state, no required order, `t.Parallel()` where possible.
- **Failures explain themselves.** On failure, print the command, its exit code, stdout
  and stderr, and the server log.

## Known intermittent failures

A test that failed once and could not be made to fail again is listed here with what is
known, so the next failure is compared against it rather than retried.

- `TestHumansModeWakesOnlyForPeople` (`e2e/modes_test.go`): timed out after 5 s waiting
  for both messages to be acknowledged, once, on GitHub's ubuntu-latest runner for
  branch `claude/dev-isolation` on 2026-10-03. It did not fail again in 130 Linux runs
  in Docker (80 on one arm64 CPU, 50 on two emulated amd64 CPUs) or on macOS.
  Hypothesis: the daemon put a bundle the stop hook had taken back to pending when the
  hook's "received" and its exit reached the daemon together, so the next stop hook
  was handed the bundle again and nothing confirmed it. That race is fixed and
  covered by `TestHookThatTookTheBundleAndLeftHasIt`; if this test fails again, the
  hypothesis is wrong.
