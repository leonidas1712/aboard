# launcher-bg

A launcher, in about 50 lines of Python, that runs each agent's session as a background
process. It is the example on the
[Extending Aboard](../../docs/extending.mdx#launchers) page, and it keeps the
[launcher protocol](../../spec/launcher.md): one JSON request on standard input, one
JSON response on standard output. Python 3 is all it needs.

## Run it

Put [aboard-launcher-bg](aboard-launcher-bg) on your `PATH` under that name. Then, from
a checkout of Aboard's repository, check it with the launcher kit:

```console
$ cp aboard-launcher-bg ~/.local/bin/
$ make launcher-kit LAUNCHER=bg
--- PASS: TestLauncherKit (5.65s)
    --- PASS: TestLauncherKit/UnknownOpIsRefused (0.13s)
    --- PASS: TestLauncherKit/WrongVersionIsRefused (0.10s)
    --- PASS: TestLauncherKit/StartRunsTheCommandWithItsFolderArgumentsAndEnvironment (0.62s)
    --- PASS: TestLauncherKit/TwoAgentsRunSideBySide (1.21s)
    --- PASS: TestLauncherKit/SecondStartOfARunningAgentIsRefused (0.62s)
    --- PASS: TestLauncherKit/StopEndsTheSessionAndIsIdempotent (0.71s)
    --- PASS: TestLauncherKit/StatusSeesTheSessionEndByItself (0.49s)
    --- PASS: TestLauncherKit/AnAgentStartsAgainAfterItStopped (1.04s)
PASS
```

Use it for an agent with `launcher: bg` in the board file, or for every agent with
`aboard swarm up --launcher bg`.

## How it works

- `info` lists only the `headless` mode, so `swarm up` gives it the command of Aboard's
  headless runner, which waits on the agent's inbox and runs one turn per batch of
  messages.
- `start` runs the request's `argv` as an argument vector, never through a shell, in
  `dir`, with `env` added to its own environment. It records the session's process id
  in a file under the temporary folder, named by swarm and agent, so a second `start`
  of a running agent is refused with `already_running`.
- The session's standard input, output and error go to `/dev/null`. A session that kept
  the launcher's output open would hold the call open, and Aboard would count it as
  failed.
- `start_new_session` puts the session in a process group of its own, so `stop` ends
  everything it started: SIGTERM, then SIGKILL if it is still running after 5 seconds.
- `status` and `stop` answer `running` or `exited` from whether the process still runs.

The end-to-end test `TestExampleLauncherPassesTheKit` runs the launcher kit against
this file on every change, and `TestExtendingPageShowsTheExampleLauncher` checks that
the docs page shows it unchanged.
