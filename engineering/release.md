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
- **Built by one automated job.** Pushing a version tag runs GoReleaser in CI
  (`.github/workflows/release.yml` with `.goreleaser.yaml`), which builds every
  platform, writes a `checksums.txt`, signs it, generates a software bill of materials
  (SBOM) per archive, and publishes them with the release. Each archive,
  `aboard_<version>_<os>_<arch>.tar.gz`, holds `aboard`, the shipped launchers
  (`aboard-launcher-<name>`), `LICENSE` and `README.md`, as plain files with no folder.
  Nothing is built or uploaded by hand. *Today:* the job and `make release-snapshot`,
  which builds every archive into `dist/` and publishes nothing; no release is
  published yet.
- **Signed.** The checksums file is signed with Sigstore's cosign, keyless, under the
  release job's GitHub identity: the bundle `checksums.txt.sigstore.json` holds a
  certificate naming `https://github.com/leonidas1712/aboard/.github/workflows/release.yml@refs/tags/v<version>`,
  issued through `https://token.actions.githubusercontent.com`. Anyone can check a
  download came from this repository's release job on that tag. The server image is
  signed the same way. People run aboard with agents acting on their machines; what
  they install must be checkable. *Today*, with the release job.
- **macOS signing and notarization: not done, by decision.** aboard has no Apple
  Developer account, so its macOS binaries are neither signed with a Developer ID nor
  notarized. What that means:
  - The install script downloads with curl and `aboard upgrade` with its own HTTP
    client; neither marks files with macOS's quarantine attribute, and Gatekeeper only
    assesses quarantined files, so these installs run without a prompt. (This is how curl and Gatekeeper
    behave today; the release checklist confirms it on a clean Mac each release.)
  - An archive downloaded with a browser is quarantined, so macOS blocks `aboard` the
    first time it runs. The person can allow it in System Settings → Privacy &
    Security ("Open Anyway"), or install with the script instead.
  - Integrity doesn't rest on Apple's signature: the cosign-signed checksums tie every
    archive to this repository's release job.
  - Once an account exists, the release job signs and notarizes with GoReleaser's
    `notarize` section. It will need these repository secrets: `MACOS_SIGN_P12` (the
    Developer ID Application certificate, base64), `MACOS_SIGN_PASSWORD`,
    `MACOS_NOTARY_KEY` (an App Store Connect API key, base64), `MACOS_NOTARY_KEY_ID`
    and `MACOS_NOTARY_ISSUER_ID`. A Homebrew cask then no longer needs to clear the
    quarantine attribute.

## Install paths

| Path | For | Today |
| --- | --- | --- |
| `curl -fsSL https://github.com/leonidas1712/aboard/releases/latest/download/install.sh \| sh` | Anyone on macOS or Linux | Yes, once a release is published (`scripts/install.sh`, attached to every release). The script picks the platform's archive, checks the checksums' signature when cosign is installed (and prints the command otherwise), verifies the archive against them, refuses an archive with anything but plain files, installs `aboard` and the launchers to `~/.local/bin` (or `ABOARD_INSTALL_DIR`) by renaming each into place, and says if that folder isn't on the `PATH`. `ABOARD_VERSION` picks a version. `e2e/installscript_test.go` runs it against a fake release server. |
| `brew install leonidas1712/aboard/aboard` | macOS and Linux with Homebrew | Configured but off. To turn it on: create the public repository `leonidas1712/homebrew-aboard`; add a fine-grained token with contents write on that repository only as the secret `HOMEBREW_TAP_TOKEN`; pass it to the release step's environment; set `skip_upload: false` under `homebrew_casks` in `.goreleaser.yaml`; and, while the macOS binaries aren't notarized, add a post-install hook that clears the quarantine attribute Homebrew sets on casks. |
| `docker pull ghcr.io/leonidas1712/aboard:<version>` | Team servers | With the release job: a multi-arch image (linux/amd64, linux/arm64) built from the release binaries, tagged with the version and, for a release that isn't a prerelease, `latest`. It is built from `Dockerfile.release`, which copies the release binary from the build context's `$TARGETPLATFORM/aboard` into the same image the root `Dockerfile` builds from source: alpine, user 10001, `aboard serve --team` with its data in `/data/aboard` on a volume at `/data`. Building it from source instead: `docker build --build-arg VERSION=… .` with the root `Dockerfile`. |
| `make install` | Building from source | Yes. Needs Go and Node. |

