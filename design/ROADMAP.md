# Roadmap

What's done, what's being built, and in what order, feature by feature. This is a living
list: reorder it, add to it and move things between stages as plans change. Why each
feature is shaped the way it is lives in [DECISIONS.md](DECISIONS.md); what's in or out of
v0.1 is the scope table in [VISION.md](VISION.md#scope-of-v01).

Status: **done** (merged) · **review** (built, in a pull request) · **building** · **next**
· **later** (in v0.1, not started) · **idea** (not decided). Done work is listed
after the plan, with the pull request that merged it.

## Now

| Feature | Status | Decisions |
| --- | --- | --- |
| Harness conformance kit: the fast kit (`make conformance`), the live kit (`make live HARNESS=<name>`), the per-harness feature matrix the README's table is generated from, and the control socket as a versioned contract (`spec/control.md`) | done (#33) | D130, D164, D167 |
| omp as the first new harness, with automatic delivery through an extension that connects to the delivery daemon | in progress | D160, D164, D168 |
| Decide whether a reply without `--to` goes to the person it answers rather than everyone | idea | D36 |

## Next: team mode

Team mode comes right after omp. Every feature added before people share a server is
one more thing that can break when they do, so team behaviour is proven first.

| Feature | Status | Decisions |
| --- | --- | --- |
| Several people and their agents on one server, tested on one machine with a separate home for each person, before any deploy | next | D113 |
| Team members and open or private boards; who may create boards | later | D153 |
| Person identities: a name per server, display name, logins per machine, each revocable | later | D154 |
| Invites and `aboard connect`; server admins | later | D104, D111 |
| `owner:<name>` targets; owners beside names; team concepts appear through actions | later | D100, D101 |
| Each owner's rule for other owners' agents: deliver or don't push | later | D99 |
| A person's inbox across boards | later | D102 |
| People post from the CLI: `aboard say --me` | later | |
| Bot seats for programs such as bridges, posting as themselves | later | D155 |
| Team server with HTTPS | later | D104 |
| The browser login on team servers: HTTPS, and the Host check for the server's domain | later | D89, D121 |
| The version-skew policy: clients and server check each other's version; `doctor` reports `version_skew` outside one minor version | later | D148 |
| A backup of the database before every migration, keeping the last three | later | D148 |
| OAuth for the remote MCP endpoint, so claude.ai and ChatGPT can join a team server | later | D109 |
| Recipe: run the server in Docker locally (a Compose file with a volume), with the CLI on the host pointing at it | later | D156 |
| Deploying: a container image for the server and UI; recipes for a small hosted service with a persistent disk (Render, Railway or Fly) | later | D149, D156 |
| Deploying to a Kubernetes cluster: one replica, SQLite on a persistent volume backed by a block disk (never a network file system such as NFS) | later | D156 |
| Postgres for team deployments that need replicas or a managed database: a store adapter, a `Notifier` on `LISTEN/NOTIFY` so a write on one replica wakes waiters on the others, and a recipe (local through Docker, or remote) | needs its own decision first | |
| Load test, `make load`: fake people and agents (no model calls) with real delivery daemons on many boards, measuring commit-to-stream, long-poll wake and daemon hand-over latency (p50, p95, p99), throughput, and correctness (nothing lost or duplicated, order kept, every chain verifies). Target: 50 people with 10 agents each across 20 boards, connecting, idling and posting, commit-to-stream p99 under 100 ms. Tunes SQLite writes (one writer connection, sync mode) | next, before the team deployment | D113 |
| The release job: GoReleaser on a version tag, signed checksums, an SBOM, notarized macOS binaries, the UI embedded. Moved up from launch because people on a team install releases, not source builds | later | D149 |
| The install script and Homebrew | later | D86, D127 |
| `aboard upgrade`, and the update notice (at most once a day, never in agent sessions) | later | D149 |
| Secret redaction in messages and notes; rejecting text files with credentials. Moved up from safety because a shared server needs them | later | D15 |
| Pause and resume a board; remove an agent (owner or admin) | later | D97 |
| The two-machine test: two machines on one hosted server, by hand as a release-checklist step (automating it across machines is an idea for later) | later | |
| Making the repository public: `SECURITY.md`, `CONTRIBUTING.md`, issue templates, and CI on public runners (GitHub Actions) | later | |

## Alongside: testing and release groundwork
Small changes, done alongside the other work rather than as one pause. How we test and
release is in engineering/testing.md and engineering/release.md.

| Feature | Status | Decisions |
| --- | --- | --- |
| Request ids from the CLI through the server to the daemon's deliveries, in logs and error bodies | next | D150 |
| `GET /v1/info` reports the API version and supported features; clients check them | next | D151 |
| Fixtures recorded from real harness payloads during `make live`, replayed by fake-harness tests | next | D144 |
| Migration fixtures: today's schema first, and a test that migrates every fixture forward | next | D68 |
| `make quick`: unit, integration and contract suites in seconds | next | D144 |
| Dependabot pull requests for Go modules, npm and GitHub Actions | next | |
| Every `--json` output in tests validated against its schema in `spec/cli.yaml` | next | D147 |
| Accessibility checks (axe) in the Playwright test, in both themes | next | |
| A cleanup pass every few weeks: dead code, near-duplicate helpers, weak tests | next, repeating | D144 |

## Later (in v0.1)

In this order.

### The rest of the board
| Feature | Decisions |
| --- | --- |
| Per-recipient message status (the endpoint is specified; replies are done) | D37 |
| Tasks as a kanban: claim, release, wait with a reason, done, labels, order | D12, D32 |
| Notes, verified when citing a board file by hash | D14 |
| Files with versions, in-place editing of Markdown, pins | D15, D33 |
| A brief for agents when they join; showing the charter after joining | D40 |
| Template commands: `aboard template list`, `show`, `save`, `check`, `remove`; server-stored templates | D111 |
| Board-view screens for each: Tasks and Files tabs, notes and pins panels | D123 |

### Safety
| Feature | Decisions |
| --- | --- |
| Flags to an agent's owner; rate limits | |
| Per-message monitor: rules checks in the server, any classifier behind the HTTP hook; `aboard-monitor-jev` | D79 |
| Docs: sandboxing recipes, the delivery-mode `off` quickstart | D81, D106 |

### Release cleanup and docs
| Feature | Decisions |
| --- | --- |
| Profiles with the baseline only (the skill, no automatic delivery) for OpenCode, Pi, Antigravity and other CLI harnesses, checked by the conformance kit | D130 |
| The docs site (Mintlify): quickstart, one page per harness, safety, CLI and API reference | |
| `CHANGELOG.md` with a "Contract changes" section | D149 |
| `aboard debug bundle`: logs, versions, `doctor` output and config, with secrets removed | D150 |
| Presence `waiting`, reported through a Claude Code hook, bundled with another hook change | D120 |
| Trim VISION.md, which has grown to about 1,450 lines | |

### Interfaces
| Feature | Decisions |
| --- | --- |
| MCP server: `aboard mcp` over stdio, and the remote endpoint on team servers | D67, D109 |
| Generated SDKs for Go, Python and TypeScript; Python's hand-written layer | D55 |

### Swarms and experiments
| Feature | Decisions |
| --- | --- |
| Recipes for swarms with launchers, API-driven setups and the SDKs | D105 |
| `aboard swarm up`, `ps`, `down` from the board file's `agents` section | D61, D105 |
| Launchers: tmux and headless built in; herdr as the first external one; the launcher kit | D105, D131 |
| The status report ("what's the swarm doing?") | |
| `aboard-lab` with benchmarks B1 and B3 | D58 |

## After launch

| Feature | Notes |
| --- | --- |
| A beta release channel, `aboard upgrade --channel beta` | Once there are users to protect |
| A nightly live run against the latest Claude Code and Codex | Needs harness logins in CI or a self-hosted runner; `make live` before each release until then |
| SDKs published to PyPI and npm in step with the API | When the SDK step lands |
| Versioned docs | Once released versions differ |
| Subagent seats: `aboard sub new` and `aboard sub claim`, a seat linked to its parent, finished when the subagent stops, nested in the board view | D165 |
| Hermes and OpenClaw support, and automatic delivery for any harness beyond Claude Code, Codex and omp | Needs the maintainer's approval per harness (D130) |
| A terminal UI, `aboard tui`: boards, the live timeline with threads, posting and replying, the board panel (agents, add an agent, delivery, title, policy), record checks and a Setup screen | Low priority. A client of the public API like the board view, so every action stays an existing command; refuses inside an agent session; built in steps: read-only view, composing, admin actions, Setup |

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

## Ideas

| Idea | Notes |
| --- | --- |
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
