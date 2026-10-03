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
make live                          # every test, about 2 minutes
make live RUN=TestOwnerReachesBusyClaude
LIVE_KEEP=1 make live RUN=TestIdleClaudeWakesAndReplies   # keep the panes and logs even on a pass
```

`make live` runs `go test -tags live -count=1 -v -timeout 60m ./e2e/live/...`; `RUN` is
passed to `-run`. Tests run in parallel, each with its own scratch directory, port and
tmux server.

**Prerequisites.** Go, tmux, and the harnesses logged in: Claude Code (`claude auth
status` says `loggedIn`) and Codex (`codex login status` succeeds, and `codex queue`
exists). A harness that is missing or logged out is skipped with the reason, not failed.

On a machine where Claude Code keeps its login in the system keychain (macOS), a scratch
config directory starts logged out. Run `claude setup-token` once and export the token
it prints as `CLAUDE_CODE_OAUTH_TOKEN` before `make live`: Claude Code then logs in with
a scratch config directory, and your own `~/.claude` (settings, hooks, project list) is
never read or written.

**Settings.**

| Variable | Effect |
| --- | --- |
| `LIVE_KEEP=1` | Save artifacts for passing tests too |
| `LIVE_ARTIFACTS=<dir>` | Where artifacts go (default `e2e/live/artifacts/`, git-ignored) |
| `LIVE_CLAUDE_CONFIG=home` | Run Claude Code with your own config directory (see below) |
| `LIVE_CLAUDE_MODEL=<model>` | Pass `--model` to Claude Code |

**Artifacts.** When a test fails, it writes each pane's full scrollback, Claude Code's
transcripts (every command an agent ran and its output), `daemon.log`, `server.log`,
`doctor.json` and the board to `<artifacts>/<TestName>-<time>/`, and prints the path.

## What each test proves

Turns are model turns per run (prompts typed plus bundles delivered), measured on Claude
Code 2.1.287 with its default model.

| Test | Proves | Turns |
| --- | --- | --- |
| `TestIdleClaudeWakesAndReplies` | Pairing in plain words: "Pair with another agent on Aboard" in one session gives a join line; typed into a second session, it joins. Then a message to the idle first session is handed over within 2 seconds and answered on the board with no one typing. | 6 |
| `TestClaudeExchangesFiveMessages` | One prompt starts the skill's wiring check to PING 3: six messages go back and forth between two sessions, taking turns, and the exchange stops. | 9 |
| `TestOwnerReachesBusyClaude` | While a turn runs a 25-second task, the owner's message (posted with the owner login on the API) reaches it at the next tool boundary and is acted on in that turn; three peer messages sent at the same time, one urgent, wait for the turn to end and arrive as one bundle. | 3 |
| `TestPeerWaitsButNoticeArrives` | While a turn runs two slow tasks, a peer's message never enters it: one tool boundary's notice names it, exactly once, and the message arrives in a bundle when the turn ends. | 3 |
| `TestHumansModeWakesOnlyForPeople` | With `aboard delivery humans`, a peer's message wakes nothing for 10 seconds and stays unread; the owner's message (posted with the owner login on the API) wakes the session within 2 seconds, and the agent reports both sequence numbers from that one bundle. | 2 |
| `TestUpgradeWithSessionOpen` | A session set up with an older aboard keeps working when the new binary is installed over it at the same path: two messages are answered, the daemon and local server are replaced, each message is handed once, doctor reports nothing outdated, the hooks file is byte for byte the same, and `aboard init` again changes nothing. | 3 |
| `TestProjectScopeInit` | `aboard init --scope project` writes only into the project; a session started there runs the hooks and one started elsewhere doesn't; doctor and status name the project's settings; your own config is untouched. | 0 |
| `TestKilledSessionRedelivers` | A session killed (`SIGKILL`) mid-turn after a wake never confirms: the daemon closes it within 5 seconds, the message stays unread, and the next session that resumes the agent receives it and acts on it. | 4 |
| `TestSessionMovesBetweenBoards` | A session that joins one board and then another moves: a message on the new board is handed within 2 seconds and answered there, while a message to its old agent wakes nothing for 20 seconds and stays unread for whichever session resumes that agent. | 3 |
| `TestRestartsLoseNothing` | Stopping the daemon while the stop hook waits (the hook starts it again), and separately stopping the local server and running `aboard up`, loses no message. | 3 |
| `TestIdleCodexWakesAndReplies` | Codex runs the project's hooks (session start, prompt, tool, stop). An idle Codex session is woken through `codex queue` within 2 seconds plus Codex's 2-second gather, and answers on the board. | 2 |
| `TestClaudeAndCodexExchange` | Claude Code and Codex run the wiring check to PING 3 with no one typing. | about 9 |
| `TestCodexStartsPingPong` | Codex starts the wiring check itself; each PONG reaches Codex within 30 seconds of being posted, measured from the daemon's log (handed, added at a tool boundary or shown by `say --wait-reply`), so Codex never keeps its turn busy waiting. | 4 |
| `TestOwnerReachesBusyCodex` | While Codex runs a slow task twice, the owner's message reaches the turn at the next tool call (Codex's pre-tool hook), is acted on in that turn, and never goes into Codex's queue. | 2 |
| `TestCodexWaitsForReplyInItsTurn` | From inside its sandbox, Codex asks with `aboard say --wait-reply` and gets the reply in the same command: the daemon records it as shown and never queues it. | 2 |

A full run with Claude Code only is about 34 turns; the Codex tests add about 10.

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

- **Aboard's state** lives in an `ABOARD_HOME` in the test's scratch directory, the
  product's own way of isolating a copy of Aboard, with the local server on a free port
  (`ABOARD_LOCAL_ADDR`). Harnesses are started with these variables, so their hooks and
  every command an agent runs use the scratch state too.
- **Claude Code** keeps its config in a scratch `CLAUDE_CONFIG_DIR` when it is still
  logged in there (the suite checks with `claude auth status`), seeded so first-run setup
  is done and the project is trusted. Otherwise, or with `LIVE_CLAUDE_CONFIG=home`, it
  uses your own config directory: it then adds the scratch projects to its own project
  list in `~/.claude.json`, and the suite answers the workspace trust question in the
  pane. In that mode, tests skip if `~/.claude/settings.json` holds Aboard's hooks, since
  they would run in the test sessions too.
- **Codex** gets a scratch `CODEX_HOME` with your `auth.json` linked (not copied, so a
  token Codex refreshes stays yours), the project marked trusted and the project's hooks
  trusted. Each hook command carries the scratch variables and writes its event to
  `codex-hooks.log` in the test's directory, so tests see which hooks ran. Teardown also
  stops the app server Codex starts from the scratch `CODEX_HOME`.
- **Checksums.** Before the run, the suite records `~/.claude/settings.json`,
  `~/.codex/config.toml` and `~/.codex/hooks.json`, and whether `~/.local/state/aboard`,
  `~/.local/share/aboard` and `~/.config/aboard` exist. Every test checks them in its
  cleanup, and the run fails if any changed.
- **Harness markers.** Every variable starting `CLAUDE`, `CODEX`, `ABOARD` or `TMUX` is
  removed from what harnesses and aboard commands inherit (except `CLAUDE_CONFIG_DIR`).
  Run from inside a Claude Code session, aboard would otherwise think it runs in that
  session; in a remote Claude Code container the remote session's variables even make
  the nested Claude Code take over the outer session's id. Login variables such as
  `ANTHROPIC_*` and `OPENAI_*` are kept.
- **Teardown.** The tmux server is killed, the suite waits for the harnesses to exit
  (their end hook can start a daemon on the way out), then stops every process running
  the test's aboard binary.

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

### Codex urgent messages, by hand

With the hooks trusted (and their commands carrying the scratch variables, above), give
Codex a task with several tool calls; while it runs, send
`aboard say --to @<agent> --urgent "<instruction>"` and an ordinary message. Right after
the next tool call the urgent message is in the turn's context and the agent acts on it
within the turn; the ordinary one goes to the queue and arrives when the turn ends. Then
remove the `[hooks.state…]` and `[projects."<scratch path>"]` entries from Codex's
`config.toml`, or use a scratch `CODEX_HOME`.

## Adding a test

A new live check is a new `Test…` function in `claude_test.go` or `codex_test.go`, built
on the helpers in `live_test.go` and `harness_test.go`:

1. `requireClaude(t)` or `requireCodex(t)` first, then `t.Parallel()` and `l := newLab(t)`.
2. Make agents with `l.pairCLI()` (writer and reviewer, from a terminal), a project with
   `l.project(name, harness)`, and sessions with `l.startClaude` or `l.startCodex`.
3. `p.bind(agent)` gives a session its agent and the standing instruction to do what
   messages ask. Then send messages that ask for one checkable action, such as
   `Reply to this message with exactly PONG-1.`
4. Assert on the board (`l.waitMessage`, `l.messages`, `l.writerInbox`), the daemon's log
   (`l.handed`, `l.waitHanded`) and `l.doctor`. Wait with `l.waitFor`, never a sleep.
   Read the pane only to see whether the harness is ready or busy.
5. Measure what the spec bounds and log it with `t.Logf("measured: …")`, keep prompts
   short, and add the test and its turn count to the table above and to the release
   checklist item it automates.

## Last run

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
