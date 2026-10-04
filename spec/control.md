# The control socket

The delivery daemon's control socket is how everything on this machine talks to the
daemon: the hooks a harness runs, the `aboard` commands, and a harness extension that
holds a connection for as long as its session runs. This page is the contract for
everything sent over it. [delivery.md](delivery.md) says what the daemon does with it.

What we want: a hook, a command or an extension written against this page keeps working
with every daemon that speaks the same protocol version, and a mismatch is reported in
plain words rather than as a hang or a wrong delivery.

How Aboard does it: one Unix socket, one JSON object per line, a protocol version on the
first message of every connection, and messages that only grow. The examples on this
page are checked against the daemon's own types by `TestControlSpecExamplesMatchTheProtocol`
in `server/internal/delivery`, so a field named here is a field the daemon reads or
writes, spelled the same way.

## Transport

- **Where.** `<state>/aboard/daemon.sock`, or `$ABOARD_HOME/state/daemon.sock` when
  `ABOARD_HOME` is set. When that path is longer than a socket path may be (macOS allows
  104 bytes), the socket is `/tmp/aboard-<uid>/<hash>.sock` instead, where `<hash>` is
  the first 16 hex digits of the SHA-256 of the state directory. The directory is 0700
  and the socket 0600.
- **Who.** The daemon reads each connection's peer user from the kernel (`SO_PEERCRED`
  on Linux, `LOCAL_PEERCRED` on macOS) and closes a connection from any other user, or
  one whose user it can't read.
- **Framing.** Each message is one JSON object followed by a newline, at most 128 KiB
  including the newline. `<`, `>` and `&` are written as they are, not escaped. A message
  over the limit gets an `invalid_request` error and the connection is closed.
- **Unknown fields are ignored**, by the daemon and by every client, so a newer client
  can send a field an older daemon doesn't know and the reverse. Fields that are empty,
  zero or false are left out.
