# How we package, release and update Aboard

What we want: a person installs one thing, trusts what they installed, and upgrades
while their sessions are open without anything breaking. Teammates who upgrade on
different days keep working together.

How Aboard does it: one binary per platform carries everything (the CLI, the delivery
daemon, the server and the web UI), and everything Aboard installs elsewhere is written
by that binary, so the pieces on one machine can't drift apart. Each section below says
what exists today and what is still to build; [design/ROADMAP.md](../design/ROADMAP.md)
tracks the work.

## Packaging

- **One static binary per platform**: macOS and Linux, on ARM and Intel, built with
  `CGO_ENABLED=0` and the `ui` tag, so the web UI's static build is embedded. A build
  stamps its version with `-ldflags -X`; Go adds the commit and commit time.
  *Today:* `make install` builds and installs from source with the UI, and `make dev`
  builds `./.bin/aboard` as a dev build (`0.1.0+dev.<commit>`) that never replaces an
  installed one.
- **Built by one automated job.** Pushing a version tag runs GoReleaser in CI, which
  builds every platform, writes a `checksums.txt`, signs it, generates a software bill
  of materials (SBOM) per archive, and publishes them with the release. Nothing is
  built or uploaded by hand. *To build.*
- **Signed.** The checksums file is signed with Sigstore's cosign using the release
  job's identity, so anyone can check a download came from this repository's release
  job. macOS binaries are also signed and notarized, so Gatekeeper accepts them.
  People run Aboard with agents acting on their machines; what they install must be
  checkable. *To build.*

## Install paths

| Path | For | Today |
| --- | --- | --- |
| `curl -fsSL <install URL> \| sh` | Anyone on macOS or Linux | To build. The script picks the platform's archive, verifies it against the signed checksums, installs `aboard` to `~/.local/bin` (or a directory given with `ABOARD_INSTALL_DIR`) and says if that directory isn't on the `PATH`. |
| `brew install <tap>/aboard` | macOS and Linux with Homebrew | To build. A tap the release job updates. |
| A container image | Team servers | Built from the root `Dockerfile` by hand (`docker build --build-arg VERSION=…`); publishing it from the release job is to build. Runs `aboard serve --team` as a non-root user with its data in `/data/aboard` on a volume at `/data`. |
| `make install` | Building from source | Yes. Needs Go and Node. |

All of them install the same binary. The skill published for `npx skills` is generated
from the same templates `aboard init` writes, so the two can't differ.

## What updates, and the rules for each

Three things update separately. Each has a rule so that an upgrade never breaks a
session that is already running.

| What | How it updates | Rule | Today |
| --- | --- | --- | --- |
| The binary (CLI, daemon, local server) | A package manager, the install script, or `aboard upgrade` | Never installed silently. A command in a terminal says once a day that a newer release exists. A running daemon or local server from an older build is replaced by the first newer command or hook that reaches it. | Replacement: yes. Notice and `aboard upgrade`: to build |
| Files installed into harnesses (the skill, hook entries, allow rules) | `aboard init --yes` | Hooks run the installed binary by its path, so a new binary takes effect without rewriting them. `aboard doctor` reports a file that differs from what this build would write; the install manifest tells an outdated file from one the person edited. | Yes |
| Team servers | A new binary or image, then a restart | Migrations run forward only, on start, in one transaction after a backup of the database. A binary older than its data refuses to start. | Yes |

### No silent installs

Aboard never downloads and runs new code on its own. The update notice is one line on
standard error, shown only to a person in a terminal: never in `--json` output, in
hooks or inside a harness session, and not at all with `ABOARD_NO_UPDATE_CHECK=1`. It
checks for a release at most once a day. `aboard upgrade` installs the latest release
the way it was first installed (it defers to Homebrew for a Homebrew install), then
runs `aboard init --yes` to update the skill and hooks in place.

### Installed files are compared by content, not stamped

Aboard doesn't write a version into the files it installs. A version mark would change
every hook entry on every upgrade, and Claude Code and Codex ask the person to trust
hooks again whenever an entry changes. Instead:

- `aboard doctor` compares each installed file, or Aboard's entries in a file it shares
  with the person (such as `.claude/settings.json`), with what this build's
  `aboard init` would write, and reports a difference as a warning. *Today.*
- `aboard init` records what it wrote in an install manifest in Aboard's own state
  (`installs.json` in the state folder, so `$ABOARD_HOME/state/installs.json` under
  `ABOARD_HOME`): each file's path, the harness, the scope, the version of `aboard`
  that wrote it, and a SHA-256 of the content it wrote (of Aboard's hook entries only,
  in a file shared with the person). Doctor then says which version wrote a file, and
  tells an outdated file (unchanged since an older `aboard` wrote it, `hooks_outdated`
  or `skill_outdated`; `init --yes` updates it) from one the person edited
  (`hooks_edited` or `skill_edited`; `init` marks it edited in the list it shows
  before replacing it). *Today.*

### Removing Aboard

`aboard uninstall` is the way out, and it relies on the manifest too. It stops the local
server and the delivery daemon, takes Aboard's entries out of files it shares with the
person, and deletes the files it owns, in every scope and project the manifest records
(and, for installs from before the manifest, in both scopes found by content). A file
the person edited is kept and named. Data stays unless `--data` is given, and the
binary always stays: uninstall prints the command that removes it, since only the
person knows how it was installed (`rm <path>`, or `brew uninstall aboard` for a
Homebrew install). [docs/install.mdx](../docs/install.mdx) is the page for people and
agents. *Today.*

