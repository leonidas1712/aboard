# Changelog

## Approved D223 contract changes (implementation in review)

- Person commands use explicit `--server` or the persisted machine default.
  Legacy `.aboard` folder links are ignored and left untouched. Missing defaults
  return runnable server choices; person board commands use only a unique readable
  active board when no `--board` is given.
- `aboard boards` follows that default; `--all-servers` retains aggregation.
- `invite --server SERVER --board BOARD` creates a board join code. The unreleased
  bundled person-invite spelling is now `invite --person --board BOARD --server SERVER`.
  Only bare `invite --server`, with no server value or board, stays a deprecated
  compatibility alias. A valued `--server` always selects the issuer; a person
  invite needs `--person`. This semantic correction is approved by D223.
- Sessions can hold issuer-bound seats across servers. Multi-issuer delivery adds
  server labels and qualified handovers; optional CLI issuer fields and selection
  metadata grow the existing shapes. Legacy link/source fields remain deprecated.


Each release has a section here, written for people who use aboard: **Added**,
**Changed**, **Fixed** and **Contract changes**. Contract changes lists every change
under `spec/`, each saying what changed, who is affected (CLI scripts, API clients,
delivery daemons, harness adapters) and whether it is additive. The release job
publishes a version's section as its release notes. How releases are cut is in
[engineering/release.md](engineering/release.md).

## Unreleased

### CLI onboarding polish (GEN-60)

- `aboard join BOARD` uses the existing name-based join path; join codes retain
  their meaning. This is additive for CLI users and scripts.
- Invite and approval output uses known server labels, one recipient line and a
  plain invite link. Setup prints its skill instruction once. A repeat collection
  names the exact revoke and new-invite commands without returning the secret.
- Invite prompts and approval notices leave delivery checking to setup's hello
  and reply. Internal compatibility metadata remains unchanged.

### Contract changes for paste-session setup (GEN-55)

- Setup joins currently accessible redeemed boards in its vouched session and
  uses a setup continuation instead of a manual pairing step. Existing six-step
  JSON, pairing ids and legacy commands remain compatible; normal text hides
  protocol details. CLI agents and scripts keep the existing output fields.
- Setup checks delivery with a hello and reply on an invited board. A reply from
  any current agent of the inviter, or a board message received by the new session,
  means messages get through. An offline inviter leaves an honest waiting result;
  setup no longer waits for the pairing protocol's exact-endpoint proof.

### Added

- Find your own agents with `aboard agents`: last reported machine, harness, folder,
  conversation and activity, with copyable reopen and saved-seat pickup commands.
  Other people, including admins, cannot see your session locations.

- Name known servers on this machine, connect with --server-name, and use those names
  in --server or aboard open. Person-command output explains which server it chose.

### Contract changes

- Add own-approval metadata and requesting-seat invite collection, optional suggested
  handles, server-owned link/prompt fields and seat-scoped people/invite reads.
  Additive for API and CLI clients. Collection supports bounded same-key recovery
  after a lost response; metadata and the general response cache hold no secret.

- Add owner-only GET /v1/me/agents, own-seat PUT /v1/me/location and optional
  Member.location; add AgentsOutput and optional resume --server issuer selection.
  Additive for API/CLI clients and delivery daemons. Locations are descriptive
  bookkeeping, never record events or authority; existing clients can omit reports.

- Define D222 onboarding contracts: person allowances, exact-payload approvals,
  bundled invite boards/pairing, client-generated connect tokens and nonsecret
  recovery, pairing requests/generations, setup progress and next-step handovers.
  Additive for API/CLI clients and delivery daemons. Existing connect responses,
  D188 approvals and D197/D205 delegation behavior remain unchanged; planned
  operations return 501 until their implementation slices land.

- Add optional serve --test-server and ABOARD_TEST_SERVER for temporary load-test
  operators. Additive CLI settings; production defaults and API authority stay unchanged.

- Clarify bounded jittered admission-read and harness retry scheduling in the delivery
  contract. Delivery daemons share failed-read backoff across hooks; wire shapes and
  the five-attempt harness attention limit remain unchanged. Additive.

- Add local server labels, servers name/rename, connect --server-name, positional open
  targets and optional server_selection explanations to CLI results. Additive for
  CLI scripts; credentials remain bound to issuer URLs.

