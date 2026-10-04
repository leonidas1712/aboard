# agmsg (fujibee/agmsg)

Notes from reading agmsg's source and documents on 2026-10-04 (v1.5.2, head `ab83d339`).
agmsg moves fast (about 500 commits a month in July and August), so check a claim
against its current source before relying on it. How Aboard positions itself is in
[../positioning.md](../positioning.md).

## What it is

"Cross-agent messaging for CLI AI agents. No daemon, no network, no complexity"
(`README.md`). The pitch: "You stop being the copy-paste courier between your agents."
MIT, by fujibee (1,221 of 1,305 commits; a handful of outside contributors). Started
2026-04-02; 1,539 stars, 156 forks and 339 open issues on 2026-10-04; #5 Product of the
Day on Product Hunt (2026-06-09). Japanese-first community, docs in English and Japanese.

- **Stack.** Bash scripts plus the `sqlite3` CLI (`scripts/`, about 210 files), installed
  as a skill at `~/.agents/skills/agmsg/`. Extras: a Tauri desktop app (`app/`), a
  Node/Postgres remote-sync server (`server/`), an Astro site (agmsg.cc), bats tests.
- **Driver model** (`ARCHITECTURE.md`): four axes, each a swappable shell driver:
  storage (`sqlite`, `jsonl-duckdb`), agent type (12: Claude Code, Codex, Gemini,
  Copilot CLI, Antigravity, OpenCode, Cursor, Grok Build, Hermes, Devin, ...), delivery
  (`monitor`, `turn`, `both`, `off`) and terminal (tmux, herdr, Orca, plain). External
  drivers load only after `agmsg plugin trust`.

## How agents take part

- **A skill and hooks in the session you already run.** `/agmsg` (Claude Code) or
  `$agmsg` (Codex and others) asks for a team and an agent name on first use; then the
  person says "send alice a message ...". No MCP. Hooks are written per project
  (`.claude/settings.local.json`, `.github/hooks/agmsg.json`, Codex `hooks.json`).
- **Identity** is (project path, agent type) to (team, name), in `teams/<team>/config.json`.
  `actas <name>` switches a session to another role under an exclusive PID-checked lock
  (`docs/actas.md`). `send.sh <team> <from> <to> <msg>` takes the sender as an argument:
  nothing authenticates it beyond file access.
- **Spawning and steering.** `spawn <type> <name>` opens a new tmux pane or terminal with
  the role pre-claimed, waits until its watcher is listening, and resumes the role's
  previous session by default; `despawn` closes it. `poke.sh` types text into another
  member's pane after checking that no one is typing (`scripts/poke.sh`).
- **People** take part through their own agent, or through the desktop app, which has a
  team room, a composer and an "app-user" member, and runs agents in its own PTYs.

## Delivery

- **monitor** (Claude Code default): a SessionStart hook tells the agent to start Claude
  Code's Monitor tool on `watch.sh`, which polls SQLite every 5 s and prints one line per
  message. The agent reacts only after its first turn ("priming"). Watchers renew before
  the Monitor tool's 30-minute cap. Codex gets monitor through an app-server bridge and a
  `codex` shell-function shim (beta); OpenCode through a third-party plugin.
- **turn**: a Stop hook runs `check-inbox.sh`, with a 60 s cooldown, and emits every
  unread message as one batch. Since #1003 a PostToolUse hook also delivers mid-turn,
  between tool calls, for any sender; that path does not mark messages read.
- **Desktop app**: injects `[agmsg] <from>: "<preview>" — run /agmsg` into the agent's
  stdin, any CLI, no hook.
- **Read state**: one read cursor per (team, agent); a message counts as read once the
  hook or watcher has printed it. No acknowledgement from the agent, so no redelivery of
  something printed but missed. A stuck-cursor guard exits the watcher loudly.

## Record, multi-machine and safety

- Storage is an append-only `events` table (`message_sent`, `message_read`) with UUIDv7
  ids, plus a legacy `messages` table (`scripts/drivers/storage/sqlite.sh`). No hash chain
  on messages; `team.sh --purge-messages` deletes a team's rows.
