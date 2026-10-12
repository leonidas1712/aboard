---
name: aboard-qa
description: How to QA an aboard release or build, by agent and by hand. Use when asked to QA a release or build, do the agent-driven QA, run a UX review, check the board view before a tag, run flow evals, or explain how a person can confirm a build by hand.
---

# QA a build

Tests check what they assert. QA checks what a person would notice: a stale card, a
"Pending" on a message they read, an id where a name should be. This skill is the
agent-driven version. The full hand version is [engineering/qa.md](../../../engineering/qa.md);
section 9 there is the review checklist.

## 1. When to run

- Before every tag, after the release's code is merged.
- After any change to onboarding, setup, delivery or the board view.

## 2. Preconditions

- `main` is pulled. Work in your own worktree and leave the main checkout alone.
- Get the live-run slot: ask on the board whether other agents may run live tests now.
  Flow evals spend model turns.
- Claude runs need `CLAUDE_CODE_OAUTH_TOKEN` in your environment. Check it with
  `test -n "$CLAUDE_CODE_OAUTH_TOKEN"`. Never print it or paste it anywhere.
- Codex must be logged in (`codex login status`).
- Everything runs with an isolated `HOME` and sandbox root. Never touch the real
  `~/.aboard`, `~/.claude` or `~/.codex`. Checksum them before and after, and confirm
  they match:

  ```sh
  find ~/.aboard ~/.claude ~/.codex -type f 2>/dev/null | sort | xargs shasum | shasum
  ```

  Live files change while other sessions run, so name any difference you see and say
  whether your run could have caused it.
- The review needs `web/node_modules`: run `make web` once. The board view embeds the UI
  built when the sandbox started, so rebuild after every pull.

## 3. Agent QA, step by step

### a) Get a team to look at

Either run the flow eval, or seed a sandbox team by hand.

**Flow eval** (spends model turns):

```sh
make flow-eval KEEP=1
```

Defaults: `SCENARIO=invite`, `MODELS=haiku,gpt-6-luna` (admin on Claude, newcomer on
Codex), `APPROVAL=manual`. Other scenarios: `existing-member`, `own-session`,
`interrupted`. `KEEP=1` leaves the team and sessions running, even after a failed
flow. Output lands in `/tmp/aboard-flow-*`; the run prints the exact folder, called
`DIR` below. It holds transcripts, `report.json`, `round3.json` (messages both ways,
the agent-to-person message, the task), `tasks.json`, and `<person>-browser.json`, a
private one-time sign-in link. Keep it private. Links expire after 60 seconds; the
review refreshes them.

Read the transcripts and `report.json` for the flow itself. The counters are
heuristics.

**Faster path: seed a team yourself.** Use it when you only need the board view. With
`SANDBOX_CMD` a sandbox runs one command and exits, so you never type in a normal
terminal. Make the team, the board, an agent for leo, and the invite:

```sh
make sandbox-team TEAM=qa
TEAM=qa SANDBOX_CMD='aboard board new qa && aboard join qa --name leo-agent' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='aboard invite --person --handle maya --board qa --as leo-agent' scripts/sandbox open leo
```

The invite is held for approval. Allow it as leo, then collect the link:

```sh
TEAM=qa SANDBOX_CMD='aboard approvals' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='aboard approvals allow APPROVAL_ID' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='aboard approvals show APPROVAL_ID --as leo-agent' scripts/sandbox open leo
```

A fresh person installs and sets up from the link (the install line builds this
checkout, not a release):

```sh
FRESH=1 TEAM=qa SANDBOX_CMD='curl -fsSL https://comeaboard.dev/install | sh' scripts/sandbox open maya
TEAM=qa SANDBOX_CMD='aboard setup INVITE_LINK --handle maya' scripts/sandbox open maya
```

`FRESH=1` refuses a person who already has a build, so use it only on the first call
that creates them. Then add activity. Run `aboard agents` in a sandbox if an agent name
is wrong. Use single quotes around commands and keep messages plain:

```sh
# person to agent, agent to agent
TEAM=qa SANDBOX_CMD='aboard say "Please look at the install notes" --to mine --board qa' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='aboard say "Looking now" --to owner:leo --board qa --as maya-agent' scripts/sandbox open maya
# agent to person
TEAM=qa SANDBOX_CMD='aboard say "Done, see the task" --to owner:maya --board qa --as leo-agent' scripts/sandbox open leo
# a task created and finished
TEAM=qa SANDBOX_CMD='aboard task new "Check install notes" --board qa --as leo-agent && aboard task done "Checked" --board qa --as leo-agent' scripts/sandbox open leo
# a brief, a file, a board add
TEAM=qa SANDBOX_CMD='printf "# Brief\nShip the notes.\n" > brief.md && aboard brief put brief.md --board qa --as leo-agent' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='printf "notes\n" > notes.txt && aboard file put notes.txt --board qa --as leo-agent' scripts/sandbox open leo
TEAM=qa SANDBOX_CMD='aboard board new second && aboard board add @maya --board second' scripts/sandbox open leo
```