### Migrations

The database schema is a sequence of numbered SQL migrations embedded in the binary
(`server/internal/store/sqlite/migrations`). They run forward only, on start. There are
no down migrations: going back means restoring a backup. Data written by a newer
schema is refused with `data_newer`, so an older binary never misreads it.

Before applying any migration, the server copies the database with `VACUUM INTO` (a
consistent copy taken while it is open) to `backups/aboard-<time>-schema-<n>.db` next
to it and keeps the newest three. The folder is created owner-only, and one that is a
link, a file or open to others stops the start before anything is copied; each copy is
created owner-only before SQLite writes it. Every pending migration then runs in one
transaction, so a failed upgrade leaves the database as it was and the error names the
copy. Going back after an upgrade that worked means stopping the server, putting a copy
in place of `aboard.db` (and removing `aboard.db-wal` and `aboard.db-shm`), and starting
the older binary. The local server does the same; it is a team server with one person
(D184, D199). *Today.*

Every released schema keeps a fixture database, and a test migrates each one forward
([testing.md](testing.md#practices)).

## Version skew

Teammates won't upgrade on the same day, and a team server lags behind its clients. The
policy, written for `0.x` where the minor number is the release step (after `1.0` it
becomes the major number):

- **A CLI, daemon or SDK works with a server one minor version older or newer.** A
  `0.4` CLI works with `0.3`, `0.4` and `0.5` servers. Within that window every
  operation both sides know works, because contracts only grow ([spec/README.md](../spec/README.md)).
- **Newer client, older server:** the client reads the server's API version and
  features from `GET /v1/info`, uses only what the server lists, and for anything else
  fails with `server_outdated`, naming the feature and the fix. *Today:* the client
  reports `server_outdated` when an older server answers `not_found` or
  `not_implemented` for an operation in the spec; the feature list is to build.
- **Outside the window**, `aboard doctor` reports `version_skew` as a warning in plain
  words ("this server runs 0.2; aboard 0.5 supports 0.4 to 0.6; upgrade the server or
  ask its admin"), and commands still run, failing with named errors rather than
  silently. *To build, in the team step.*
- **On one machine there is no skew:** the daemon and local server are replaced by the
  newest binary that reaches them.
- **Installed harness files:** a binary accepts hook entries written by the previous
  minor version, so an upgrade works before `aboard init --yes` runs.

## Release channels

After launch: a stable channel and a beta channel, chosen with
`aboard upgrade --channel beta`, so people who opt in meet problems before everyone
else. Until then, every release is stable.

## Changelog

`CHANGELOG.md` has one section per release, with a draft written from the commit
messages and then edited for readers: **Added**, **Changed**, **Fixed**, and
**Contract changes**. Contract changes lists every change under `/spec`, each saying
what changed, who is affected (CLI scripts, API clients, delivery daemons, harness
adapters) and whether it is additive. *To build, with the first release.*

## Landing a pull request

What we want: nothing reaches `main` without passing the checks its change calls for,
and landing a PR is one command that a person, a session or an agent runs the same way.

GitHub Actions doesn't run the checks today, so local checks are the merge gate, and
`scripts/land-pr <number>` applies them:

1. It finds the PR's branch with `gh`, and uses the worktree that already has it
   checked out or creates one at `.claude/worktrees/land-pr-<number>`. It never
   switches branches, pulls or commits in the main checkout.
2. It merges `origin/main` into the branch, and stops on a conflict, naming the files
   and leaving the merge in the worktree to resolve.
3. It runs the checks for what changed against `origin/main`: none for a change to only
   `design/` or `engineering/`; otherwise `make fmt-check lint vet generate-check
   core-size harness-table-check test e2e`, plus `make web-check` when `web/` changed.
   It says what it runs and why.
4. It pushes, merges with `gh pr merge --merge` (retrying while GitHub says the base
   branch was modified), and confirms GitHub reports the PR merged.
5. Only then does it remove what it created (the temporary worktree and local branch)
   and delete the branch on GitHub. A failure at any step leaves everything in place
   and says what to do next.

`--dry-run` prints the plan, including any conflict with `main`, and changes nothing.
It notes when a change touches delivery, setup or upgrades, which must pass `make live`
before merging. `--live` runs `make live-affected` after the checks, which needs harness
logins, and merges only if it passes; without it, the script doesn't run the live tests. The script's header
lists its exit codes, and `e2e/landpr_test.go` runs it against a local repository
with a fake `gh`.

Once GitHub Actions runs the checks again, the script merges only after they pass on
GitHub, and the local run becomes a first check rather than the gate. *To build.*

## Cutting a release

1. `make check` and `make web-check` pass on `main`.
2. `make live` passes on a machine with Claude Code and Codex logged in
   ([e2e/live/PROOFS.md](../e2e/live/PROOFS.md)), and the steps by hand in
   [e2e/RELEASE_CHECKLIST.md](../e2e/RELEASE_CHECKLIST.md) are checked in a sandbox.
3. The changelog's section for the version is written, including contract changes.
4. Bump `version` in `server/internal/cli/build.go`, commit, and push a `vX.Y.Z` tag.
   The release job builds, signs and publishes everything.
5. Install from the published script on a clean machine and run the quickstart.

After launch, a nightly job runs the live suite against the latest Claude Code and
Codex releases, so a harness update that breaks delivery shows up before a person
meets it. It needs harness logins in CI or a self-hosted runner.
