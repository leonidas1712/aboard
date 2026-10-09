# Load proof

`make load` runs fake people and agents against a real server and real delivery
daemons. It calls no models and uses no existing login, home or Aboard state.

The default topology is 50 people, ten agent seats per person, and 20 boards.
Each person runs one daemon. Each seat has a fake omp extension connection, which
speaks the public control protocol and confirms the messages it receives.

For a small proof:

```sh
make load LOAD_ARGS='--people 2 --agents 2 --boards 2 --rounds 2'
```

The tool builds the real binary unless `--binary` names one. `--timeout` bounds the
whole run, including setup. Public invites and joins provision the topology. Rate
limits stay unchanged; a 429 waits for Retry-After. Setup is timed separately and
excluded from latency samples. With one loopback source, 500 joins can take about
17 minutes under the 30-per-minute join limit. The default timeout is 30 minutes.

`--writers N --writes-per-writer N` adds a separate concurrent-writer phase after
delivery verification. The defaults are sixteen writers and ten messages each;
`--writers 0` disables it. Each writer is a distinct provisioned agent, capped by
the available seats. They start together and post sequentially within each writer.
Responses and read-back prove distinct message identities, ordering within a
writer and the recorded body and sender. Every board's chain is verified afterward.
The fake extension receivers have finished before this phase; real daemons and
person streams remain open. This isolates API write throughput from delivery drain.

`--soak 10m` repeats delivery rounds for at least that measurement duration, while
still completing at least `--rounds` rounds. The whole-run `--timeout` still includes
setup, so set it high enough. Validation metadata accumulates for the whole soak;
the tool does not claim constant driver memory.

Each round posts one message per board, addressed to every agent on that board.
Every message has a unique marker. A person's stream must first report its boards
before measurement starts. Streams may coalesce head updates: observing a head at
or above a message's sequence proves that the stream has reached that message.
Connection establishment is limited to 16 simultaneous dials. Before measurement,
each observer gets a dedicated connection and authenticates its seat through `/v1/me`.
Idle observer connections may redial before a round. All observer requests must
finish connection establishment and transmit before the measured post. Redials
share the 16-dial bound across seats. Connection setup is outside latency samples.
Open requests are not limited: all 500 observers must still be transmitted before a
measured post. This measures established-connection delivery, not cold-connection
capacity. Failed observations are not retried.
Each independent inbox long poll reads its recipient before that extension confirms
the message, so daemon acknowledgments cannot swallow that observation. Extensions
validate and confirm independently as each board post returns; a delayed post on
another board, a stream or another extension must not consume their harness response
windows. Unknown or failed post outcomes never authorize a receipt. All stream and poll checks still have to pass. At the
default topology this adds 500 observer inbox requests per round, alongside the
daemons’ own traffic. The observers never acknowledge. HTTP request-write callbacks
establish that all observer requests were transmitted before posting; the public
API supplies no proof that each server handler has entered its waiting state.

The JSON report contains topology, setup and measurement seconds, successful posts
per second for this post-and-drain cadence, throttled requests, correctness counts, and latency distributions in
milliseconds (sample count, p50, p95 and p99):

`resources.samples` records elapsed seconds, phase, server RSS bytes and interval
CPU percent, sampled once per second through setup and measurement. CPU comes from
the change in the process's cumulative CPU time divided by elapsed time; the first
sample has no CPU value, and OS accounting precision limits short intervals. CPU
can exceed 100% on multiple cores. Sampling errors are counted, never replaced with
invented zeros. Only the owned server PID is sampled, without its command arguments.
The sampler requires a POSIX `ps` (macOS or Linux); resource data is preserved on
failure. `resources.available` requires at least two successful samples.
Configured SSE counts are labelled as configuration, not an active gauge.

`concurrent_writers` reports its own successful posts, write-response distribution,
post-only elapsed time, throughput, throttles and verified read-backs. This timing
excludes read-back verification and delivery gathering. It is a bounded concurrent
workload, not an unlimited saturated-capacity claim.

- `request_to_write_response`: posting request start through decoding a successful
  response. Failed posts do not contribute a sample.
- `request_to_stream`: posting request start to each person's first head covering
  the message. This is a conservative upper bound on commit-to-stream latency.
  The public API does not expose an exact commit timestamp. A p99 below 100 ms
  therefore proves the commit-to-stream target; a slower result does not establish
  the server's exact commit latency.
- `request_to_long_poll`: posting request start to the addressed seat's inbox
  response. The request is started before posting and never advances its cursor.
- `request_to_handover`: posting request start to the fake extension receiving the
  message. This includes the production two-second gathering window.

Posts are sequential from the admin, with a full fan-out drain between rounds.
The throughput describes that workload, including gathering and confirmation; it
is not saturated write capacity or a 50-writer throughput claim. Fake extensions are
confirmed independently, before bounded parallel server cursor checks. Stream
observations and unrelated extensions cannot delay those confirmations beyond their
harness response windows.

The proof fails on missing, duplicate or out-of-order messages per permanent seat,
an incorrect acknowledgement cursor, residual unread messages, or an invalid board
chain. All boards are checked through the public audit command. Bodies, credentials
and login files are never included in the report. Child processes, sockets and all
state live in a private temporary directory and are removed after shutdown.

The command exits nonzero on correctness errors, run timeout, or a
`request_to_stream` p99 of at least 100 ms. Performance findings and SQLite tuning
are separate changes; this tool does not change server settings or limits.

## Failed runs

A failed run reports `status: incomplete`, the stage that failed, and the counters
collected so far. Setup time, measurement time, successful posts, observed deliveries,
and throttled requests are retained even when later checks fail. Incomplete samples
never count as a performance or correctness pass.

Before stopping child processes, the tool records whether each server or daemon
already exited and its numeric exit code when available. It also counts structured
warning and error log entries. A small fixed set of diagnostic categories may identify
open-file exhaustion, socket reset, timeout, SQLite busy/locked, and runtime panic.
These are observations from owned subprocess logs, not an inferred root cause.

Diagnostics contain process roles and fixture indices, counts and category names.
They contain no arbitrary log text, bodies, credentials, command input or log paths.
The tool still cancels and removes all child state after recording this summary. A
failed observation is not retried and does not relax limits, gathering or timeouts.

For an explicitly provisioned, disposable remote server, use `--server URL` with
`--admin-key-file PATH`. The URL must be an HTTPS origin (HTTP is accepted only on
loopback for tests). The key file must be a private regular file. The tool validates
the origin before reading the key and refuses HTTP redirects. It uses only that
issuer's credentials, starts local delivery daemons in scratch homes, and never
starts or stops the remote server. Provisioning creates people, boards and seats;
use a fresh temporary server, then destroy it yourself after the run.

Remote runs report local server resource sampling as unavailable. Measure the
remote machine separately, and report network round-trip measurements separately
from the end-to-end delivery samples.

Round diagnostics split observer preparation, delivery and cursor verification;
latency distributions include the maximum as well as percentiles. Cursor checks
run with at most sixteen requests in flight, after all fake harness confirmations.