### b) Run the UX review

```sh
scripts/ux-review --eval DIR
```

Or, for a team you seeded:

```sh
scripts/ux-review --sandbox-team qa --people leo,maya --out /tmp/ux-qa
```

With an isolated `HOME`, set `PLAYWRIGHT_BROWSERS_PATH` to your Playwright browser
folder. Other options: `--viewports desktop,phone`, `--headed`, `--out DIR`.

It signs in as each person and walks the Inbox and every card in it, each board
(conversation, first thread, Tasks as Recent and By status, Files, the full brief), the
account menu and People, at 1440x900 and 390x844. On the desktop it also tries the
keys. Output goes under `<out>/ux/`, which for `--eval` is `DIR/ux/`:

- `<person>/<desktop|phone>/<step>.png` and `.txt`: the screenshot and visible text.
- `summary.json`: every step, elements wider than the screen, key results, errors.

Read `summary.json` first. A failed key check or an error is a finding.

### c) Review and write findings

Open every screenshot and read its text file, as the person it belongs to, who knows
only what the docs say. Ask of each screen (details in qa.md section 9):

- Is a card stale, or does it contradict another? Is a status wrong, such as Pending on
  a message that was read?
- Does anything show an id (`mem_`, `brd_`, `apr_`, `inv_`, `prq_`) or jargon outside a
  command to copy? Is a server address shown where the guide uses its name?
- Is there a wait with no next step, or something still new after it was seen?
- Did a draft survive the keyboard checks? Could a key hint pass for a badge?
- Does anything overflow or overlap at 390px?
- Is any text odd: "someone" where a name is known, a wrong tense, a time in the wrong
  direction?

Write `ux-findings.md` in the output folder. One entry per finding, most severe first:

- Severity: **blocker** (wrong or misleading in the main flow), **should fix**
  (confusing, or broken in a corner) or **nit**.
- The screenshot path, relative to the output folder.
- What a person would expect there.
- A suggested fix.

Mark findings already fixed in an open PR as known.

### d) Triage

- Fix blockers and should-fixes as small PRs. Board view work goes to a Claude worker.
  Server and CLI work goes to codex.
- Log nits in `design/ROADMAP.md` or as a task. Do not fix them in this run.
- Rebuild, then repeat b and c on the fixed build to confirm each fix. Say which fixes
  you could not confirm.

### e) Clean up

```sh
python3 scripts/flow_eval.py --cleanup DIR     # after a flow eval
make sandbox-team-clean TEAM=qa                # after a seeded team
pgrep -fl 'codex.*app-server'                  # none of yours should remain
```

Compare the real-config checksums with the ones from before. Release the live-run slot
on the board.

### f) Report

Needs first, as the project rules say. Then:

1. Pass or fail, in one line.
2. Findings fixed, with PR numbers.
3. Findings deferred, with severity and why.
4. What the person should still eyeball: what a screenshot cannot show, such as how the
   real browser feels, or a harness login you did not run.

Name the output folder and attach `ux-findings.md`.

## 4. Manual QA: explaining it to a person

When the person wants to confirm by hand, give them this short version and link
[engineering/qa.md](../../../engineering/qa.md) for the rest:

1. `git pull`, then `make sandbox-team TEAM=qa` and `make sandbox NAME=leo TEAM=qa`.
2. For Claude Code, run `claude setup-token` once and export `CLAUDE_CODE_OAUTH_TOKEN`
   before opening a sandbox shell. Codex is copied in for them.
3. Start Codex with approvals on: `codex -a on-request`.
4. In leo's agent: "Make a board called qa and bring maya onto it." Allow the approval
   in the Inbox. Open maya's shell with `make sandbox NAME=maya TEAM=qa FRESH=1` and
   paste the prompt into her agent.
5. Go through the checklist in qa.md section 5.
6. Stop with `aboard down` in each shell, then `make sandbox-team-stop TEAM=qa`. Resume
   by running the start commands again. Wipe it with `make sandbox-team-clean TEAM=qa`.

Tell them to type only in shells whose prompt starts with `(aboard:<person>@<team>)`.

## 5. Pitfalls

- **Codex approvals off** blocks install and setup ("Operation not permitted"). Use
  `codex -a on-request`.
- **Run commands only in sandbox shells.** A normal terminal uses the installed aboard,
  with different commands and output.
- **Stale Codex app-servers** can outlive a cleanup. Cleanup should stop them. Check
  with `pgrep -fl 'codex.*app-server'` and report any that remain.
- **Shell quoting.** Never put backticks inside a double-quoted `aboard say` message.
  The shell runs them. Use single quotes or plain words.
- **`+dev` builds and the minimum version.** A sandbox build has a `+dev` version. If
  something says aboard is too old, check the server's minimum version before blaming
  the build.
- **The board view is built at sandbox start.** After a pull, rebuild (`make web`), then
  restart the team with `make sandbox-team-stop` and `make sandbox-team`, or run
  `make sandbox-update NAME=<person> TEAM=<team>`. Otherwise you review old screens.
