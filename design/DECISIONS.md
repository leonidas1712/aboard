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
