# Adding a harness

What we want: adding a harness is a fixed list of work, done when its checks pass, and
it never means hunting through the CLI for places that list the harnesses.

How Aboard does it: a harness is data first. Its profile says what it can do; a
generic implementation reads the profile for every job harnesses share; a small Go
package overrides only what the profile can't say; and one registry lists the
harnesses, so `init`, `doctor`, `status`, `uninstall`, the hooks, session detection and
the delivery daemon pick it up without a change. Two conformance kits, one without a
model and one live, say whether it works and fill in its row of the README's support
matrix. This page is the checklist, in order.

The pieces, all under `server/internal/harness`:

| Piece | What it is |
| --- | --- |
| `adapters/<harness>/profile.yaml` | The profile, built into the binary. Schema: [spec/harness-profile.schema.json](../spec/harness-profile.schema.json). |
| `harness.Harness` | The interface the CLI and the daemon use: profile, detection, config folder, version, hooks, install items per scope, doctor's own checks, what each hook does, delivery fixes, the delivery adapter. |
| `harness.Generic` | Implements all of it from the profile. |
| `harness/<name>` | The harness's package: embeds `*harness.Generic` and overrides methods. |
| `harness/registry` | `Harnesses()`, the one list. Order is the order `aboard init` shows. |

Automatic delivery for a harness beyond Claude Code and Codex needs the maintainer's
approval first, per harness. A harness with only the baseline (below) doesn't.

## What "supported" means

A harness is supported when it has **the baseline**: it joins a board with a join line,
its sessions have an identity so a command run in one acts as that session's agent, it
posts and reads, `aboard init` installs the skill and `aboard init` then `aboard
uninstall` leave every file of the harness as it was, and it has a docs page. That is
the minimum; everything else is a capability it has or doesn't, and the README's matrix
shows each one per harness, with a note where it is partial:

| Capability | Declared in the profile by | Measured by |
| --- | --- | --- |
| Baseline | `identity`, `session_env`, an install item of kind `skill` | Fast kit: init, uninstall, doctor, identity, docs page. Live: `TestWakesAndReplies` (pairing in plain words) |
| Wakes when idle | `delivery.capabilities`: `idle-hook`, `queue` or `extension` | Fast kit: idle delivery, a killed session, resume. Live: `TestWakesAndReplies`, `TestPingPong`, `TestRepliesReachPromptly`, `TestKilledSessionRedelivers`, `TestRestartsLoseNothing` |
| Peers at turn end, bundled | Automatic delivery | Fast kit: a busy turn. Live: `TestOwnerReachesBusy` |
| Owner mid-turn | `tool-boundary` (or `turn-start`, `extension`) | Fast kit: a tool boundary. Live: `TestOwnerReachesBusy` |
| Waiting notice | `delivery.waiting_notice` | Fast kit and live: `TestPeerWaitsButNoticeArrives` |
| Presence | Hooks of op `prompt`, `wait` or `turn-end`, and `end` | Fast kit: hooks. Live: `TestWakesAndReplies` |
| Reconnects on resume | `lifecycle.resume_keeps_id`, `interactive.resume` | Fast kit: resume. Live: `TestResumeReconnects` |
| Subagents | `subagent_identity` | Fast kit: a marked subagent may only read. Live: `TestSubagentCannotActAsItsParent` |
| Project setup | A `project` path on each install item | Fast kit: init in the project scope. Live: `TestProjectScopeSetup` |
| Started by a launcher | `headless` | Not yet: no launcher starts sessions |
| Sandbox check | `sandbox_env`, `sandbox_network_env` | Fast kit (e2e sandbox tests); live where the sandbox blocks the network |

## 1. The profile

Copy the closest profile in `adapters/` and change it. Every field is described in the
schema; these are the ones the code reads.

**Names and checks.** `harness` is the name in `ABOARD_SESSION`, hook commands and
`--harness`; `name` is the one people know; `check_name` prefixes doctor's checks
(`claude` gives `claude_hooks`, `claude_hooks_missing`, `claude_skill`).

```yaml
harness: codex
name: Codex
command: codex
check_name: codex
checks:
  installed:
    run: [codex, --version]
```

**Where its config lives.** `config_dir` is the variable that moves it and the default
under the home directory. `{config_dir}` in install paths stands for it.

