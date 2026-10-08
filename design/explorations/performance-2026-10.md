# Performance recommendations, October 2026

Reduce repeated reads before changing SQLite's durability. The strongest route
to tens of milliseconds is to make each notification refresh only the data that
changed, then share repeated board reads where measurement still justifies it.
Batch acknowledgement and presence transactions next. Keep the dedicated writer
and bounded reader pool from #237 as the foundation.

This is an independent investigation, not an implementation or an approved design
decision. Internal query and notification improvements fit the existing storage
and delivery work. New batch API contracts need review before implementation.
Changing durability or making presence volatile needs an explicit decision.

## Evidence and limits

Source inspection uses main at `bf78ab0`, plus these PR heads:

| PR | Inspected head | What it establishes |
| --- | --- | --- |
| [#233](https://github.com/leonidas1712/aboard/pull/233) | `517ea7d` | Corrected connection priming and confirmation cadence; request-to-write timing |
| [#237](https://github.com/leonidas1712/aboard/pull/237) | `fc326964` | Four reusable readers and a separate single writer; matched pool comparisons |
| [#238](https://github.com/leonidas1712/aboard/pull/238) | `f06ec648` | Reconnect jitter and bounded retries of waiting inbox reads; unrelated to steady-state query amplification |
| [#239](https://github.com/leonidas1712/aboard/pull/239) | `c91ba1c6` | Separate concurrent-writer phase, resource samples and optional soak; instrumentation rather than a server optimization |

The [load methodology at #237](https://github.com/leonidas1712/aboard/blob/fc3269640d9864bc09af774d1cc5ce2bab316351/lab/load/README.md)
and [scaling report](https://github.com/leonidas1712/aboard/blob/fc3269640d9864bc09af774d1cc5ce2bab316351/lab/load/scaling-2026-10-09.md)
are the sources for these measurements:

| At 250 seats, milliseconds | Before pool change p50 / p95 / p99 | After pool change p50 / p95 / p99 |
| --- | --- | --- |
| Write response | 111.7 / 328.8 / 579.3 | 107.9 / 258.2 / 326.9 |
| Request to stream | 613.6 / 995.8 / 1205.5 | 259.0 / 471.4 / 546.7 |
| Request to long poll | 289.9 / 583.4 / 769.4 | 205.7 / 403.6 / 556.8 |
| Request to hand-over | 3059 / 3618 / 3953 | 2495 / 2838 / 3036 |

The topology is 25 people, 250 agent seats, 15 boards and 50 configured SSE
streams. It is not 250 SSE streams on one board. The driver also starts an inbox
observer per recipient. Posts address every agent on that board. Recipient-only
wake-ups cannot remove those necessary observations.

The earlier diagnostic 100-seat profile recorded 20,016 read transactions and
1,514 write transactions for 30 posts. These include bookkeeping: about 667 read
transactions and 50 write transactions per post, not that many message inserts.
Write-start waits totalled about 137 seconds across goroutines; commits averaged
0.87 ms. The pool discarded 1,886 connections. These are diagnostic observations,
not elapsed-time components that can be added together. The diagnostic build used
Go 1.27.1; the pool comparison used Go 1.26.8.

Board reports #1405 and #1406 describe a separate 16-writer experiment on main
`2fbe98a` with driver #239: write p99 was 3167 ms at 50 seats and 4998 ms at 100.
These are not measurements of #237. Its `writers.go` addresses each writer's
owner, whereas the delivery ladder addresses all agents. Compare each workload
against itself before attributing a gain.

This investigation ran small query-plan checks only. No load ladder, live harness,
test server or full test suite ran. Query plans used a fresh schema in temporary
storage and then an in-memory database through the pinned Go driver. No real
database, configuration or excluded local folder was read. No runtime code changed.
The latency target remains unproven.

## Ranked changes

Gains below are hypotheses about eliminated work, not benchmark results. They
overlap and must not be multiplied. S means a local adapter/query change; M means
several domain/client paths and their regression coverage; L means a new delivery
mechanism or public batch contract with recovery tests.

| Rank | Change | Expected gain and limit | Risk | Size |
| --- | --- | --- | --- | --- |
| Foundation | Keep #237's separate writer and four readers | Measured stream p99 about 2.2x better at 250; write p95 about 1.3x. Moves in-process writer contention into a bounded pool, not unlimited SQLite busy handlers | Low; retain transaction snapshots and connection PRAGMAs | S, already proposed |
| 1 | Refresh HeadFeed by changed board and change kind; owner-specific receipt signals; avoid the redundant read before waiting | Potential order-of-magnitude reduction in full-board snapshot work during ack/presence bursts. Actual factor depends on boards followed, listeners and coalescing. Most direct route to a large ladder improvement | Medium; lost wakes, membership removal and credential expiry must remain correct | M |
| 2 | Share committed board changes or board snapshots, with bounded fan-out and recovery | Repeated shared reads fall from approximately listeners × changes to one per board generation, plus per-reader authorization and private state. A 10-listener hot board can avoid about 10 copies of the shared read; total latency will improve less | High if this becomes a content/authorization cache; moderate for head metadata and dirty generations | M for metadata; L for payload fan-out |
| 3 | Batch durable acks and presence; coalesce refreshes in the daemon | A batch of k seats can replace k commit boundaries with one, while still updating k rows. Ten seats per daemon provides an upper opportunity of 10x fewer commits for a synchronized batch, not a promised 10x write-latency gain | Medium/high; confirmed-prefix acknowledgements, exact seat credentials, durable success and partial failure semantics | M for scheduling; L for public batching |
| 4 | Narrow projections and fix demonstrated index gaps | Removes table scans, temporary sorts and repeated correlated lookups. Likely incremental at this small topology; can be much larger with retained members, tasks or unread history | Low/medium; preserve filters and count meanings; indexes add write/storage cost | S/M |
| 5 | Reuse prepared statements for fixed hot SQL | Avoids preparation/finalization per invocation. Profile supports testing it; no numeric speedup is established. Unlikely to solve the fan-out multiplier alone | Low/medium; pool ownership, transaction binding, cancellation, closing rows/statements | S/M |
| 6 | Move presence renewal off the main writer queue | Removes renewal transactions from contention entirely if presence becomes volatile; batches reduce them without that semantic change | High for volatility, restart behavior and applied delivery mode; requires a decision | L |
| 7 | Consider WAL `synchronous=NORMAL` only if commit timing later dominates | Can reduce sync cost; little direct evidence for a 10x gain here because commits averaged 0.87 ms while write starts averaged about 90 ms | High product risk: acknowledged commits can be lost after power/OS failure | S code, explicit durability decision |

Start with rank 1 and the small projections/indexes it needs. Measure before
building a general event cache. Rank 2 is the next step if independently refreshed
listeners still duplicate substantial work. Rank 3 addresses the remaining writer
queue. Keep the implementation slices independently measurable.

## What the hot paths do

[`PostMessage`](../../server/internal/board/messages.go) runs through
[`writeAs`](../../server/internal/board/people.go), which checks the credential
again inside the write transaction. The method resolves access and recipients,
checks policy, resolves mentions/tasks/files/asks, seals the next event, inserts
the message, updates the board and reads the message back with enrichment. Only
after commit does it signal the board. Preserve that transaction's authority and
sequence guarantees. Do not publish before commit or move mutable permission
checks outside it to shorten the lock.

[`sqlite.Open`](../../server/internal/store/sqlite/sqlite.go) configures WAL,
foreign keys, a ten-second busy timeout and immediate write transactions. The
pinned driver's `tx.go` uses deferred `BEGIN` for `ReadOnly` transactions even
with `_txlock=immediate`. Read transactions are not all taking the writer lock.
At #237, reads and writes use separate pools, with maximum/idle counts of four
and one respectively. There is no explicit `synchronous` setting in that change.
Check the actual writer connection before any durability experiment.

[`notify.InProcess`](../../server/internal/notify/notify.go) closes one channel
per key. Multiple writes before a reader rearms can already coalesce. It does not
queue an event per listener, and it already separates board, presence, read and
credential keys. Changing the channel implementation alone misses the expensive
part: what readers do after receiving the signal.

[`HeadFeed.Next`](../../server/internal/board/heads.go) watches all boards the
person follows and their presence/read keys. Each loop reads credential state,
then `Heads`, then [`presenceOn`](../../server/internal/board/presence.go): three
read transactions before waiting or returning an update. `Heads` loads full board
records. `presenceOn` rereads each board, checks human membership, counts unread
messages and loads every member. Full board records include a correlated open-task
count. Full member records include human and task lookups. Presence, receipts and
unread changes are discovered by comparing those snapshots.

After returning an update, the next `Next` call performs the reads again before
waiting. This protects against a lost wake; simply deleting it is incorrect.
Carry a subscription/generation acquired before the snapshot into the next wait,
or use an equivalent atomic subscribe-and-snapshot protocol. Preserve the startup
rule: initial state is read before HTTP success is sent.

[`Inbox`](../../server/internal/board/messages.go) watches the board key before
reading. A waiting read checks credentials in a separate transaction and again
with the seat and messages. It also annotates messages and constructs
[`taskWork`](../../server/internal/board/taskwork.go), including tasks, asks and
brief context, even when the inbox is empty and will wait again. Delay that
enrichment until returning a response, with a consistent authorized snapshot.
Keep the timeout response's documented work fields and lifecycle wake behavior.

[`Ack`](../../server/internal/board/messages.go) reads the seat and board, reads
the member before and after `SetCursor`, and signals `read/<board>` only when the
cursor moves. `SetPresence` writes every renewal, but signals `presence/<board>`
only when state or applied delivery mode changes. Suppressing identical presence
notifications is therefore already implemented; renewal write suppression is a
different proposal.

The daemon's [`serverConn.handle`](../../server/internal/delivery/serverconn.go)
coalesces head notices by board, then fetches watched inboxes serially, handles
refreshes and acknowledgements serially, and finally reports presence. One slow
inbox can hold later acknowledgements and presence reports for that server. The
mailbox already coalesces the latest presence per seat within its current batch.
The session also suppresses unchanged reports until the one-minute renewal.

[`maybeAck`](../../server/internal/delivery/session.go) already acknowledges the
highest contiguous confirmed/skipped prefix, with one ack in flight per seat.
After the ack empties its unread list, it refreshes the inbox to find another page.
Count those follow-up reads separately. Batch across seats or mailbox turns;
do not replace confirmed-prefix logic with the maximum sequence seen.

## Narrow wake-ups and shared fan-out

Use changed-board and changed-kind information first. A presence update should
not recount messages or rescan other boards. A receipt should wake its owner's
streams and refresh that seat's cursor, rather than every person on the board.
Head changes still need each relevant person's unread count. A slim authorized
query can retrieve that person's membership, head and count together.

Recipient-specific inbox keys are useful for direct messages. Include readable
waking mentions, `all`, role and owner addressing, sender exclusions, and the
existing treatment of changed roles and reused names. A recorded recipient list
is not a universal replacement for the current inbox SQL: name/role matching and
owner recipients use different predicates. Membership, token replacement,
removal, lifecycle and delivery-setting changes need a separate control wake.
Otherwise a waiting request can retain access or an obsolete mode until timeout.

The current daemon follows human board heads and fetches all its watched agents
on that board. Narrowing server-side long-poll wakes does not eliminate those
daemon GETs. Either keep them cheap or add a reviewed public notification/batch
primitive. Never give the daemon a database shortcut.

For shared fan-out, begin with committed head maxima and dirty generations, not
message bodies. A shared board snapshot may serve several readers, but every
outgoing result must still respect current credential, membership, policy and
owner-only receipt visibility. Do not cache a person's authorization merely by
board ID. Store/content revisions and authorization checks need a defined
linearization point comparable to today's authorized read transaction.

The recovery design needs all of these properties:

- Subscribe before taking the initial snapshot. A commit during subscription or
  snapshot must leave a changed generation that the reader observes.
- Publish only successful commits. Concurrent goroutines can publish commit
  callbacks out of order, even with one SQL writer; use monotonic head maxima or
  order deltas by sequence before delivery.
- Bound each subscriber's memory. A slow socket must not block a commit or other
  subscribers. On overflow, mark it dirty and recover through an authorized
  snapshot or close it for reconnect.
- Treat head coalescing separately from event replay. Existing SSE head events
  can jump sequences; they carry heads, not every event. A payload cache would
  need replay from the durable log after the last contiguous sequence, with
  duplicates removed and all visibility rules applied.
- Recover bookkeeping separately. Presence and cursors never advance the event
  sequence. A sequence-gap check cannot notice a missed receipt or presence
  change. Use independent generations and snapshots for them.
- Clear process-local generations on restart; reconnect begins with a fresh
  snapshot. Retain credential expiry and periodic reconciliation. Presence TTL
  expiry itself must still produce the correct state without a write.

This shares repeated work while keeping the store authoritative. A ring of raw
events adds payload filtering, retention and replay complexity that head metadata
does not need. Build it only if the simpler measured candidate still misses by
an order of magnitude.

## Acknowledgements, presence and writer service time

Keep successful ack responses durable. A server must not answer success, queue an
ack only in memory, and lose it on restart. A batch must validate each acting seat
under its actual credential and current membership inside the transaction, check
each board's head, and update monotonic cursors. Define all-or-nothing versus
per-entry failures and idempotency before adding an endpoint. An owner identity
or a list of seat names must not silently replace the existing acting authority.

Try draining already queued work before adding a batching delay. If necessary,
use a small bounded window whose delay fits the latency budget; flush promptly
on shutdown and distinguish confirmed journal state from server-acked state.
Deduplicate only within the same permanent seat and credential generation. Keep
an explicit bound so bookkeeping cannot monopolize the sole writer. Savepoints
or per-entry results introduce complexity that deserves its own tests.

A small bounded pool for daemon inbox fetches can remove serial head-of-line
blocking. It must preserve per-seat generation/order and reserve progress for
acks. Unbounded goroutines would recreate the server stampede. Avoid changing the
two-second gathering window to make the hand-over metric look better.

Presence is a three-minute lease today. Batch renewals while keeping state changes
prompt, and spread renewal timers across daemons. Moving only renewal persistence
to a volatile lease store is a later option: it needs restart/expiry behavior and
a decision about the applied delivery mode, which currently does not expire with
presence. A second connection to the same SQLite file does not create a second
writer. Separate storage would create a consistency boundary with membership and
revocation, so it is a larger design choice.

Also measure [`SaveResponse`](../../server/internal/store/sqlite/responses.go):
idempotent API writes save their response in a separate transaction after the
handler. Key-use recording is already throttled in
[`keys.go`](../../server/internal/board/keys.go). Attribute writes by operation
before assuming all 1,514 are acks or presence. Folding idempotency persistence
into domain transactions is a separate correctness change, not a quick removal
of bookkeeping.

## Query plans actually run

The probe applied every migration from `bf78ab0` to a fresh empty schema. It ran
`EXPLAIN QUERY PLAN` with the exact expanded SQL for `BoardsOfHuman`, `Members`,
`HumanMember`, `Inbox`, human `CountUnread` and the owner-count subquery in
`messageSelect`. Parameters used synthetic IDs, cursor zero and inbox limit 101.
No `ANALYZE` or production statistics were used. The first check used Python's
SQLite 3.43.2. The repeat used `modernc.org/sqlite v1.60.1` from this repository's
module selection, reporting SQLite 3.53.4, one in-memory connection, Go compilation
limited to one package at a time and two scheduler threads, with temporary home,
state and build cache. These are plan-shape observations, not timing results.

| Query | Existing plan, SQLite 3.53.4 | Plan after candidate indexes |
| --- | --- | --- |
| Human board membership subquery | `SCAN members` | `SEARCH members USING COVERING INDEX candidate_human_boards (human_id=?)` |
| Members in join order | `SEARCH members USING INDEX sqlite_autoindex_members_3 (board_id=?)`, then `USE TEMP B-TREE FOR ORDER BY` | `SEARCH members USING INDEX candidate_board_members (board_id=?)`; no temporary order sort |
| One human's board seat | Board/name index prefix, filtering human and kind | `candidate_human_member (board_id=? AND human_id=? AND kind=?)` |
| Inbox | `sqlite_autoindex_messages_2 (board_id=? AND seq>?)`; correlated `json_each` scans; indexed sender/reply joins | Same message range access; the owner-count subquery improves as below |
| Human unread count | `sqlite_autoindex_messages_2 (board_id=? AND seq>?)` | Same |
| Distinct agent owners per returned message | Board/name index prefix plus `USE TEMP B-TREE FOR count(DISTINCT)` | Covering `candidate_agent_owners (board_id=?)`; no temporary distinct tree |

The candidate SQL below ran only on the disposable schema, together in a second
plan pass. It is not a migration proposal to add all four without measurement:

```sql
CREATE INDEX candidate_human_boards ON members(human_id, board_id)
  WHERE kind = 'human' AND status = 'active';
CREATE INDEX candidate_board_members ON members(board_id);
CREATE INDEX candidate_agent_owners ON members(board_id, human_id)
  WHERE kind = 'agent';
CREATE INDEX candidate_human_member ON members(board_id, human_id, kind);
```

The rowid suffix in the simple board index supplies the requested member order.
The human-board index cannot serve `HumanMember` as written, because that query
does not filter active status. The agent-owner count currently includes inactive
agents; preserve its meaning when optimizing it. The Python version chose the
composite human-member index for that count, while the pinned Go driver chose the
partial agent-owner index. Do not assert one plan across engine versions.

The full member projection still performs correlated indexed human/task lookups
after adding indexes. A presence-only projection avoids that work entirely.
Likewise, `Heads` does not need the full board projection's JSON decoding or
open-task count. Return the required head and authorization fields through a
small store port.

Inbox and unread queries already have the important board/sequence range index.
Adding another identical index would only add writes. JSON recipient predicates
still inspect candidate messages after the cursor; a long stale cursor can make
that expensive. A normalized recipient read model is a later, larger option if
long-history measurements justify it. Unread `COUNT(*)` remains proportional to
the selected unread range; an index does not turn it into constant-time counting.
The per-message distinct-owner count can be computed once per board/read
transaction instead of once per returned message.

Before shipping an index, repeat the exact plans on populated fixtures representing
retained members, many boards, long unread history and many tasks. Compare with and
without statistics, then measure write cost as well as reads. SQLite documents
[how to interpret query plans](https://www.sqlite.org/eqp.html); the printed plan
format is not a stable application API.

## Statement reuse and durability

The store calls `QueryContext`, `QueryRowContext` and `ExecContext` with SQL strings;
there is no explicit prepared-statement cache in the inspected adapter. The local
pinned driver's `conn.go` prepares one-shot calls and closes/finalizes their
statements. Its `stmt.go` retains a single statement handle when explicitly
prepared, and `rows.go` resets reusable statements. Statement reuse is therefore
a concrete candidate, not an assumption that `database/sql` already caches SQL.

Prepare a bounded set of stable hot queries per pool after migrations, bind them
to transactions with the appropriate `Tx.StmtContext` path, and close them with
the store. Do not obtain a second pooled connection from inside a transaction;
with one writer that can deadlock. Avoid an unbounded cache keyed by dynamic
`IN (...)` shapes. Measure preparation count, allocation and query time after the
read multiplier is removed, with the same query, rows and authorization checks.
Driver lifecycle and cancellation tests matter more than a trivial SELECT score.

WAL `synchronous=NORMAL` keeps consistency but can lose recently committed work
after an OS crash or power loss; `FULL` adds commit synchronization. That tradeoff
is documented by [SQLite's synchronous reference](https://www.sqlite.org/pragma.html#pragma_synchronous).
The 0.87 ms average commit suggests reducing fsync is not the first fix. Even
eliminating that direct cost would not explain a 90 ms average write-start wait;
queueing effects must be measured separately. Keep durability unchanged while
removing unnecessary transactions. Inspect commit tails and WAL checkpoint work
before revisiting the setting, and configure every writer connection if approved.

## Validation for the implementation lane

Use a focused real-store test/benchmark before another ladder. Cover one board
with many listeners and many boards with a few listeners, direct and all-recipient
messages, ack bursts, unchanged renewals and presence transitions. Count read/write
transactions, statements, rows, preparations, pool waits and empty wakes by cause.
Time writer queue acquisition, transaction execution, commit, commit-to-notifier
and notifier-to-flush separately. Pool wait moving from SQLite into `database/sql`
is not itself a reduction in total wait.

Regression coverage must include subscribe/read races, concurrent commits whose
callbacks run out of order, coalesced heads, slow readers and overflow, reconnect,
revocation/expiry, member removal/rejoin, policy changes, owner-only receipts,
presence expiry, and ack recovery after restart. Verify cursor monotonicity and
every board's chain. Contract tests must retain each existing refusal and
visibility rule.

Then use one matched uninstrumented 100-seat comparison and one final 250-seat
comparison, coordinated through the shared laptop slot. Pin toolchain, driver,
topology and server heads. Report write response, stream and long-poll latency
separately from the gathering-inclusive hand-over metric. Treat write p95 around
20 ms, stream p99 around 100 ms and hand-over around 2.2 s as directional targets.
Do not spend repeated runs chasing a few milliseconds once the order of magnitude
is reached. No full-500, soak or hosted claim follows from the checks in this note.

The full repository check and affected live delivery proof remain implementation
merge gates. This documentation-only investigation does not claim those checks
or any new latency result.
