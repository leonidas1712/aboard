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

`make sandbox NAME=<name>` opens a shell where a dev build, Claude Code and Codex use
folders of their own, so testing a branch by hand never touches your installed `aboard`,
your boards or your harness settings. `make sandbox-clean NAME=<name>` removes it.

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
