# The launcher protocol

What we want: `aboard swarm up` can hand an agent's session to any host (a tmux
window, a herdr pane, a background process, a container), and adding a host means
writing one small program, with nothing else in Aboard changing.

How Aboard does it: a **launcher** starts a session, reports whether it still runs,
and stops it. Aboard does everything else: it creates the board and the agent's seat,
builds the harness's command line from its profile, and puts the agent's identity in
the session's environment. The launcher never talks to the Aboard server and never
holds a token.

Two launchers are built into `aboard`: `tmux` and `headless`. Any other name `N` runs
the command `aboard-launcher-N` from the `PATH` (for example `aboard-launcher-herdr`,
in [launchers/herdr](../launchers/herdr)). Built-in and external launchers answer the
same three operations, and pass the same kit (below).

## Calls

Aboard runs the launcher's command with no arguments, writes **one** JSON request to its
standard input and closes it. The launcher writes **one** JSON response to standard
output and exits: with status 0 when the response is an answer, and with any other
status when it is an error. Anything on standard error is for people: Aboard shows it
when a call fails. A call that hasn't answered within 60 seconds is killed and counts as
failed (`launcher_failed`).

Every request and response carries `v`, the protocol version. This page is version
**1**. A launcher that gets a version it doesn't speak answers `launcher_protocol_mismatch`.
Fields are only added; a launcher ignores request fields it doesn't know.

### `info`: what the launcher is

```json
{"v":1,"op":"info"}
{"v":1,"name":"herdr","modes":["interactive"]}
```

`modes` lists the run modes it starts: `interactive` (a terminal session a person can
watch and type in) and `headless` (a background process with no terminal). Aboard sends
`start` only for a mode the launcher lists.

### `start`: start one agent's session

```json
{"v":1,"op":"start","swarm":"aboard-docs","agent":"claude","harness":"claude-code","mode":"interactive","argv":["claude","--model","claude-sonnet-5-5","You are claude on the Aboard board docs. Run aboard status now, then wait for messages."],"env":{"ABOARD_AGENT":"claude","ABOARD_LAUNCH":"lch_8f2a61c04b9d3e7a5c1f0e2d"},"dir":"/Users/alex/projects/docs"}
{"v":1,"handle":"aboard-docs:@3","attach":"tmux -L aboard-docs attach -t claude"}
```

| Request field | Meaning |
| --- | --- |
| `swarm` | The group this session belongs to: one per board on this machine. A launcher keeps a swarm's sessions together (one tmux server, one herdr session) and apart from everything else the person runs |
| `agent` | The agent's name, unique within the swarm. Name the window or pane after it |
| `harness` | The harness, as in its profile (`claude-code`, `codex`, `omp`), for display and logs |
| `mode` | `interactive` or `headless` |
| `argv` | The command line to run, as an argument vector. Run it as given, never through a shell that would split or expand it |
| `env` | Variables the session must have, on top of the environment the launcher would give it anyway. They carry the agent's identity (below), so a launcher that drops them starts a session with no seat |
| `dir` | The absolute folder to run it in |

| Response field | Meaning |
| --- | --- |
| `handle` | Required. The launcher's own reference to the session, a string Aboard stores and passes back unchanged to `status` and `stop`. It must stay valid for as long as the session runs, across Aboard processes |
| `attach` | Optional. One line a person runs to watch the session, which `swarm up` and `swarm ps` print |
| `pid` | Optional. The process id of the session's main process, when the launcher knows it |

A `start` for an agent whose session in the swarm still runs fails with
`already_running`; Aboard checks `status` first, so it only sees this when something
else started one.

### `status`: does the session still run?

```json
{"v":1,"op":"status","swarm":"aboard-docs","agent":"claude","handle":"aboard-docs:@3"}
{"v":1,"state":"running"}
```

