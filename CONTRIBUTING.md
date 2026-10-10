# Contributing

Aboard is early: the design still moves, and much of it is being built. Small fixes are
welcome as pull requests straight away. For anything larger, such as a new command, a
contract change or a new harness, open an issue first so we can agree on the shape
before you write the code.

[AGENTS.md](AGENTS.md) holds the project's rules in full. It is written for both people
and coding agents.

## Build

You need [Go 1.26](https://go.dev/dl/). The web UI also needs
[Node.js 20.9 or later](https://nodejs.org/).

```bash
make dev       # builds this checkout into .bin/aboard (with the UI if web/out exists)
make install   # builds the UI, then installs aboard with the UI into $(go env GOPATH)/bin
```

`make sandbox NAME=<name>` opens a shell where a dev build and every harness with a
profile (Claude Code, Codex, omp) use folders of their own, so testing a branch by hand
never touches your installed `aboard`, your boards or your harness settings. The next
section is the daily loop.

## Testing team flows on one machine

One team server can serve several isolated people on your laptop. Start it from the
checkout:

```sh
make sandbox-team TEAM=qa
make sandbox NAME=leo TEAM=qa
```

The server runs in team mode with `leo` as its first admin. The leo sandbox signs in
with that server's bootstrap key. In another terminal, open a second person:

```sh
make sandbox NAME=maya TEAM=qa
```

Maya's shell shows the qa server URL but has no login. Start a harness in each shell. Have leo's
agent create a board and invite maya, then paste the real invite prompt into maya's
agent. This exercises invite redemption, approvals and pairing on the same server.
Reopen either shell with the same command to keep its state. The prompt identifies
both the person and team, such as `(aboard:maya@qa)`. Run QA commands in those
shells: other terminals may still use your installed aboard instead of the dev build.

The team and its people live under `~/.aboard-sandboxes/teams/qa`. Each person has a
separate HOME, ABOARD_HOME and harness config folders. Codex copies the existing login
into private team state, so refreshing it cannot change the original. Claude Code uses
`CLAUDE_CODE_OAUTH_TOKEN` when you exported it before opening the shell. The server binds only to loopback, behind a local HTTPS proxy. Its
certificate is trusted by the sandbox CLI through `SSL_CERT_FILE`; for manual browser
QA, accept the local certificate warning for the printed address. No system trust
store is changed. This dev certificate must not be used for deployment.

Open a second team with another TEAM name to test server selection. Use
`make sandbox-update NAME=maya TEAM=qa` to restart that person's daemon on a new build.
To update the shared server too, run `make sandbox-team-stop TEAM=qa`, then
`make sandbox-team TEAM=qa`; the server address and all people's state are kept. To
remove the server and all its people's data:

```sh
make sandbox-team-clean TEAM=qa
```

This tool starts with the local dev binary installed. It does not simulate a machine
without aboard; the optional fresh-install mode is not available.

## Developing with a sandbox

A sandbox runs a build of your checkout next to your real Aboard install, with its own
server, delivery daemon, boards and harness folders, so you can start agents against it
and watch them in the board view. Nothing in it reads or writes your real `~/.aboard`,
`~/.claude`, `~/.codex` or `~/.omp`. It needs Go, Node 20.9 or later (the first build
installs the web UI's packages) and the harnesses you want to try on your `PATH`.

### The daily loop

1. Get the latest code: `cd ~/aboard && git pull` (in your main checkout, or in your
   own worktree on a branch).
2. Rebuild and restart the sandbox on it: `make sandbox-update NAME=dev`. It rebuilds
   the web UI when its sources changed, rebuilds `.bin/aboard`, stops the sandbox's
   server and daemon and starts them on the new build, and keeps the sandbox's boards
   and data. If the sandbox doesn't exist yet, this says so; create it with step 3.
3. Start one terminal per agent. In each: `make sandbox NAME=dev`, then run the harness:
   `claude`, `codex` or `omp`. The first `make sandbox` creates the sandbox; every later
   one opens another shell in it, without wiping it, and prints the same banner. A shell
   has `(aboard:dev)` in its prompt.
4. Open the board view at the `Board view:` address in the banner (or run `aboard open`
   in a sandbox shell).
5. When you are done: `make sandbox-clean NAME=dev` stops the sandbox's server and daemon
   and deletes its folder. Your real setup is not touched.

Agents already running when you run `make sandbox-update` keep the hooks they loaded;
restart them to pick up the new build.

### What is isolated, and how

A sandbox is a folder, `~/.aboard-sandboxes/<name>` (or under `ABOARD_SANDBOXES`):

| Folder | What it is |
| --- | --- |
| `bin/aboard` | A link to `.bin/aboard`, first on the shell's `PATH`. |
| `aboard-home/` | The sandbox's `ABOARD_HOME`: its server, data, delivery daemon and port. |
| `<harness>/` | One folder per harness profile in `adapters/` (`claude-code/`, `codex/`, `omp/`). The profile's `config_dir.env` points at it: `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR`. |
| `project/` | A git project set up with `aboard init --yes --scope project`, where the shell starts. |

The shell also drops the variables that would make a command take itself for another
session or use another Aboard: `ABOARD*`, the harness's session and sandbox markers from
its profile, and every variable that starts with the harness command in capitals
(`CLAUDE*`, `CODEX*`, `OMP*`). It turns harness auto-updates off.

### Logins per harness

A new config folder has no login. Each harness gets one like this:

- **Claude Code:** its keychain login doesn't carry to a new config folder. Run
  `claude setup-token` once, `export CLAUDE_CODE_OAUTH_TOKEN=<token>` in the terminal
  you run `make sandbox` from (add it to your shell profile to keep it), then open the
  sandbox again. Or run `/login` inside Claude Code. The sandbox pre-answers Claude
  Code's first-run questions.
- **Codex:** the profile's `login_files` (`auth.json`) is linked from your own
  `~/.codex` (or `CODEX_HOME`), never copied, so a refreshed token reaches both. If
  there is none, run `codex login` in the sandbox shell.
- **omp:** with `CLAUDE_CODE_OAUTH_TOKEN` set, the shell also sets
  `ANTHROPIC_OAUTH_TOKEN` to it, as the live suite does, so omp logs in to Anthropic.
  Otherwise log in inside omp. Your own `~/.omp/agent` is never used.

The banner says, per harness, how it logs in.

### Adding a harness so it is sandboxed

Only its profile: `adapters/<harness>/profile.yaml` needs `config_dir` (the variable the
harness reads its config folder from, and its default under your home folder),
`session_env` (the variables it sets for its sessions) and, if a file in the config
folder holds the login, `login_files`. `scripts/sandbox` reads the profiles, so the next
`make sandbox` gives the harness its own folder and drops its markers. A profile without
`config_dir` fails the harness conformance kit, and `scripts/sandbox` refuses it. [engineering/adding-a-harness.md](engineering/adding-a-harness.md)
has the rest of the checklist.

### Troubleshooting

- **`There is no dev build`:** run `make sandbox NAME=<name>` or `make dev`, which build it.
- **`make sandbox-update` says there is no sandbox:** create it with `make sandbox NAME=<name>`.
- **The board view looks old:** the sandbox embeds the UI built when you last ran
  `make sandbox` or `make sandbox-update`; run it again after changing `web/`.
- **A harness asks you to log in or answers onboarding questions:** see Logins above.
- **Hooks or the daemon use the wrong home:** start the harness from a sandbox shell
  (the prompt shows `(aboard:<name>)`), not from another terminal. Check with
  `echo $ABOARD_HOME`.
- **Something is stuck:** `make sandbox-clean NAME=<name>` stops the sandbox's server and
  daemon and removes it; then start again. Run `aboard down` only inside a sandbox shell,
  where it stops the sandbox's processes and not your own.
- **An old sandbox from before the `<harness>/` folders** (`claude-config/`,
  `codex-home/`): clean it and open it again.

## Test

```bash
make check
```

`make check` runs everything CI runs: formatting, lint, vet, the generated-code check,
the core's size budget, the README's harness table, `go test -race`, the end-to-end
tests, the harness conformance kit, the extension tests (which need
[Bun](https://bun.sh)) and `govulncheck`. The end-to-end tests also need
[tmux](https://github.com/tmux/tmux), for the swarm and launcher tests. Tools are pinned and installed into `.bin/` on
first use. A change to the web UI also runs `make web-check`.

`make quick` runs the static checks alone (formatting, lint, vet, the generated-code
check and the core's size) in about a minute; run it before asking for review.
`scripts/install-hooks` adds a git pre-push hook that runs it on every push, if you
want one. CI runs the full checks on every pull request, and a pull request merges once
they pass.

A change to delivery, setup or upgrades also passes `make live`, which drives real Claude
Code, Codex and omp sessions in tmux. It needs tmux and logged-in harnesses, spends model
turns, and gives each run its own home folder, so it never touches your real harness
settings. `make live HARNESS=<name>` runs one harness; `make live-affected` runs only
the harnesses your change touches. [e2e/live/PROOFS.md](e2e/live/PROOFS.md) says what it
proves.

How we test, and which tests a change needs, is in
[engineering/testing.md](engineering/testing.md).

## Docs and contracts first

- **The quickstart is the acceptance test.** [docs/quickstart.mdx](docs/quickstart.mdx)
  must keep working from scratch on a fresh machine after every change, and every
  command the docs show is covered by a test in `e2e/` or a step in
  [e2e/RELEASE_CHECKLIST.md](e2e/RELEASE_CHECKLIST.md).
- **Change the contract before the code.** The contracts live in [spec/](spec): the
  OpenAPI spec (Go types and handlers are generated from it; never edit generated code),
  the event types, the board file schema and the CLI's `--json` output. Contracts only
  grow; [spec/README.md](spec/README.md) has the checklist for a change.
- **Thin slices, failing test first.** Each change works end to end, from the CLI
  through the API to the record, and comes with the test that failed before it.

## Where decisions live

- [design/VISION.md](design/VISION.md): the design and what's in the first release.
- [design/DECISIONS.md](design/DECISIONS.md): every decision, with its reason. A new
  decision is recorded there.
- [design/PHILOSOPHY.md](design/PHILOSOPHY.md): how we keep the core small. Read it
  before adding anything to the server.
- [design/ROADMAP.md](design/ROADMAP.md): what's being built, and what's next.
- [engineering/](engineering): how we write Go, tests and text, and the
  [glossary](engineering/glossary.md).

## Commits and pull requests

Follow [engineering/writing.md](engineering/writing.md): a short imperative summary line
(under about 60 characters), a blank line, then a body saying why the change was made
and anything a reviewer needs to know. A pull request says what changed, why, and which
tests cover it.

## Security

Report vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes, never in a
public issue.
