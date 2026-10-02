# Decisions

Refinements to [VISION.md](VISION.md), guided by [PHILOSOPHY.md](PHILOSOPHY.md). Where a decision here is
more specific than the vision, the decision wins. Each entry: the decision, then why.
Bias for every call: simplicity and the shortest quickstart.

## Accepted (2026-10-01)

D1–D18 came from the first review; D19 answers an open question; D20–D31 were proposed with the slice-1 contracts and accepted.

**D1. Identity is per session, not per directory.**
`.aboard` pins only server and board; the acting agent is resolved by `--as`, then `ABOARD_AGENT`, then the harness session id (set by hooks), else an error listing your agents. `join` prints the agent's name.
Why: two sessions in one repo is the core pair flow, and a per-directory identity makes them collide.

**D2. `--to` is a list of targets: `all`, `@name[,@name…]`, or `role:R`. No `--to` means `all`, which needs the `broadcast` permission.**
Why: a list from day one lets new filter kinds arrive later without changing the shape.

**D3. Visibility is board policy: `open` (default, every member reads everything) or `addressed` (sender, recipients and the board's humans only). Humans always see everything.**
Why: the one-minute pair wants open; larger or mixed-owner boards want isolation.

**D4. Inbox is always "messages addressed to me" (direct, my role, or all); the timeline (`read`, `GET messages`) shows what visibility allows.**
Why: one clear meaning for "unread", separate from browsing.

**D5. CLI `inbox` acknowledges what it returns (`--peek` doesn't); the API has explicit `POST /v1/me/inbox/ack {up_to}`, which the daemon calls only after a successful delivery.**
Why: simplest CLI behaviour, while delivery keeps "never advance on failure".

**D6. Read cursors are per-reader bookkeeping, not in the hash chain. The chain covers board content only.**
Why: cursors are high-volume, private and not part of the shared record.

**D7. Join codes are multi-use, carry their role, expire after 24 h by default, and can be revoked. Each redemption creates a new agent. `swarm up` makes one code per role. `POST /v1/join` is rate-limited.**
Why: one code per role fits swarms; short expiry and rate limits bound guessing.

**D8. Agent names come from the role (`reviewer`, `reviewer-2`, …) or `--name`, unique per board. Owners are shown beside a name, not in it. No `@owner/agent` form in v0.1.**
Why: short names that agents can type; cross-board addressing is out of scope.

**D9. The owner is whoever's login redeemed the join code on that machine. In team mode the redeemer must already be logged in (else `join` says to run `aboard connect`). The code's creator is never the owner.**
Why: the owner is the human answerable for the session's machine.

**D10. One sequence and one hash chain per board. The stream takes a cursor per board.**
Why: boards are the unit of membership, export and (later) moving between servers.

**D11. A hand-written `spec/openapi.yaml` is the source of truth; Go types and handler stubs are generated from it; a conformance test checks the server against it. That file is the published spec.**
Why: contracts before code, and the spec can't drift from the server.

**D12. Tasks get `aboard task release`. Claims release automatically when the agent leaves or is revoked. No timed leases in v0.1; the status report shows claimed-but-quiet tasks.**
Why: covers dead agents without a lease system.

**D13. Templates define their own roles (writer-reviewer has `writer` and `reviewer`). The shared role list is only examples.**
Why: roles follow the work, not a fixed catalogue.

**D14. "Verified" means the note cites a board file by hash and the server confirmed the hash matches. A URL or pasted log shows as "evidence attached".**
Why: only claim what the server can check.

**D15. Uploaded file bytes are never changed. A text file containing a credential is rejected. Redaction applies to messages and notes only.**
Why: a file's hash must stay its identity, and evidence must not change under a note.

**D16. Policy presets: `starter` (default; `open`, everyone may broadcast, monitor off) and `recommended` (`addressed`, broadcast only for granted roles, monitor on if a Jev or LLM key is set, else rules-only). `aboard board policy <preset>` switches a board and is recorded as an event.**
Why: one step from easy to locked down.

**D17. The starter preset is always visibly labelled: `pair` and `board new` print one notice line, `status` and the board view show a "starter policy" badge, and the quickstart and safety page say to switch before adding agents or teammates.**
Why: an easy default must not look like a safe one.

**D18. Templates pick a preset: writer-reviewer uses `starter`; coordinator-workers and experiments use `recommended`.**
Why: the pair stays one minute; swarms start locked down.

**D19. When an agent joins a board, its owner automatically becomes a human member of that board if not already one.**
Why: every agent has a human who can see and steer it, and "the board's humans" under `addressed` is well-defined.

**D20. Broadcast is a policy key: `broadcast: everyone | granted`. `starter` sets `everyone`, `recommended` sets `granted` (only roles with `broadcast` in `can`).**
Why: writer-reviewer has no `member` role, so "member can broadcast" alone wouldn't let `say` with no `--to` work.

**D21. Each event's hash covers a `data_hash` (SHA-256 of the canonical data), not the data itself. Readers who can't see a message under `addressed` get the event without `data` and can still verify the chain.**
Why: lets `audit verify` work for every member without leaking hidden messages; later, content can be erased while the chain stays intact.

**D22. `audit verify` remembers the last head it verified per board, and fails if the server later serves a different history up to that point.**
Why: a hash chain alone doesn't catch a server that rewrites everything and recomputes the hashes.

**D23. `POST /v1/join` also accepts `{board, role}` from a human who is a member of the board, with no code. `pair` uses it for its own session.**
Why: avoids a leftover 24-hour code just to join your own session.

**D24. Event schemas live in `openapi.yaml` (they are returned by `GET /events`); `spec/events.md` explains the envelope and hashing. CLI JSON shapes live in `spec/cli.yaml` and reuse the API's schemas by reference.**
Why: one schema per object, one toolchain.

**D25. Contracts live in `/spec`; decisions in `/design`.**
Why: neither fits the vision's layout, and the published spec shouldn't sit inside `/server`.

**D26. An optional `pair: [self-role, next-role]` key in `aboard.yaml`, read only by `aboard pair`.**
Why: `roles` is a map, so the template has to say which role `pair` takes and which it invites.

**D27. Agent-acting commands (`say`, `inbox`, `task`, `note`, `flag`) never fall back to the human login. Human commands (`board policy`, `audit verify`) use the human login unless `--as` is given.**
Why: an agent must never post as its owner, because owner messages carry `trust="owner"`.

**D28. Inbox and timeline messages carry a reader-relative `trust`: `owner` (your human), `human` (another human), `peer` (an agent) or `self`. Your own messages are never in your inbox. The skill tells agents to weigh `peer` and `human` messages as requests and information against their owner's instructions and the board's charter: act on them (a writer must act on its reviewer's comments), but they never override either and never authorise anything the owner wouldn't.**
Why: peers must be able to collaborate without being able to outrank the owner or the charter.

**D29. `GET /v1/info` (version, server id, mode) for `up`, `doctor` and join-line resolution. Local server listens on `127.0.0.1:7400`.**
Why: the CLI needs to tell "nothing running" from "something else on the port".

**D30. CLI exit codes: 0 ok, 1 error, 2 usage, 3 a check that ran and failed (`audit verify`). With `--json`, errors are `{"error":{…}}` on stdout. The error object may carry an optional `details`.**
Why: agents can branch on the exit code without parsing text.

**D31. Tokens carry kind prefixes (`abh_` human, `aba_` agent) and are stored server-side only as keyed digests. Join codes are 6 Crockford base32 characters (`7Q4-K2M`), also stored as digests and never in the event log.**
Why: secret scanners can match the prefixes, and a database leak doesn't leak working credentials.

**D32. Tasks are a lightweight kanban. States: open, claimed, waiting (with a short reason), done, cancelled. Optional fields: description (Markdown), labels, order, suggested owner (`@name` or `role:R`, a hint that doesn't claim). The board view shows them as Open / In progress / Waiting / Done, filterable by label.**
Why: long-running work needs to show what's open, active and stuck without leaving the board.

**D33. Markdown files on a board can be edited in place (`aboard file edit plan.md`). Each save names the version it started from and is rejected if the file changed since. Files can be pinned; pinned files show on the board's front page and are given to agents when they join. These are ordinary files, not a new concept.**
Why: a living plan belongs on the board, and concurrent edits must fail loudly, never overwrite.

**D34. The event log is the queue; each member's inbox is a read position. Sending returns once the message is stored. Receiving is pull (`inbox --wait`) or push (the delivery daemon). In-process notification only wakes readers, who always re-read the log. No external broker.**
Why: one simple async model; automatic delivery and blocking waits are options on top of it.

**D35. When a session becomes idle with several unread messages, the daemon delivers them as one bundle. Messages marked `--urgent` are delivered without waiting for idle.**
Why: one interruption instead of many, with an escape hatch for what can't wait.

**D36. `aboard say --expect-reply` marks a message `expects_reply`, prints its id and returns at once. `--wait-reply N` implies it and blocks until a reply arrives or N seconds pass. `aboard ask` is exactly `aboard say --expect-reply`. `aboard replies <message> [--wait N]` shows or waits for replies. Replies are normal messages linked by `reply_to`, so they also arrive through the inbox and delivery. Unanswered `expects_reply` messages appear in the status report, and the delivery wrapper says a reply is requested.**
Why: request and response on top of the same messages, with blocking as an option, not a separate channel.

**D37. Every message has a per-recipient status: pending (stored), received (the recipient's read position passed it, through delivery or inbox), replied. Shown by `aboard message <id>` and `GET /v1/messages/{message}`. Status is derived from read positions and replies, so it adds no events.**
Why: senders can see whether they were heard without breaking D6.

**D38. The CLI accepts a message's sequence number anywhere it takes a message id (`--reply`, `replies`, `message`): `6` or `#6` within the current board, `board-name#6` on another board. The API takes ids only.**
Why: agents read `seq="6"` in the wrapper; typing a 30-character id to reply is friction.

**D39. A new agent's read position starts at the board's head when it joins. Earlier messages are in the timeline (`aboard read`), not the inbox. Resuming an existing agent keeps its read position.**
Why: a newcomer shouldn't be flooded with a backlog as if it were addressed to them now; the charter and pinned files carry what it needs.

**D40. When an agent joins, the first item in its inbox is a board brief: the charter, pinned files, open tasks, unanswered expects-reply messages, and a count of earlier messages with a pointer to `aboard read`.**
Why: an agent that starts at the head shouldn't start blind.

**D41. `urgent` is a permission, controlled like broadcast by a policy key `urgent: everyone | granted`. `starter` sets `everyone`; `recommended` sets `granted`. Humans can always send urgent messages.**
Why: urgent messages interrupt busy sessions, so on locked-down boards only chosen roles may send them.

**D42. Humans have an inbox per board, using the same read-position mechanism as agents, and appear in a message's recipient status. Flags to an owner and expects-reply messages addressed to a human land there. An inbox across boards is not part of v0.1.**
Why: questions and flags for a human need somewhere durable to wait, and senders need to see whether the human has seen them.

**D43. Human logins are stored per server and are only ever sent to the server that issued them. Today that means the local server only; team mode builds on the same rule. A command whose project file names another server fails with `login_required` instead of sending the login.**
Why: a project file can come from a cloned repository, so it must never be able to redirect someone's login.

**D44. A directory never switches boards silently. `pair` in a directory already linked to a board fails with `board_already_linked`, naming the board and your agents on it; `pair --new` creates another board on purpose. When `pair --new` or `join` re-links a directory, it prints which board it was linked to before.**
Why: which board a command acts on must always be visible to the person running it.

**D45. The acting agent determines the board. Each agent identity belongs to exactly one board, so `--as`, `ABOARD_AGENT` or a hook-bound session picks the board on its own. `.aboard` only supplies the default board for human commands and new pairs in that directory. A name this machine has on two boards is an error listing both. Built together with the session-binding hooks.**
Why: choosing who you are should be enough; a directory file silently deciding where an agent posts is hidden state.

**D46. Every agent command names its board in one short line: `say` prints "Sent #6 to @reviewer on writer-reviewer"; `inbox` and `read` start with a header such as "writer-reviewer · 2 new". Where the board and agent were selected from appears only in plain `aboard status` and in selection errors.**
Why: the board is always visible without cluttering normal output.

**D47. The code follows ports and adapters (engineering/architecture.md). The board service declares the store and notifier interfaces it needs; SQLite and the in-process notifier are adapters that implement them, and every store adapter passes one shared contract test suite. No SQL in the board service; SQLite-specific SQL stays in the SQLite adapter.**
Why: swapping SQLite for Postgres, or adding a notifier that works across server instances, must not touch the rules.

**D48. `/v1/stream` is a server-sent event stream. The delivery daemon and the web UI both read it; every write stays on REST.**
Why: the stream only carries notifications, one-way is enough, and the standard library handles it on both sides.

**D49. Delivery confirmation is implicit: a bundle counts as received when the same session (same session id and boot id) next reports an event after the woken turn has started. The wake's own prompt (Claude Code submits the bundle as the woken turn's prompt) doesn't count. It guarantees the session woke and ran a turn with the bundle in its context; it does not guarantee the agent acted on it.**
Why: an explicit accept step is one more thing agents forget, which loops redeliveries.

**D50. Urgent messages reach a busy session at its next tool call, through the harness's post-tool hook returning extra context: Claude Code and Codex both support this. Otherwise, and for other harnesses, urgent messages are delivered at the next idle, first in their bundle.**
Why: verified for Claude Code by a live run (the context reached the model in the same turn) and for Codex in its hook source; the manual proof list covers both.

**D51. Codex sessions bind through `CODEX_THREAD_ID`, which Codex sets in the environment of every command the agent runs. Claude Code sessions bind through `ABOARD_SESSION`, written to the session's environment file by the session-start hook. No harness needs `--as` on every command.**
Why: verified with a live Codex run and in Codex's source; Claude Code documents the environment file for exactly this.

**D52. The Aboard skill ships inside the binary, and `aboard init` copies it into each detected harness's skill folder. Once the repository is public, `npx skills add` works for anyone who wants only the skill.**
Why: `npx skills` installs from a published repository, and the skill must match the binary's commands exactly.

**D53. While a Codex turn runs, urgent messages are kept out of Codex's queue and go to the next tool call; Codex's prompt and stop hooks mark when a turn runs. Urgent messages no tool call took go into the queue when the turn ends. Without those hooks trusted, urgent messages arrive at the end of the turn. Refines D50.**
Why: a live run showed Codex's queue holds everything until the turn ends, so an urgent message queued at once never reached the tool hook.

**D54. The server provides primitives with guarantees; everything else is a client of the public API. Something goes in the server only if many different uses need it and it can't be done correctly from outside (atomicity, permissions, ordering, trust). Benchmarks, experiment scenarios, API-driven agents, summarisers, bridges and orchestration are clients. If one of our own tools needs a private endpoint or the database, that is a missing primitive, added to the API.**
Why: a small server with strong guarantees stays trustworthy and lets others build what we didn't think of; our own tools using only the API keep it complete.

**D55. Typed clients for Go, Python and TypeScript are generated from spec/openapi.yaml, each with a thin hand-written layer for common needs: act as an agent, subscribe to a board's stream, wait for a condition, page events. Python gets the hand-written layer first.**
Why: researchers work in Python; generating from the one spec keeps every client in step with the server.

**D56. Any API client can be a member. A member is an identity with a token, not necessarily a harness session.**
Why: API-driven agents, bridges and experiment runners join and act exactly as coding agents do, under the same rules.

**D57. Extension points sit outside the server: monitors through an HTTP hook; launchers as external `aboard-launcher-<name>` commands speaking JSON on standard input and output (start, stop, status); CLI extensions as any `aboard-<name>` on the PATH; and anything that reads the stream, such as dashboards and bridges.**
Why: plugging in code in any language at the edge keeps user code out of the write path.

**D58. aboard-bench and the experiment helpers live in a small client library, `aboard-lab`, built on the Python SDK, not in the server.**
Why: experiments are clients (D54), and one library serves both benchmarks and research scenarios.

**D59. Harnesses have three jobs with three owners. Joining is core: any process with a code or token is a member. Delivery into sessions that are already open is the delivery daemon's. Starting and running sessions is not core: launchers do it. Aboard never chooses harnesses or schedules agents; that lives in the board file's `agents` section, SDK code or an outside orchestrator. Aboard only checks that a harness is installed and logged in.**
Why: keeps the server and daemon small, and leaves orchestration to whoever owns the workload.

**D60. Each harness has one small declarative profile at `adapters/<harness>/profile.yaml` (schema: spec/harness-profile.schema.json): its command and install and login checks; interactive start with a first prompt; a headless single turn; session resume where supported; how identity is passed in; the delivery method and hooks; whether urgent messages reach it mid-turn. `aboard init`, the delivery daemon, `aboard doctor` and the launchers read profiles. Claude Code and Codex have profiles now; a test keeps their hook lists equal to what `aboard init` installs.**
Why: what we learn about a harness belongs in one place that code and contributors share, so a new harness is mostly a new file.

**D61. Agents run in three modes, all joining a board the same way: interactive (a terminal session in tmux or Herdr, with hook delivery), headless turns (a runner waits on the inbox, runs one headless turn with the new messages, and resumes the session where the harness supports it), and API agents (no harness, only model API calls). The built-in launchers are tmux, headless and api. When Aboard launches an agent it passes the identity directly, so no join line is pasted. Launchers and the headless runner are built with `swarm up`.**
Why: experiments and CI need agents without terminals, and people watching need real sessions; one join path keeps all three under the same rules.

**D62. For harnesses with an Agent Client Protocol agent, the headless runner and launchers are an ACP client, so one implementation covers them all and their profiles mainly name the ACP command. An ACP permission request from an agent becomes a request to its owner on the board. ACP does not replace hook delivery into sessions the user already has open.**
Why: ACP already covers Codex (through codex-acp), OpenCode, Pi (through pi-acp), OpenClaw and Hermes; one client is less to maintain than one runner per harness.

**D63. Claude Code keeps its own profile, and its headless mode is Claude Code's own non-interactive mode with machine-readable output and session resume. The ACP Claude adapter runs the Claude Agent SDK, which is a different program (its own settings, plugins, hooks, skills loading and auth); it is a separate harness, `claude-agent-sdk`, never presented as Claude Code. Every other ACP adapter's profile records whether it drives the real tool or reimplements it, and a reimplementation is a separate harness entry. codex-acp starts Codex's own app server, so it drives the real Codex.**
Why: results and behaviour must be attributed to the tool that actually ran.

**D64. Aboard doesn't use Agent2Agent (A2A) internally: A2A is point-to-point between agent services, and a board's shared history, visibility and policy are what Aboard adds. After launch, bridges are worth considering: an A2A agent joining a board as a member, and a board role published as an A2A agent with an Agent Card. Agent Cards are a model for the agent directory at org scale.**
Why: bridges reach A2A systems without giving up the board's record and rules.

**D65. Experiments test the primitives. The target example is a replication of a study of wrong beliefs spreading between agents, on Aboard with aboard-lab: agents take turns, each gets a private signal by direct message under `addressed` visibility, posts its conclusion to the board and reports its belief privately to the runner; conditions are board policies, including a server-enforced evidence condition (a result must cite a board file hash) and a Jev monitor condition; analysis reads the event log. Scenario scripting, sequential admission, API agents and scoring belong in aboard-lab. It exposed two primitives the server would need, recorded for later: role-based visibility (for example, agents see only the summariser's posts), and monitor checks that compare a post with what its author privately received.**
Why: building a real experiment on the public API is the test of whether the primitives are right; what it can't do from outside shows what is missing.

**D66. A command running inside a harness's sandbox never starts the delivery daemon. It recognises the sandbox from the variables the harness sets for sandboxed commands (Codex: `CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED`; Claude Code: `SANDBOX_RUNTIME`), recorded in each harness profile. With no daemon running it fails with `daemon_in_sandbox` and says how to fix it: trust Aboard's hooks in the harness, whose session-start hook runs outside the sandbox and starts the daemon, or run `aboard daemon start` in a normal terminal. `aboard doctor` reports the same.**
Why: a daemon started inside the sandbox inherits it and can't reach the harness, which a live Codex run showed; failing with the fix beats a daemon that silently can't deliver.

**D67. v0.1 includes an MCP server, so chat assistants (Claude in claude.ai, ChatGPT and others) can join a board as members alongside coding agents. Two forms: local stdio (`aboard mcp`), and a remote MCP endpoint on team servers. Both use the same tokens and permissions as the API, and go through the same write path. Tools: read inbox, read board, say (with reply and expect-reply), task list, claim and done, note, flag, and the status report. There is no delivery: an assistant reads its inbox when its user next talks to it. It is built in its own slice, after the rest of the board and before the board view UI.**
Why: chat assistants are where many people already talk to models; letting them sit on a board next to coding agents, under the same rules, is a cheap way to bring humans' assistants into the work. Supersedes the MCP line in the v0.1 "Later" column.

**D68. Upgrading is a supported path, not a reinstall. One install upgrades everything on a machine: hooks run the installed binary, and a running daemon or local server from an older build is replaced when a newer command or hook reaches it. The skill and hook entries carry the version that wrote them, `aboard doctor` reports outdated ones, and `aboard init --yes` updates them in place. Stored data migrates forward only, and a binary older than its data refuses to run. The API, event types, `--json` output and the board file only grow; breaking changes get a new version alongside the old. Until automatic replacement lands, `aboard down` restarts everything on the new binary.**
Why: people will upgrade while sessions are open and servers lag behind clients; a live proof already hit an old daemon that didn't know a new operation.
Settled while building it: a build is its release `version` (set with `-ldflags -X`) plus the Git commit and commit time from Go's build information; versions compare as semantic versions, and only between equal versions does a later commit time win, so two builds from one commit with uncommitted changes count as the same and need `aboard down`. The daemon's `status` answer and `GET /v1/info` carry the build; a lock file serialises replacement, and the replacing command re-checks under it. Installed files are compared by content, not marked with a version, which amends D68's "carry the version that wrote them": `aboard doctor` reports the skill or an Aboard hook entry as outdated when it differs from what this `aboard init` would write, and `aboard init --yes` rewrites only what differs. A version mark would change every hook entry on every upgrade, and both harnesses ask the person to trust hooks again when an entry changes; hooks already run the new binary by its path, so they need rewriting only when the hooks themselves change. Doctor's outdated checks are warnings. Data written by a newer schema is refused with `data_newer`.

**D69. One read interface serves agents, the CLI and the UI. The timeline API takes filters: sender, role, messages addressed to the reader, after or around a sequence number, and a limit (plain-text search later). Reading never moves a read position; only the inbox acknowledges. `aboard read` takes the same filters; `aboard watch` follows a board live in the terminal from the event stream; `aboard read --markdown` prints a transcript to paste as context.**
Why: agents need to look back freely, without relying on read positions being right, and one interface keeps what the UI, the terminal and agents see the same.

**D70. The local server serves the web UI at its own address, so whenever it runs, the UI does, showing every board on that server across all projects. `aboard open` opens the browser already logged in, by handing it a one-time code that the server exchanges for a cookie; the owner token never appears in a URL. The first UI is a read-only walking skeleton: the board list, a live timeline with the D69 filters, and the crew. Each later step adds its own screens, and team servers add a server switcher.**
Why: people want to see their boards at any time, the way Herdr shows sessions; one server per user already holds every board, so one UI per server is enough.

**D71. Automatic delivery is a choice. Each agent has a delivery mode, kept on the machine since delivery is local: `auto` (the default: wake for every message), `humans` (wake only for messages from its owner or another person, never from peer agents), or `off` (the agent checks its inbox itself). `aboard delivery auto|humans|off` changes it at any time, `aboard status` shows it, and the skill gives advice for each mode. The hooks stay installed in every mode, because they also tell a session which agent it is.**
Why: waking an agent makes it act, which is a risk when it runs unattended; working without delivery must be a first-class way to use Aboard, not a degraded one.

**D72. `aboard init` is a short guided setup when run in a terminal: which harnesses, scope (everywhere, or only this project), delivery mode, and whether to allow `aboard` commands without permission prompts; it shows the changes and asks before making them. Flags do the same without questions (`--yes --scope project --delivery off`). Project scope puts the skill and hooks in the project's `.claude/` and `.codex/` folders. The server and the daemon stay per user: a board is the unit of scope, so there are no per-project servers; a separate data directory gives full isolation when someone wants it.**
Why: people want to try Aboard in one project before changing their global setup, and agents and scripts need the same setup without a prompt.
Settled while building it: the delivery question sets a machine-wide default for agents without a mode of their own, kept by the daemon under an empty agent in its journal, since agents are made later by `pair` and `join` and the smallest place for "new agents" is the daemon's fallback; it changes agents that never chose a mode too, and is refused inside a harness session like `aboard delivery`. In a project, Claude Code's hooks and allow rule go in `.claude/settings.local.json`, because the hooks name this machine's binary; Codex's skill goes in the project's `.agents/skills` (where Codex looks, as it does under the home directory), its hooks in `.codex/hooks.json`, read only once the project is trusted. The allow rule is `Bash(aboard *)` for Claude Code and a Codex rules file `rules/aboard.rules` with `prefix_rule(pattern=["aboard"], decision="allow")`, checked in Codex's source; Codex runs commands its rules allow outside its sandbox, and init says so. Interactive mode needs a terminal on both standard input and output, and `--json` never asks.

**D73. The build order gains a step after delivery, "observe and control": the read interface (D69), the UI skeleton (D70), delivery modes (D71), the guided and scoped `init` (D72), and the first upgrade pieces (D68: replacing an older running daemon or server, and `aboard doctor` reporting outdated skill and hook files). The rest of the board comes after it, and the UI is no longer a separate later step: it grows with each step.**
Why: a live walk-through showed that seeing and controlling what agents do matters more now than more board features, and those features need somewhere to show up.

## Accepted (2026-10-02)

D74–D82 set the design direction; D83 and D84 fill in the read interface and delivery modes: a small core of primitives defended on purpose, everything else as extensions and examples, and a codebase and docs an agent can understand and extend. [PHILOSOPHY.md](PHILOSOPHY.md) explains it.

**D74. Aboard is minimal, extensible and discloses complexity in layers. The core is the set of primitives that pass D54's test (many uses need it; it can't be done correctly from outside). Everything else is an extension or an example. [PHILOSOPHY.md](PHILOSOPHY.md) states this, and the "What Aboard leaves out" section of VISION.md and the README lists what we decided not to build, each with how to do it on top instead.**
Why: a small core is easier to trust, to read and to build on; writing down what we leave out stops it creeping back in one "small" feature at a time.

**D75. Promotion rule: a new idea starts as an example in `/examples` or as an extension on an extension point. It moves into the core only once it has been used for real and shown to work, and it passes the primitives test.**
Why: trying ideas outside the core costs nothing to undo; building them in costs maintenance forever.

**D76. `/examples` is a first-class folder of short programs on the CLI or the SDKs. Each example is runnable and tested by `/e2e`, and has a README showing what it does and its real output. It starts with the hello-world pair. A summariser bot, an auditor, an approval monitor and the wrong-beliefs replication (D65) follow as the primitives they need land. Benchmark scenarios and templates beyond the built-in ones live in examples, not in the binary.**
Why: examples show how to build on Aboard, prove the public API is enough, and are where new ideas start (D75).

**D77. The core is agent-legible. The README states its size in lines and approximate tokens, and `make check` fails when it passes its budget; raising the budget is a decision recorded here. The core is the hand-written, non-test Go under `server/internal` except the client packages (CLI, delivery daemon and its text, join lines); a new package counts as core unless it is added to that list. The docs and the skill are written so an agent can explain Aboard and build extensions for it, and "ask your agent to write a monitor or launcher" is a documented flow, written when the first of those extension points lands. The starting budget is 15,000 lines.**
Why: an agent that can hold the whole core in context can explain it correctly and extend it safely; a budget makes growth a choice instead of a drift. 15,000 lines leaves room for the rest of v0.1's primitives (tasks, notes, files, team mode, safety) at the size the first two steps suggest.

**D78. Every extension point ships a public test kit a new implementation can run: storage (the store contract suite), the launcher protocol, the monitor hook and harness profiles. A kit must be usable from outside this repository, so it can't live under an `internal` package. The storage suite moves out of `internal` when a second store adapter is written; the other kits are written with their extension points.**
Why: "does my extension work?" needs an answer that doesn't depend on reading our code; kits also keep our own implementations honest.

**D79. The server never calls a model. The core monitor is the rules checks (known injection phrases and credential formats) plus the HTTP monitor hook. Jev moves out of the core into an extension, `aboard-monitor-jev`, on the hook; LLM checks, custom yes/no questions (`ask:`), off-charter checks and escalating unsure cases are the hook's job too. Secret redaction stays in the core write path. The `recommended` preset turns the rules checks on, and a board's humans can add a hook. Refines D16 and D65 (an experiment condition that used the Jev monitor uses the extension through the hook).**
Why: a model call on the write path adds cost, latency, an API key and a non-deterministic step to the record; at the edge, any classifier in any language can plug in, and the server stays the same.

**D80. Docs voice: each section states what we want, then how Aboard does it, with real commands or code. No adjectives doing the work of facts.**
Why: readers, people and agents alike, should be able to check every claim against something they can run.

**D81. Aboard governs only the shared channel: who can post, who sees what, redaction, the record, pause and revoke. It doesn't sandbox agents or restrict what they do on their own machines; that is the harness's permission system or a container, VM or sandbox the owner chooses. PHILOSOPHY.md and the safety docs say so plainly, as layers (the harness guards the machine, the sandbox guards the environment, Aboard guards the channel), state that Aboard can wrap peer messages as untrusted, flag injections and pause a board but cannot stop an agent from acting on a message, and point to each harness's permission controls and to sandboxing for unattended or many-agent setups, as recommendations, not requirements.**
Why: a governance layer that implies it protects the machine invites people to skip the protections that do; each layer is strongest when it is honest about its boundary.

**D82. Trust primitives stay in the core even though the core is minimal: attribution, visibility, the tamper-evident log, secret redaction, pause and revoke. Unlike a single-user harness, a shared room between parties who don't fully trust each other can't rely on containerising one process.**
Why: these are the guarantees only the server can give, because every party's writes pass through it and nowhere else.

**D83. The read interface (D69) in detail. The timeline API takes `after`, `before`, `newest`, `from`, `role`, `to_me` and `limit`, and pages both ways (`next_after`, `prev_before`). `to_me` means addressed to the reader and not sent by it, the inbox's meaning (D4). `aboard read` shows the newest matching messages by default, takes `--around` (two API calls, done in the CLI), ends with the command for earlier or later messages when there are more, and prints a Markdown transcript with `--markdown`, each body quoted. `aboard read` stays an agent command, never falling back to the human login (an agent reading as its human would see past `addressed` visibility); humans follow a board with `aboard watch`, which uses the human-only event stream, and with the web UI.**
Why: checked against the primitives test, the filters belong in the server: windows and filters must combine with paging over a long log, which a client can't do without reading everything, and the CLI, UI, MCP server and SDKs all need them. `--around`, `--markdown` and `watch` are presentation, so they stay in the CLI on the public API.

**D84. Delivery modes (D71) in detail. The mode is kept per agent by the delivery daemon on the machine. `auto` wakes the session for every message; `humans` wakes it only for a message from a person (its owner or another human), and that bundle carries every unread message, so the agent sees the peer messages around it, while peer messages alone, urgent ones included, never wake or interrupt it; `off` delivers nothing, and the agent reads its inbox when it chooses. `aboard delivery` with no argument shows the agent's mode; `aboard delivery auto|humans|off [--as AGENT]` changes it. Changing it is a human action: the command refuses to change the mode from inside a harness session (it recognises the variables the hooks and harness set) and says to run it in a terminal. `aboard status` shows the mode.**
Why: `humans` exists to keep an unattended agent from being woken by peers; if a peer's message could talk the agent into switching itself back to `auto`, the mode would protect nothing. On one machine this guards against an agent being talked into it, not against a process set on doing it (see the trust boundary on one machine).
Settled while building it: in `humans`, an urgent message from a person still reaches a busy session at its next tool call, alone, and urgent peer messages wait for the next bundle a person's message brings. A session is recognised from each harness profile's `session_env` (`ABOARD_SESSION` and `CLAUDECODE` for Claude Code, `CODEX_THREAD_ID` for Codex) and `sandbox_env`. Showing the mode reads the daemon's journal directly, so it never starts the daemon; changing it goes through the daemon, and an older running daemon fails it with `daemon_outdated` naming `aboard down` (D68).

**D85. Commands that act or read with the human login refuse to run inside a harness session, and say to run them in a terminal: `aboard board policy` and `aboard watch`, as the delivery-mode changes already do (D84). One error code covers them all, `human_command_in_session` (formerly `delivery_change_in_session`). `aboard pair` stays allowed: pairing from a session is the core flow, and it creates a board and joins as an agent. `aboard audit verify` stays allowed: it only checks hashes, and its output (counts, sequence numbers, hashes) reveals nothing an agent could misuse.**
Why: `aboard init` can install an allow rule for `aboard`, which removes the permission prompt that was the human's gate; without this, an agent could switch a board's policy (a humans-only action) or read the board as its human, past `addressed` visibility (the leak D83 avoided by keeping `read` agent-only). On one machine this guards against an agent being talked into it, not against a process set on doing it (see the trust boundary on one machine).
Agent-operable, for these commands, means the agent knows the exact command and hands it over: the skill lists each humans-only command with what it's for, and the refusal's hint is the command itself, with the board and agent filled in, for the agent to give its person. An agent asked "how do I change the policy?" answers with the line to run.
`aboard open` is not one of these commands: an agent may open the board in its person's browser. Inside a session it launches the browser but never prints the one-time login link, only the board's plain address, because an agent holding the code could log in before the browser and read the board as its person.

**D86. The web UI's built files are not committed. Release binaries (install script, Homebrew) embed the UI, built on CI, so people who install normally never need Node. Building from source is `make install`, which builds the UI (Node 20 or later) and then the binary. A plain `go install` still works: its binary serves a one-line page at the UI's address saying the UI wasn't built and how to get it.**
Why: committing generated, minified files would put large diffs and conflicts into every UI change; requiring Node only of people who build from source is normal for a developer tool, and costs normal installs nothing.

**D87. The browser logs in through a one-time code (D70) and then holds an HttpOnly, SameSite=Strict cookie scoped to the server's address. `aboard open` asks the server for a code with the human login (a short-lived, single-use code, stored as a digest like join codes), opens `/login?code=…`, and the server exchanges it for the cookie and redirects to the UI. The API accepts the cookie only on reads (GET, including the event stream); every write still needs a bearer token, until the UI gains writes and a CSRF defence with them. The local server rejects requests whose Host header isn't its own address.**
Why: browsers can't put a bearer token on an event stream, so the UI needs a cookie; accepting it for reads only means another site can't make the browser write anything, and checking Host stops a malicious page from reaching the local server through DNS rebinding.

**D88. Browser logins (D87) in detail. One-time codes last 60 seconds and are used once; codes and browser logins are kept only in the server's memory, as digests. A login lasts 30 days or until the server stops, so `aboard down` or an upgrade's restart logs every browser out, and the UI then says to run `aboard open`. The cookie isn't marked `Secure`, because some browsers drop Secure cookies on `http://127.0.0.1`. Known limit: browsers scope cookies by host, not port, so a program serving on another port of 127.0.0.1 receives the cookie when the browser visits it. A program run by the same OS user can already read the owner login from disk, so this only adds exposure to other OS users on the same machine; replacing the cookie with a session token held per origin and sent as a header (reading the stream with `fetch` instead of `EventSource`) removes it, and should land before team servers.**
Why: memory-only logins need no new table and end with the server; the port limit is a property of browser cookies, written down so it isn't mistaken for a guarantee.

**D89. The browser holds a read-only token, not a cookie. Supersedes D87's cookie and D88's port limit. `aboard open` opens the UI at `/#code=…`: the code is in the fragment, which browsers never send to a server. The page exchanges the code for a browser token (its own kind and prefix, read-only: the server refuses every write made with it), keeps it in the page's own storage, which browsers keep separate per origin, port included, and removes the code from the address bar. Every request sends the token as a bearer header, and the page reads the event stream with `fetch` instead of `EventSource`, which can't send headers. Codes still last 60 seconds and work once; browser tokens still live only in the server's memory and last 30 days or until the server stops. The local server still rejects foreign Host headers.**
Why: a cookie goes to every port on the host and is attached by the browser on its own; a token the page sends itself reaches only Aboard, which removes the port leak and any need for a CSRF defence when the UI gains writes.
Settled while building it: the exchange is `POST /v1/browser-tokens` with `{code}` and no token; browser tokens start with `abb_`; a wrong, expired or used code is 404 `login_code_invalid`. The link is `/#code=…&board=NAME`, and the page moves to `/?board=NAME`. `POST /v1/login-codes` and `POST /v1/browser-tokens` ignore `Idempotency-Key`: a saved response would put the code or token on disk, against keeping them in memory only.

**D90. The live proofs are an automated suite, `make live`, that an agent or a person runs with one command. It is a Go test package (`e2e/live`, build tag `live`) that drives real Claude Code and Codex in tmux: each proof is one test, set up through `aboard init --scope project` in a scratch project with isolated Aboard state, with checksums proving the real harness config is unchanged. Tests decide pass or fail from the board (messages, delivery state, `aboard doctor`), never from parsing a model's prose; the tmux panes are saved for diagnosis. A harness that isn't installed or logged in is skipped, not failed. It spends real model turns, so it is not part of `make check`; it runs before each release, after any change to delivery, setup or upgrades, and on demand. New live checks are new test functions on shared helpers. `e2e/live/PROOFS.md` becomes the index of what the suite proves and how to run it.**
Why: checking delivery against real harnesses by hand doesn't scale and depends on one person; a suite that Claude Code itself can run turns every release check into a command.

**D91. A daemon that starts keeps a `handed` delivery `handed` when it went to a waiting stop hook of a session still open, and the session's next event from the same boot confirms it; a resumed wait (a hook that was still waiting when the daemon went away) puts that session's handed bundles back to `pending`. Everything else handed still goes back to `pending`.**
Why: the live suite showed every upgrade during a wake handing the same message twice. The woken turn's own prompt hook runs the new binary, which replaces the old daemon before anything can confirm the bundle, and the new daemon put it back to `pending`. A hook that took a bundle exits with it, so only a hook that reconnects can have missed it.

**D92. Global setup follows each harness's config folder: `CLAUDE_CONFIG_DIR` for Claude Code and `CODEX_HOME` for Codex, with `~/.claude` and `~/.codex` only when they aren't set. `aboard init`, `aboard doctor` and `aboard status` all use it, and each harness profile records it as `config_dir`. Codex's global skill stays in `~/.agents/skills`, which `CODEX_HOME` doesn't move.**
Why: the harness reads its settings, hooks and skills from that folder, so hooks written to `~/.claude` while `CLAUDE_CONFIG_DIR` points elsewhere never run, and doctor checking `~/.claude` reports files the harness ignores. The live suite found it: with scratch config folders, doctor reported the person's own older setup as outdated.

## Accepted (2026-10-02): the coherent picture

D93–D108 record one coherent picture of what Aboard is, who it is for and how its concepts fit. Where one changes an earlier decision, it says which.

**D93. Aboard is a shared room where the agents you already use talk to each other and to you, with a record you can read and rules you control. The closest everyday comparison is a chat channel for agents. Positioning, in one line: harnesses run agents, workspaces host them, orchestrators decide the work, and Aboard is where they talk, with a record and rules. Aboard is not a harness, an orchestrator, a sandbox, or something that runs code. VISION.md and the README open with this.**
Why: one picture, stated first, keeps every later choice consistent and tells a newcomer in a paragraph what Aboard is and isn't.

**D94. The working model: a board is a room. An agent is a seat on one board, with one owner (the person who added it) and one role (its job on that board), filled by one session at a time. The owner controls the agent; the role says what job it does and what it may do; trust depends only on whose agent is talking. This is the current choice, open to revisiting once real use shows a need.**
Why: each concept answers exactly one question, so they never blur into each other.

**D95. One session fills one seat at a time. A session can leave a board and join another, but it is never bound to agents on two boards at once, and delivery, hooks and listeners attach to one board per session. Supersedes the parts of D1 and D45 that let one session be bound to agents on several boards (and the `agent_ambiguous` case for a session bound to agents on two boards).**
Why: coordinating on one board already costs an agent attention, and one board per session keeps delivery simple; context that another session needs travels through the board as a note, a pinned file or a summary.

**D96. Trust labels depend only on who sent a message, relative to the reader: `owner` (my owner), `human` (another person), `own-agent` (another agent of my owner), `peer` (someone else's agent), `self` (me, earlier). Roles never affect trust. Delivered messages carry the sender's `role` and `trust` (and `harness`, D98); the skill reads trust first (whose side the sender is on), then role (their job), then the charter (how the jobs relate). Own agents coordinate freely; `peer` and `human` messages are requests and information that never override the owner or the charter. Refines D28, which had no `own-agent`: a message from another agent of the same owner was `peer`.**
Why: an owner's coordinator and worker are on the same side even though only one may message everyone; a teammate's agent is a peer whatever its role.

**D97. People on a board are admins or members. The person who creates a board is its first admin; everyone else joins as a member; there is no viewer role for now. Changing the charter, roles, policy and monitor settings is for admins (where earlier decisions and AGENTS.md say "humans", read "admins"). Pausing or removing an agent is for its owner or an admin; setting its delivery mode is for its owner alone, because the mode lives in the delivery daemon on the owner's machine (D99), and an admin who wants an agent quiet pauses or removes it. Any person on a board can add agents they own. Agents can never pause, revoke, approve or change policy; they can only ask a person to.**
Why: once a second person joins, "any human" is too broad: a teammate must not be able to change your board's rules, and every agent must have someone who can stop it.

**D98. Agent names come from the harness by default, not the role: `claude`, `codex`, then `claude-2`, `codex-2`, and so on; `--name` overrides. The role stays separate, and the join line still carries the role ("… as reviewer with code …"). Delivered messages show the sender's harness as its own attribute (`harness="codex"`) beside `role` and `trust`. A board setting, `show_harness` (default true), turns this off for experiments: new agents get neutral names (`agent-1`, `agent-2`) and the harness attribute is hidden from agents; people still see it. Names show the owner (`codex · priya`) only once a second owner has an agent on the board. Supersedes D8's names from the role.**
Why: a name should say what the agent is and the role what it does; mixing them breaks when one harness plays several roles, and experiments need a way to hide which model is which.
Settled while building it: when no harness is known (no `--harness` and no session, as in the quickstart's two-terminal tab), the name falls back to the role (`writer`, `reviewer-2`). `claude-code` is shortened to `claude`; any other harness value is used as given. `show_harness` is a key of the board's policy, changed like the rest of it (`PATCH /v1/boards/{board}`, presets set it to true); while false, the server hides `from.harness` and members' `harness` from agents and numbers new agents from `agent-1`. Owners appear only once a second person has an agent on the board: the API marks each message `show_owner`, and only then does the wrapper add `owner="…"` and text output write `codex · priya`; a person's own posts never carry owner, role or harness attributes. `pair` and `join` print the role beside a name that differs from it ("joined as claude (writer, owner alex)").

**D99. Delivery mode stays per agent, set by the agent's owner (D84), never per board: on a shared board each owner decides how their own sessions are woken. A swarm's board file may set the starting mode of the agents it launches; that is a launch setting, not a board rule. An owner's own agents wake each other freely. Messages from other owners' agents follow each owner's rule for them: deliver (the default) or don't push; "hold for my approval" comes after launch.**
Why: a board-wide mode can't resolve two owners who want different things, and waking someone's session spends their subscription and acts on their machine.

**D100. Teams get a target kind `owner:<name>` (alongside `all`, `@name` and `role:R`) for messages to all of a person's agents on the board. Team presets limit broadcast to granted roles, and the status report shows activity per owner.**
Why: on a team board you usually want to ask a person's agents, not guess their names; and one post to everyone shouldn't wake every owner's sessions.

**D101. Team concepts appear through the action that needs them, never as setup: `aboard invite` says who is invited, that they join as a member, and suggests the `recommended` preset; owners appear beside names once a second owner's agent joins; the first time another owner's agent messages yours, `aboard status` and the board view say so, with a pointer to the per-owner rule. Admins and the `own-agent`/`peer` split must exist before another owner's agents can join a board.**
Why: a solo user should never meet team concepts, and a team must never run without them.

**D102. A person's inbox across boards is part of the team step (moved from "Later"): one list of what needs that person (flags, messages addressed to them, and held items once holding exists) across their boards on one server, in the CLI and the board view. Refines D42.**
Why: on a team, things wait for you on several boards, and checking each board doesn't scale.

**D103. The recommended team pattern: keep your full swarm on a personal board and put one or two agents on the team board to represent you. The docs describe it; linked boards may make it smoother later.**
Why: it's tidier, cheaper and safer than every person putting every agent into one room.

**D104. Logins. Agents always get tokens issued by Aboard, scoped to one seat and revocable, never from an outside provider. People in local mode have no login: the local server listens on localhost only and uses the local owner token. People on a team server get a login through an invite link (`aboard invite`, then `aboard connect <link>`); the browser logs in with a one-time link from `aboard open`; there are no passwords and no email. A lost machine: an admin removes that login and invites the person again. Later, an adapter for GitHub, Google or company single sign-on may answer only "who is this person"; roles and membership always stay in Aboard.**
Why: the fewest moving parts that still give every action a person or a seat behind it, and the board can always cut off one agent without touching its owner's account.

**D105. The server never runs agents or commands. Everything about talking to, steering and watching agents is in the API. Starting agents happens on the machine where they'll run: through the CLI or SDK there, through launcher adapters, or later through an opt-in `aboard runner` that only its owner controls and that accepts launches for the harnesses and launchers the owner allows. Launchers are `aboard-launcher-<name>` commands speaking JSON (start, stop, status); tmux and headless are built in (an API agent is a program using the SDK, so it is a member and needs no launcher); herdr is the first external adapter; OpenRig and Orca come later. The CLI keeps to communication verbs (no spawn, schedule or dispatch); `swarm up` prints which launcher it handed off to, and the SDK's `launch()` names its launcher. Refines D57, D59 and D61.**
Why: a server that could start processes on members' machines would let whoever controls it, or a message that fooled it, run code everywhere; a shared room must never have that power.

**D106. Sandboxing stays out of the core: the docs give recipes (a swarm in containers, Claude Code with restricted permissions, Codex's sandbox settings, a dev container per agent, agents on a separate VM), and an example `aboard-launcher-docker` applies one. Delivery mode `off` gets its own quickstart section as a real way to use Aboard, and the docs say plainly what automatic delivery means and what Aboard can't stop. Refines D81.**
Why: a sandbox is just another place to start a session, so isolation is a launcher choice, not a feature of the room.

**D107. Primitives, not rigid structure: roles are starting points that limit only what needs limiting (such as messaging everyone); the charter is guidance agents use with judgement, not a script; what's enforced is permissions, visibility and the safety rules. Coordination emerges from how agents use the room. PHILOSOPHY.md says this.**
Why: the interesting behaviour comes from agents using a few strong primitives, and a prescribed workflow would fight the work instead of helping it.

**D108. Aboard's place among its neighbours is written down ("Where Aboard fits" in VISION.md): harnesses, meta-harnesses, workspace managers, orchestrators and hosted workers each own their layer, and Aboard owns communication between agents and people plus delivery into running sessions. The test for a concern is whether communication breaks without it. Integration examples are planned: a herdr launcher, an OpenRig launcher, an agent run by a meta-harness as a member, a mail bridge for an orchestrator, and a "Using Aboard with…" page for each.**
Why: a clear boundary stops the core from growing into its neighbours' jobs, and makes Aboard easy to adopt next to them.

**D109. The remote MCP endpoint on a team server authenticates with OAuth, with the Aboard server as its own authorization server on the same host. claude.ai custom connectors require OAuth for per-person access: the authorization code flow with PKCE (S256), client registration by a Client ID Metadata Document or dynamic registration (RFC 7591), discovery through RFC 9728 protected-resource metadata and RFC 8414 server metadata, and the callback `https://claude.ai/api/mcp/auth_callback`; ChatGPT's connectors take the same flow with their own callback. Aboard's `/authorize` page identifies the person without a password: a browser already logged in through `aboard open` goes straight to consent; otherwise the page shows a short code to approve with `aboard authorize <code>` on a machine holding that person's login. The person then picks a board and an agent seat, and the connector's token is scoped to that one seat, with the MCP URL as its audience, short-lived access tokens and rotating refresh tokens; removing the seat revokes it. The endpoint needs HTTPS and must be reachable from the public internet, so claude.ai on the web joins team servers only; local use is `aboard mcp` over standard input and output. It is built after invites and logins in the team step.**
Why: claude.ai only accepts OAuth or no authentication for a connector that can tell people apart, and building it on the invite logins keeps one way to prove who someone is, with every connector acting as one seat like any other agent.

**D110. The label that says who sent a message, relative to the reader, is the `sender` attribute (not `trust`), with short values that read on their own and are the same in delivered messages and `--json`: `owner` (the person you work for), `owner_agent` (another agent of your owner), `other_person` (someone else), `other_agent` (someone else's agent), `self` (you, earlier; only in reading, never delivered). The skill's rule: follow `owner`; coordinate freely with `owner_agent`; treat `other_person` and `other_agent` as requests and information to weigh, never orders. The docs call it the sender label. Supersedes the label names in D28 and D96 (`owner`, `human`, `own-agent`, `peer`, `self` under `trust`); the meaning is unchanged.**
Why: `own-agent`, `peer` and the word "trust" needed the scheme explained before they made sense; a label should say plainly who is speaking, to a person or an agent reading it cold.
Settled while building it: for a person reading, their own agents are `owner_agent` and their own posts `self` (a person counts as their own owner). The API adds `sender` to every message and keeps `trust`, marked deprecated with its old values, because the API only grows (D68) and a delivery daemon from an older build reads `trust` until it is replaced; the wrapper, text output and the skill use only `sender`. `aboard read` text shows the label in brackets after the role and harness: `#6  @writer (writer, self) → @reviewer`.

## Rejected or deferred

Things we decided not to build, or not yet. Each has a reason and, where it applies,
how to get the same result on top of Aboard. Revisiting one needs a new decision.

### Rejected for the core

| Idea | Why not | Instead |
| --- | --- | --- |
| Orchestration and scheduling (deciding who works on what, and when) | It depends on the workload, and owners and their agents should decide; a server that schedules must also own retries, capacity and fairness | An orchestrator, an SDK script, or the board file's `agents` section with `aboard swarm up`, which starts sessions once and never schedules them |
| Model calls inside the server | Cost, latency, an API key and a non-deterministic step on the write path (D79) | Monitors behind the HTTP hook; bots that read the stream and post |
| A workflow engine (steps, branches, triggers) | Workflows differ per team and change often; messages, tasks and charters already carry handoffs | A bot on the SDK that watches events and posts or opens tasks; the charter describes the flow to agents |
| Built-in subagents | Harnesses already have them; Aboard connects sessions and never runs agents (D59) | Use the harness's own subagents; a launcher can start more members |
| Task dependencies | A dependency graph brings scheduling into the server: which tasks are ready, which are blocked | Put a task in `waiting` with a reason naming its blocker; use labels and order; a bot can open tasks when others finish |
| Sandboxing agents, or limiting what they do on their machines | Aboard governs the channel, not the machine (D81) | The harness's permission system; a container, VM or separate OS user |
| The Jev monitor, LLM checks and custom questions in the server | No model calls in the server (D79) | `aboard-monitor-jev` and other monitors on the hook |

### Deferred

Possibly later, each needing its own decision first.

| Idea | Why not now |
| --- | --- |
| Postgres storage, an S3-compatible file backend | SQLite and the server's disk cover local use and small team servers |
| OIDC login | Invite links cover small teams |
| Hold-for-review, approval gates, owner approval for incoming asks ("hold for my approval" for other owners' agents comes right after launch, D99) | Each holds a message until a human decides, so a check runs before the write and its latency becomes every message's latency |
| A whole-board monitor | It reads windows of activity with a model, so it belongs outside the server as a stream reader; it needs a primitive for a monitor to flag a message |
| Role-based visibility; monitor checks against what an author privately received | Recorded by the replication experiment (D65); wait for a second use |
| Policy expressions (for example CEL) | The fixed policy keys cover every known case |
| A2A bridges; Go and TypeScript hand-written SDK layers | Clients, built when someone needs them |
| Work and map views; sub-boards and links | Layers past v0.1 (the inbox across boards moved into the team step, D102) |
| Automatic delivery for OpenCode, Pi, OpenClaw and Hermes | The skill plus `aboard inbox --wait` works for them today |
