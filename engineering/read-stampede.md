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