- **No daemon.** A client that can't connect starts the daemon (`aboard daemon start`,
  which returns once it answers) and connects again, except inside a harness's sandbox,
  where a daemon would inherit the sandbox (see delivery.md, "Never started inside a
  harness's sandbox").

## Versions

Every connection's first message carries `v`, the protocol version. This page is
version **1**.

- The daemon answers a first message whose `v` isn't its own with
  `daemon_protocol_mismatch` and closes the connection:

  ```json
  {"v":1,"error":{"code":"daemon_protocol_mismatch","message":"The running delivery daemon (aboard 0.1.0) speaks control protocol 1; this aboard speaks 2.","hint":"Install the same aboard as the running daemon, or run aboard down so this one starts its own."}}
  ```

- Later messages on the same connection (`received`, `claim`, and the extension
  connection's messages after `hello`) also carry `v`, and are read as the version the
  connection opened with.
- Additions don't change the version: new operations, new events, new optional fields
  and new error codes. A client that sends an operation the daemon lacks gets
  `invalid_request` ("The delivery daemon has no operation …"), which is how a client
  tells an older daemon apart. Only a change an older client would misread changes the
  version, following the deprecation path in [README.md](README.md).
- A newer `aboard` replaces a running daemon from an older build before it talks to it
  (delivery.md, "Upgrades"), so on one machine a mismatch lasts only until the next
  `aboard` command runs.

## Messages

A **request** goes from a client to the daemon. Every field but `v` and `op` is
optional; each operation says which it reads.

| Field | Type | Meaning |
| --- | --- | --- |
| `v` | integer | Protocol version |
| `op` | string | The operation |
| `harness` | string | The session's harness, as in its profile: `claude-code`, `codex` |
| `session` | string | The harness's own session id |
| `boot` | string | The session's boot id: changes whenever the session's process does. Empty means the one the daemon has on record |
| `source` | string | What started the session: `startup`, `resume`, `clear` or `compact` |
| `resumed` | boolean | The client reconnects after the daemon went away, so this isn't the session's next event |
| `wake` | boolean | A prompt that is the bundle a waiting hook just woke the session with, not a later event |
| `started` | string | When the hook's or command's process started (RFC 3339) |
| `agent` | object | An agent: `{"server","board","name"}` |
| `mode` | string | A delivery mode to set: `focused`, `all`, `humans` or `off` (`auto`, the earlier name of `all`, is accepted and saved as `all`) |
| `process` | object | The harness process the request came from: `{"pid","start"}`, `start` in the system's own units, so a reused pid isn't mistaken for it |
| `reply_to` | integer | The message whose replies a hold keeps out of bundles |
| `seqs` | array of integers | Messages a claim records as received |
| `id` | integer | The delivery an extension confirms |
| `cwd` | string | The extension session's working directory |
| `harness_version` | string | The harness's version, as it reports it |
| `extension_version` | string | The extension's own version |
| `subagent` | string | The harness's id for the subagent a hello comes from |
| `launch` | string | A launch ticket from `ABOARD_LAUNCH`, on `register` or `hello` (see "Launch tickets") |

A **response** goes from the daemon to a client: the answer to a request, or an event
on a connection that stays open.

| Field | Type | Meaning |
| --- | --- | --- |
| `v` | integer | Protocol version |
| `event` | string | On a connection that stays open: `waiting`, `deliver`, `release`, `welcome` |
| `bundle` | string | Messages in the delivery format (delivery.md, "The delivery format") |
| `id` | integer | The delivery a `deliver` event on an extension connection carries |
| `notice` | string | The waiting notice: names waiting messages without their content |
| `boot` | string | The session's boot id |
| `agents` | array of agents | The agents bound to the session |
| `reopened` | boolean | The session had closed and started again with the same id |
| `lost` | agent | The agent the session filled until another session resumed it |
| `previous` | agent | The agent a bind moved the session away from |
| `mode` | string | An agent's delivery mode |
| `changed` | boolean | The request changed the mode |
| `held` | boolean | A hold, or an inbox read's hold on the agent's deliveries, took effect |
| `claimed` | array of integers | The messages a claim recorded |
| `received` | array of integers | The agent's messages past its read position that a session here has received |
| `status` | object | The daemon's state, for `aboard doctor` (below) |
| `error` | object | `{"code","message","hint"}`, the same shape the CLI prints |

## Operations that answer once

The client sends one request and reads one response; then either side closes the
connection.

### `register`: a session started

Sent by a session-start hook. The daemon checks the session with the harness's delivery
adapter first (Codex: the thread exists and isn't a sub-agent), records it as open, and
binds it again to the agent it filled if it had closed (D157 in DECISIONS.md: a resumed
session reconnects by itself).

```json
{"v":1,"op":"register","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001","boot":"9a1f0c2b7d4e6f80","source":"resume","process":{"pid":4182,"start":1759500000}}
{"v":1,"boot":"9a1f0c2b7d4e6f80","agents":[{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"reviewer"}],"reopened":true}
```

With `launch`, the session was started by `aboard swarm up` (see "Launch tickets"
below): once registered, it is bound to the agent the ticket names, and the answer's
`agents` names that agent.

```json
{"v":1,"op":"register","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000002","boot":"7c3e1a9b0d2f4e68","source":"startup","launch":"lch_8f2a61c04b9d3e7a5c1f0e2d"}
{"v":1,"boot":"7c3e1a9b0d2f4e68","agents":[{"server":"http://127.0.0.1:7400","board":"docs","name":"claude"}]}
```

A new boot id makes every bundle handed to the session's old process and not confirmed
go again; a register with the boot on record confirms them. When another session resumed
the agent while this one was closed, the answer has no `agents` and names it in `lost`.

### `prompt`: a turn started

The session is busy: a waiting hook is released, and only the owner's messages reach it
until the turn ends. Unless `wake` is set, it is the session's next event and confirms
what was handed to it.

```json
{"v":1,"op":"prompt","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001","boot":"9a1f0c2b7d4e6f80","wake":true}
{"v":1}
```

### `turn_start`: a turn started, and what it is given

Sent by a hook of op `prompt` (Claude Code's and Codex's `UserPromptSubmit`) and by a
harness extension as a turn starts, before the model runs (omp: `before_agent_start`,
on a one-shot connection of its own). It does what `prompt` does, `wake` included, and
its answer's `bundle` carries every message still waiting for the agent in `focused`
mode, the quiet ones in their "while you were away" block (delivery.md, "Delivery
modes"), under 9,000 bytes; empty in any other mode or when nothing waits. The caller
adds it to the turn (as `additionalContext`, or as a message); it is confirmed by the
session's next event. A daemon from before this operation answers `invalid_request`, and
the hook then sends `prompt`.

```json
{"v":1,"op":"turn_start","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001","boot":"9a1f0c2b7d4e6f80","started":"2026-10-03T14:02:11.5Z"}
{"v":1,"bundle":"Aboard: while you were away, 1 other message arrived on writer-reviewer. They didn't wake you; read them, and answer only if one needs you:\n<aboard-messages board=\"writer-reviewer\" count=\"1\" quiet=\"true\">…</aboard-messages>"}
```

### `turn_end`: a turn ended

For a harness whose stop hook doesn't wait (Codex). Confirms what the turn's tool calls
received, marks the session idle, and lets the owner's messages that no tool call took go
to the harness's queue.

```json
{"v":1,"op":"turn_end","harness":"codex","session":"019a0000-0000-7000-8000-000000000001"}
{"v":1}
```

### `boundary`: what a tool boundary adds to the turn

Sent by a tool hook between tool calls of a busy turn. The answer carries the owner's
messages in `bundle` and the waiting notice in `notice`, either or both empty, together
under 9,000 bytes. `started` lets the daemon tell which bundles the session had before
this hook began. `urgent` is the same operation's name in earlier builds and is answered
the same way.

```json
{"v":1,"op":"boundary","harness":"codex","session":"019a0000-0000-7000-8000-000000000001","started":"2026-10-03T14:02:11.5Z"}
{"v":1,"bundle":"<aboard-messages board=\"writer-reviewer\" count=\"1\">…</aboard-messages>","notice":"<aboard-notice board=\"writer-reviewer\" waiting=\"1\">1 waiting on writer-reviewer: #7 from writer (owner_agent); run aboard inbox when convenient</aboard-notice>"}
```

### `end`: the session closed

Bundles handed and not confirmed wait for the session to come back or for another
session to resume its agent.

```json
{"v":1,"op":"end","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001"}
{"v":1}
```

### `bind`: an agent takes the session

Sent by `aboard pair`, `join` and `resume` run in a session. A session holds one agent;
`previous` names the one it moved away from. A harness whose delivery waits for an idle
hook must have registered first; one whose harness confirms on handing (Codex) can be
bound from any command run in the session.

```json
{"v":1,"op":"bind","harness":"codex","session":"019a0000-0000-7000-8000-000000000001","agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"reviewer"}}
{"v":1,"previous":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"writer"}}
```

### `agents`: who the session is

Sent by any command run in a session that needs its agent.

```json
{"v":1,"op":"agents","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001"}
{"v":1,"agents":[{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"writer"}]}
```

### `mode`: an agent's delivery mode

Without `mode` it shows the mode; with one it sets it (`aboard delivery`). The empty
agent names the default for agents without a mode of their own.

```json
{"v":1,"op":"mode","agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"writer"},"mode":"humans"}
{"v":1,"mode":"humans","changed":true}
```

An agent with no mode of its own, and no default, is `focused`. A mode saved as `auto` by
an earlier build is answered as `all`.

### `status`: the daemon's state

For `aboard doctor`, `status` and `down`. `build` is the daemon's build, the same fields
as the server's `GET /v1/info`; a daemon whose status has none is from an older aboard.
`stalled` lists deliveries handed to an idle session that started no turn within 10
seconds (reason `no_turn_started`), until a turn starts or the session closes; they are
never handed again because of it. A daemon from before stalls were tracked leaves the
field out. `bindings` lists every agent with a session, the session as
`<harness>:<id>`; `open` is true while that session is open, and `turned` once it has run a turn, which a harness needs before it can resume the session. A closed session keeps its
agent until another session takes it, which is how `aboard swarm up` finds the session
to resume.

```json
{"v":1,"op":"status"}
{"v":1,"status":{"pid":4182,"build":{"version":"0.1.0","commit":"3f9a0c1e2b4d","commit_time":"2026-10-03T09:00:00Z"},"open_sessions":2,"servers":[{"url":"http://127.0.0.1:7400","connected":true}],"attention":[{"id":12,"agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"writer"},"seqs":[9],"reason":"harness_error"}],"skipped":[],"stalled":[{"id":14,"agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"reviewer"},"seqs":[11],"reason":"no_turn_started"}],"agents":[],"bindings":[{"agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"writer"},"session":"claude-code:5f1c2d3e-0000-4000-8000-000000000001","open":true,"turned":true}]}}
```

## Connections that stay open

### `wait`: a stop hook waits while the session is idle

The hook's open connection is the idle signal. The daemon answers `waiting` once the
wait counts, then `deliver` with a bundle, or `release` when the session moves on (a
prompt, the session's end, another wait). After `deliver`, the hook sends `received` and
exits; the session's next event confirms the bundle.

```json
{"v":1,"op":"wait","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001","boot":"9a1f0c2b7d4e6f80","started":"2026-10-03T14:02:11.5Z","process":{"pid":4182,"start":1759500000}}
{"v":1,"event":"waiting"}
{"v":1,"event":"deliver","bundle":"<aboard-messages board=\"writer-reviewer\" count=\"1\">…</aboard-messages>"}
{"v":1,"op":"received"}
```

A wait from a hook that started before the session's latest prompt belongs to an earlier
turn: it gets `waiting` and then `release` at once. A hook whose connection closed
because the daemon went away connects again with `resumed`, so its wait doesn't count
as the session's next event, and any bundle handed to it then is handed again.

### `hold`: a command waits for a reply itself

`aboard say --wait-reply` holds a connection while it waits, so replies to `reply_to`
stay out of every bundle. `held` is false when no open session here holds the agent.
When the command shows messages, it sends `claim`, and the daemon records them as
received by the session, as if it had delivered them; `claimed` leaves out any already
handed to the session. The hold ends when the connection closes.

```json
{"v":1,"op":"hold","agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"reviewer"},"reply_to":9}
{"v":1,"held":true}
{"v":1,"op":"claim","seqs":[10]}
{"v":1,"claimed":[10]}
```

### `inbox`: a command reads an agent's inbox

`aboard inbox` and `aboard say` hold a connection while they read the agent's inbox on
the server, so a message can't reach the agent's session and the command at the same
time: until the connection closes, the daemon hands nothing to the agent's session and
gives no waiting notice for it (delivery.md, "Received once"). The answer lists in
`received` the agent's messages past its read position that a session here has
received, which the command leaves out; `held` is false, and nothing is held, when no
session here holds the agent. A command that runs in the agent's own session (not in a
subagent, and not `aboard inbox --peek`, which changes nothing) sends its `harness`,
`session`, `boot` and `started`: it is that session's next event, so a bundle handed
before `started` is confirmed first. The command sends nothing more; how far it then
acknowledges reaches the daemon from the server's stream, as any client's
acknowledgement does.

```json
{"v":1,"op":"inbox","harness":"claude-code","session":"5f1c2d3e-0000-4000-8000-000000000001","boot":"9a1f0c2b7d4e6f80","started":"2026-10-03T14:02:11.5Z","agent":{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"reviewer"}}
{"v":1,"held":true,"received":[13]}
```

## Launch tickets

What we want: a session `aboard swarm up` starts fills its agent's seat as it starts,
with no join line pasted and no command typed, and nothing else can take that seat by
copying the session's environment.

How Aboard does it: before it starts a session, `swarm up` writes a **launch ticket**,
a file `<state>/launches/<ticket>.json` holding the agent (`{"server","board","name"}`),
and the launcher puts the ticket in the session's environment as `ABOARD_LAUNCH`, with
`ABOARD_AGENT` naming the agent. The ticket is `lch_` and 24 hex digits. Whatever first
reports the session to the daemon hands it in as `launch`:

| Harness | Hands it in |
| --- | --- |
| Claude Code | The session-start hook's `register`, which sees Claude Code's environment |
| omp | The extension's `hello` |
| Codex | Its hooks don't see Codex's environment, so the first `aboard` command the session runs sends `register` with it (every Codex command carries `CODEX_THREAD_ID` and `ABOARD_LAUNCH`) |

The daemon registers the session as usual, then takes the ticket, which removes the
file, and binds the session to its agent as `bind` would. A ticket works once: a
harness started later from inside that session inherits `ABOARD_LAUNCH`, but the ticket
is gone, so it gets no seat. A ticket that is missing or malformed binds nothing and is
no error; the session goes on with no agent, and its commands still act as
`ABOARD_AGENT`. `swarm down`, and a later `swarm up`, remove tickets no session took.

## Who sends what

| Client | Operations |
| --- | --- |
| A hook of op `session-start` | `register` |
| A hook of op `prompt` | `turn_start` (with `wake` when the harness hands back a waiting hook's bundle as the prompt), or `prompt` when the daemon doesn't know `turn_start` |
| A hook of op `wait` | `wait`, then `received` |
| A hook of op `turn-end` | `turn_end` |
| A hook of op `tool` | `boundary` |
| A hook of op `end` | `end` |
| A hook of op `mark-subagent` | Nothing: it never contacts the daemon |
| `aboard pair`, `join`, `resume` in a session | `bind` |
| Any command that needs the session's agent | `agents`, or `register` for a session the daemon doesn't know yet |
| `aboard delivery`, `aboard init` | `mode` |
| `aboard say --wait-reply` | `hold`, then `claim` |
| `aboard inbox` | `inbox` |
| `aboard say` | `inbox`, for its note about the agent's own inbox |
| `aboard doctor`, `status`, `down`, `swarm up`, `swarm ps` | `status` |
| An `aboard` command in a Codex session whose launch ticket is still waiting | `register` with `launch` |
| A harness extension | The extension connection, below, `turn_start` and `boundary` |

A hook that gets an error, or can't reach the daemon, prints one line starting
`aboard hook:` to standard error and exits 0, so a broken daemon never blocks a session.

## Errors

| Code | When |
| --- | --- |
| `daemon_protocol_mismatch` | The first message's `v` isn't the daemon's version |
| `invalid_request` | An unknown operation, a message over 128 KiB, an unknown harness, a missing session, agent or `reply_to`, or an unknown delivery mode |
| `session_unknown` | `wait`, `bind` or `agents` for a session the daemon has no record of, for a harness that needs its hooks to register sessions |
| `codex_subagent_target` | `register` or `bind` for a sub-agent: messages go to the root conversation |
| `codex_target_absent` | `register` or `bind` for a session the harness says doesn't exist |
| `harness_unavailable` | The harness couldn't be asked about the session |
| `subagent_session` | `hello` from a subagent (extension connection, below) |
| `daemon_not_running` | The daemon is stopping |
| `internal` | The daemon couldn't read or write its journal |

The `codex_` codes are named for the first harness that gave them and keep their names;
any harness's adapter may return them.

## The extension connection

What we want: a harness with no shell hooks, only an extension API that runs inside the
harness's own process (omp's TypeScript extensions), gets the same delivery a hook-based
harness gets, with its open connection as the proof that the session is alive.

How Aboard does it: the extension holds one connection to the control socket per
session, for as long as the session runs. Over it the extension registers the session,
the daemon pushes bundles and the extension confirms them, and the extension reports
when turns start and end. A profile declares it with identity kind `extension`,
delivery method and capability `extension`, and lifecycle liveness `connection`. The
daemon serves it for any harness whose delivery adapter has that capability; omp's
extension (`adapters/omp/aboard.ts`) is the first client.

### Opening: `hello` and `welcome`

The extension connects when the session starts, and again when the harness switches the
process to another session. Its first message is `hello`:

```json
{"v":1,"op":"hello","harness":"omp","session":"0199a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d","boot":"4d2c9b1e0a7f6e5d","source":"startup","process":{"pid":51234,"start":1759500321},"cwd":"/Users/alex/projects/docs","harness_version":"18.5.1","extension_version":"0.1.0"}
{"v":1,"event":"welcome","boot":"4d2c9b1e0a7f6e5d","agents":[{"server":"http://127.0.0.1:7400","board":"writer-reviewer","name":"omp"}]}
```

| Field | Rule |
| --- | --- |
| `v` | Required. The protocol version the extension speaks |
| `harness` | Required. The harness's name in its profile |
| `session` | Required. The harness's stable session id (for omp, the session manager's id, not a model provider's) |
| `boot` | Required. A random id the extension makes once per harness process, so bundles handed to an earlier process go again |
| `source` | `startup` for a new session; `resume` when the harness reopened an earlier session with the same id. A resumed session is bound again to the agent it filled, as with `register` |
| `resumed` | True when the extension reconnects after the connection dropped, with the same session and boot |
| `process` | Required. The harness process: `pid`, and `start` when the extension can read it in the system's own units. When `start` is left out the daemon reads it from the process table as the hello arrives. The connection is the session's liveness; the process is what tells the daemon, after it restarted, that a session whose extension never came back has ended |
| `cwd`, `harness_version`, `extension_version` | Recommended. The daemon logs them with the session's start, for debugging a setup |
| `subagent` | Set only by an extension running inside a subagent. The daemon refuses it with `subagent_session`: messages go to the root conversation |

`welcome` carries what `register` answers: `boot`, `agents`, and `reopened` or `lost`
for a session that comes back. An error closes the connection. The daemon logs the hello
as `session started`, with the connection's `pid`, `cwd`, `harness_version` and
`extension_version`.

### While the session runs

| Direction | Message | Meaning |
| --- | --- | --- |
| extension → daemon | `{"v":1,"op":"prompt"}` | A turn started: the session is busy |
| extension → daemon | `{"v":1,"op":"turn_end"}` | The turn ended: the session is idle |
| daemon → extension | `{"v":1,"event":"deliver","id":41,"bundle":"…"}` | A bundle for the idle session |
| extension → daemon | `{"v":1,"op":"received","id":41}` | The extension added delivery 41 to the session |
| extension → daemon | `{"v":1,"op":"goodbye"}` | The session is closing |
| daemon → extension | `{"v":1,"event":"release"}` | This connection no longer serves the session; don't reconnect |

These messages name no session: the connection's `hello` did. The daemon sends nothing
back for `prompt`, `turn_end`, `received` or `goodbye`.

- **Delivery.** The daemon sends `deliver` only while the session is idle (after
  `welcome`, or after `turn_end`, until the next `prompt`). The extension adds the bundle
  to the session as a new turn (omp: `sendMessage` with `triggerTurn`), then answers
  `received` with the delivery's `id`. `received` confirms the delivery, as a harness's
  queue accepting a bundle does, and the daemon then acknowledges its messages on the
  server. The extension never adds a delivery twice: it remembers the ids it added for
  as long as the process runs, and answers `received` again for an id it has already
  added.
- **Busy.** After `prompt`, the daemon holds bundles until `turn_end`. For the owner's
  messages mid-turn, the extension sends `boundary` on a separate, one-shot connection
  at each tool boundary, exactly as a tool hook does, with `harness`, `session` and
  `boot`, and adds what comes back to the running turn (omp: `sendMessage` delivered as
  an aside). Its answer is confirmed by the session's next `prompt`, `turn_end` or
  `boundary`.
- **A turn's start.** Before the model runs a turn, the extension sends `turn_start` on
  a separate, one-shot connection, with `harness`, `session` and `boot`, and adds what
  comes back to the turn (omp: the message `before_agent_start` returns). Its answer is
  confirmed by the `prompt` that follows it on the connection.
- **Presence.** `prompt` reports the agent `working` and `turn_end` reports it `idle`,
  as the hooks do.
- **Closing.** `goodbye` closes the session as `end` does, and the daemon closes the
  connection. A connection that closes without `goodbye` also closes the session: the
  harness process ended or lost its connection. Bundles sent and not answered with
  `received` go again to the next connection for that session and boot, or to the next
  session that takes the agent.
- **One connection per session.** A `hello` for a session that already has a connection
  replaces it: the daemon sends `release` on the older one and closes it.

### Reconnecting

- When the connection drops without a `release`, the extension reconnects: after 200 ms,
  then doubling up to 5 seconds between tries, for as long as the session runs. If the
  socket isn't there, it runs `aboard daemon start` first, outside any sandbox the
  harness applies to commands.
- The reconnecting `hello` carries the same `session` and `boot`, `resumed: true`, and
  the same `source` as the first one. The daemon then sends again every delivery it sent
  on the earlier connection that wasn't confirmed; the extension answers `received` for
  those it already added, without adding them again.
- After `release`, or after the session closes, the extension doesn't reconnect.

### Version mismatch and older daemons

- `daemon_protocol_mismatch` (a different `v`) and `invalid_request` for `hello` (a daemon
  from before the extension connection) both mean this aboard and the extension don't
  match. The extension shows the error's `message` and `hint` once, in the harness's own
  way of telling the person (omp: a notification), and retries on the reconnect
  schedule with at most 30 seconds between tries, since running a newer `aboard`
  replaces an older daemon. It never falls back to another protocol version.
- The extension is installed by `aboard init` from the binary, so the extension and the
  daemon it talks to come from the same build unless the person upgraded one without the
  other; `aboard doctor` reports an installed extension that differs from the one this
  aboard installs, as it does for hooks.
