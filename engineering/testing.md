# How we prove things work

A test exists to prove that something a person, an agent or another part of Aboard
relies on keeps working. Coverage numbers are not a goal, and a test is not free: it
has to be read, run and kept up to date. So we are intentional about tests, and write
them where they protect the most, in this order.

1. **Contracts over internals.** The code follows ports and adapters
   ([architecture.md](architecture.md)): the domain depends on ports, never on a
   concrete store, harness or launcher. Every adapter of a port (store, notifier,
   harness delivery, launcher, monitor, login provider) passes that port's shared
   contract suite. Adding or swapping an adapter is done with confidence by running
   the suite, not by reading the other adapters.
2. **End-to-end tests for real features.** The real binary, driven the way a person
   or an agent drives it, and live tests with real harnesses (`make live`). The real
   world is messy: many harnesses, each with its own hooks and quirks, running in
   tmux, herdr and other terminal managers. That is where most bugs live, so
   end-to-end tests carry the most weight.
3. **Unit and integration tests only where they add value.** Stable logic that is easy
   to get subtly wrong (the rules engine, the hash chain, redaction, name allocation,
   join-line parsing) and server behaviour under concurrency (one winner for a task
   claim, no duplicate delivery, races that `-race` and CI find). Don't write tests
   that mirror the code or pin implementation details: a test that doesn't protect a
   behaviour someone relies on is a cost.

## The layers

| Layer | What it proves | Runs | Today |
| --- | --- | --- | --- |
| Contract suites | Every adapter of a port behaves the same | `make quick`, `make check` | Store (`board/boardtest`), delivery adapter, journal and server (`delivery/deliverytest`), the control socket's messages against spec/control.md, every harness (`make conformance`); launcher and monitor suites come with those ports |
| End-to-end (`/e2e`) | Features work through the real binary, as documented | `make check` | Yes |
| Live (`e2e/live`) | Delivery, setup and upgrades work in the real harnesses; the support matrix in the README comes from its results | `make live` (`HARNESS=<name>` for one), before each release and after any change to delivery, setup or upgrades | Yes |
| Integration | API behaviour, permissions, error codes, OpenAPI conformance, concurrency | `make quick`, `make check` | Yes |
| Unit | Pure logic with real edge cases | `make quick`, `make check` | Yes |
| Docs as tests | Every command the docs show still works, with the output they show | `make check` | The quickstart; the rest as pages are written |
| UI (Playwright) | The board view renders and its flows work, in both themes, with no accessibility violations | `make web-check`, CI | Smoke test; accessibility checks to add |

### Contract suites

A port's contract suite is a function any adapter's tests call with a way to build that
adapter. It checks what the domain relies on, not how the adapter does it: for the
store, that one task claim wins, that events append in one sequence, that "name taken"
comes back as the domain's error. A new adapter is done when the suite passes; a bug
found in one adapter becomes a case in the suite, so every other adapter is checked for
it too.

Extension points used from outside the repository (harness profiles, launchers, monitor
hooks) ship their suites as public test kits, so a third party can run them without
reading our code.

### The harness conformance kits

Every harness with a profile passes two kits, written once and driven by its profile;
[adding-a-harness.md](adding-a-harness.md) is the step-by-step for a new one.

- **The fast kit**, `make conformance` (`HARNESS=<name>` for one harness), needs no
  model and runs in `make check`. Its in-process half
  (`server/internal/harness/registry/conformance_test.go`) checks the profile against
  its schema, that every capability it declares has code behind it, the delivery
  adapter against the delivery port's contract, and identity markers among all the
  harnesses. Its other half (`e2e/conformance_test.go`) drives the real binary with a
  session played from the profile: init and uninstall in each scope, doctor, identity,
  each hook, subagents, and delivery when idle, at a turn's end, at a tool boundary,
  after a killed session and on resume. The only per-harness code is a fake of what the
  harness does outside Aboard's hooks (`kitFakes`, `adapterFixtures`).
- **The live kit**, `make live HARNESS=<name>`, runs the scenarios in
  `e2e/live/scenarios_test.go` in the real harness, with a small driver per harness for
  its screen ([e2e/live/PROOFS.md](../e2e/live/PROOFS.md)). Each scenario's result is
  saved in `e2e/live/support.json`, and `make harness-table` writes the README's
  support matrix from those results and the profiles; `make check` fails when the table
  is out of date.

### End-to-end tests

Build the real `aboard` binary and drive it exactly as a user or agent would: CLI
commands and HTTP calls against a real server, with real SQLite in a temp directory and
its own `ABOARD_HOME`.

- Every command in `docs/quickstart.mdx` runs in an e2e test, with the same arguments,
  and the test checks the output the page shows.
