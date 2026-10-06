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

Each round posts one message per board, addressed to every agent on that board.
Every message has a unique marker. A person's stream must first report its boards
before measurement starts. Streams may coalesce head updates: observing a head at
or above a message's sequence proves that the stream has reached that message.
Independent inbox long polls observe each recipient before its extension confirms
the delivery, so daemon acknowledgments cannot swallow those observations.

The JSON report contains topology, setup and measurement seconds, successful posts
per second, throttled requests, correctness counts, and latency distributions in
milliseconds (sample count, p50, p95 and p99):

- `request_to_stream`: posting request start to each person's first head covering
  the message. This is a conservative upper bound on commit-to-stream latency.
  The public API does not expose an exact commit timestamp. A p99 below 100 ms
  therefore proves the commit-to-stream target; a slower result does not establish
  the server's exact commit latency.
- `request_to_long_poll`: posting request start to the addressed seat's inbox
  response. The request is started before posting and never advances its cursor.
- `request_to_handover`: posting request start to the fake extension receiving the
  message. This includes the production two-second gathering window.

The proof fails on missing, duplicate or out-of-order messages per permanent seat,
an incorrect acknowledgement cursor, residual unread messages, or an invalid board
chain. All boards are checked through the public audit command. Bodies, credentials
and login files are never included in the report. Child processes, sockets and all
state live in a private temporary directory and are removed after shutdown.

The command exits nonzero on correctness errors, run timeout, or a
`request_to_stream` p99 of at least 100 ms. Performance findings and SQLite tuning
are separate changes; this tool does not change server settings or limits.
