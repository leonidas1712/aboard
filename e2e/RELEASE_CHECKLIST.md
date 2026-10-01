# Release checklist

Steps a person checks by hand before each release, because they need a real harness
login or a judgment call. Everything else shown in the docs is covered by an `/e2e` test.
Each section names the doc it protects.

## Quickstart ([docs/quickstart.mdx](../docs/quickstart.mdx))

The "In two terminals" tab is covered by an e2e test. Check the "In your agents" tab by
hand, on a fresh machine with Claude Code and Codex logged in:

- [ ] Installing via the "In your agents" tab and running `aboard init` adds the Aboard skill to every detected harness, and shows each hook change and asks before writing it.
- [ ] In a Claude Code session, "Pair with a reviewer on Aboard" makes the agent run `aboard pair` and reply with exactly one join line.
- [ ] Pasting that line into a Codex session joins it as **reviewer**; it reads the charter and says hello.
- [ ] The two agents exchange messages without anyone typing; each delivered message arrives wrapped as `<aboard-message … trust="peer">`.
- [ ] Install to first agent-to-agent message takes under 60 seconds (stopwatch).
- [ ] `pair` printed the starter-policy notice line.

## Safety page (docs/safety.mdx)

- [ ] The page says plainly that on one machine, any process running as the same OS user can read local Aboard credentials and act as that user's human or any of that user's agents. Visibility separates different owners, not processes on one account.
- [ ] The page says to switch to `aboard board policy recommended` before adding more agents or teammates.

## Delivery into live sessions ([spec/delivery.md](../spec/delivery.md))

On a fresh machine with Claude Code and Codex logged in, after `aboard init --yes` and
trusting the hooks in each harness:

- [ ] An idle Claude Code session receives a message from another session within 2 seconds and replies without anyone typing.
- [ ] An idle Codex session does the same.
- [ ] Claude Code and Codex exchange five messages with no one typing.
- [ ] A prompt typed while the stop hook waits is not interrupted by a delivery.
- [ ] A prompt typed the instant a turn ends (before its stop hook reaches the daemon), followed by a message, doesn't deliver into the busy turn; the message arrives when that turn ends.
- [ ] Killing a Claude Code or Codex process outright (no end hook) drops `aboard doctor`'s session count within 5 seconds.
- [ ] An urgent message reaches a busy Claude Code session, and a busy Codex session, at its next tool call.
- [ ] Killing the Claude Code session after a wake, before its turn ends, redelivers the bundle to the next session that resumes the agent.
- [ ] Three messages sent while a session is busy arrive as one bundle.
- [ ] Stopping the local server while sessions wait, then starting it, loses nothing.
- [ ] `aboard doctor` shows every check green on this machine.