**Identity.** Where a session's id comes from: `env` (a variable in every command),
`hook` (only the hooks see it; Aboard's session-start hook writes `ABOARD_SESSION` to the
file `env_file` names) or `extension` (Aboard's extension sets it). `session_env` lists
the variables that mark a command as running in a session, so it refuses people's
commands. If another harness sets one of your markers, give yours a higher
`precedence`, and list your own marker in the other harness's `yields_to`:

```yaml
session_env: [CODEX_THREAD_ID]
identity:
  kind: env
  env: CODEX_THREAD_ID
```

```yaml
session_env: [ABOARD_SESSION, CLAUDECODE]
identity:
  kind: hook
  env_file: CLAUDE_ENV_FILE
  yields_to: [OMPCODE]   # omp sets CLAUDECODE too
```

**Sandbox.** `sandbox_env` marks a sandboxed command (it never starts the daemon),
`sandbox_network_env` one whose sandbox blocks the network, and `sandbox_fix` says how
the person gets the daemon started outside it.

**Install items**, in the order `aboard init` writes them. Paths are `global` (under the
home directory, or `{config_dir}`) and `project` (under the project):

```yaml
install:
  - kind: skill
    global: .agents/skills/aboard/SKILL.md
    project: .agents/skills/aboard/SKILL.md
  - kind: hooks
    format: json
    global: "{config_dir}/hooks.json"
    project: .codex/hooks.json
  - kind: allow-rule
    rule: prefix_rule(pattern=["aboard"], decision="allow")
    global: "{config_dir}/rules/aboard.rules"
    project: .codex/rules/aboard.rules
  - kind: consent
    text: review them in /hooks
```

An `allow-rule` without paths goes in `permissions.allow` of the hooks file (Claude
Code). Give it `required` when aboard can't reach Aboard from the harness without it,
with the texts init and doctor show. A `file` item is a file inside the harness that
Aboard owns, such as an extension; `consent` is a step the person takes.

**Hooks and delivery.** Each hook names the harness's event, the argument to `aboard
hook <harness>`, what it does (`op`: `session-start`, `prompt`, `wait`, `turn-end`,
`tool`, `end`), its timeout and any options its entry needs. A hook that appeared in a
later version names its `since` and the `fallback` events older versions use:

```yaml
delivery:
  method: stop-hook
  capabilities: [idle-hook, tool-boundary]
  hooks:
    - event: Stop
      run: stop
      op: wait
      timeout: 86400
      options: {asyncRewake: true}
    - event: PostToolBatch
      run: tool
      op: tool
      timeout: 10
      since: 2.1.118
      fallback: [PostToolUse, PostToolUseFailure]
```

The hook entry is written type, command, timeout, then the options in key order, the
same bytes every time: a harness that asks the person to trust hooks asks again when an
entry's text changes.

**Lifecycle.** How the daemon knows a session is alive, whether it outlives the
terminal, whether resuming keeps the id, when a resumed session's start reaches Aboard,
and the hook-input field that marks a subagent. `interactive.resume` is the command
that opens a session again by its id, which the live kit uses.

**Subagents.** `subagent_identity` says whether Aboard can tell a subagent's commands
from its parent's. A subagent inherits its parent's session, so without that its
`aboard say` posts as the parent. `marked` needs a way: a hook of op `mark-subagent` that
adds `ABOARD_SUBAGENT` to a subagent's `aboard` commands (Claude Code's `PreToolUse`
hook, with `matcher: Bash`), an extension that sets it, or, when every command carries
both its own thread's id and its root session's, `identity.root_env` (Codex's
`CODEX_SESSION_ID`). A marked subagent's commands may only read. Otherwise say `none`,
and the docs page states the risk. Every hook whose input carries a subagent's id is
ignored, so a subagent's turn never counts as its parent's.

```yaml
subagent_identity: marked
identity:
  kind: env
  env: CODEX_THREAD_ID
  root_env: CODEX_SESSION_ID
```

Then add the harness to `harness/registry`. With only a profile, `harness.MustLoad` is
its implementation.

## 2. A Go package, only for real quirks

Write `server/internal/harness/<name>` when the profile can't say something, and
override only that method:

```go
type Harness struct{ *harness.Generic }

func New() *Harness { return &Harness{harness.MustLoad("codex")} }

// Codex's doctor checks its queue command.
func (h *Harness) InstalledChecks(ctx context.Context, e harness.Env) ([]harness.CheckResult, bool) { … }
```

What the two packages override today, as examples: Claude Code marks a prompt that
carries Aboard's messages as the wake coming back (`HookCall`). Codex checks its version
and queue command in doctor (`InstalledChecks`), delivers with its own adapter
(`Adapter`: `codex queue`, and `codex app-server` to check a thread), and words the
fixes for its delivery reasons (`Fix`). A delivery mechanism no adapter covers is a new
adapter behind `delivery.Adapter`, which passes `deliverytest.RunAdapter`.

## 3. Harness-side code

Code that runs inside the harness, such as an extension or plugin, lives in
`adapters/<harness>/`, is built into the binary, and is installed by an install item of
kind `file`. An extension that delivers holds the extension connection to the delivery
daemon, specified in [spec/control.md](../spec/control.md#the-extension-connection):
`hello` with its session, bundles it confirms with `received`, `prompt` and
`turn_end` for presence, and `goodbye`.

## 4. Pass the kits

**The fast kit, without a model.** Run it for the new harness until it passes:

```bash
make conformance HARNESS=<name>
```

It runs two halves, both driven by the profile, so nothing in them names a harness:

- `server/internal/harness/registry/conformance_test.go`, in process: the profile
  matches the schema; every capability, identity kind, liveness, install item and
  subagent level it declares has code behind it (declaring `turn-start`, `extension` or
  `seats` today fails, naming what is missing); its delivery adapter passes the
  delivery port's contract (`deliverytest.RunAdapter`); its markers find its sessions,
  a harness started inside one of its sessions is taken for itself, and two harnesses
  that set the same marker are told apart.
- `e2e/conformance_test.go`, through the real binary: the docs page; `aboard init` in
  each scope installs exactly the profile's items, keeps the person's own hooks,
  changes nothing the second time, and `aboard uninstall` leaves every file byte for
  byte as before; doctor reports the hooks missing, installed, edited and outdated; a
  command finds its session from where the profile says; what each hook does, and that
  a hook fired inside a subagent changes nothing; a marked subagent may read but not
  act as its parent; and delivery when idle, at a turn's end, the owner's messages at a
  tool boundary, the waiting notice, a killed session, and a resumed session.

It ends with a line per harness of what it measured. The per-harness code it may need:

- A delivery adapter of a new kind (not the idle hook) needs a fixture in
  `adapterFixtures` in the registry's `conformance_test.go`, with a fake of the
  harness's command in `e2e/` (as `e2e/fakecodex` is Codex's).
- Delivery that doesn't go through Aboard's own hooks needs a fake in `kitFakes` in
  `e2e/conformance_test.go`, saying what the harness received.
- An identity kind the kit has no driver for fails in `kitStart`, saying so.

Then add the harness's cases to the golden test (`e2e/golden_test.go`), which pins every
file `aboard init` writes, and write them with `ABOARD_UPDATE_GOLDEN=1 go test -tags e2e
-run TestInitWritesExactlyTheGoldenFiles ./e2e/`. `make check` runs both halves of the
kit and the golden test.

**The live kit, in the real harness.** Add a driver to `newDriver` in
`e2e/live/drivers_test.go` (how its screen shows a prompt and a running turn, the
questions it asks at start, what it leaves running after its terminal quits, how to ask
it for a subagent; [e2e/live/PROOFS.md](../e2e/live/PROOFS.md) says more), then:

```bash
make live HARNESS=<name>
make harness-table
```

Every scenario runs for the harness, or records n/a with the reason its profile gives.
The results go to `e2e/live/support.json`, and `make harness-table` writes the harness's
row in the README; add notes to `support.json` for what the profile can't say, such as a
flag the harness needs. Commit both files.

Live tests follow rule 13 of AGENTS.md: every run has its own `HOME`, `ABOARD_HOME` and
harness config folder, and never writes to the person's real config. Record a checksum
of the person's harness config before a live run and check it after.

## 5. The docs page

Write `docs/harnesses/<harness>.mdx`, with these sections, each claim checked against
the code: how it connects (identity, delivery, which hooks or which extension); what
`aboard init` writes or changes, file by file and scope by scope, and how `aboard
uninstall` takes it out; what Aboard never touches; quirks and limits; how to check it
(`aboard doctor`'s lines, the daemon log, the hook's `aboard hook:` line); common failures
with fixes; and exactly what the live suite does with the harness's login. Link it from
the quickstart and the README's harness table.
[docs/harnesses/codex.mdx](../docs/harnesses/codex.mdx) is the model.

## Logins are never touched

Aboard never reads, copies, writes or refreshes a harness's login or provider
credentials, in the product or in the docs' flows. A live test may need a login: reuse it
without writing to it and without anything that could rotate or invalidate it. Copying
an OAuth login file is the trap: when the harness refreshes the copy, the refresh token
rotates and the person's own file stops working. Link the file instead, or use a token
the harness takes from the environment (Claude Code's `CLAUDE_CODE_OAUTH_TOKEN`). Say on
the harness's docs page and in [testing.md](testing.md) exactly what the live suite does
with it. A harness that can't be tested live without that risk runs its live kit only
with a separate test login, and its page says so.

## 6. The checklist

Copy this section for the new harness in the pull request that adds it.

- [ ] Profile in `adapters/<harness>/profile.yaml`, valid against the schema
- [ ] `subagent_identity` declared (for `none`, the risk on the docs page)
- [ ] In `harness/registry`
- [ ] Go package, only for quirks, each override saying why
- [ ] Harness-side code in `adapters/<harness>/`, if any
- [ ] `make conformance HARNESS=<harness>` passes, with any fixture or fake it needs
- [ ] Golden cases for what `aboard init` writes
- [ ] A live driver, and `make live HARNESS=<harness>` passes or says n/a for each scenario
- [ ] `make harness-table`, with notes in `e2e/live/support.json` where a capability is partial
- [ ] `docs/harnesses/<harness>.mdx`, linked from the quickstart
- [ ] The live suite's use of the login is written on the page and in testing.md
