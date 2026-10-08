# Reducing stream read work

The load profile found repeated head, member and unread queries after notifications.
Each human stream refreshes all its boards, including boards the notification did not
change. Read acknowledgements also wake streams belonging to other people.

The first change adds indexes for active human board membership, board member order
and distinct agent owners. Next, stream refreshes should use the notified board and
change kind. A membership change or periodic tick still refreshes the complete set.
Every exposed snapshot must check the credential and board access in its transaction.
No payload or authority cache may outlive those checks.

Presence and read positions do not advance the event sequence. Event gap recovery
alone cannot recover those changes. Post-commit notifications can arrive out of order;
they are refresh hints, not snapshots or authorization.

Use a focused benchmark or the 100-seat ladder while developing. Measure the 250-seat
ladder once after the read and bookkeeping changes, preserving delivery and chain checks.
The latency targets describe the desired range rather than strict merge thresholds.

## First candidate

HeadFeed retains watch channels across returned updates. It re-arms changed channels
before refreshing their boards. New boards get a second snapshot after subscription,
closing the discovery window. Membership changes and ticks still refresh all boards.
The two authorized snapshot transactions remain, including the second access check.
Read notifications now name the verified owner as well as the board.

A focused benchmark posts to one hot board, then consumes the update on ten listeners
whose person follows ten boards. Go 1.26.8, macOS arm64, ten iterations, no race
instrumentation, real SQLite and identical fixture/benchmark source:

| Per post | Before | Candidate |
| --- | ---: | ---: |
| Post plus ten sequential stream updates | 19.34 ms | 4.44 ms |
| Recorded BoardByID reads | 100 | 20 |
| Full BoardsOfHuman reads | 10 | 0 |

The before build is `20b06a9`, before runtime edits and the index migration. This is
an in-process benchmark, not concurrent SSE, request-to-stream latency or a percentile.
It isolates multi-board refresh work; it does not establish the 250-seat target.
The query-plan, changed-board and owner-receipt regressions fail before their fixes.
Change-kind projections and batching remain subsequent work.

The stream member read will use a narrow internal store projection: permanent member
and board IDs, name, kind, owner ID, status, cursor and presence. It must include human
members so the existing active-owner filter still hides agents whose person left.
Token, role, display and current-task fields are not needed for this snapshot.

Change-kind refreshes distinguish board events, presence, agent receipts and the
person's own read position. Presence and agent receipts do not recount the person's
unread messages. Human read acknowledgements have their own owner-scoped key. A board
event or reconciliation tick still checks every field needed on the affected boards.

The hot board lookup also needs only ID, name, head sequence and lifecycle. A narrow
store projection avoids policy/role JSON and open-task counts on those reads; existing
membership and credential reads remain the authority checks.

## First 100-seat iteration

Ten people, ten agents each, ten boards, three rounds; the existing #233 delivery
driver, Go 1.26.8, private home/state and an ephemeral local server. Candidate
`f373470` contains scoped board refreshes, owner receipts and the indexes; the narrower
projections and change-kind changes came afterwards and are not measured here.

| Milliseconds, p50 / p95 / p99 | Pool-only candidate | Scoped-board candidate |
| --- | --- | --- |
| Write response | 38.5 / 95.1 / 108.3 | 19.1 / 59.6 / 64.7 |
| Request to stream | 129.2 / 227.9 / 261.2 | 80.1 / 155.3 / 196.1 |
| Request to long poll | 91.9 / 189.7 / 234.8 | 55.0 / 108.6 / 137.0 |
| Request to handover | 2270 / 2463 / 2521 | 1183 / 2160 / 2196 |

The [pool report](../lab/load/scaling-2026-10-09.md) supplies the comparison.
The candidate verifies 30 posts, 300 deliveries and ten chains, with no reset.
Setup took 183.28 seconds; the measured phase took 6.96 seconds (4.31 posts/s versus
2.32 previously). This is the delivery ladder's paced throughput, not a concurrent
writer capacity measurement. Three setup requests were throttled. Sampled peak RSS
was 58.45 MiB; the old `ps` moving CPU estimate peaked at 294.4%, not a CPU timeline.
The run completed its correctness checks but exited 1 for stream p99 above 100 ms.
One local iteration is not a guarantee of either percentile or hosted performance.

## Durable bookkeeping groups

Only acknowledgement and presence writes may share a commit. Each callback keeps its
own credential, membership and policy checks inside the transaction and its own
savepoint. A refused callback rolls back its changes without discarding unrelated
successful callbacks. A failed commit reports failure to every otherwise successful
caller. No caller receives success or emits a notification before that commit.

Groups take at most sixteen already queued requests, with no gathering delay. Event
writes remain separate. Cancelled requests do not run; callbacks already running keep
their request context. The store owns the flush worker and cancels and joins it on
close. Read-only and other stores retain the ordinary write path. SQLite's durability
settings stay unchanged.
