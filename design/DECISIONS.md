# Decisions

Refinements to [VISION.md](VISION.md). Where a decision here is
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
