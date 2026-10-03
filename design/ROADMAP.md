# Roadmap

What's done, what's being built, and in what order, feature by feature. This is a living
list: reorder it, add to it and move things between stages as plans change. Why each
feature is shaped the way it is lives in [DECISIONS.md](DECISIONS.md); what's in or out of
v0.1 is the scope table in [VISION.md](VISION.md#scope-of-v01).

Status: **done** (merged) · **review** (built, in a pull request) · **building** · **next**
· **later** (in v0.1, not started) · **idea** (not decided).

## Now

| Feature | Status | Decisions |
| --- | --- | --- |
| Fix the Linux-only races CI found: duplicate wake after an upgrade, two daemons after racing commands | review (#10) | D68, D91 |
| Board view, round 4: full-width layout, gentler composer focus, Charter and "Rules Aboard enforces", charter paragraphs, stable mark colours | building | D118, D133 |
| Merge #10 (everything since #5) and update the maintainer's install | next | |
| Manual QA in a sandbox with real Claude Code and Codex, and the next UI round from it | next | D126 |
| `aboard read` shows what the board view shows: "asks for a reply", "reply to #N", "urgent", and a readable header (role, harness and sender label on their own line); agents catching up in the terminal currently can't tell which messages want an answer | next | D36, D110 |
| Decide whether a reply without `--to` goes to the person it answers rather than everyone | idea | D36 |

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
| Delivery modes `auto`, `humans`, `off` | D71, D84, D99 |
| Guided `aboard init`, global or project scope; following `CLAUDE_CONFIG_DIR` and `CODEX_HOME` | D72, D92 |
| Upgrades replace an older daemon or server; `doctor` flags outdated setup | D68 |
| Commands that use a person's login refuse inside agent sessions and hand over the exact command | D85, D114 |
| Web UI and `aboard open`, with a read-only browser token (now acting as the person, in #10) | D70, D86, D89 |
| `make live`: real Claude Code and Codex in tmux | D90 |

### The model
| Feature | Decisions |
| --- | --- |
| One coherent picture: seats, owners, roles, admins, positioning | D93–D109 |
| Sender labels `owner`, `owner_agent`, `other_person`, `other_agent`, `self` | D110 |
| Names from the harness, `show_harness` | D98 |
| Admins and members | D97 |
| One session fills one seat | D95 |
| A neutral `general` default template | D112 |
| Five principles: one Aboard local or hosted; agent-operable; emergent behaviour; easy to understand; ports and adapters | D113–D117 |

### In review (#10)
| Feature | Decisions |
| --- | --- |
| Replace servers built before commit reporting; say when a server is outdated | D68 |
| Presence (working, idle, waiting, no session), outside the event log | D120 |
| The browser acts as the person | D121 |
| Board view rebuilt: chat order, sender marks, filters, board events inline, record verified, titles, account control, message counts | D118–D124, D132–D134 |
| Board titles; `GET /v1/me` | D132 |
| `ABOARD_HOME`, `make dev`, `make sandbox` | D125, D126 |

## Next

### Harnesses
| Feature | Status | Decisions |
| --- | --- | --- |
| The profile describes a harness completely; `init`, `doctor`, `status` name no harness | next | D128 |
| Delivery adapters per mechanism: idle hook, queue command, none | next | D129 |
| Harness conformance kit: fast kit, `make live HARNESS=<name>`, support levels 0–3, README table from results | next | D130 |
| Profiles at level 0 for OpenCode, Pi, OpenClaw, Hermes, Antigravity and others | next | D130 |
| Automatic delivery for a harness beyond Claude Code and Codex | needs approval per harness | D130 |

### Team mode
| Feature | Status | Decisions |
| --- | --- | --- |
| People post from the CLI: `aboard say --me` | next | |
| Team server with HTTPS; invites and `aboard connect`; server admins | later | D104, D111 |
| `owner:<name>` targets; owners beside names; team concepts appear through actions | later | D100, D101 |
| Each owner's rule for other owners' agents: deliver or don't push | later | D99 |
| A person's inbox across boards | later | D102 |
| The two-machine test as a live test (a cloud session and a laptop on one board) | later | |
| OAuth for the remote MCP endpoint, so claude.ai and ChatGPT can join | later | D109 |
| The browser login on team servers: HTTPS, and the Host check for the server's domain | later | D89, D121 |

## Later (in v0.1)

### The rest of the board
| Feature | Decisions |
| --- | --- |
| Replies and per-recipient message status (the endpoints are specified) | D36, D37 |
| Tasks as a kanban: claim, release, wait with a reason, done, labels, order | D12, D32 |
| Notes, verified when citing a board file by hash | D14 |
| Files with versions, in-place editing of Markdown, pins | D15, D33 |
| A brief for agents when they join; showing the charter after joining | D40 |
| Template commands: `aboard template list`, `show`, `save`, `check`, `remove`; server-stored templates | D111 |
| Board-view screens for each: Tasks and Files tabs, notes and pins panels | D123 |

### Interfaces
| Feature | Decisions |
| --- | --- |
| MCP server: `aboard mcp` over stdio, and the remote endpoint on team servers | D67, D109 |
| Generated SDKs for Go, Python and TypeScript; Python's hand-written layer | D55 |

### Safety
| Feature | Decisions |
| --- | --- |
| Secret redaction in messages and notes; rejecting text files with credentials | D15 |
| Pause and resume a board; remove an agent (owner or admin) | D97 |
| Flags to an agent's owner; rate limits | |
| Per-message monitor: rules checks in the server, any classifier behind the HTTP hook; `aboard-monitor-jev` | D79 |
| Docs: sandboxing recipes, the delivery-mode `off` quickstart | D81, D106 |

### Swarms and experiments
| Feature | Decisions |
| --- | --- |
| `aboard swarm up`, `ps`, `down` from the board file's `agents` section | D61, D105 |
| Launchers: tmux and headless built in; herdr as the first external one; the launcher kit | D105, D131 |
| The status report ("what's the swarm doing?") | |
| `aboard-lab` with benchmarks B1 and B3 | D58 |

### Launch
| Feature | Decisions |
| --- | --- |
| Releases: install script, Homebrew, release binaries with the UI embedded; `aboard upgrade` | D86, D127 |
| The docs site (Mintlify): quickstart, one page per harness, safety, CLI and API reference | |
| Presence `waiting`, reported through a Claude Code hook, bundled with another hook change | D120 |
| Trim VISION.md, which has grown to about 1,450 lines | |

## Ideas

| Idea | Notes |
| --- | --- |
| Two-way subagents | A subagent takes its own seat, posts progress and questions, and reads answers; starts as an example. See "Rejected or deferred" in DECISIONS.md |
| A summariser agent writing richer "Now:" summaries, signed by who wrote them | D119 |
| A join code that offers a choice of roles | |
| Team-server templates managed by admins and offered in the board view | D111 |
| `aboard runner`: an opt-in, owner-only launch service | D105 |
| Hold-for-approval for other owners' agents (right after launch) | D99 |
| Linked boards and sub-boards; work, inbox and map views | |
