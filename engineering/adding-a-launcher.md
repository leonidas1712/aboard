# Adding a launcher

What we want: anyone can host `aboard swarm up`'s sessions somewhere new (a terminal
manager such as Orca or OpenRig, a container, a cluster's scheduler) by writing one
small program, and nothing else in Aboard changes.

How Aboard does it: a launcher only hosts sessions. It starts one, says whether it still
runs, and stops it. Everything else is Aboard's, the same for every launcher: the board
file, the board, each agent's seat, the harness's command line (from its profile), the
agent's identity in the session's environment, resuming an agent's last session, and
waiting until each session has taken its seat. One protocol and one kit make a launcher
supported. This page is the checklist.

## The pieces

| Piece | What it is |
| --- | --- |
| [spec/launcher.md](../spec/launcher.md) | The contract: `info`, `start`, `status` and `stop`, one JSON request on standard input and one response on standard output, the error codes, and what `env` carries |
| `server/internal/launcher` | The port: the `Launcher` interface and the protocol's types |
| `server/internal/launcher/tmux`, `headless` | The built-in launchers, in Go behind the port |
| `server/internal/launcher/external` | Runs an `aboard-launcher-<name>` command from the `PATH` through the protocol |
| `server/internal/launcher/launchertest` | The kit: `Run` for any `Launcher`, `RunCommand` for an external command |
| [launchers/herdr](../launchers/herdr) | The first external launcher, a standalone program using only the standard library: an example to copy |

`aboard swarm up` (`server/internal/cli/swarm.go`) picks a launcher by name: `tmux` and
`headless` are built in; any other name runs `aboard-launcher-<name>`. Built-in or not,
it calls the same four operations.

## The checklist

1. **Decide where a swarm lives.** Every `start` names a `swarm` (one per board on this
   machine, such as `aboard-docs-3f9a0c`). Keep a swarm's sessions together and apart
   from everything the person runs: tmux uses a server of its own (`tmux -L <swarm>`),
   herdr a session of its own (`herdr --session <swarm>`). Never touch the person's own
   sessions, workspaces or config, and remove what you made for a swarm when its last
   session stops.
2. **Start the command as given.** `argv` is an argument vector: never run it through a
   shell. Run it in `dir`, with every variable in `env` on top of the environment you
   would give it anyway. `env` holds the agent's identity (`ABOARD_AGENT`, the launch
   ticket `ABOARD_LAUNCH`); a session that loses it starts with no seat. Make sure one
   session's variables never reach another's (tmux's `-e` on `new-session`, for
   example, sets them for every later window too).
3. **Return a handle** that names the session for as long as it runs, across processes:
   `status` and `stop` get it back from another `aboard` run. Add an `attach` line a
   person can run to watch it, when there is one.
4. **Report the truth in `status`**: `running`, `exited` once the program ended (by
   itself or by `stop`), `unknown` only when you can't tell.
5. **Stop it properly**: end the session's processes (hang up, then terminate, then
   kill), answer `exited` once they are gone, and answer `exited` again, with no error, for
   a session that already ended.
6. **Refuse what you must** with the codes in spec/launcher.md: `already_running`,
   `mode_unsupported`, `launcher_protocol_mismatch`, `invalid_request`, and
   `host_unavailable` with a hint saying how to install the host.
7. **Pass the kit.** Put the command on your `PATH` and run
   `make launcher-kit LAUNCHER=<name>`. It needs no harness and no model: a shell script
   stands in for the harness. For a launcher in this repository, also run the kit from
   `make test` (see `launchertest/herdr_test.go`, which runs the herdr launcher against a
   stand-in for herdr, `e2e/fakeherdr`), and against the real host in the live suite
   (`TestHerdrLauncherPassesTheKit`).
8. **Prove it with `swarm up`.** An e2e test starts agents through it with
   `e2e/fakeagent` (`TestSwarmUpThroughTheHerdrLauncher`), and the live suite with real
   harnesses (`TestSwarmUpStartsEveryHarness`).
9. **Document it**: a row in [docs/swarm.mdx](../docs/swarm.mdx)'s launcher table, with
   how to install it and how a person watches a session.

## What a launcher never does

It never talks to the Aboard server, holds a token, creates seats or decides which
agents run: those stay with `swarm up` on the person's login. It never schedules or
restarts sessions on its own (`swarm up` starts them once). A launcher that would start
sessions on another machine is a different design, the opt-in runner, which needs its
own decision.
