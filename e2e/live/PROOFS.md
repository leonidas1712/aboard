# Live delivery proofs

The live suite proves that delivery, setup and upgrades work in the harnesses people
actually run. Each test starts real Claude Code or Codex sessions in tmux, sets up a
scratch project with `aboard init --yes --scope project`, sends messages, and decides pass
or fail from the board, the delivery daemon's log and `aboard doctor`, never from what a
model writes. It spends real model turns, so it is not part of `make check`. Run it
before a release, after any change to delivery, setup or upgrades, and whenever you want
to see delivery work for real.

## Running it

```bash
make live                                  # every scenario for every harness, and the tests about one harness
make live HARNESS=codex                    # one harness: its scenarios and its own tests
make live RUN=TestOwnerReachesBusy/claude-code
LIVE_KEEP=1 make live RUN=TestWakesAndReplies   # keep the panes and logs even on a pass
make live LIVE_PARALLEL=6                  # at most 6 tests at once (default 12)
make live-affected                         # only the harnesses your changes against origin/main touch
make live-affected BASE=HEAD~3             # the same, against another base
make live-smoke                            # one prompt per harness: does its model answer?
make harness-table                         # then write the results into the README's table
```

`make live` runs `go test -tags live -count=1 -v -timeout 60m -parallel 12 ./e2e/live`;
`RUN` is passed to `-run`, `HARNESS` (a harness's name, or several separated by commas)
picks the harnesses, and `LIVE_PARALLEL` sets `-parallel`.

**Every test runs in parallel.** Each test has its own lab: scratch directory, `HOME`,
Aboard state, port and tmux server, so no test waits for another. Every top-level test
calls `t.Parallel()` (through `parallel(t)` in `timing_test.go`, or in `eachHarness`):
go test runs a top-level test that doesn't one at a time, holding up the whole run until
its subtests end. The two things labs could otherwise share are handed out once per
run: the local server's port, and the length of a test's slow task, which tests find
with `pgrep "sleep <seconds>"` across the whole machine. `LIVE_PARALLEL` (default 12)
caps how many run at once; see [engineering/testing.md](../../engineering/testing.md)
for choosing it. The run ends with a table of how long each test took (from when it
started running to the end of its teardown), longest first, then their sum, the wall
time and the effective parallelism: the sum over the wall time.

**Running only what a change touches.** `make live-affected` compares your branch,
committed or not, with `BASE` (default `origin/main`), and runs `make live` for only the
harnesses the changed files touch; a harness picked this way runs its own scenarios and
every cross-harness pair it is in. It prints its decision first, for example
`runs omp + cross pairs, because adapters/omp/aboard.ts changed`.
`go run ./e2e/live/affected` prints the decision without running anything. The mapping,
first match wins, is the table in `e2e/live/affected/main.go`:

| Changed file | Runs |
| --- | --- |
| `skills/` (the skill every agent follows) | every harness |
| `*.md`, `docs/`, `design/`, `web/`, `examples/`, `.github/` | nothing |
| `e2e/live/support.json`, `e2e/live/affected/` | nothing |
| `e2e/live/`, `e2e/support/` | every harness |
| the rest of `e2e/`, and `*_test.go` anywhere else | nothing |
| `adapters/<harness>/`, `server/internal/harness/<harness>/` | that harness and its cross-harness pairs |
| `server/internal/delivery/` (daemon, hooks, control socket), `server/internal/cli/`, the rest of `server/` | every harness |
| anything else | every harness, to be safe |

When every harness is picked one by one, everything runs.

**Prerequisites.** Go, tmux, and the harnesses logged in: Claude Code through
`CLAUDE_CODE_OAUTH_TOKEN` (below), Codex (`codex login status` succeeds, and `codex
queue` exists), and omp 18.5.1 or later, which logs in with the same
`CLAUDE_CODE_OAUTH_TOKEN`. A harness that isn't installed is skipped with the reason;
Codex logged out is skipped too.

Run `claude setup-token` once and export the token it prints as
`CLAUDE_CODE_OAUTH_TOKEN` before `make live`. Claude Code then logs in with a scratch
config directory, and your own `~/.claude` (settings, hooks, project list) is never read
or written. Without the token, or with one that doesn't log Claude Code in, every Claude
Code test fails with that instruction: the suite never falls back to your own config.

**Settings.**

| Variable | Effect |
| --- | --- |
| `LIVE_KEEP=1` | Save artifacts for passing tests too |
| `LIVE_ARTIFACTS=<dir>` | Where artifacts go (default `e2e/live/artifacts/`, git-ignored) |
| `LIVE_CLAUDE_MODEL=<model>` | The model Claude Code runs with, as `--model` (default `claude-sonnet-5-5`) |
| `LIVE_CODEX_MODEL=<model>` | The model Codex runs with, as `-m`, `codex exec` included (default `gpt-6.1-sol`) |
| `LIVE_OMP_MODEL=<model>` | The model omp runs with, as `--model` (default `anthropic/claude-sonnet-5-5`, so omp never picks a local model) |

**Models.** The suite proves Aboard's wiring to each harness, not what a model can do,
so every harness runs a cheap model by default, on every start and every resume. Each
variable above names another for one run, for example `LIVE_CODEX_MODEL=gpt-5.6-luna
make live HARNESS=codex`. When a scenario is too hard for the cheap model, step up one
tier (Haiku to Sonnet, Luna to Sol), not to the top model, and say so in the scenario's
comment. After changing a default, run `make live-smoke` first: it starts each harness
once in a lab like every other test's, with the model the suite would use, asks it to
reply with exactly `SMOKE-OK`, and prints whether that came back (`TestModelSmoke`, one
turn per harness, `HARNESS` picks harnesses as for `make live`).

**Artifacts.** When a test fails, it writes each pane's full scrollback, Claude Code's
transcripts (every command an agent ran and its output), `daemon.log`, `server.log`,
`doctor.json` and the board to `<artifacts>/<TestName>-<time>/`, and prints the path.

## The live kit

The live kit is the scenarios in `scenarios_test.go`, written once and run for every
harness with a driver (`drivers_test.go`), as subtests named after the harness:
`TestWakesAndReplies/claude-code`, `TestWakesAndReplies/codex`. A driver holds what a
harness's profile can't say: how its screen shows a prompt and a running turn, the
questions it asks at start, what it leaves running after its terminal quits, and how to
ask it for a subagent. A scenario that doesn't apply to a harness records n/a with the
reason, from the harness's profile: a Codex session killed after a wake has nothing to
hand again, since Codex's queue confirms a bundle when it takes it.

Each scenario's result (pass, fail or n/a, the day, the harness's version, and the
test) is saved in [support.json](support.json) when the run ends. `make harness-table`
turns it, with the profiles, into the support matrix in the README, with a line per
harness under it naming the versions and days its capabilities were proven on; `make
check` fails when that table is out of date. A capability counts as supported once a
scenario that measures it passed; a failure in the latest run makes it partial until it
passes again. Notes in `support.json` are written by hand, for what a profile can't say.
The entries from before the kit name the earlier test that proved them.