All of them install the same binary. The skill published for `npx skills` is generated
from the same templates `aboard init` writes, so the two can't differ.

## What updates, and the rules for each

Three things update separately. Each has a rule so that an upgrade never breaks a
session that is already running.

| What | How it updates | Rule | Today |
| --- | --- | --- | --- |
| The binary (CLI, daemon, local server) | A package manager, the install script, or `aboard upgrade` | Never installed silently. A command in a terminal says once a day that a newer release exists. A running daemon or local server from an older build is replaced by the first newer command or hook that reaches it. | Yes: replacement, the notice and `aboard upgrade` (`e2e/selfupgrade_test.go`) |
| Files installed into harnesses (the skill, hook entries, allow rules) | `aboard init --yes` | Hooks run the installed binary by its path, so a new binary takes effect without rewriting them. `aboard doctor` reports a file that differs from what this build would write; the install manifest tells an outdated file from one the person edited. | Yes |
| Team servers | A new binary or image, then a restart | Migrations run forward only, on start, in one transaction after a backup of the database. A binary older than its data refuses to start. | Yes |

### No silent installs

Aboard never downloads and runs new code on its own. The update notice is one line on
standard error, shown only to a person in a terminal: never in `--json` output, in
hooks or inside a harness session, and not at all with `ABOARD_NO_UPDATE_CHECK=1`. It
checks for a release at most once a day, alongside the command and for at most two
seconds, reading the latest release's `checksums.txt`, and keeps the result in
`update-check.json` in the state folder. A dev build never checks. `aboard upgrade`
installs a release over an install-script install, checked as the script checks it,
and then runs the new binary's `aboard init --yes` for the harnesses the install
manifest records, to update the skill and hooks in place. For a Homebrew install or a
source build it changes nothing and names the command to use (`brew upgrade aboard`,
or `git pull` and `make install`). Every check comes before anything is replaced; a
refresh that fails after the swap exits 1 with `upgrade_setup_failed` and says to run
`aboard init --yes`, then `aboard doctor`.

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
adapters) and whether it is additive. *Today:* `CHANGELOG.md` with an Unreleased
section; the release job publishes a version's section as its release notes, and
refuses a release (not a prerelease) whose section is missing.

## Landing a pull request

What we want: nothing reaches `main` without passing the checks its change calls for,
and landing a PR is one command that a person, a session or an agent runs the same way.

