# Delivery bookkeeping write demand

This comparison counts server transactions for an isolated local 500-seat workload:
50 people, 10 seats each, 20 boards, one delivery round, followed by 16 concurrent
writers posting 10 messages each. Both runs completed 20 delivery posts, 500
handoffs, 20 verified chains and all 160 concurrent posts. Cursor, ordering and
message identity checks passed.

The server archive is `7c9a8a3`, with load-client fixes at `c711f10`; the intervening
merge to `a401517` changes only the load tool. The comparison changes only the delivery
adapter's ACK/presence request keys and the redundant empty queue-claim update.
Private instrumentation counted transaction kinds and phase boundaries. Private
join/connect limits were raised for setup in both runs. These changes were not
included in the product. This is a transaction-demand comparison, not a latency or
capacity proof; both runs missed the stream latency target.

| Count | Before | After |
| --- | ---: | ---: |
| Setup queue-report transactions | 1,000 | 500 |
| Setup saved-response transactions | 2,520 | 1,520 |
| Delivery + confirmation saved-response transactions | 1,571 | 20 |
| Delivery ACK requests | 500 | 500 |
| Delivery + confirmation presence requests | 1,051 | 1,048 |
| Physical batched ACK/presence transactions, all phases | 1,513 | 1,464 |
| Concurrent post transactions | 160 | 160 |
| Concurrent saved-response transactions | 160 | 160 |

ACK cursors are monotonic, and a session serializes its presence updates. The
adapter now sends those two operations without an Idempotency-Key, avoiding a
separate response-cache transaction. The API still accepts keys from other clients.
Authentication, cursor checks and held delivery-mode handling are unchanged.

A successful queue claim already installs an empty report. The publisher now stops
after durably saving that successful claim when its desired report is empty. Lost
claim responses retain the original retry key; a later nonempty report retains the
same epoch and starts at revision 1. Queue claims and updates keep their stable keys.

The observed extra response writes disappeared, while the server's existing
ACK/presence batching remained in use. The small difference in presence requests
and batching depends on scheduling; it is not a further claimed optimization.
Queue-report writes in these runs occurred during startup, not once per delivery
post. The counts do not establish a new Fly throughput limit. Real-harness validation at `43db117` passed collectively: the initial 12-case run
passed 10 cases, including every single-harness and cross-harness ping-pong, plus
Claude Code and omp held-mode and daemon/server restart cases. Two Codex cases
failed before binding while waiting for startup; a serial retry of those same
held-mode and restart cases passed with unchanged assertions and timeouts. Both
runs left protected configuration and authentication unchanged, and retained artifact
scans found no real-login values. This is collective evidence, not a clean initial
run. Remote scale measurement follows separately.