- **Remote sync** (`server/`, `docs/remote-setup.md`): each install keeps its local
  SQLite and syncs to a self-hosted Node + Postgres server that stores opaque envelopes
  with a per-team sequence. No authentication ("reaching the server is the permission").
  Optional end-to-end encryption (`age-v1`) with keys handed over by hand; a careful,
  cited threat model (`docs/security.md`). A hosted service is mentioned (guess: paid,
  token in the URL).
- No roles, policy, permissions, rate limits or secret redaction. Loop stopping, task
  claims and turn-taking are left to prompts (README FAQ). Files are not stored: write to
  disk, send a path.

## UX and install

- **Install.** `npx agmsg` (a Node bootstrapper that runs `setup.sh` at the matching tag),
  `curl .../setup.sh | bash`, a clone plus `./install.sh`, or the Claude Code plugin
  marketplace. It asks one question (command name, default `agmsg`) and then, with no
  further consent, writes `~/.agents/skills/agmsg/` (scripts, `db/messages.db`,
  `config.yaml`, a copy of `uninstall.sh`) and a rendered skill into every harness home
  that exists: `~/.claude/commands/`, `~/.copilot/skills/`, `~/.config/opencode/skills/`,
  `~/.hermes/skills/`, `~/.grok/skills/`, Antigravity; it adds `writable_roots` to
  `~/.codex/config.toml` (`install.sh` 940-1076). It ends with "Restart your agent" and
  the command per harness. Needs only bash and sqlite3; Git Bash on Windows.
- **Joining.** In each session: `/agmsg` runs `whoami.sh`; if unregistered, the agent
  asks for a team name (joins or creates), suggests unused agent names that follow the
  roster's naming pattern, runs `join.sh`, then asks for a delivery mode ("Empty input
  means monitor") and writes hooks into the project (`.claude/settings.local.json`,
  `.codex/hooks.json`). On one machine there is no code to pass: both sessions type the
  same team name. Without an allowlist Claude Code asks permission for every script call;
  the skill prints the rule to add (`drivers/types/claude-code/template.md`). Guess: two
  agents talking in two to three minutes, mostly restarts and prompts; there is no stated
  target.
- **Day to day.** The person talks to the agent ("send alice ...", "check my messages",
  "who's on the team"); the agent picks the script. Messages appear as Monitor events or
  a hook block. `team` shows the roster with pane, activity, delivery mode and identity
  checks. Other harnesses: Codex monitor needs a shell function that wraps `codex`;
  Copilot is turn-only.
- **Desktop app** (Tauri, nine languages): sidebar of teams and agents, "+ New" team or
  agent, each agent spawned in an embedded terminal pane, a "# team room" chat, and a
  composer that sends as the team's "app-user". It offers to update an outdated CLI.
- **Spawning.** `spawn <type> <name> [--boot-prompt ...]` opens a tmux pane or OS terminal
  and returns `status=ready` once the watcher listens; per-type flags live in
  `spawn_options.yaml`; `despawn` asks the member to close itself, `--force` kills its pane.
- **Recovery.** `doctor` shows "who holds what" for the whole install, read-only;
  `doctor --fix` repairs only what is safe after one confirmation (`scripts/doctor.sh`).
  `fix` repairs a seat's own identity; `delivery.sh status`; `where`; `version` with
  git-describe provenance. Errors name the next step (a stuck watcher says to restart or
  run `actas`). Known rough edges in the README: "monitor priming" (nudge a fresh session
  with "hi"), upgrades or skill managers silently dropping hooks (#133), Monitor tasks
  that must be checked in `TaskList`.
- **Undo.** `~/.agents/skills/agmsg/uninstall.sh` removes this install's skill files,
  commands, hooks and Codex roots, confirming each step; `--keep-data`, `--yes`, `--all`.
  `reset`, `leave` and `drop` undo a project's registration or a role.
- **Licence.** Plain MIT (`LICENSE`); no rider or field-of-use restriction found in the
  licence, README, `PRIVACY.md`, `SECURITY.md`, `CONTRIBUTING.md` or `package.json`.

## Worth learning from

- Mid-turn delivery at the PostToolUse hook; CLI-version gating of that hook.
- Poke's draft check before typing into a pane; the stuck-cursor guard.
- A `llms.txt` for agents; a JSON read API (`api.sh`) for third-party clients, which
  grew a small ecosystem (kanban, viewers, a TUI).
- A security document that cites `file:line` for each claim and states what it doesn't
  cover.
