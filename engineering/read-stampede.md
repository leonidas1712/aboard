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
