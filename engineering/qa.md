# QA a release by hand

Follow this page top to bottom to check the team flows of a build on one laptop: a
team server, an admin (leo) and a colleague (maya) who starts with nothing installed,
each with their own agent. It uses `make sandbox`; the background is in
[CONTRIBUTING.md](../CONTRIBUTING.md#developing-with-a-sandbox). Nothing here touches
your real `~/.aboard`, `~/.claude` or `~/.codex`.

## 1. Before you start

```sh
cd ~/aboard && git pull
```

**Logins.**

- **Claude Code:** the keychain login does not carry into a sandbox. Run
  `claude setup-token` once, then export the token in the terminal before you open a
  sandbox shell. Add the export to `~/.zshrc` to keep it. Never paste the token into an
  agent.

  ```sh
  export CLAUDE_CODE_OAUTH_TOKEN=…
  ```

- **Codex:** `make sandbox` copies the login from `~/.codex` into the sandbox for you.

**Where to type.** Run commands only in the shells whose prompt starts with
`(aboard:<person>@<team>)`, for example `(aboard:maya@qa)`. Any other terminal uses your
installed aboard, with different commands and output.

**Codex version.** Mid-turn delivery needs a recent Codex:

```sh
which -a codex
codex --version
```

## 2. Start a team

```sh
make sandbox-team TEAM=qa
```

Builds the web UI and aboard if needed, starts a team server for `qa` behind a local
HTTPS address, and prepares leo. It prints the address. Open the board view there and
accept the browser's warning for the local certificate. The command line already trusts
it; no system setting changes.

```sh
make sandbox NAME=leo TEAM=qa
```

Opens a shell for leo, the team's admin, already signed in. Start Claude Code or Codex
in it.

```sh
make sandbox NAME=maya TEAM=qa FRESH=1
```

Opens a shell for maya with no aboard installed and an empty project, with your other
tools still on the path. She has no account. The install step in the invite prompt
(`curl -fsSL https://comeaboard.dev/install | sh`) installs this checkout's build into
her sandbox instead of downloading a release; it says so first.

Run the same command again to open another shell in the same person.

## 3. Codex permissions

Start Codex with approvals on, so it can ask to run aboard outside its sandbox:

```sh
codex -a on-request
```

Or run `/permissions` inside Codex and choose an option that asks. With approvals off
("never ask"), Codex cannot install aboard or run it, because its sandbox blocks the
network. Setup then fails with "Operation not permitted".

If you must work without approvals, do the setup yourself in the person's shell (the
install line, then `aboard init --harness codex --allow-commands`), restart Codex and
say "continue Aboard setup". The
[colleague guide](../docs/guides/bring-a-colleague-aboard.mdx) describes this flow.
`aboard setup` adds the aboard-only allow rule itself when it sets up Codex (and
`Bash(aboard *)` for Claude Code), and says so; plain `aboard init` still leaves it to
`--allow-commands`. The manual backup when the agent can't run aboard is in
[If your colleague's agent can't run aboard](../docs/guides/bring-a-colleague-aboard.mdx#if-your-colleagues-agent-cant-run-aboard).

## 4. Invite a colleague

1. In leo's agent, type: `Make a board called qa and bring maya onto it.`
2. The agent asks for approval. Allow it in the Inbox (`aboard open` opens the board
   view), or run the command it printed:

   ```sh
   aboard approvals allow <approval id>
   ```

3. The agent hands you a link and a prompt.
4. Paste the prompt into maya's agent.
5. Approve the harness's prompts when asked. In Claude Code run `/hooks` and approve
   aboard's hooks; in Codex approve the hooks. Restart the harness
   (`claude --continue` or `codex resume`) and say "continue Aboard setup".

## 5. What to check

- [ ] Leo's agent welcomes maya, and maya's agent replies.
- [ ] Leo's Inbox shows the approval, then "@maya joined".
- [ ] Adding someone who already has an account (`aboard board add @maya --board qa`)
      shows a board add in their Inbox.
- [ ] Rename yourself in the board view's account menu. On an invite card in the
      Inbox, **Edit handle** changes the suggested handle.
- [ ] A second session of leo's joins the board: in another leo shell, start an agent
      and run `aboard join qa`.
- [ ] `aboard agents` shows where each agent ran.
- [ ] With two teams, `aboard servers` lists both and commands go to the one you pick.
      Open the second team with another name, for example `make sandbox-team TEAM=qa2`
      and `make sandbox NAME=leo TEAM=qa2`.

Write down anything that felt off: a confusing line, a wait, a step that needed
guessing.

## 6. Stop and resume later

Everything is kept. (Stop and clean use a helper that `make sandbox-team` builds, so run
it once first on a new checkout.)

1. Quit the harnesses.
2. In each person's shell run `aboard down`.
3. Stop the server:

   ```sh
   make sandbox-team-stop TEAM=qa
   ```

To resume, run the same commands as in step 2. To use a newer build for a person:

```sh
make sandbox-update NAME=leo TEAM=qa
```

This rebuilds and restarts that person's delivery daemon on the new build, and keeps their data.
Agents already running keep their old hooks until restarted. A FRESH person has no
linked build and `sandbox-update` refuses them; re-run the install line in their shell
instead. The shared team server
restarts with stop, then `make sandbox-team TEAM=qa`; its address and all data are kept.

## 7. Wipe it all

1. In each person's shell run `aboard down`.
2. Run:

   ```sh
   make sandbox-team-clean TEAM=qa
   ```

This stops the team server, each person's server and daemon, and Codex processes
from their private package directories, then deletes
`~/.aboard-sandboxes/teams/qa`: the server data, the certificate, and every person's
home, boards, harness folders and project. Your real `~/.aboard`, `~/.claude` and
`~/.codex` are not touched.

`FRESH=1` refuses a person who already has a build, so for another fresh start use a
new `NAME`, or clean first.

## 8. The automated version

```sh
make flow-eval
```

Runs a team flow with small models in private tmux sessions: the admin on Claude Haiku,
the newcomer on Codex `gpt-6-luna` with nothing installed. A scripted person copies the
inviter's handover, answers the handle question and restarts when an agent asks. It does
not fix a failed flow for the agents.

Options: `SCENARIO=invite|existing-member|own-session|interrupted` (default `invite`),
`MODELS=<admin model>,<newcomer model>`, `APPROVAL=manual|auto`. Set `FLOW_CODEX` to use
a newer Codex than the one on your path.

Use `KEEP=1` to leave the private team and harness sessions running for a browser
review, including after a failed flow. The output names each person's board URL and
private `leo-browser.json` / `maya-browser.json` sign-in file. These files contain
one-time login links, not person keys; keep them private. Links expire after 60 seconds.
Run the printed `--browser-signin` command immediately before the browser review to
refresh them. A person whose account was never created has no sign-in file.
Run the printed `--cleanup` command afterwards; it stops only this eval's sessions
and team. Without `KEEP=1`, cleanup remains automatic.

The invite scenario continues after hello: each person sends a message to the other
person's agent while it is working, the inviter's agent writes to its person, and an
agent creates and finishes a task. `round3.json` records these observed checks;
`tasks.json` keeps the task list for the UI review.

It spends model turns, so ask for the shared live-run slot first, and export
`CLAUDE_CODE_OAUTH_TOKEN` with a Codex login in place. Each run writes to
`/tmp/aboard-flow-*`: transcripts, the board record, end-state checks, `report.json`
with friction counters (errors, repeated commands, questions), and the prompts the
scripted person answered. Read the transcripts: the counters are heuristics. A failed
run keeps its evidence. It checks that your real harness and aboard files are unchanged.

## 9. Agent UX review

Before every tag, an agent looks at the board view the way a person would. Tests check
what they assert. A person also notices a card that is out of date, a "Pending" on a
message they read, or an id where a name should be. `scripts/ux-review` signs in as each
person of a running team. It walks the board view at desktop (1440×900) and phone
(390×844) sizes and saves a screenshot and the visible text of every screen. A
reviewing agent then reads them with the checklist below.

**Run it** after `make flow-eval KEEP=1`, on the folder it printed:

```sh
scripts/ux-review --eval /tmp/aboard-flow-…
```

It reads each person's `<person>-browser.json` (a one-time sign-in link, private to
you). It refreshes an expired link with the `--browser-signin` command that the flow
eval prints. Stop the team afterwards with the flow eval's `--cleanup`.