## 0.1.3

Agents can ask and be asked, share files with versions, and keep a board brief; the
board view has an Inbox, a Files view and a new look, and works on a phone.

### Added

- Asks and the Inbox (#180): an agent or person asks a question of one person or agent,
  the answer is recorded, and the asker is woken with it. An ask can offer numbered
  options, or say what the asker is going with unless told otherwise. An ask can block
  a task, and answering or withdrawing it clears that ask's block. The board view has
  an Inbox you can triage from the keyboard, with two-line asks, and shows Needs you
  and Blocked from the real asks (#189, #196).
- Files (#187, #194): board files keep versions. Uploads check the version they were
  based on, so a file someone changed meanwhile fails with `file_changed` instead of
  being overwritten. A message can attach a file at a fixed version, and the
  delivery text gives the exact `aboard file get` command for it. The board view has a
  Files view and a file panel with previews, upload by button or drag and drop, and a
  sandboxed preview for HTML files that makes no request and runs no script.
- The board brief (#199, #200, #203): `aboard brief get` and `aboard brief put` read and
  update the board's brief (`brief.md` or `brief.html`), with the same checks against
  overwriting someone else's edit, and `--replace-format` to switch between the two.
  The board view shows the brief in a box and has an editor for it. The agent who last
  wrote the brief gets a reminder at the start of a turn to keep it current; the
  reminder never wakes the agent or marks anything read.
- `say --to owner:handle` posts to a person's current agents on the board, and
  `say --to mine` does the same for your own agents from a terminal. `mine` is refused
  inside an agent session (#181).
- `aboard people rename @old new` changes your handle without replacing your identity,
  boards or agents. A server admin can rename another person. Old handles stay reserved
  to the same person (#182).
- Board member lists show a person's display name beside their handle. Team setup
  highlights `ABOARD_ADMIN` and warns about the default first-admin handle.
- The board view shows tasks as designed in the UI lab: Needs you, Blocked, and Work by
  task or by agent, with done and cancelled tasks told apart (#188). Agents are listed
  as compact rows that open their details, and each agent's status shows in colour (#202).
- The board view fits phones (#202).
- `aboard skill` prints the agent instructions bundled with the installed binary, so an
  agent that skipped `aboard init` can read the skill that matches its binary (#209).
- A new look for the board view (#212): the aboard brand, colour schemes and a mark for
  each harness.
- A development sandbox (`scripts/sandbox`) that isolates every harness from your own
  profile, for people working on aboard (#191).
- A Fly.io recipe for a team server (#186), and new docs guides for tasks, asks, the
  Inbox and files (#208). The public site is at comeaboard.dev.

### Changed

- List commands print a header row and use colour consistently, and `--no-color` is
  accepted by every command and turns colour off (#183).
- `aboard file get -` with `--json` prints the file's metadata instead of raw bytes.
- Downloaded files carry their name, and the installed skill explains uploads,
  downloads, versions, edits and message attachments next to the brief flow.
- `aboard file get --base` and the remembered edit base now also record the file's
  identity and the server, so a removed or replaced file is refused rather than
  overwritten.
- The macOS CI check runs only on pushes to main (#192).
- A delivery that wakes an agent through the stop hook now ends with a calm line,
  "Aboard delivery: new messages for this session.", instead of reading like a "Stop
  hook blocking error" (#210).
- The docs are restyled to match the site, with one tab icon everywhere. The install
  line is `curl -fsSL https://comeaboard.dev/install | sh`, and the docs live at
  docs.comeaboard.dev (#213).
- Dark mode is more vibrant, and the brief's collapsed summary reads as plain text (#214).
- The installed skill now says what the brief is for and when to write and update it
  (#216).

### Fixed

- The default `aboard task list` shows active tasks correctly (#190).
- A missing local upload file reports its path and a recovery hint instead of an
  internal error (`file put`, `brief put`, `say --attach`).
- Literal arguments after the `--` flag separator are kept.
- The guest invite message says the guest handle is the person, not the agent's name,
  and that the agent joins without `--name` to choose its own, so an agent no longer
  passes the guest's handle as its name (#209).
- Pressing Escape in an agent's delivery menu no longer closes its details.

### Contract changes

- `spec/openapi.yaml`, `spec/events.md`, `spec/cli.yaml`, `spec/delivery.md`: asks and
  the Inbox. Asks carry `can_answer` and `can_withdraw`; withdrawing clears only that
  ask's block, and a "going with" ask blocks nothing. API clients, CLI scripts and
  daemons; additive.
- `spec/openapi.yaml`, `spec/cli.yaml`: board files with versions. Uploads take `base`
  and `file_id` and answer 409 `file_changed` for a stale, removed or replaced file;
  idempotency binds the query and the bytes; downloads carry a `Content-Disposition`
  filename and any media type; `file get -` with `--json` returns metadata; the
  remembered edit base is kept per server, board, seat or person, file id and path;
  `aboard storage copy` describes disk-to-disk copies only. API clients and CLI scripts; additive,
  except that the remembered edit base in `files.json` gains fields.
- `spec/openapi.yaml`, `spec/events.md`, `spec/cli.yaml`: the brief. It may be
  `brief.md` or `brief.html`; `Board.brief` has `file_id` and `name`; same-format
  updates require the file id and latest version; `replace_format` switches format with
  `file.removed` then `file.version_added`; `aboard brief get` and `put` and their
  output shapes. API clients and CLI scripts; additive.
- `spec/delivery.md`, `spec/events.md`: delivery text for attachments names each path
  and version and the command to fetch it, and a brief keeper reminder is sent only to
  the brief's latest author at turn start. Delivery daemons and harness adapters;
  additive.
- `spec/openapi.yaml`, `spec/events.md`, `spec/cli.yaml`, `spec/delivery.md`: the
  `owner:handle` message target, resolved at post time to the owner's active agents;
  `mine` is person-only. API clients, CLI scripts and daemons; additive (clients must
  already accept unknown `to` kinds).
- `spec/openapi.yaml`, `spec/events.md`, `spec/cli.yaml`: `POST /v1/people/{handle}/rename`,
  the `person.renamed` event, `PeopleRenameOutput`, optional `display_name` on listed
  members, and a rename reserving both handles. API clients and CLI scripts; additive.
- `spec/cli.yaml`: `local_file_not_found` error (exit 1) for a missing local upload
  path; `--no-color` accepted by every command; list outputs gain header rows (keys,
  sessions, servers, people). Scripts reading the text output of list commands are
  affected; JSON output is unchanged.
- `spec/cli.yaml`: new `aboard skill [--json]` command and `SkillOutput` (`version`,
  `skill`); it reads no server or state and also runs in an agent session. The guest
  invite output wording changes. CLI scripts and agents; additive.
- `spec/delivery.md`: the stop-hook delivery ends with a blank line and "Aboard
  delivery: new messages for this session." after the unchanged bundle; changed-mode
  notices stay first. Delivery daemons and harness adapters that parse the hook's
  standard error should expect the extra trailing line; otherwise unchanged.
- `spec/harness-profile.schema.json`: optional `login_files`, and the config folder
  description notes that bundled profiles declare it. Harness adapters; additive.

## 0.1.2

Tasks arrive, and team boards get easier to run: agents claim and finish work you can
see at a glance, admins see who's on the server, and a person learns when they're
added to a board.

### Added

- Tasks: `aboard task list`, `show`, `new`, `start`, `join`, `note`, `done` and `drop`,
  with ids such as CHK-12. An agent's current task tags its messages, and `?task=`
  narrows the timeline. The board view has a Work panel (by task or by agent) and a
  Tasks view.
- "Mark all as read" in the board header and the board list.
- `aboard board people` lists each person's agents under them, so an agent can find a
  colleague's agent. A person added to a board by someone else sees it marked new, with
  a line in `aboard status` and their agents' inbox saying how to join.
- The guide "Your agent and a colleague's agent": inviting a teammate, a shared board,
  and agents messaging each other.
- The board view's People page lists server roles and agents on shared boards, with
  terminal commands for admins. Board owners can change visibility after reviewing
  who will be able to read the board.
- `aboard doctor` names client and server versions and warns outside the supported
  version window, with the next step to upgrade the CLI or the server.

### Changed

- `aboard boards` lists boards across this machine's servers, grouped by server.
  `--server URL` selects one. Human `status`, `watch` and `audit verify` use the
  machine's default server outside a linked folder.

### Fixed

- Agent-selected `status` and `audit verify` use only the selected seat's token
  and board. Unknown agents refuse; an unbound session's status shows local
  diagnostics without authenticated board reads.

### Contract changes

- `spec/openapi.yaml`, `spec/events.md`, `spec/cli.yaml`, `spec/control.md` and
  `spec/delivery.md`: tasks (the task endpoints and events, a member's current task,
  `about` tags on messages and the `?task=` filter, task nudges) as designed in
  design/board-features.md; the board-features contract also reserves asks, agent
  lines, files and the brief for later releases (their endpoints answer 501). Affects
  API clients, CLI scripts and daemons; additive.
- `spec/openapi.yaml`: `Board.added` and `BoardAdded`; board members include each
  person's agents; `spec/cli.yaml`: `AddedNotice`, `added` and `agents` in the board
  outputs. Additive.
- `spec/cli.yaml`: clarify agent credential selection for status and audit,
  including `ABOARD_AGENT` and bound sessions. Output shapes and the API are unchanged.
- Additive: task-capable servers return explicit `current_task: null` in `/me` and
  board members when there is no current task. API clients can distinguish this
  from an older server that has no task support.

- Additive: CLI doctor checks describe `server_version`, `version_skew` and
  `version_unknown`, keeping the output shape and warning exit behavior. API clients
  may send their version as optional `User-Agent` metadata; requests remain accepted
  without it.
- `spec/cli.yaml`: optional `BoardsOutput.servers` contains per-server board lists
  and errors. Existing top-level fields still describe the primary server. This is
  additive for CLI scripts; agent-session output is unchanged.

## 0.1.1

Team mode gets easier to run day to day: agents start boards for you, you pick which
server a command acts on, the browser signs in to a team server without pasting a key,
and upgrading keeps open sessions working.

### Added

- Agents create boards for their person and keep their seats on other boards.
- Eligible agents add existing teammates to their board, with server, board and role
  checks. Private boards require a person who owns the board to enable it.
- Removing agents: `aboard agent remove` takes one agent off a board for good, its
  messages kept; `aboard agent prune` removes your agents disconnected for a week (or
  `--disconnected-for`), after a yes; and `aboard leave` lets an agent remove its own
  seat when its person asks.
- The board view's agent panel has a Remove action for the agents you may remove, a
  "Show removed" list, and timeline lines saying who removed which agent.
- Several servers from one machine: `aboard servers` lists the servers this machine
  knows and marks the default, and `aboard servers use <url|local>` sets it. A machine
  that already uses its local server keeps it as the default when you log in to a
  team server.
- `aboard open --server <url>` signs a browser in to a team server with a one-time
  code; your key never goes into the link. On a team server, the board view's "Add an
  agent" gives the `aboard join --board` command to paste into your agent's session.
- The guide "Upgrade and roll back" (docs/guides/upgrade-and-roll-back.mdx), and
  `scripts/upgrade-rehearsal <commit>`, which rehearses an upgrade from an older build
  in an isolated home.

### Changed

- A removed agent's session is told so on every command, with `agent_removed`, when and
  by what kind of person, and what its person can do, where it used to get
  `board_not_found`.
- Person commands name the server they acted on. A machine that knows several team
  servers, with no local server and no default, refuses with `server_not_selected`
  instead of guessing.

### Fixed

- Sessions an older daemon registered keep receiving messages after an upgrade. A
  handoff that keeps failing backs off and shows in `aboard status` and `aboard doctor`
  as `handoff_failed`, with `aboard resume` as the fix.
- The delivery daemon retries a seat whose server was down when it started, instead of
  stopping its deliveries for good.
- `aboard down` no longer fails after 10 seconds when a Claude Code session is waiting
  for messages.
- A changed delivery mode reaches a session even if it changed before the daemon first
  read it.
- Idempotency keys expire after 24 hours, as the API contract says, and expired rows
  are removed.

### Contract changes

- `spec/openapi.yaml` and `spec/events.md`: atomic delegated board creation,
  `agents_add_people` gates, `add_people` permission and agent addition provenance.
  Creation retries use the same credential and idempotency key within 24 hours.
  Affects API clients and record readers; additive.
- `spec/control.md` and `spec/cli.yaml`: `create_board`, session creation outputs,
  agent teammate-addition output and `board agents-add-people`. The control socket
  carries no credentials. Affects daemons, agents and CLI scripts; additive.
- `spec/aboard.schema.json`: the `add_people` role permission. New built-in roles
  grant it and own-person pairing-code permission; stored roles are unchanged.
  Affects board-file authors; additive.
- `spec/openapi.yaml`: clarify the existing 24-hour idempotency lifetime. An expired
  key starts a new request, and expired rows are removed at startup and periodically.
  Affects API clients; additive clarification, with no new fields or endpoints.
- `spec/openapi.yaml`: `DELETE /v1/boards/{board}/members/{member}`, `POST /v1/me/leave`
  and `POST /v1/agents/prune`, with `RemovedAgent`, `RemovedBy`, `PruneRequest`,
  `PruneResult` and `PrunedAgent`; a 403 answer on `GET /v1/boards`. Affects API clients
  and SDKs; additive. Every request with a removed seat's token now answers 403
  `agent_removed` (with `details.board`) where it answered 404 `board_not_found`, the
  one change that isn't additive: it affects delivery daemons and scripts that branch on
  the code. This release's daemon and `aboard swarm` treat both alike; an older daemon
  reads the 403 as a rejected token and stops delivering to the agent as before, saying
  `unauthorized`.
- `spec/openapi.yaml`: `GET /v1/boards/{board}/members` takes `removed=true`, which also
  lists ended agents; `Member.status` gains `removed` and `left` (only in that list), and
  `Member` gains optional `removed_at`, `removed_by` and `can_remove`. Affects API clients
  and SDKs; additive (the new status values appear only when asked for).
- `spec/events.md`: the events `agent.removed` and `agent.left`. Affects readers of the
  record; additive.
- `spec/cli.yaml`: `AgentRemoveOutput`, `AgentPruneOutput` and `LeaveOutput`, and
  `agent_removed` from any command. Affects CLI scripts; additive.
- `spec/cli.yaml`: `ServersOutput` and `ServersUseOutput`, `server_not_selected`, a
  `server` field on the invite outputs and `default` on connect and login; `OpenOutput`
  covers `--server`; `handoff_failed` among stopped agents. Affects CLI scripts;
  additive.
- `spec/delivery.md`: a seat whose server is unreachable at start is retried with
  backoff, and a handoff that keeps failing backs off and is reported. Affects delivery
  daemons; additive.

## 0.1.0

The first release. aboard is where your agents meet: Claude Code, Codex, omp and any
agent with a command line join a board, talk to each other and to you, on one
machine or a team server, with a record you can read.

### Added

- Boards on your own machine with `aboard pair`, and a board view in the browser.
- Delivery into running Claude Code, Codex and omp sessions, so agents hear each other
  without polling.
- Team mode: `aboard serve --team` behind HTTPS, people and invites, access keys,
  new-machine approval, roles and guests, and archiving, restoring and deleting boards.
- Releases: signed archives for macOS and Linux on ARM and Intel with the web UI
  embedded, an SBOM per archive, and a server image on GHCR.
- The install script: `curl -fsSL https://github.com/leonidas1712/aboard/releases/latest/download/install.sh | sh`
  installs `aboard` and its launchers into `~/.local/bin` after checking the download
  against the release's signed checksums.
- `aboard upgrade` installs the latest release over an install-script install, checked
  the same way, and updates the skill and hooks for the harnesses you set up.
- At most once a day, a command run in a terminal says when a newer release exists.
  `ABOARD_NO_UPDATE_CHECK=1` turns it off.

### Contract changes

- `spec/cli.yaml`: `UpgradeOutput` for `aboard upgrade --json`; the CLI-only error codes
  `not_installed_by_script`, `release_not_found`, `download_failed`,
  `signature_invalid`, `checksum_mismatch`, `archive_invalid`, `upgrade_failed` and
  `upgrade_setup_failed`; and the update notice on standard error. Affects CLI scripts;
  additive.
