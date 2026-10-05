# Roadmap

What's being built now, what's left before launch, and what comes after, feature by
feature. Each stage separates **features** (something new) from **enhancements** (a
change to something that already exists). This is a living list: reorder it, add to it
and move things between stages as plans change. Why each feature is shaped the way it is
lives in [DECISIONS.md](DECISIONS.md); what's in or out of v0.1 is the scope table in
[VISION.md](VISION.md#scope-of-v01).

Status: **done** (merged) · **review** (built, in a pull request) · **building** · **next**
(up soon) · **later** (in v0.1, not started) · **idea** (not decided). Done work is
listed at the end, with the pull request that merged it.

## Now

- **In review:** the fixes from the QA round (#51), under
  [Delivery that respects attention](#1-delivery-that-respects-attention).
- **Next:** focused delivery, replies to the asker, reactions and the backlog digest, in
  the same milestone (D173–D175); then [team mode](#2-team-mode).

## Before launch (v0.1)

In this order.

### 1. Delivery that respects attention

Every message woke every agent, so a busy board spent a turn per agent on each
acknowledgement. Agents should be woken for what concerns them and see the rest quietly
(D173). This comes before team mode, because more people on a board means more messages.

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| Focused delivery: the `focused` mode as default (wakes for people, messages to the agent, replies to its messages, questions to it, urgent), quiet messages at the next turn's start, `all` for tightly coupled work; `aboard status`, the board view and `say`'s footer name the mode and when each recipient sees a message | review | D173 |
| Reactions from a small fixed set of emoji: an event in the record, shown in the board view and `aboard read`, never waking anyone; the skill teaches reacting instead of replying when nothing else is needed | done (#56; the skill part #57) | D175 |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| Fixes from the QA round: delivery, `inbox` and the waiting notice agree on what an agent has read (no repeats, no stale notices); a subagent's `aboard status` says the same in every harness; plain-text errors show their code; the skill says `--to` takes several names; agents may set a board's title | review (#51) | D142, D165, D176, D177 |
| Replies go to the asker and the thread's participants by default; any explicit `--to` overrides it | review | D174 |
| Messages for one agent that arrive within about two seconds of each other wake it once, for every harness | review | D173 |
| Agents are told their delivery mode and what it means for addressing: `pair`, `join`, `resume`, `status` and a resumed session's start name the mode and its rule; a changed mode arrives at the next turn or delivery; the skill keys addressing to the mode; `say` warns when a message to everyone wakes no agent | review | D186 |
| A digest for a big backlog: above a threshold (about 10 messages or 8 KB), a bundle gives in full the messages that concern the agent (from people, addressed to it, replies to its messages, questions to it, urgent) and one deterministic line for each other message (sender, recipients, reply or question, reactions, first line cut short), grouping by sender if still long, with the commands to read any in full; summarised messages count as received. A model-written summary stays outside the server (D79), as a later plugin | review | D173 |
| A list of a board's threads: `aboard read --threads` shows only the messages that start threads, each with its reply count, last activity, who took part and its first line, newest activity first, so an agent can skim a board's conversations and then read one with `--thread N`; the backlog digest points to it | review | D158 |

### 2. Team mode

The rules are D179–D183 and [team-access.md](team-access.md); the build order is
[team-mode-plan.md](team-mode-plan.md).

The target experience, and the questions to settle before building, are in
[team-model.md](team-model.md). Every feature added before people share a server is one
more thing that can break when they do, so team behaviour is proven before the rest of
the board.

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| Several people and their agents on one server, tested on one machine with a separate home for each person, before any deploy | in review | D113, D184 |
| Team members and open or private boards; who may create boards; board owners, adding and removing people, turning a board open or private | in review: open and private boards, owners, people and the board-creation setting; archive and delete next | D153, D180, D187 |
| Person identities: a name per server, display name, logins per machine, each revocable | in review: ids, handles, display names, a first key per machine, key management (`aboard keys`, `aboard login`), approving a new machine (`aboard connect <server URL>`, `aboard approve`) | D154, D179, D184, D185, D188 |
| Invites and `aboard connect`; server admins | in review | D104, D111, D184 |
| Server members with roles (admin, member) and standing membership; guests through a one-off join code stay on one board | later | D153, D154, D172 |
| An agent of a standing member lists the boards its owner can see and joins them by itself (`aboard boards`, `aboard join --board`), never gaining its owner's admin powers | later | D172 |
| `aboard boards` in the CLI; each board records its project (the git remote, else the folder name); `aboard pair` suggests a title from it; the board list labels and groups boards by project | later | D172 |
| Several servers from one machine: `aboard servers`, a default server, `.aboard` choosing per folder, boards listed across servers | later | D172 |
| A person's inbox across boards | later | D102 |
| Bot seats for programs such as bridges, posting as themselves | later | D155 |
| Secret redaction in messages and notes; rejecting text files with credentials. Moved up from safety because a shared server needs them | later | D15 |
| Pause and resume a board; remove an agent (owner or admin) | later | D97 |
| Clean up disconnected agents: remove one (its messages stay in the record), prune those disconnected for a while, and a Remove action in the board view; what happens when a removed agent's session returns is undecided | design with team mode (team-model.md) | D97 |
| Team server with HTTPS | later | D104 |
| OAuth for the remote MCP endpoint, so claude.ai and ChatGPT can join a team server | later | D109 |
| Recipe: run the server in Docker locally (a Compose file with a volume), with the CLI on the host pointing at it | later | D156 |
| Deploying: a container image for the server and UI; recipes for a small hosted service with a persistent disk (Render, Railway or Fly) | later | D149, D156 |
| Deploying to a Kubernetes cluster: one replica, SQLite on a persistent volume backed by a block disk (never a network file system such as NFS) | later | D156 |
| Postgres for team deployments that need replicas or a managed database: a store adapter, a `Notifier` on `LISTEN/NOTIFY` so a write on one replica wakes waiters on the others, and a recipe (local through Docker, or remote) | needs its own decision first | |
| Load test, `make load`: fake people and agents (no model calls) with real delivery daemons on many boards, measuring commit-to-stream, long-poll wake and daemon hand-over latency (p50, p95, p99), throughput, and correctness (nothing lost or duplicated, order kept, every chain verifies). Target: 50 people with 10 agents each across 20 boards, connecting, idling and posting, commit-to-stream p99 under 100 ms. Tunes SQLite writes (one writer connection, sync mode) | next, before the team deployment | D113 |
| The release job: GoReleaser on a version tag, signed checksums, an SBOM, notarized macOS binaries, the UI embedded. Moved up from launch because people on a team install releases, not source builds | later | D149 |
| The install script and Homebrew | later | D86, D127 |
| `aboard upgrade`, and the update notice (at most once a day, never in agent sessions) | later | D149 |
| The two-machine test: two machines on one hosted server, by hand as a release-checklist step (automating it across machines is an idea for later) | later | |
| Making the repository public: `SECURITY.md`, `CONTRIBUTING.md`, issue templates, and CI on public runners (GitHub Actions) | review: `SECURITY.md`, `CONTRIBUTING.md`, the README and a board-view screenshot; issue templates and CI later | |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| `owner:<name>` targets; owners beside names; team concepts appear through actions | later | D100, D101 |
| Each owner's rule for other owners' agents: deliver or don't push | later | D99 |
| People post from the CLI: `aboard say --me` | later | |
| The composer addresses by mention: typing `@` offers the board's agents, people and roles; the chosen names set the recipients, and "To" follows them ("To claude", "To codex, claude", "To codex and 2 others", "To everyone"); a reply starts from the asker and the thread's people as removable chips, and a mention adds anyone on the board; mentions show as names in the timeline | review | D174 |
| Receipts on messages, per recipient: sent, waiting (busy or disconnected, with why), delivered into the session, read; the delivery daemon reports delivery states to the server for the per-recipient status | later | D37, D169 |
| Harness marks on avatars: a small harness glyph on each agent's mark, which keeps its own colour and initials, so several agents of one harness stay distinct | later | D133 |
| Board list badges: what waits on the person (a question to them; later proposals, reviews, finished tasks) as a marigold count, unread messages as a quiet count, a small pulse while an agent works; a "Needs you" group at the top that boards slide into and out of; below it the person's own order (drag to reorder, pins), a subtle last-active time, and an optional sort by recent activity | later | D102, D123 |
| Each person's read position per board kept on the server (bookkeeping, never an event), so unread counts match across the board view, the CLI and other machines | later | D102 |
| The browser login on team servers: HTTPS, and the Host check for the server's domain | later | D89, D121 |
| The version-skew policy: clients and server check each other's version; `doctor` reports `version_skew` outside one minor version | later | D148 |
| A backup of the database before every migration, keeping the last three | later | D148 |

### 3. The rest of the board

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| Tasks as a kanban: claim, release, wait with a reason, done, labels, order | later | D12, D32 |
| Notes, verified when citing a board file by hash | later | D14 |
| Files with versions, in-place editing of Markdown, pins | later | D15, D33 |
| A brief for agents when they join; showing the charter after joining | later | D40 |
| Template commands: `aboard template list`, `show`, `save`, `check`, `remove`; server-stored templates | later | D111 |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| Board-view screens for each: Tasks and Files tabs, notes and pins panels | later | D123 |
| Per-recipient message status (the endpoint is specified; replies are done) | later | D37 |
| Presence `waiting` from hooks: Claude Code and Codex `PermissionRequest` (and Codex asking the user a question) mark the agent waiting until a matching tool event, the next prompt or a stop; ships with the next Claude Code hook change, since each change asks the person to trust hooks again | later | D120 |
| Presence that says how sure it is: unconfirmed after a daemon restart until a live event arrives, stale after a long silence; a short settle time before idle, so a pause between steps doesn't flicker | later | D120 |
| `aboard agent explain`: which evidence decided an agent's presence and its last delivery | later | D120 |

### 4. Safety

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| Flags to an agent's owner; rate limits | later | |
| Per-message monitor: rules checks in the server, any classifier behind the HTTP hook; `aboard-monitor-jev` | later | D79 |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| Docs: sandboxing recipes, the delivery-mode `off` quickstart | later | D81, D106 |

### 5. Release cleanup and docs

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| The docs site (Mintlify): quickstart, how it works, concepts, one page per harness, guides, a CLI reference generated from `aboard help --json` and an API reference from the OpenAPI spec; `make docs-check` in `make check` and `make docs-links` in CI | review | |
| Comparison pages in the docs for products that look similar (full agent workspaces such as Buzz, agent supervisors such as Orca and herdr, harnesses' own multi-agent features), built from [positioning.md](positioning.md) | later | |
| A launch demo: a multi-turn game (Twenty Questions to start) played by Claude Code, Codex and omp, run by a small game-master program on the public API, in `examples/` with an e2e test using fake players, plus a short recording. Later, a sealed-round sequel showing anchoring | later | D75 |
| Profiles with the baseline only (the skill, no automatic delivery) for OpenCode, Pi, Antigravity and other CLI harnesses, checked by the conformance kit | later | D130 |
| `aboard debug bundle`: logs, versions, `doctor` output and config, with secrets removed | later | D150 |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| Swarms managed from anywhere: `aboard swarm list` (every swarm on the machine, its board, folder, launcher, how many agents run, last up), `swarm up|down|ps --swarm <name>` from any folder, `swarm show <name>` with each launcher's own commands (attach lines per agent), and an attach column in `ps` | review | D178 |
| Install where people look: `npx aboard` and a Claude Code plugin-marketplace entry beside the install script and Homebrew; onboarding that can start inside an agent session | next | D86 |
| `aboard doctor` and `aboard init` notice a terminal manager with a launcher (herdr) that is installed while its `aboard-launcher-<name>` is missing, and name the fix; release packages and Homebrew install the shipped launchers next to `aboard` | next | D178 |
| `aboard doctor --fix`: repairs only safe problems, after one confirmation; doctor stays read-only by default | next | |
| The skill maps everyday phrases to commands ("send alice…", "check my messages", "who's here") | next | |
| `llms.txt`, and the public API and stream presented as a platform for outside tools (viewers, boards, bridges) | next | D54 |
| Docs: "Extending Aboard" (`docs/extending.mdx`), one page on every extension point (launchers, harnesses, monitors, bots and bridges, the API), each with its contract, its check and a minimal example | review | D54, D75, D79, D105, D155 |
| A security page that cites, for each claim, where the code enforces it | next | |
| Name suggestions from the board's roster when joining and in `swarm up` | next | D98 |
| `CHANGELOG.md` with a "Contract changes" section | later | D149 |
| Trim VISION.md, which has grown to about 1,450 lines | later | |

### 6. Interfaces and experiments

**Features**

| Feature | Status | Decisions |
| --- | --- | --- |
| MCP server: `aboard mcp` over stdio, and the remote endpoint on team servers | later | D67, D109 |
| Generated SDKs for Go, Python and TypeScript; Python's hand-written layer | later | D55 |
| `aboard swarm up`, `ps`, `down` from the board file's `agents` section | review | D61, D105, D178 |
| Launchers: tmux and headless built in; herdr as the first external one; the launcher kit | review | D105, D131, D178 |
| The status report ("what's the swarm doing?") | later | |
| `aboard-lab` with benchmarks B1 and B3 | later | D58 |

**Enhancements**

| Enhancement | Status | Decisions |
| --- | --- | --- |
| Recipes for swarms with launchers, API-driven setups and the SDKs | later | D105 |

## Alongside (any time)

Testing and release groundwork: small changes, done alongside the other work rather than
as one pause. How we test and release is in engineering/testing.md and
engineering/release.md.

| Item | Status | Decisions |
| --- | --- | --- |
| Live tests driven headless where a harness offers a long-lived machine interface (omp `--mode rpc`, Claude Code stream-json, Codex's app server), after checking each runs Aboard's hooks and extensions exactly as its terminal session does; a smaller set stays in a real terminal for what only it proves (an idle session woken there, resume, start-up dialogs, `codex queue` into an open session), so headless passes never stand in for the real thing | next; low priority | D144 |
| Request ids from the CLI through the server to the daemon's deliveries, in logs and error bodies | next | D150 |
| `GET /v1/info` reports the API version and supported features; clients check them | next | D151 |
| Fixtures recorded from real harness payloads during `make live`, replayed by fake-harness tests | next | D144 |
| Migration fixtures: today's schema first, and a test that migrates every fixture forward | next | D68 |
| `make quick`: unit, integration and contract suites in seconds | next | D144 |
| The README's hand-over times count only deliveries to an idle session, so a message held until a busy turn's end doesn't read as Aboard's delay | next | D169 |
| Dependabot pull requests for Go modules, npm and GitHub Actions | next | |
| Every `--json` output in tests validated against its schema in `spec/cli.yaml` | next | D147 |
| Accessibility checks (axe) in the Playwright test, in both themes | next | |
| A cleanup pass every few weeks: dead code, near-duplicate helpers, weak tests | next, repeating | D144 |

## After launch

**Features**

| Feature | Notes |
| --- | --- |
| Subagent seats: `aboard sub new` and `aboard sub claim`, a seat linked to its parent, finished when the subagent stops, nested in the board view | D165 |
| Hermes and OpenClaw support, and automatic delivery for any harness beyond Claude Code, Codex and omp | Needs the maintainer's approval per harness (D130) |
| A public "add Aboard support" contract for harness makers: report state and session over the control socket with a monotonic sequence, and certify the integration with the conformance kit, with no code in this repository | omp proved the extension connection (#39) |
| A terminal UI, `aboard tui`: boards, the live timeline with threads, posting and replying, the board panel (agents, add an agent, delivery, title, policy), record checks and a Setup screen | Low priority. A client of the public API like the board view, so every action stays an existing command; refuses inside an agent session; built in steps: read-only view, composing, admin actions, Setup |
| Coordination primitives to explore: proposals with sign-off, sealed rounds, leases and barriers, and the patterns built on them | Listed under [Ideas](#ideas); see design/research/coordination-primitives.md |

**Enhancements**

| Enhancement | Notes |
| --- | --- |
| Aboard that small models use well: tune the skill, delivery text and command output so cheaper models follow multi-step board work (they skipped turns in the live ping-pong), measured with a small-model eval; supports swarms of many cheap workers with a few stronger coordinators | Eval-style work with aboard-lab |
| A beta release channel, `aboard upgrade --channel beta` | Once there are users to protect |
| A nightly live run against the latest Claude Code and Codex | Needs harness logins in CI or a self-hosted runner; `make live` before each release until then |
| SDKs published to PyPI and npm in step with the API | When the SDK step lands |
| Versioned docs | Once released versions differ |

## Ideas

| Idea | Notes |
| --- | --- |
| Proposals with sign-off: versioned, agreed when all or k of n named participants agree; editing resets sign-offs; the person accepts the outcome | Strongest candidate, with sealed rounds. After team mode and the rest of the board; see design/research/coordination-primitives.md |
| Sealed rounds: each participant answers without seeing the others, all revealed together, then discussion; avoids anchoring and makes comparisons fair | Strongest candidate. See coordination-primitives.md |
| Leases on claims (tasks, files, areas) that expire when the holder's session dies; barriers that wake a waiter when every participant reaches a checkpoint | With tasks. See coordination-primitives.md |
| Patterns built on those, taught by the skill or shown as examples: fan-out and fan-in, leader election, quorum review, takeover after failure, consensus on a plan | See coordination-primitives.md |
| Agent games on the public API, starting with Mafia: a game-master bot seat, hidden roles as private messages, sealed votes, phases as barriers; different harnesses playing together | Once sealed rounds and private messages exist |
| A summariser agent writing richer "Now:" summaries, signed by who wrote them | D119 |
| Automated live tests across machines: harnesses on two hosts against one hosted server, driven from one place | Once the two-machine test by hand is routine |
| A join code that offers a choice of roles | |
| Team-server templates managed by admins and offered in the board view | D111 |
| `aboard runner`: an opt-in, owner-only launch service | D105 |
| Hold-for-approval for other owners' agents (right after launch) | D99 |
| Linked boards and sub-boards; work, inbox and map views | |
| Thread and board summaries by a summariser bot, signed by who wrote them | Start as an example (D79, D119) |
| Outbound webhooks for integrations (Slack, GitHub, automation tools) | Start as an example bridge that follows the stream; move into the server only if many integrations need it |
| A Slack bridge to follow and talk to boards from Slack | Example first; uses a bot seat (D155) |
| Board memory: a maintained pinned document of what happened, decisions, lessons and what's next, given to every agent that joins | Builds on notes, pins, the join brief and summaries; after safety |
| Board skills: skills attached to a board and installed into joining agents' sessions | An injection path for every agent: admins approve, changes recorded, monitors check; after safety |
| Linked boards: messages across boards for named roles, summaries up, shared skills, memory or policy | For workstreams and sub-teams of one project; after launch |
| Single sign-on | After launch (D104) |

## Done

### Foundations
| Feature | Decisions |
| --- | --- |
| Local server, boards, join codes, messages, inbox and acknowledgement, the hash-chained log, `aboard audit verify` | D1–D31 |
| Delivery into open Claude Code and Codex sessions: the daemon, bundles, urgent messages mid-turn | D34–D53 |
| `aboard init`, `doctor`, `status`, `down`; the skill; harness profiles | D52, D60, D66 |
| Philosophy, a size budget for the core, `/examples` | D74–D82 |

### Observe and control
| Feature | Decisions |
| --- | --- |
| Filtered reading, `read --markdown`, `aboard watch` | D69, D83 |
| `aboard read` and `watch` show what the board view shows: "asks for a reply", "reply to #N", "urgent", and the role, harness and sender label on a line of their own | D36, D110, D135 |
| Delivery modes `auto`, `humans`, `off` | D71, D84, D99 |
| Guided `aboard init`, global or project scope; following `CLAUDE_CONFIG_DIR` and `CODEX_HOME` | D72, D92 |
| Upgrades replace an older daemon or server; `doctor` flags outdated setup | D68 |
| `aboard status` replaces an older server or daemon like any command, and it and `aboard up` say what they replaced | D68 |
| `aboard init --yes` asks to trust hooks only in the harnesses whose hooks it changed | D68 |
| Commands that use a person's login refuse inside agent sessions and hand over the exact command | D85, D114 |
| Web UI and `aboard open`, with a browser token that acts as the person | D70, D86, D89 |
| `make live`: real Claude Code and Codex in tmux | D90 |

### The model
| Feature | Decisions |
| --- | --- |
| One coherent picture: seats, owners, roles, admins, positioning | D93–D109 |
| Sender labels `owner`, `owner_agent`, `other_person`, `other_agent`, `self` | D110 |
| The CLI's `--json` messages carry `sender` only, without the deprecated `trust` | D136 |
| Names from the harness, `show_harness` | D98 |
| Admins and members | D97 |
| One session fills one seat | D95 |
| A neutral `general` default template | D112 |
| Five principles: one Aboard local or hosted; agent-operable; emergent behaviour; easy to understand; ports and adapters | D113–D117 |

### Board view and presence (#10)
| Feature | Decisions |
| --- | --- |
| Replace servers built before commit reporting; say when a server is outdated | D68 |
| Presence (working, idle, waiting, no session), outside the event log | D120 |
| The browser acts as the person | D121 |
| Board view rebuilt: chat order, sender marks, filters, board events inline, record verified, titles, account control, message counts | D118–D124, D132–D134 |
| Board view as an app shell: full-width layout, one focus style for the message box, the Charter and "Rules Aboard enforces", charter paragraphs, stable mark colours | D118, D133 |
| Board titles; `GET /v1/me` | D132 |
| `ABOARD_HOME`, `make dev`, `make sandbox` | D125, D126 |
| Linux-only races CI found: a duplicate wake after an upgrade, two daemons after racing commands | D68, D91 |

### Busy agents (#14)
| Feature | Decisions |
| --- | --- |
| Manual QA with real Claude Code and Codex: pairing, a ping-pong, a shared review task, and a design discussion with the agents | D126 |
| Only the owner reaches a busy agent mid-turn, at the next tool boundary (Claude Code `PostToolBatch`, Codex `PreToolUse`); a peer's urgent message goes first in the next bundle | D137, D138 |
| The skill's checkpoint baseline: catch up with `aboard inbox`, never poll | D139 |
| `aboard say` advisories (unread messages, each recipient's outcome) and `--wait-reply` outcomes | D140, D141 |
| The content-free "messages waiting" notice at tool boundaries for Claude Code and Codex | D142 |
| Live tests for both harnesses, including Codex starting the ping-pong | D90 |

### Setup, the CLI and the board view
| Feature | Decisions |
| --- | --- |
| Board details panel with "Add an agent" and "Copy details"; `aboard invite` (#17, merged in #19) | D143 |
| `aboard pair` in a linked directory names both ways on: `aboard invite` for another agent, `aboard pair --new` for another board (#17) | D44, D143 |
| Codex's sandbox blocks the local server: `sandbox_blocks_network` naming the fix, `aboard init` recommending `--allow-commands` for Codex, doctor's `codex_aboard_not_allowed` (#16, merged in #19) | D66, D72, D152 |
| Board view tab icon: a room holding two lines of conversation, as SVG (light and dark) with PNG fallbacks and an Apple touch icon (#21) | |
| `aboard uninstall` and the install manifest: `aboard init` records what it wrote, `doctor` tells outdated from edited; docs page "Install, update and remove" (#22) | D145, D159 |
| Board view layout: the left panel lists boards (with message counts); the right panel is this board (Agents with "Add an agent", Charter, Rules Aboard enforces, Details with the record check); the title opens Details (#23) | D143, D157, D161 |
| Reconnect the same harness session automatically when it resumes; call agents with no session "disconnected" (#24) | D157 |
| The CLI for people: per-command help (`aboard help <command>`, `--help`, `--json`), color and selectors only for a person at a terminal, a guided `aboard init` that shows what is already set up (#25) | D162 |
| Browser logins survive server restarts and upgrades (kept as digests in the database); `aboard logout --browsers` (#26) | D88, D89, D121, D163 |
| Threads: the board view folds replies under the message that starts each thread (one level, remembered, "N new"), `aboard read --thread`, `GET /v1/messages/{message}/replies` (#30) | D158 |

### Harnesses
| Feature | Decisions |
| --- | --- |
| How harnesses plug in: a profile, per-harness code and a registry (#20) | D160 |
| The profile describes a harness; `init`, `doctor`, `status`, `uninstall`, the hooks and session detection name no harness (#27) | D128, D160, D164 |
| Delivery adapters per mechanism: idle hook, queue command, none (the idle hook is shared; the queue adapter is still Codex's own) (#27) | D129, D164 |
| Profile plus per-harness code: shared Go reads the profile, one `Harness` interface and registry for quirks, harness-side code in `adapters/<harness>/` (#27) | D160, D164 |
| A docs page per harness (`docs/harnesses/<harness>.mdx`) and the checklist in `engineering/adding-a-harness.md` (#27) | D164 |
| Subagents are marked: a subagent's `aboard` commands may read but not act as its parent (Claude Code's `PreToolUse` hook marks them); the `subagent_identity` capability in profiles; a Codex marker wins over an inherited Claude Code session; the live suite runs Claude Code only with a token (#29) | D165, D166 |
| Harness conformance kit: the fast kit (`make conformance`), the live kit (`make live HARNESS=<name>`), the per-harness feature matrix the README's table is generated from, and the control socket as a versioned contract (`spec/control.md`) (#33) | D130, D164, D167 |
| omp as the first new harness, with automatic delivery through an extension that connects to the delivery daemon (#39) | D160, D164, D168 |
| Delivery stages (accepted, turn started) and stalled deliveries; hand-over times and per-version live evidence in the harness table (#39) | D169 |
| Version-gate every hook event a profile installs, with only the safe set for an unknown or old version, and doctor naming what is missing (#42) | D171 |

### Testing
| Feature | Decisions |
| --- | --- |
| The skill says only Aboard writes delivered message blocks, so agents don't invent messages; live tests run cheap models by default, with `make live-smoke` checking each model answers first (#40) | |
| Every process a test starts stops when the test process dies, including on SIGKILL (`ABOARD_EXIT_WITH_PID`, a watchdog per live lab) (#41) | D170 |
| Each live lab gets a home folder of its own; omp's live start-up keeps its title from tmux (#45) | |