`state` is `running`, `exited` (it ran and has ended, or the launcher has no such
session any more) or `unknown` (the launcher can't tell, for example because its host
isn't reachable). Aboard treats `unknown` as running for `swarm up`, so it never starts a
second session beside one that may still run, and shows it as is in `swarm ps`.

`blocked` is optional: `true` when the session runs but waits for the person, such as a
harness asking whether to trust the folder as it starts. Only a launcher that can tell
sends it (the herdr launcher passes on herdr's `agent_status` of `blocked`); leaving it
out means "can't tell". While `swarm up` waits for a seat, it says which agent waits on a
question and gives its attach line.

```json
{"v":1,"state":"running","blocked":true}
```

### `stop`: end the session

```json
{"v":1,"op":"stop","swarm":"aboard-docs","agent":"claude","handle":"aboard-docs:@3"}
{"v":1,"state":"exited"}
```

The launcher ends the session's processes (closing a terminal window does this, as for
a person closing it) and answers once they are gone, or after at most 10 seconds.
Stopping a session that already ended answers `exited`, not an error. When a swarm's
last session stops, the launcher removes what it made for the swarm (the tmux server,
the herdr session).

## Errors

```json
{"v":1,"error":{"code":"already_running","message":"claude already runs in swarm aboard-docs.","hint":"Stop it first with aboard swarm down claude."}}
```

The same shape as every Aboard error. Codes a launcher uses:

| Code | When |
| --- | --- |
| `invalid_request` | A field is missing or malformed, or the op is unknown |
| `launcher_protocol_mismatch` | `v` is a version the launcher doesn't speak |
| `mode_unsupported` | `start` asked for a mode `info` doesn't list |
| `already_running` | `start` for an agent whose session still runs |
| `host_unavailable` | The host program (tmux, herdr) isn't installed or won't start; the hint says how to install it |
| `start_failed` | The host refused to start the session |

Aboard reports a failed call as `launcher_failed`, with the launcher's own code, message
and standard error in its details, and a launcher it can't find as `launcher_not_found`.

## The identity in `env`

A launched session never pastes a join line. `env` holds:

| Variable | Meaning |
| --- | --- |
| `ABOARD_AGENT` | The agent's name. Every `aboard` command the session runs acts as this agent, and so on its board |
| `ABOARD_LAUNCH` | A one-time launch ticket. The session's first contact with the delivery daemon (Claude Code's session-start hook, omp's extension) hands it in, and the daemon binds the session to the agent the ticket names. A ticket works once: a harness started later from inside that session inherits the variable but can't take the seat (spec/control.md, "Launch tickets") |
| `ABOARD_HOME`, `ABOARD_LOCAL_ADDR` | Passed on when `swarm up` runs with them, so the session uses the same Aboard |

A harness whose profile says `interactive.launch: prompt` (Codex) gets neither
`ABOARD_AGENT` nor `ABOARD_LAUNCH`: it runs its sessions and their commands in a
long-running process that may have been started outside the swarm and may serve the
person's other sessions, so the environment would either not reach the session or reach
sessions that aren't the agent. Its ticket is in the first prompt instead (`argv`), on
a first line asking the agent to run `aboard status --launch <ticket>`.

A session resumed by `swarm up` (the harness's own resume, with the session id the
agent last had) gets `ABOARD_AGENT` (unless its profile says `launch: prompt`) but no
ticket: the resumed session binds again to its agent by itself.

## The kit

`make launcher-kit LAUNCHER=<name>` runs the launcher kit
(`server/internal/launcher/launchertest`) against a launcher, built in or on the `PATH`,
with a stand-in for the harness (a shell script), so it needs no harness and no model.
It checks that:

- `info` names the launcher and its modes;
- `start` returns a handle, and the process runs in `dir` with exactly `argv` (spaces and
  quotes intact) and every variable in `env`;
- `status` says `running` while it runs, and `exited` once it ends by itself;
- two agents in one swarm run side by side, each with its own handle;
- a second `start` for a running agent fails with `already_running`;
- `stop` ends the process, `status` then says `exited`, and a second `stop` answers
  `exited`;
- an agent can be started again after it stopped;
- an unknown op and a wrong version are refused with their codes.

A launcher is supported when it passes the kit; `make check` runs it against `tmux`,
`headless` and the herdr launcher (with a stand-in for herdr), and the live suite runs it
against the real herdr.