- Every feature in scope has at least one e2e test showing it works.
- Steps that need a real harness login are live tests, or, when they need a judgment
  call, steps in `e2e/RELEASE_CHECKLIST.md`.

```go
func TestQuickstartTwoTerminalPair(t *testing.T) {
	env := e2e.NewEnv(t) // its own ABOARD_HOME, the built binary, a free port
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

The live suite ([e2e/live/PROOFS.md](../e2e/live/PROOFS.md)) drives real harnesses in
tmux and decides pass or fail from the board, the daemon's log and `aboard doctor`,
never from what a model writes. It spends model turns, so it is not part of
`make check`.

#### What the live suite does with logins

A real harness needs a login, and Aboard never writes to one or does anything that could
rotate or invalidate it. Each harness's docs page says the same.

- **Claude Code.** Each test runs Claude Code with a scratch `CLAUDE_CONFIG_DIR`, logged
  in with `CLAUDE_CODE_OAUTH_TOKEN` from the environment (from `claude setup-token`), and
  the person's `~/.claude` is never read or written. Without the token, or with one that
  doesn't log Claude Code in, the Claude Code tests fail with a message saying to set
  it; they never skip and never fall back to the person's own config.
- **Codex.** Each test has its own `CODEX_HOME`. The person's `auth.json` is linked into
  it, never copied, so a token Codex refreshes is written to the person's own file, not
  to a copy that would leave theirs with a spent refresh token. Nothing else of theirs
  is read.
- **omp.** Each test runs omp with a scratch `HOME`, so omp's `~/.omp` (its `agent.db`
  with logins, settings and sessions) is the test's own, and the person's is never read
  or written. omp logs in to Anthropic from `ANTHROPIC_OAUTH_TOKEN`, which the suite
  sets to `CLAUDE_CODE_OAUTH_TOKEN`, the token it already uses for Claude Code; an OAuth
  token from the environment has no refresh token, so nothing can rotate it. Without
  the token, the omp tests fail with a message saying to set it; they never skip and
  never fall back to the person's own login. omp loads extensions from its agent folder
  as it starts, so every start also checks omp's own list (`/extensions`) holds only the
  project's `aboard`: an extension from the person's `~/.omp` would run inside the test.
- **Every harness.** Harnesses run in the lab's tmux with exactly the lab's environment,
  never the person's shell profile, and with a `HOME` in the lab, so nothing the lab
  runs (aboard, its daemon, the harnesses, their hooks) finds a folder in the person's
  home: Codex's `~/.agents/skills`, for one, follows `HOME`, not `CODEX_HOME`. Every
  `aboard doctor` a live test runs fails the test if it names a path in the person's
  home. The run checks the person's harness config and setup folders by sha256 before
  and after every test. Variables that name the person's harness session
  (`CLAUDE*`, `CODEX*`, `ABOARD*`, `TMUX*`, `OMP*`, `PI_*`) or their terminal app
  (`TERM_PROGRAM`, `ORCA*`, `KITTY*` and the like) are dropped, so a harness neither
  thinks it runs in the person's session nor reports to the person's apps.

#### Which models the live suite runs

The suite proves Aboard's wiring to a harness, not what a model can do, so each harness
runs the cheapest model that passes its scenarios, passed on every start and resume:
Claude Code `claude-sonnet-5-5`, Codex `gpt-6-luna`, omp `anthropic/claude-sonnet-5-5`.
Haiku 4.5 was tried first and missed multi-step scenarios (it skipped turns in the
ping-pong and never started the wiring check), so Claude Code and omp run Sonnet.
`LIVE_CLAUDE_MODEL`, `LIVE_CODEX_MODEL` and `LIVE_OMP_MODEL` name another for one run.
When a scenario is too hard for the cheap model, step it up one tier (Haiku to Sonnet,
Luna to Sol), never straight to the top model. `make live-smoke` checks each harness
answers one prompt with its model before a full run spends turns.

### Extension tests

Code Aboard installs inside a harness (omp's extension, `adapters/omp/aboard.ts`) is
TypeScript the harness's own Bun runs. Its tests (`adapters/<harness>/*.test.ts`) run it
with Bun against a stand-in for the harness's extension API and a fake delivery daemon
on a real socket, speaking spec/control.md. `make extension-test` runs them, and fails,
saying how to install Bun, when Bun isn't installed; `make check` and
`make conformance` run them too.

### Integration tests

Start the server in-process with `httptest.NewServer`, real storage in a temp
directory, and call it through the client generated from `spec/openapi.yaml`. Use these
for API behaviour: permissions, visibility, idempotency, every error code, long-poll
and ack semantics, and what happens when many requests race.

### Unit tests

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

### UI tests

The Playwright test builds `aboard` with the UI embedded, on its own home directory and
port, and drives a board in Chromium. It runs each screen in the light and the dark
theme, and runs an automated accessibility check (axe) on each, failing on any
violation. (The accessibility check is not added yet.)

## Practices

- **Hermetic.** Every test gets its own `ABOARD_HOME`, temp `HOME`, scratch harness
  config folders and a free port. Nothing reads or writes the real config, and tests
  run in parallel.
- **Nothing outlives the test that started it.** A test process can die before its
  cleanups run: interrupted, timed out, or killed along with `go test`. What it started
  must stop anyway, and the local server and the delivery daemon run in sessions of
  their own, started by hooks and commands rather than by the test, so killing the
  test's process group doesn't reach them. Three things cover this, and
  `TestProcessesStopWhenTheTestThatStartedThemIsKilled` (e2e) and
  `TestLabStopsWhenTheTestThatStartedItIsKilled` (live, no model turns) prove it by
  killing a helper test with SIGKILL:
  - Every env and lab sets `ABOARD_EXIT_WITH_PID` to the test process's id. Any
    `aboard` process started with it, and everything it starts inherits it, checks
    five times a second that the process still runs and stops as an interrupt would
    stop it once it doesn't. The fake harness does the same. Unset, which it is for
    everyone but the tests, nothing changes. `scripts/sandbox` keeps it, so a sandbox a
    test opens stops with the test.
  - The test binary exits once `go test`, its parent, is gone, so killing `go test`
    stops the tests and, through the variable, what they started.
  - Each live lab starts a watchdog shell in a process group of its own. When the test
    process is gone it kills the lab's tmux server and what ran in its panes, then
    every process whose command line names the lab's directory, and removes the tmux
    socket's directory. The lab's cleanup stops the watchdog after a normal end.

  A process id can be reused, so a process can outlive its test by as long as an
  unrelated process holds that id; on a test machine that is rare and short.
  `pgrep -fl aboard-e2e-bin-` after a run shows anything left.
- **Injected clock and ids.** Behaviour that depends on time or randomness takes a
  `clock.Clock` and a reader for randomness ([go.md](go.md)). Tests move the fake clock
  instead of waiting.
- **The fake harness.** `e2e/fakeharness` plays a harness process that runs one hook
  and stays up, and `e2e/fakecodex` plays Codex's queue command, so delivery is tested
  end to end on every change without a model. The conformance kit plays any harness
  from its profile with them.
- **Fixtures recorded from real harnesses.** The live suite saves the hook payloads and
  queue requests real Claude Code and Codex send, and the fake harness replays them, so
  the tests that run on every change use what harnesses really send, not what we assume
  they send. Refresh them when a harness changes its payloads. (Not built yet.)
- **CLI output is checked against its contract.** Documented commands are pinned byte
  for byte by e2e tests. Every `--json` output a test sees is validated against its
  command's schema in `spec/cli.yaml`, and a command with no schema fails the test.
  Golden files only for the human output of main commands that no e2e test already
  pins. (The schema check is not built yet.)
- **Two entry points.** `make quick` runs unit, integration and contract suites in
  seconds, for the inner loop. `make check` runs everything CI runs, and nothing is done
  until it passes. (`make quick` is not built yet.)
- **Migration fixtures.** Each released schema version keeps a small database fixture,
  and a test migrates every fixture forward to the current schema and checks the board
  reads back the same and its chain still verifies. (Not built yet; the first fixture
  is the schema of the first release.)
- **Bugs start as failing tests,** at the highest level that's practical: e2e if a
  person or agent saw it, a contract suite if an adapter got it wrong, integration if
  it's API behaviour.

## Rules

- **Don't mock our own components.** Use the real store, real events, real rules. Fakes
  are allowed only at true outside boundaries: harness processes (the fake harness),
  monitors and model APIs, and the clock.
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
- **Test names describe behaviour** in plain words:
  `TestAgentCannotBroadcastWhenRoleLacksPermission`, not `TestPostMessage2`.
- **Tests are independent.** Each one gets its own temp directory, port and server. No
  shared state, no required order, `t.Parallel()` where possible.
- **Failures explain themselves.** On failure, print the command, its exit code, stdout
  and stderr, and the server log.

## Flaky tests

A red build is never normal. A test that fails without a code change is fixed, never
skipped, disabled, loosened or quarantined: a longer timeout or a retry hides the race
it found, and agents quickly learn to ignore failures that are "usually fine".

Reproduce it first (run it many times, under `-race`, on fewer CPUs, in Docker on the
other OS), find the cause and fix it, with a test that fails before the fix. If it
can't be reproduced within a time box, keep the test as it is and record the failure
below with the evidence, so the next failure is compared against it rather than
retried.

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