CI is the merge gate: the `check` workflow (`.github/workflows/check.yml`: `make check`
on Linux and macOS, the web UI and the docs site) must pass on the exact commit that
merges. Only what CI can't run happens on a person's machine: `make live`, which needs
harness logins. Before asking for review or landing, run `make quick` (format, lint,
vet, generated code and the core's size, in about a minute); it catches most of what
would fail CI without waiting for it. `scripts/install-hooks` installs a git pre-push
hook that runs it on every push, if you want that (opt-in; `scripts/install-hooks
--remove` takes it out, and `git push --no-verify` skips it once).

`scripts/land-pr <number>` lands a PR:

1. It finds the PR's branch with `gh`, and uses the worktree that already has it
   checked out or creates one at `.claude/worktrees/land-pr-<number>`. It never
   switches branches, pulls or commits in the main checkout.
2. It merges `origin/main` into the branch, and stops on a conflict, naming the files
   and leaving the merge in the worktree to resolve.
3. It asks GitHub for the `check` workflow's result on the branch's head after that
   merge, the exact commit it will merge (`scripts/ci-status`, which reads the
   workflow's newest run for that commit; a run passes only when every job in it
   passed):
   - **passed**: nothing runs here;
   - **failed**: it stops with exit 3 and the run's address. If the same test fails on
     unchanged `main`, it is a known flake ([testing.md](testing.md#flaky-tests)):
     rerun the failed jobs (`gh run rerun --failed <run id>`) and land again;
   - **no result yet** (main moved, so the merge is a new commit, or CI is still
     running): it pushes the branch so CI runs on that commit, and waits for it, up to
     30 minutes (`LAND_PR_CI_WAIT`, in seconds). If CI hasn't finished by then, it
     stops with exit 6; the branch is pushed, so run it again later.
4. With `--live`, it runs `make live-affected` (beside CI, while it waits), and merges
   only if it passes. Without `--live`, it notes a change to delivery, setup or upgrades,
   which must pass `make live` before merging.
5. It pushes, merges with `gh pr merge --merge --match-head-commit <the checked
   commit>` (retrying while GitHub says the base branch was modified), and confirms
   GitHub reports the PR merged.
6. Only then does it remove what it created (the temporary worktree and local branch)
   and delete the branch on GitHub. A failure at any step leaves everything in place
   and says what to do next.

`--local` runs the checks here instead of asking CI, by what changed against
`origin/main`: none for a change to only `design/` or `engineering/`; otherwise `make
fmt-check lint vet generate-check core-size harness-table-check test e2e`, plus `make
web-check` when `web/` changed. It's for when GitHub can't run CI; once `main` requires
the `check` workflow's jobs (below), GitHub still refuses the merge until CI passes.
`--dry-run` prints the plan, including any conflict with `main` and CI's result so far,
and changes nothing. The script's header lists its exit codes, and
`e2e/landpr_test.go` runs it against a local repository with a fake `gh`.

## Cutting a release

### Once, before the first release

These are repository settings, made by the maintainer:

1. **The `release` environment** (Settings → Environments → New environment
   `release`): add the maintainer as a required reviewer, and limit deployment to tags
   matching `v*`. The release job is the only job with write permissions, and it waits
   for that approval, whether a tag push or a run by hand started it, so only the
   maintainer can publish, with or without the CI override below.
2. **Protect version tags** (Settings → Rules → New tag ruleset, target `v*`): only the
   maintainer may create, update or delete them, so nobody else can start a release.
3. **Require CI on `main`** (Settings → Rules → New branch ruleset, target the default
   branch): require status checks to pass before merging, with the `check` workflow's
   jobs `check (ubuntu-latest)`, `check (macos-latest)`, `web` and `docs`. This is what
   makes CI the merge gate for everyone, not only for `scripts/land-pr`.
4. **Workflow permissions** (Settings → Actions → General): keep the default
   `GITHUB_TOKEN` read-only; the release job asks for `contents`, `id-token` and
   `packages` write itself, and the gate job only `actions: read`, to read the `check`
   workflow's runs.
5. **The image**: after the first release, set the `aboard` package on GHCR to public
   (Packages → aboard → Package settings → Change visibility), so team servers can pull
   it without logging in.
6. **The image's two Dockerfiles stay alike**: `Dockerfile.release` (the release
   binaries) and the root `Dockerfile` (a build from source) must set the same user,
   volume, environment and entrypoint; change both together.

No secrets are needed: signing is keyless, with the job's own GitHub identity, and the
release and image are published with the job's `GITHUB_TOKEN`.

### What the release workflow checks

The release workflow runs no test suites: CI ran them on the commit already, and a
flaky test there should never decide whether a release can go out. Its first job, the
gate, checks with read-only permissions that:

- the run is for a `v*` tag pushed to the repository, or a run by hand on that tag
  (never a pull request);
- the tag matches `version` in `server/internal/cli/build.go` (a candidate's suffix
  aside);
- the `check` workflow passed on the tag's exact commit. `scripts/ci-status` asks
  GitHub for the workflow's newest run on that commit (`gh run list --workflow
  check.yml --commit <sha>`), which passes only when every job in it passed. A run still
  going is waited for, up to 20 minutes; a commit with no run fails at once, since it
  never went through a pull request or `main`;
- the code builds (`go build ./...`).

If CI didn't pass, the release stops before anything is built, naming the run and the
override. Fix the cause and tag a commit where CI passed, or, for a known flake
([testing.md](testing.md#flaky-tests)), rerun the run's failed jobs (`gh run rerun
--failed <run id>`) and then rerun the release workflow.

### Releasing with CI red (the override)

When the maintainer decides a release goes out although CI isn't green, for example
because a known flake is failing and its fix is on the way, they run the release
workflow by hand on the tag:

```sh
gh workflow run release.yml --ref v0.1.1 -f tag=v0.1.1 -f override_ci=true \
  -f reason="<the known flake and its issue>; every other job passed"
```

(or Actions → release → Run workflow, choosing the tag under "Use workflow from" and
filling in the same fields). The run must be on the tag itself: the signature names
the ref the workflow ran on, the install script and `aboard upgrade` check that it is
the tag, and the `release` environment allows only `v*` tags. A reason is required.
The gate records the failing run and the reason in the job summary, the release notes
end with them, and the `release` environment still waits for the maintainer's
approval. The tag must be one whose `release.yml` has these inputs.

### A dry run, publishing nothing

Run the `release` workflow by hand with no tag (Actions → release → Run workflow, on
`main`). It runs the gate on that commit and builds every archive, SBOM and the image
without publishing or signing, with read-only permissions. Locally, `make
release-snapshot` builds the archives into `dist/` the same way.

### A release candidate, end to end

Before the first release, and before any release that changes the pipeline, cut a
release candidate. A tag with a suffix, such as `v0.1.0-rc.1`, is published as a GitHub
prerelease: it never becomes "latest", so the install script, `aboard upgrade` and the
image's `latest` tag skip it.

1. On `main`, with `version` in `server/internal/cli/build.go` at `0.1.0`:
   `git tag v0.1.0-rc.1 && git push origin v0.1.0-rc.1`.
2. Approve the `release` environment when the job asks. The gate checks CI passed on
   the tagged commit, then the job builds, signs and publishes the prerelease with its archives, `checksums.txt`, its
   bundle, the SBOMs and `install.sh`, and pushes `ghcr.io/leonidas1712/aboard:0.1.0-rc.1`.
3. Check the signature from any machine with cosign, in a folder with the prerelease's
   `checksums.txt` and `checksums.txt.sigstore.json`:
   `cosign verify-blob --bundle checksums.txt.sigstore.json --certificate-identity https://github.com/leonidas1712/aboard/.github/workflows/release.yml@refs/tags/v0.1.0-rc.1 --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt`.
4. On a clean macOS machine and a clean Linux machine, install the candidate with the
   prerelease's own script:
   `curl -fsSL https://github.com/leonidas1712/aboard/releases/download/v0.1.0-rc.1/install.sh | ABOARD_VERSION=v0.1.0-rc.1 sh`,
   then run the quickstart. A candidate is always installed by its version: the
   `releases/latest/download` address never points at a prerelease, so the one-line
   install in the docs works only once `v0.1.0` itself is published. Land the docs that
   lead with it together with that release.
5. `docker pull ghcr.io/leonidas1712/aboard:0.1.0-rc.1`, run it with a volume, and open
   the board view.
6. If anything fails, fix it on `main` and cut `v0.1.0-rc.2`. Delete a failed
   candidate's prerelease and image if you like; its tag can stay.

### The release

1. The `check` workflow passed on the commit of `main` you'll tag (the commit's checks
   on GitHub). The release job checks this itself and stops if not; see the override
   above for a known flake.
2. `make live` passes on a machine with Claude Code and Codex logged in
   ([e2e/live/PROOFS.md](../e2e/live/PROOFS.md)), and the steps by hand in
   [e2e/RELEASE_CHECKLIST.md](../e2e/RELEASE_CHECKLIST.md) are checked in a sandbox.
3. `CHANGELOG.md` has a section for the version (`## 0.1.0`, moved from Unreleased),
   including contract changes. The release job publishes it as the release notes, and
   refuses a release without one.
4. `version` in `server/internal/cli/build.go` is the version; the job refuses a tag
   that doesn't match it (a candidate's suffix aside). Bump it in a pull request if
   needed.
5. `git tag vX.Y.Z && git push origin vX.Y.Z`, and approve the `release` environment.
6. Install from the published script on a clean machine and run the quickstart; on a
   machine with the previous release, run `aboard upgrade`.

After launch, a nightly job runs the live suite against the latest Claude Code and
Codex releases, so a harness update that breaks delivery shows up before a person
meets it. It needs harness logins in CI or a self-hosted runner.