Every test also measures its deliveries from the daemon's log as it cleans up: from a
message's posting (the board's event log) until the daemon began handing it over
(`began` in the `bundle handed` line, Aboard's part), and from then until the session
confirmed it (`bundle confirmed`, the harness's part: a Claude Code stop hook's next
event, Codex's queue taking it, omp's extension adding it). The run saves the median of
each per harness as `handover` in `support.json`, and the README's line for the harness
shows it. A message posted while its session was busy waits for the turn's end, which
counts as Aboard's part; the median keeps those few from deciding the typical value.

Turns are model turns per run of one harness (prompts typed plus bundles delivered),
measured on Claude Code 2.1.287 with its default model.

| Scenario | Proves | Capabilities | Turns |
| --- | --- | --- | --- |
| `TestWakesAndReplies` | Pairing in plain words: "Pair with another agent on Aboard" in one session gives a join line; typed into a second session, it joins and says hello (the baseline, recorded as `JoinsAndTalks`). Then a message to the idle first session is handed over within 2 seconds of the daemon's 2-second gather, its presence goes working and back to idle, and it answers on the board with no one typing. | Baseline, wakes when idle, presence | 6 |
| `TestTaskWorkflowFromSkill` | Two native sessions pair through the quickstart's plain-language join flow. A work request, without CLI commands, has the first session use its installed skill to create and start a task, post a progress message, and finish it. The public API must retain the task's owner and final note, record that message's `about` with `how: current`, and report a null current task after completion. | Task records and automatic tagging | not measured |
| `TestAskAnsweredInTheBoardViewWakesTheAsker` | A native session uses its installed skill to ask its person with options, then goes idle. Chromium answers through the real board view; the recorded option-2 decision must precede the agent's acknowledgment, without another owner prompt. Build the exported UI with `make web` and install Playwright Chromium first. With an isolated runner home, set `PLAYWRIGHT_BROWSERS_PATH` to the installed browser cache; profiles remain temporary. | Asks, browser answers, wake delivery | not measured |
| `TestPingPong` | One prompt starts the skill's wiring check to PING 3: six messages go back and forth between two sessions of the harness, taking turns, and the exchange stops. | Wakes when idle | 9 |
| `TestPingPongAcrossHarnesses` | The same between two harnesses, once for each pair (`claude-code-with-codex`). | Wakes when idle | about 9 |
| `TestOwnerTargetsWakeEachCurrentAgent` | An owner target wakes both current seats; each replies to the exact request and gets a received receipt. | Owner targets | not measured |
| `TestRepliesReachPromptly` | The session starts the wiring check itself with a person's agent; each PONG reaches it within 30 seconds of being posted, measured from the daemon's log (handed, added at a tool boundary or shown by `say --wait-reply`), so it never keeps its turn busy waiting. | Wakes when idle | 4 |
| `TestOwnerReachesBusy` | While a turn runs a slow task twice and then posts DONE, the owner's message (posted with the owner login on the API) reaches it at the next tool boundary and is acted on before DONE; two peer messages sent at the same time, one urgent, never enter the turn: a harness whose hook waits for idle is handed them in one bundle after DONE, and a queue holds them until then. | Owner mid-turn, peers at turn end | 3 |
| `TestPeerWaitsButNoticeArrives` | While a turn runs two slow tasks, a peer's message never enters it: one tool boundary's notice names it, exactly once, and the message arrives in a bundle when the turn ends (or the agent fetches it itself after the notice). n/a for a harness whose queue takes peers' messages at once. | Waiting notice | 3 |
| `TestQuietMessageArrivesWithTheOwnersNextPrompt` | In the default `focused` mode, another agent's message to everyone, asking nothing, doesn't wake the idle session for 15 seconds; the owner's next prompt starts a turn, the harness's turn-start mechanism adds the message before the model runs (the daemon logs `turn start`), the agent answers from it, and it is acknowledged. n/a without the `turn-start` capability. | (focused delivery) | 2 (3 for a harness that runs its session-start hook only at the first prompt) |
| `TestReplyWakesOnlyTheAsker` | The session asks a person's agent a question; the answer, sent with `--reply` and no `--to`, goes to the asker only, wakes it and is acted on, while a third agent's idle session on the board is handed nothing and never works. | (focused delivery) | 3 (5 for a harness that runs its session-start hook only at the first prompt) |
| `TestKilledSessionRedelivers` | A session killed (`SIGKILL`) mid-turn after a wake never confirms: the daemon closes it within 5 seconds, the message stays unread, and the next session that resumes the agent receives it and acts on it. n/a for a harness whose queue confirms a bundle when it takes it. | Wakes when idle | 4 |
| `TestRestartsLoseNothing` | Stopping the daemon (a waiting hook starts it again, or `aboard daemon start` for a harness with none), and separately stopping the local server and running `aboard up`, loses no message. | Wakes when idle | 3 |
| `TestProjectScopeSetup` | `aboard init --scope project` writes every install item into the project; doctor and status name the project's setup; a session started there runs the hooks and one started elsewhere doesn't; your own config is untouched. | Project setup | 0 (1 for a harness that runs its session-start hook only at the first prompt) |
| `TestResumeReconnects` | A session that quits (the agent shows as disconnected; for a harness whose sessions outlive the terminal, its background process is stopped too) and is resumed with the profile's `interactive.resume` in the same pane keeps its session id and is its agent again with no `aboard resume`: the message sent while it was closed, which wakes nothing, is handed when its first turn ends and answered. | Reconnects on resume | 3 |
| `TestSubagentCannotActAsItsParent` | The session has one subagent run `aboard status` and `aboard say`; the subagent's commands are refused (`subagent_without_seat` in the transcripts, and the mark where a hook adds it), nothing reaches the board, and the session reports with `SUBAGENT-DONE`. For Claude Code it also logs the hook input, whether SubagentStart and SubagentStop fired, and whether Claude Code asked before the marked command. | Subagents | 1 |
| `TestSwarmUpStartsFreshAfterNoTurn` | An agent whose session ran no turn (an empty first prompt), stopped with `swarm down`, starts fresh on the next `swarm up`, which says why, and takes its seat in a new session. n/a for a harness whose session starts only with its first turn (Codex). | Started by a launcher | 0 |
| `TestSwarmUpResumesTheLastSession` | An agent `aboard swarm up` started in tmux, which ran a first turn (`aboard say FIRST-TURN`), stopped with `swarm down`, is resumed by the next `swarm up` with the profile's `interactive.resume` and its last session id (`swarm ps` says resumed, same session), and answers the message the owner posted while it was stopped. | Started by a launcher | 2 |

Swarm tests, every harness at once:

| Test | Proves | Turns |
| --- | --- | --- |
| `TestSwarmUpStartsEveryHarness` | `aboard swarm up` with a board file of one Claude Code, one Codex and one omp, each in a project folder set up as the kit sets up that harness, starts each through the launcher (subtests `tmux` and `herdr`, the herdr launcher built from `launchers/herdr`), with its identity in its environment (Codex's in its first prompt): every agent is seated with no join line, claude's message asks codex for `PONG-SWARM` and codex posts it, and `swarm down` leaves every session exited. Recorded as started by a launcher. tmux and herdr keep their sockets, and herdr its sessions, in a folder of the test's own; omp gets its scratch home through a wrapper on the swarm's `PATH`. | 4 per launcher |
| `TestHerdrLauncherPassesTheKit` | The launcher kit against the real herdr, with a home and config folders of the test's own: no model, only herdr. | 0 |

`TestEveryHarnessHasALiveDriver` checks every harness with a profile has a driver, and
starts no harness. `TestModelSmoke` starts each harness with its model in a folder with
no Aboard setup and checks it answers `SMOKE-OK`; it records nothing in the table.

Tests about one harness stay as they were, skipped when `HARNESS` leaves that harness
out:

| Test | Proves | Turns |
| --- | --- | --- |
| `TestHumansModeWakesOnlyForPeople` | With `aboard delivery humans`, a peer's message wakes nothing for 10 seconds and stays unread; the owner's message (posted with the owner login on the API) wakes the session within 2 seconds, and the agent reports both sequence numbers from that one bundle. Claude Code. | 2 |
| `TestUpgradeWithSessionOpen` | A session set up with an older aboard keeps working when the new binary is installed over it at the same path: two messages are answered, the daemon and local server are replaced, each message is handed once, doctor reports nothing outdated, the hooks file is byte for byte the same, and `aboard init` again changes nothing. Claude Code. | 3 |
| `TestRenamedPersonKeepsAgentDelivery` | Renaming a person preserves the bound session and token: the renamed person wakes their existing agent, it replies, and the current owner field uses the new handle. | 3 |
| `TestSessionKeepsBothBoards` | A Claude Code session joining a second board retains both distinct seats. Each board gets exactly one correctly threaded reply through explicit `--board` routing, and each seat independently acknowledges its messages on the server. | 3 |
| `TestCodexWaitsForReplyInItsTurn` | From inside its sandbox, Codex asks with `aboard say --wait-reply` and gets the reply in the same command: the daemon records it as shown and never queues it. | 2 |
| `TestSwarmUpSeatsCodexBehindASharedAppServer` | Codex's app server daemon is started first (`codex app-server daemon start`, under the test's `CODEX_HOME`, with the lab's environment and none of the swarm's), as a Codex the person ran earlier leaves it; then `aboard swarm up` starts a Codex agent in tmux. Its thread runs on that app server (the process its prompt hook ran under is that server, and no other app server started), so nothing it runs sees the environment `swarm up` started it with. It still takes its seat, from the launch ticket on its first prompt's first line; no app server under the test's `CODEX_HOME` has `ABOARD_AGENT` in its environment (read with `ps eww`); and it answers the owner's message with `SHARED-PONG`. The test stops the daemon (`daemon stop`, then signals), and the lab's watchdog stops any process whose environment holds the lab's `CODEX_HOME` if the test process dies first. | 2 |
| `TestCodexSandboxNeedsTheAllowRule` | With Codex's default sandbox (network off) and no allow rule, `aboard status` run by `codex exec` says the server can't be reached from Codex's sandbox, not that it stopped, and doctor warns `codex_aboard_not_allowed`; after `aboard init --scope project --allow-commands`, the same command reaches the running server and daemon. Read from the command's output in Codex's event stream. Recorded as the sandbox check. | 2 |

A full run with Claude Code only is about 45 turns; Codex adds about 35, and the
exchange between them about 9.

**Renamed when the scenarios were written once.** `TestIdleClaudeWakesAndReplies` and
`TestIdleCodexWakesAndReplies` are `TestWakesAndReplies`; `TestClaudeExchangesFiveMessages`
is `TestPingPong`; `TestClaudeAndCodexExchange` is `TestPingPongAcrossHarnesses`;
`TestCodexStartsPingPong` is `TestRepliesReachPromptly`; `TestOwnerReachesBusyClaude` and
`TestOwnerReachesBusyCodex` are `TestOwnerReachesBusy`; `TestProjectScopeInit` is
`TestProjectScopeSetup`; `TestResumedClaudeSessionReconnects` and
`TestResumedCodexSessionReconnects` are `TestResumeReconnects`;
`TestClaudeSubagentCannotActAsItsParent` is `TestSubagentCannotActAsItsParent`.
`TestPeerWaitsButNoticeArrives`, `TestKilledSessionRedelivers` and
`TestRestartsLoseNothing` keep their names and now run per harness.

## Checked by hand

These stay as steps in [RELEASE_CHECKLIST.md](../RELEASE_CHECKLIST.md):

- **The stop-hook race** (a prompt typed the instant a turn ends) needs typing faster
  than a turn's stop hook starts, which tmux can't do reliably. A forced e2e test covers
  the ordering.
- **A prompt typed while the stop hook waits is not interrupted.** Covered by e2e with a
  fake harness; not worth turns live.
- **Doctor green on the machine** with the hooks installed globally, and anything that
  needs `aboard init` without `--scope project`: the suite never writes global config.

## How the suite keeps your machine untouched

- **A home folder of its own.** Everything a test runs (aboard and its daemon, the
  harnesses, their hooks and every command an agent runs) has `HOME` set to a folder in
  the test's scratch directory, so a folder found from `HOME`, such as Codex's
  `~/.agents/skills`, is the test's own. omp gets a scratch `HOME` of its own (below).
  Every `aboard doctor` a test runs fails the test if it names a path in your own home
  folder (other than a harness program on your `PATH`), and `TestLabStaysOutOfYourHome`
  runs Aboard's setup for every harness on the machine and checks doctor that way
  without spending a model turn.
- **Aboard's state** lives in an `ABOARD_HOME` in the test's scratch directory, the
  product's own way of isolating a copy of Aboard, with the local server on a free port
  (`ABOARD_LOCAL_ADDR`). Harnesses are started with these variables, so their hooks and
  every command an agent runs use the scratch state too.
- **Claude Code** keeps its config in a scratch `CLAUDE_CONFIG_DIR`, logged in with
  `CLAUDE_CODE_OAUTH_TOKEN` (the suite checks with `claude auth status`), seeded so
  first-run setup is done and the project is trusted. It never uses your own config
  directory.
- **Codex** gets a scratch `CODEX_HOME` with your `auth.json` linked (not copied, so a
  token Codex refreshes stays yours), the project marked trusted and the project's hooks
  trusted. Each hook command carries the scratch variables and writes its event to
  `codex-hooks.log` in the test's directory, so tests see which hooks ran. Teardown also
  stops the app server Codex starts from the scratch `CODEX_HOME`.
- **omp** runs with a scratch `HOME`, so its `~/.omp` (the `agent.db` with logins, its
  settings and sessions) is the test's own, past first-run setup and with update checks
  off; aboard's own omp setup follows `PI_CODING_AGENT_DIR`, set to the same scratch
  folder for every command. It logs in to Anthropic from `ANTHROPIC_OAUTH_TOKEN`, set to
  `CLAUDE_CODE_OAUTH_TOKEN`; an OAuth token from the environment has no refresh token, so
  nothing can rotate it. Your own `~/.omp` is never read or written. Each time a test
  starts omp, it opens omp's Extension Control Center (`/extensions`) and fails unless
  the only extension omp found is the project's `aboard` (or none, in a folder without
  Aboard's setup), so an extension from your `~/.omp` can never run inside a test. omp
  runs with images off (`PI_FORCE_IMAGE_PROTOCOL=off`): in the lab's tmux it would
  otherwise send a Kitty image command that tmux takes as the pane's title, and the
  suite would never see omp's prompt.
- **Checksums.** Before the run, the suite records the sha256 of
  `~/.claude/settings.json`, `~/.codex/config.toml`, `~/.codex/hooks.json` and
  `~/.omp/agent/config.yml`, and of every entry (a whole folder's tree, or where a link
  points) in `~/.local/bin`, `~/.local/share/claude/versions`,
  `~/.omp/agent/extensions`, `~/.omp/agent/skills`, `~/.agents/skills` and
  `~/.claude/skills`, and whether `~/.local/state/aboard`, `~/.local/share/aboard` and
  `~/.config/aboard` exist (omp's `agent.db` changes whenever you use omp, so the
  folders a test could write to stand for it). Every test checks them in its cleanup,
  and the run fails if any changed. Contents are compared, not times, so a file another
  app rewrites with the same bytes is no false alarm.
- **Harness markers.** Every variable starting `CLAUDE`, `CODEX`, `ABOARD`, `TMUX`, `OMP` or `PI_` is
  removed from what harnesses and aboard commands inherit (except `CLAUDE_CONFIG_DIR`).
  Run from inside a Claude Code session, aboard would otherwise think it runs in that
  session; in a remote Claude Code container the remote session's variables even make
  the nested Claude Code take over the outer session's id. Login variables such as
  `ANTHROPIC_*` and `OPENAI_*` are kept.
- **Terminal markers.** The variables your terminal app sets (`TERM_PROGRAM`,
  `LC_TERMINAL`, and those starting `KITTY`, `GHOSTTY`, `WEZTERM`, `ITERM`, `VSCODE`,
  `ALACRITTY`, `WARP`, `ZELLIJ`, `CMUX`, `HERDR` or `ORCA`, among others) are removed too.
  A harness in the lab draws to the lab's tmux, not to your terminal, and must never
  report its state to your terminal app.
- **Teardown.** The tmux server is killed, the suite waits for the harnesses to exit
  (their end hook can start a daemon on the way out), then stops every process running
  the test's aboard binary.
- **A killed run.** When the test process dies before its teardown (interrupted, timed
  out, killed), each lab's watchdog does the same: it kills the tmux server and what
  ran in its panes, then every process naming the lab's directory. Aboard's own
  processes also stop by themselves, since the lab sets `ABOARD_EXIT_WITH_PID`
  ([engineering/testing.md](../../engineering/testing.md)).

## What we learned driving the real harnesses

Claude Code 2.1.287:

- Started interactively without completed first-run setup, it asks for a theme and then
  to log in, even when `claude auth status` reports a login from the environment. The
  theme answer is saved in the config directory's `settings.json`, so with your own
  config the suite stops and asks you to finish first-run setup by hand rather than
  answering. A scratch config directory seeded with `hasCompletedOnboarding` avoids both
  questions.
- A project whose settings pre-approve a tool (here `Bash(aboard *)`) shows the workspace
  trust question with "No, exit" selected; Down then Enter trusts it.
- With `TERM=linux` it draws its prompt as `>` instead of `❯`. The suite starts every
  harness with `TERM=tmux-256color`.
- A turn is running while the footer shows `esc to interrupt`. The prompt box (a `❯`
  line between two rules) shows during turns too, so "idle" is the prompt box without
  that footer, held for a second. The box can show a suggested prompt as placeholder
  text.
- The pane shows a delivery only as "Stop hook feedback", not the bundle, so tests time
  wakes from the daemon log's `bundle handed` lines.
- It refuses a long `sleep` run on its own in the foreground. Tests keep a turn busy with
  a `slow-task.sh` script in the project instead.
- A `!` shell command in the prompt box also starts a model turn in response.
- Type text and Enter as separate `send-keys` calls, and send Enter once the text shows.
- A first prompt saying "do exactly what it asks with the aboard command and nothing
  else" made the agent refuse to run a script a message asked for. The current first
  prompt says to do what a message asks, including any command it names.
- After an upgrade, the woken turn's own prompt hook runs the new binary, which replaces
  the old daemon before the bundle can be confirmed. The new daemon used to put the
  bundle back to pending and hand it again, a second wake for a message already
  answered; it now keeps it handed, and the turn's next event confirms it.
  `TestUpgradeWithSessionOpen` checks each message is handed once.
- In the plain-words pairing, the joining agent once ran `aboard join` a second time to
  read the charter, which made a second agent (`reviewer-2`): the text output has no
  charter, and each join with a valid code makes a new agent. The skill now says to join
  once with `--json` and never run join again.

Codex 0.159.3 (the earlier proofs by hand, and the suite):

- Don't pass settings with `-c` or use `--dangerously-bypass-hook-trust`: either makes
  Codex run its own embedded app server instead of the shared one, and `codex queue`
  then can't reach the session.
- Codex runs hooks only once they are trusted. When the first prompt is sent with
  untrusted hooks, it shows "Hooks need review" (1. Review hooks, 2. Trust all and
  continue, 3. Continue without trusting) and runs no hook, session start included, until
  it is answered; `/hooks` does the same. Trusting writes one
  `[hooks.state."<hooks.json path>:<event>:<group>:<handler>"]` entry with a
  `trusted_hash` per hook to `$CODEX_HOME/config.toml`. The hash covers the hook, not the
  file's path, and changes when its command does, so Codex asks again after a hook
  command changes. Its app server's `hooks/list` reports each hook's `key` and
  `currentHash`; the suite writes those entries before Codex starts, so the dialog never
  shows.
- Codex hooks don't see the environment Codex was started with, so for live runs each
  hook command needs the scratch variables in it, or a hook starts a daemon on the real
  home's state. `aboard init` writes `ABOARD_HOME=…` into each hook command when it is
  set; the suite adds the rest (`env PATH=… ABOARD_LOCAL_ADDR=… CODEX_HOME=… <aboard> hook
  codex …`).
- Start the daemon outside Codex (`aboard daemon start`) before the first command in
  Codex: a daemon started inside Codex's sandbox inherits the sandbox and can't run
  `codex app-server`. The suite does this with the test's `CODEX_HOME`.
- A turn is running while Codex shows `esc to interrupt` (`Working (4s • esc to
  interrupt)`, or `Starting MCP servers` at the start of the first turn). Idle, its
  prompt box shows the placeholder `Ask Codex to do anything` and its footer `? for
  shortcuts`; it no longer shows `context left`.
- Its start screen shows the prompt box and `? for shortcuts` before Codex takes
  prompts. A prompt typed then can stay in the box after Enter, so the suite presses Enter
  again until the box no longer holds it.
- Codex shows a sent prompt above its prompt box with the same `›` marker, so "the prompt
  was taken" means the last `›` line no longer holds it.
- Codex starts a shared app server (`codex app-server --listen unix:// --managed-daemon`)
  from `$CODEX_HOME/packages`, which keeps running after the session exits.
- With the first prompt "Run `aboard resume reviewer`. … Now reply only OK.", the model
  twice replied OK without running the command. The prompt now says "Run the command …
  now" and "Once the command has run, reply only OK".
- The project's `.codex/config.toml` allows the network in the sandbox
  (`[sandbox_workspace_write] network_access = true`).

omp 18.5.1, driven by hand in tmux with a scratch `HOME`, no login and a model that
doesn't exist, so no model ran (the suite's driver comes from this):