Or run it on a team you made by hand (section 2), with each person set up through
their sandbox shell:

```sh
scripts/ux-review --sandbox-team qa --people leo,maya --out /tmp/ux-qa
```

Each sign-in uses `aboard open --json` in that person's sandbox. With an isolated
`HOME`, set `PLAYWRIGHT_BROWSERS_PATH` to your Playwright browser folder. It needs
`web/node_modules` (`make web`).

For each person and size it visits:

- the Inbox, and every card in it (approvals, invites, arrivals, board adds, pairing
  requests);
- each board: the conversation, the first thread, Tasks as Recent and as By status,
  Files and the full brief;
- the account menu and People.

On the desktop it also tries the keys:

- hovering the Inbox shows the I hint;
- I opens the Inbox;
- [ and ] move through the sidebar;
- Esc leaves the message box and keeps the text;
- a draft survives switching boards and back;
- ? opens the keys sheet.

It writes the following under `<out>/ux/`:

- `<person>/<desktop|phone>/<step>.png` and `.txt`: a screenshot and the visible text
  of each step.
- `summary.json`: every step with its file paths, any element wider than the screen,
  the keyboard results (pass or fail) and errors.

`node --test scripts/uxreview/lib.test.cjs` tests its plumbing.

**Review it.** Open every screenshot and read its text. Ask these questions of each
screen as the person it belongs to, who knows only what the docs say:

- **Stale or contradictory cards.** Does a card say it waits for something that already
  happened, such as "Waiting for maya to choose an agent" after maya joined? Do two
  cards tell different stories about the same event?
- **Wrong statuses.** Is there a "Pending" or "Received" on a message the other side has
  read or answered? Is there a task state that disagrees with the conversation?
- **Ids and jargon.** Does anything show `mem_`, `brd_`, `apr_`, `inv_` or `prq_`, a
  pairing, an endpoint or a seat, other than inside a command to copy?
- **Waiting with no next step.** Does the screen say what happens next, or what to do?
- **Seen but unread.** Is something still counted as new after the person has seen it?
- **Shortcut hints.** Could a key hint be taken for a count or a badge?
- **Lost drafts.** Did the keyboard checks keep the text?
- **Phone layout.** Does anything overflow, overlap or get cut off at 390px? Is
  there room left for the content itself?
- **Odd text or timestamps.** Look for a time in the past for something in the
  future, "someone" where a name is known, or a present tense on something decided.
- **Against the guide.** Does anything contradict
  [Bring a colleague aboard](../docs/guides/bring-a-colleague-aboard.mdx)? For example, a
  server address where the guide uses the server's name.

Write `ux-findings.md` in the output folder. List each finding with:

- its severity: **blocker** (wrong or misleading in the main flow), **should fix**
  (confusing, or broken in a corner) or **nit**;
- the screenshot path, relative to the output folder;
- what a person would expect there;
- a suggested fix.

Put the most severe first. Mark findings already fixed in an open PR as known.

```markdown
1. **Blocker (known, #333).** Leo's Inbox still says "Waiting for maya to choose an
   agent" after maya joined and confirmed delivery.
   Screen: `ux/leo/desktop/04-inbox-pairing-3.png`.
   Fix: close the pairing request when setup joins the board.
```

## 10. Troubleshooting

- **Claude can't find the keychain or asks you to log in:** set
  `CLAUDE_CODE_OAUTH_TOKEN` (step 1), then open the sandbox shell again.
- **"Operation not permitted" or "This environment disables sandbox approval
  requests":** Codex approvals are off. Restart it with `codex -a on-request`.
- **Old usage text or an unknown command:** you are in a normal terminal. Use the
  `(aboard:<person>@<team>)` shell.
- **The board view looks old:** run `make sandbox-update NAME=<person> TEAM=<team>`.
- **"Hooks need review":** run `/hooks` in the harness and approve aboard's hooks.
- **Something stuck:** stop (step 6) or clean (step 7) and start again.
