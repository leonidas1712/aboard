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