- With no settings it opens a five-step setup ("Sign in to your providers"); `startup:
  setupWizard: false` in the agent folder's `config.yml` skips it, and `startup:
  checkUpdate: false` turns update checks off.
- Its terminal title (`#{pane_title}`) is `π > <folder>` while it waits for the person,
  an animated spinner in place of `>` while a turn runs, and `!` while it waits for an
  answer. The prompt box is the last line starting `╰─`; typed text shows there.
- A tmux client under `LC_ALL=C` prints the stored idle title as `_ > <folder>`.
  `tmux -u` preserves its UTF-8 `π` in both the readiness check and title artifacts;
  the busy/attention title checks still apply. A scratch tmux regression proves this
  without starting a harness or model.
- Ctrl-C twice within a moment quits; one Ctrl-C a second apart doesn't.
- Without a usable model omp picked a local model it found on the machine, so the suite
  always passes `--model`.
- Aboard's extension in the project's `.omp/extensions` connected as omp opened (`session
  started` with `"connection":"extension"` in the daemon's log). `!aboard join <line>`
  typed in omp's prompt runs in omp's shell without the model, and bound the session:
  `aboard status` there showed the agent from the session. A message for the agent was
  handed and confirmed within 3 ms (`bundle handed`, `bundle confirmed`); omp then
  reported that it had no model to run the turn with. Quitting said goodbye, and the agent
  showed no session.

### Codex urgent messages, by hand

With the hooks trusted (and their commands carrying the scratch variables, above), give
Codex a task with several tool calls; while it runs, send
`aboard say --to @<agent> --urgent "<instruction>"` and an ordinary message. Right after
the next tool call the urgent message is in the turn's context and the agent acts on it
within the turn; the ordinary one goes to the queue and arrives when the turn ends. Then
remove the `[hooks.state…]` and `[projects."<scratch path>"]` entries from Codex's
`config.toml`, or use a scratch `CODEX_HOME`.

## Adding a scenario or a harness

**A harness.** Add its driver to `newDriver` in `drivers_test.go`: how to start it in a
project set up with `l.project` and in a plain folder, how its screen shows a prompt and
a running turn, the questions it asks at start and before a command, what it leaves
running after its terminal quits, and how to ask it for a subagent. Its profile gives the
rest, including `interactive.resume`. Then `make live HARNESS=<name>` and `make
harness-table`. `TestEveryHarnessHasALiveDriver` fails until the driver exists.

**A scenario.** A new `Test…` function in `scenarios_test.go` that calls `eachHarness`,
built on the helpers in `live_test.go`, `harness_test.go` and `drivers_test.go`:

1. `eachHarness(t, "<Scenario>", func(t, d, rec) {…})` runs it for every harness, after
   the driver's `require`, in parallel. Call `rec.notApplicable(reason)` first when the
   profile says the scenario doesn't apply.
2. `l := newLab(t)` and `d.setUp(l)` before any harness starts. Make agents with
   `l.pairCLI()` (writer and reviewer, from a terminal), a project with `l.project(name,
   d.p.Harness)`, and sessions with `d.start`.
3. `p.bind(agent)` gives a session its agent and the standing instruction to do what
   messages ask. Then send messages that ask for one checkable action, such as
   `Reply to this message with exactly PONG-1.`
4. Assert on the board (`l.waitMessage`, `l.messages`, `l.writerInbox`), the daemon's log
   (`l.handed`, `l.waitHanded`, `l.reached`) and `l.doctor`. Wait with `l.waitFor`, never
   a sleep. Read the pane only to see whether the harness is ready or busy.
5. Add the scenario to the capabilities it measures in `e2e/support/matrix.go`, to the
   table above with its turns, and to the release checklist item it automates. Measure
   what the spec bounds and log it with `t.Logf("measured: …")`.

A test about one harness starts with `only(t, "<harness>")`.

## Last run

Before the scenarios were written once, so under the earlier names.

2026-10-02, Claude Code 2.1.287 (default model), no Codex installed. `make live`: 1 minute
47 seconds, about 31 turns. The Codex rows are from 2026-10-02 on macOS with Codex 0.159.3
(GPT-6.1-Sol, its default), `make live RUN=Codex`: 40 seconds, 2 turns.

| Test | Result | Measured |
| --- | --- | --- |
| `TestIdleClaudeWakesAndReplies` | Pass | handed 6 ms after posting, reply 1.8 s |
| `TestClaudeExchangesFiveMessages` | Pass | six messages, PING 1 to PONG 3, then quiet |
| `TestUrgentReachesBusyClaude` | Pass | urgent acted on mid-turn; the three ordinary in one bundle after it |
| `TestHumansModeWakesOnlyForPeople` | Pass | peer: no wake; owner: handed 9 ms after posting, both seqs reported |
| `TestUpgradeWithSessionOpen` | Pass | answers 4.1 s and 2.2 s after posting; the first message handed twice (since fixed: rerun handed each once, answers 2.7 s and 1.4 s) |
| `TestProjectScopeInit` | Pass | |
| `TestKilledSessionRedelivers` | Pass | session closed 3.0 s after the kill; redelivered to the next session |
| `TestSessionMovesBetweenBoards` | Pass | 2026-10-03, run on its own: handed 5 ms after posting, reply 2.0 s; old seat: no wake in 20 s, still unread |
| `TestRestartsLoseNothing` | Pass | answers 2.6 s after the daemon restart, 2.9 s after the server restart |
| `TestIdleCodexWakesAndReplies` | Pass | hooks ran; queued 2.1 s after posting (2 s of it Codex's gather), reply 6.2 s |
| `TestClaudeAndCodexExchange` | Skipped | Claude Code needs `CLAUDE_CODE_OAUTH_TOKEN` on that machine |

The proofs before this suite, run by hand with Claude Code 2.1.286 and Codex 0.159.3,
passed the same checks for Codex: idle wake through the queue, the exchange with Claude
Code, urgent at the next tool call once the hooks were trusted (after a fix: urgent
messages wait for the next tool call while a turn runs, instead of going to the queue),
restarts and doctor.
