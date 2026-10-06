# Board features: tasks, asks, agent lines, files and the brief

Status: design for review. Nothing here is built. The contracts in `/spec` already carry
it, marked additive; the build plan at the end says which slice lands each part. The
decisions are D206–D213 in [DECISIONS.md](DECISIONS.md).

This note turns the board the UI lab settled on (`web/lab/DIRECTION.md` on the
`claude/ui-lab` branch) into a server model, an API, a CLI, delivery text and a build
order. The lab mocks everything; this says what the server records, what the clients
compute, and what an agent types.

## Contents

1. [What we want](#what-we-want)
2. [Few nouns](#few-nouns)
3. [The server model](#the-server-model)
4. [Tasks](#tasks)
5. [Messages about tasks](#messages-about-tasks)
6. [Asks and decisions](#asks-and-decisions)
7. [Agent lines and the state word](#agent-lines-and-the-state-word)
8. [The Inbox](#the-inbox)
9. [Files, versions and approvals](#files-versions-and-approvals)
10. [Storage: the blob store and the database as adapters](#storage-the-blob-store-and-the-database-as-adapters)
11. [The brief](#the-brief)
12. [The record](#the-record)
13. [Permissions](#permissions)
14. [The agent's side](#the-agents-side) (first-class: defaults, nudges, errors, the skill, a walkthrough, failure paths)
15. [API changes](#api-changes)
16. [CLI](#cli)
17. [Control socket and delivery](#control-socket-and-delivery)
18. [The primitives test](#the-primitives-test)
19. [Migration from today's data](#migration-from-todays-data)
20. [Build plan](#build-plan)
21. [Open questions](#open-questions)

## What we want

A person running several agents should be able to see the work, decide what only they
can decide, and trust what they approved, without asking an agent "where are we?". The
agents produce the data behind that view, and agents forget: context gets compacted,
sessions die, instructions from three hours ago are gone. So the right thing has to be
the easy thing, and most of the data has to come from defaults rather than from an
agent remembering a rule.

How Aboard does it: four small ideas on the board (tasks, asks, an agent's line, files),
each a few fields and events the server keeps exactly, and everything else (the Inbox,
"Worth a look", nudges, the brief's steward) computed by clients from those facts.

## Few nouns

The design adds two nouns and reuses the rest.

| Noun | New? | What it is |
| --- | --- | --- |
| **Task** | Planned since D32, reshaped here | A unit of work with an id (`CHK-17`), one owner at a time, helpers, About and Where it stands |
| **Ask** | No: a kind of message | A message asking one member to decide, with optional options; blocking unless it says what it's going with |
| **Decision** | No: a kind of message | A reply that answers an ask |
| **Line** | New, small | What an agent says it's on: "Working on …" or "Paused on … until 14:20". Bookkeeping, like presence |
| **File** | Planned since D15, reshaped here | Bytes on the board with versions, maintained or one-off, linked to tasks and messages, with approvals |
| **The brief** | No: a file | The maintained file named `brief.md` |

"Blocked", "Needs you", "Worth a look", "late" and "idle" are words for facts the server
or a client derives. Nobody sets them.

## The server model

What the server keeps, in the board's record and its read models (rebuilt from the
record) or as bookkeeping (never in the record, like read positions and presence):

| Thing | Where | Rebuilt from |
| --- | --- | --- |
| Task (title, About, Where it stands, state, owner, helpers) | Read model | `task.*` events |
| A board's task prefix and next number | Read model on the board | `board.task_prefix_set`, `task.created` |
| An agent's current task | Read model on the member | `task.started`, `task.joined`, `task.done`, `task.dropped` |
| Which tasks a message is about | Read model (`message_tasks`) | `message.posted` `about` |
| An ask's fields and state | Read model (`asks`) | `message.posted` `ask` and `answer` |
| File, its versions, approvals | Read model | `file.*` events |
| File bytes | The blob store, by SHA-256 | Never rebuilt: content-addressed, written before the event that names them |
| Agent line ("Working on", "Paused on") | Bookkeeping on the member | Not rebuilt; cleared by task events and session end |
| The state word (working, paused, late, …) | Computed at read | Presence, line, clock |
| Counts (open tasks, messages since Where it stands, freshness) | Computed at read | Read models and `seq` |

Nothing here runs a model, schedules work or holds a message for approval.

## Tasks

What we want: one place that says what is being done, by whom, and how it stands, with
one owner at a time and an id an agent or a person can type.

How Aboard does it:

**Ids.** The server numbers tasks per board, 1, 2, 3, and gives each a reference made of
the board's prefix and its number: `CHK-17`. The reference is fixed when the task is
made and never changes, even if the task is renamed or the board's prefix changes later
(new tasks then get the new prefix; numbers keep counting). Agents choose titles, never
ids. Each task also has a permanent id (`tsk_…`) for the API.

- **The prefix** is 2 to 6 capital letters and digits, starting with a letter. A board
  gets one when its first task is made: the first three letters of its name in capitals
  (`general` → `GEN`, `checkout-v2` → `CHE`), with a digit added when that is taken
  (`GEN2`). A board owner changes it (`aboard board prefix CHK`), which affects only new
  tasks.
- **Prefixes are unique on a server**, including every prefix a board has used. So a
  reference names one task on the server, and an agent with seats on several boards can
  type `aboard task start CHK-17` without `--board`: the CLI finds the board among its
  seats. A prefix being taken tells a person only that some board uses it, as a taken
  board name already does.
- Wherever a command takes a task, it accepts `CHK-17`, `chk-17`, or the number alone
  (`17`) on the current board.

**State.** A task is `open` (no owner: "not picked up"), `in_progress` (it has an
owner), `done` or `cancelled`. There is no "waiting", "claimed" or "blocked" state:

- **Blocked is derived.** A task is Blocked while it has an open blocking ask (see
  [Asks](#asks-and-decisions)), and the board shows who it's waiting on. An agent never
  sets it, so it can never forget to unset it.
- Starting a task claims it, so "claimed but not started" doesn't exist.

**Fields.**

| Field | Meaning | Who writes it |
| --- | --- | --- |
| `title` | One line, at most 120 characters | Whoever opens it; later the owner or a person |
| **About** | What the task is and why, a line or two (Markdown, at most 4,000 characters). Written when it's opened; rarely changes | Whoever opens it; later the owner or a person |
| **Where it stands** | Two or three lines the owner keeps current: the task's own brief (at most 2,000 characters). Versioned | The owner and helpers (`aboard task note`), or a person |
| `owner` | The member responsible. One at a time | `start` (atomic: exactly one wins) |
| `with` | Helpers | `join` |
| `done_note` | The final note | `done` |

About and Where it stands are versioned by the record: each change is a `task.updated`
event, so every version and its author can be read back.

**The commands, one noun.**

| Command | Does |
| --- | --- |
| `aboard task list [--done] [--mine] [--all]` | Not picked up first, then in progress (Blocked marked), then, with `--done`, done |
| `aboard task show ID` | About, Where it stands, owner and helpers, Blocked on, and its conversation (threads and lone messages) |
| `aboard task new "title" [--about "…"] [--no-start]` | Opens a task and starts it in one step (an agent); a person's `new` opens it without starting |
| `aboard task start ID` | Claims it (it must be open, or yours), makes it your current task, sets your line to "Working on: <title>" |
| `aboard task join ID` | Helps on someone's task without taking it over; makes it your current task |
| `aboard task note "…" [--task ID]` | Sets Where it stands on your current task |
| `aboard task done "final note" [--task ID] [--cancelled]` | Closes it, clears your line, suggests the next task not picked up |
| `aboard task drop [ID] [--reason "…"]` | Gives it back (owner) or stops helping (helper). Rare |

`aboard tasks` isn't a command: it fails with a usage error whose hint is
`aboard task list`.

**The current task.** Each agent has at most one current task: the one it last started,
opened or joined, until it finishes or drops it. It is a read model of the record, so it
survives the session: a new or resumed session for the same agent is still on CHK-17.
It drives three defaults: what messages are about, what an ask blocks, and what
`task note` and `task done` act on.

**Ownership by people.** A person may own a task (`aboard task start CHK-17` in their
own terminal, or the board view's "Take it"). People have no line, so owning a task
changes nothing else for them.

**Releasing.** A task's owner and helpers come off it when their seat ends (removal,
leaving: D182 already promises "held tasks are released with a reason"). Any person on
the board may drop anyone's task (the board view's "Give back"), which is how work moves
off an agent whose session died and isn't coming back. An agent can drop only its own.

**Left out on purpose.** Labels, order and a suggested owner (D32) wait until a board
shows it needs them; due dates and dependencies stay out (VISION's "What Aboard leaves
out"). Leases on claims stay in the coordination-primitives research.

## Messages about tasks

What we want: the conversation and the work link both ways, from the record, without
anyone tagging by hand.

How Aboard does it: every message records which tasks it is about, in `about`, inside
the posting transaction, the way mentions are recorded (D191). The server computes the
default, so the CLI, the board view, an SDK and a bot all get the same (as D174 does for
a reply's recipients):

1. **Given:** `about` in the request (`aboard say --task CHK-12`). `about: []`
   (`--no-task`) means none.
2. **Thread:** a reply without `about` is about what the message it answers is about.
   So a whole thread stays on its task without anyone repeating it.
3. **Current:** otherwise, an agent's message is about its current task. People have no
   current task; their messages are about nothing unless they say so or reply in a
   tagged thread.
4. **Named:** any task reference in the body (`CHK-16`), outside code and links, by the
   mention rules, is added. A reference to no task stays text.

Each entry records how it got there (`given`, `thread`, `current`, `named`), so the
board view can show "tagged automatically" and the record never pretends an agent chose
something it didn't. A message is about at most 8 tasks.

`GET /v1/boards/{board}/messages?task=CHK-17` (and `aboard read --task CHK-17`) is a
real filter that pages like the others: the messages about the task, which the board
view shows under their thread roots. A task's conversation count and its thread list
come from the same read model.

## Asks and decisions

What we want: a question that needs a person's (or another agent's) decision is never
lost in the chat, says whether work is stopped on it, and its answer reaches the asker
and stays in the record.

How Aboard does it: an ask is a message with an `ask` object. It adds no event type and
no new endpoint for writing: `POST /v1/boards/{board}/messages` takes it.

```
aboard ask [@name] "question" ["option" …] [--going-with "X" [--at 16:00]] [--task ID | --no-task]
```

- **Who it goes to.** One member. With no `@name`, an agent's ask goes to its own
  person; a person's ask must name someone. An ask can go to an agent (`ask @codex …`)
  and blocks the same way.
- **Options.** Up to four extra arguments, each at most 80 characters. The one asked can
  always answer in their own words instead.
- **The task.** The ask is about the asker's current task by default (the
  [about rules](#messages-about-tasks) apply, and the ask blocks the first task that
  wasn't only named in the text); `--task` and `--no-task` override.
- **Blocking by default.** An ask blocks its task until it's answered or withdrawn: the
  task shows Blocked, waiting on the one asked.
- **Going with.** `--going-with "X"` makes it non-blocking: "going with X unless you
  say". `--at 16:00` says when the agent will go ahead (default: now). Nothing is blocked.
  After that time, an unanswered going-with ask reads "went with X"; it can still be
  answered, and an answer then reaches the agent like any other.
- **It asks for a reply.** An ask is always `expects_reply`, so it counts in the board
  list's "Needs you" (D195) and in the asked agent's wake rules (D173) exactly as a
  question does today.

**Answering.** A reply to an open ask by someone allowed to answer it is the answer:

- who may answer: the member asked, or the asker's person (who steers their own agent);
- `option` (1 to 4) says which option was picked; the body says it in words (the CLI and
  the board view fill in the option's text);
- the answer is a message, so it wakes the asker the ordinary way: a reply to its own
  message concerns it in every delivery mode but `off`;
- **the answer is the decision.** The record holds it as `answer` on the reply's
  `message.posted`; a board's decisions are its answered asks. No separate decision event
  (the lab's question 4).
- A later answer (an override, after the agent went with its default) replaces the
  earlier one as the ask's answer; both stay in the record, and the later one wakes the
  agent again.
- Anyone else's reply to an ask is an ordinary reply and answers nothing.

**Withdrawing.** The asker withdraws an ask it no longer needs
(`aboard ask --withdraw 93 "found it in the runbook"`): a reply with
`answer.withdrawn`, which unblocks the task. Only the asker may.

**Ask state**, derived from the record: `open`, `answered`, `withdrawn`, or for a
going-with ask past its time with no answer, `went_with`.

**Reading asks.** `GET /v1/asks` lists asks with their state, across the caller's
boards for a person (the Inbox) and on its board for an agent, filtered by `to_me`,
`from_me`, `state`, `task` and `board`. `aboard ask --open` prints the agent's own open
asks and the ones waiting on it: what to pick up after compaction or a new session.

Existing `aboard say --expect-reply` stays exactly as it is. `aboard ask` was an alias for
it (D36); it now makes an ask. A question without the `ask` object is still a question.

## Agent lines and the state word

What we want: at a glance, what each agent is on, and whether it's working, waiting for
something it named, late, idle or gone, in one word every screen agrees on.

How Aboard does it: each agent has at most one **line**, which is bookkeeping like
presence (D120): it changes often, it's about now rather than the record, and it never
enters the hash chain.

| Line | Shown as | Set by |
| --- | --- | --- |
| `working` | "Working on: Rotate the staging key" | `aboard working "…"`; `task start` and `task new` (the task's title); the harness's todo or plan hook |
| `paused` | "Paused on: CI run #4812 · until 14:20" | `aboard paused "…" --until 14:20` |

- A line records its `text` (at most 120 characters), `until` (paused only), the task it
  was set for, who set it (`set_by`: the agent, or its person with `--as`), its source
  (`command`, `task`, `plan`) and when.
- **The last writer wins.** An explicit command holds until the next task change or
  todo update, which then replaces it; that's the lab's rule, and it needs no
  precedence table.
- **Clearing.** `task done` and `task drop` clear a line set for that task, in the same
  transaction as the event. A session ending (presence `no_session`) clears a `working`
  line; a `paused` line stays, so a person sees it go late. `aboard working --clear`
  clears it.
- **The layers, from most to least automatic:** Aboard's own events (task start and
  done, session end) → the harness's plan hook (Claude Code's `TodoWrite`, through the
  tool hook Aboard already installs; Codex's `update_plan`, still to be checked) → the
  explicit command → the fallback "last said: …", which the board view shows from the
  agent's latest message when there's no line.
- **People have no line.** A person sets their own agent's line with `--as <agent>` from
  a terminal (`PUT /v1/boards/{board}/members/{member}/line`), shown "set by leo".
  `aboard working` without an agent fails with the usual `agent_not_selected`.

**The state word**, computed by the server on every member read, so the CLI, the board
view, the Inbox and the MCP server say the same:

| State | When | Checked in this order |
| --- | --- | --- |
| `waiting` ("waiting on you") | Presence `waiting`: a permission prompt in the agent's session. Detecting one is a future, harness-dependent hook (ROADMAP) | 1 |
| `paused` | A paused line whose `until` hasn't passed | 2 |
| `late` | A paused line whose `until` has passed | 2 |
| `disconnected` | Presence `no_session` | 3 |
| `working` | A working line, or presence `working` | 4 |
| `idle` | Neither | 5 |

## The Inbox

What we want: one place for the person's decisions, ordered by what holds up work.

How Aboard does it: the Inbox is a client view over public reads, no new server state.

- **Needs you:** `GET /v1/asks?to_me=true&state=open` across the person's boards,
  blocking asks first, then going-with asks; newest first within each.
- **Worth a look:** from each board's members and tasks: agents `late` on a pause,
  agents `idle` with no current task for 30 minutes, tasks Blocked on someone other than
  the viewer for 3 hours or more, and tasks in progress whose owner is `disconnected` for
  30 minutes or more. Ordinary pauses and fresh blocks between agents stay on the board.
- **Counts for the sidebar:** each board in `GET /v1/boards` carries `asks_to_me`
  (`blocking`, `going_with`) beside the existing `needs_reply` and `unread`.

The thresholds are the client's; the facts are the server's. A CLI inbox across boards
for a person (D102) uses the same reads when it's built.

## Files, versions and approvals

What we want: an agent's output, a person's input, and the board's maintained documents
live on the board with their history; a person's approval sticks to the exact version
they saw; and a stale write fails instead of overwriting someone's work.

How Aboard does it:

- **A file** has a name on the board (letters, digits, `.`, `_`, `-`; at most 100
  characters; folders wait for the "versioned folder" design), and **versions** 1, 2, 3.
  Each version is the bytes' SHA-256, their size and media type, who wrote it, when, and
  the version it replaced.
- **Writing names the version it replaces** (`base`). A new file has base 0. A base that
  isn't the latest fails with 409 `file_changed`, naming the latest version and who wrote
  it, so two agents editing one file never silently lose each other's work (D33's rule,
  for every file).
- **Maintained or one-off.** A maintained file is kept current and shown first (the
  brief, a status page); a one-off answers one question. `maintained` replaces D33's
  "pinned": one flag, set on the file, changeable later.
- **Links.** A file is about tasks (`about` on the file, the same tags as a message), and
  a message can carry file versions (`files`, `aboard say --attach report.html`). The
  file's page lists the messages it was posted in and the tasks it's for; a task's page
  lists its files.
- **Approvals.** A person records their own approval of one version
  (`aboard file approve report.html`, or the board view): it names the version and its
  digest. The file then reads "you approved v3 · 2 changes since" for that person, and
  the others see each person's approval. Approving is a person's statement, so agents
  can't; a later version never inherits an approval. Taking it back writes
  `file.approval_removed`.
- **Bytes never change** (D15): uploads are stored as sent, and a text file that
  contains a credential is refused (`file_has_secret`). The limit is 50 MB
  (`file_too_large`).
- **Freshness facts** for every file, counted from the record since its latest
  version: messages posted, tasks done, asks answered. Objective, never a verdict.
- **Not here:** deleting bytes (the record is append-only; erasing content is its own
  design), folders, previews of HTML (the lab's safe preview needs a security review
  first), and a "review record" with findings.

## Storage: the blob store and the database as adapters

What we want: whoever deploys Aboard picks where the database and the file bytes live,
by configuration, as with any open-source server: a single box with SQLite and a disk
folder today; later a cluster with Postgres and S3-compatible storage. The domain never
knows which.

### Today

Files aren't built, so nothing stores file bytes yet, and no file interface exists. The
database is already behind ports: `board.Store` (its `Read` and `Write` transactions,
`ReadTx` and `Tx`), with SQLite as the only adapter and `boardtest` as its contract
suite. This design adds the second port the same way (engineering/architecture.md
already lists "file storage" as a reason for an interface).

### The blob port

Declared by the domain (`server/internal/board`, `ports.go`), small, named for the job:

```go
// Blobs stores file bytes by their SHA-256. Bytes never change once stored, so a
// digest names the same bytes forever, and storing the same bytes twice keeps one copy.
type Blobs interface {
	// Put reads r to the end, storing at most max bytes, and returns the bytes'
	// digest and size. More than max fails with ErrTooLarge and stores nothing; a
	// read error stores nothing. Storing bytes already stored succeeds and keeps one copy.
	Put(ctx context.Context, r io.Reader, max int64) (Blob, error)
	// Open streams the bytes stored under digest, checking them against it as they
	// are read (ErrCorrupt at the end when they don't match). ErrNotFound when absent.
	Open(ctx context.Context, digest string) (io.ReadCloser, Blob, error)
	// Stat says whether bytes are stored under digest, and their size.
	Stat(ctx context.Context, digest string) (Blob, bool, error)
	// Walk calls fn for every stored blob, in no particular order. For checks,
	// copying between stores and removing unreferenced bytes.
	Walk(ctx context.Context, fn func(Blob) error) error
	// Delete removes bytes nothing references. Only the unreferenced-bytes sweep calls it.
	Delete(ctx context.Context, digest string) error
}

type Blob struct {
	Digest string // "sha256:" and 64 hex digits
	Size   int64
}
```

- **Streaming:** `Put` hashes while it writes to a temporary place and moves the bytes
  under their digest only when the whole stream is in, so a partial upload never appears.
- **Dedupe:** two uploads of the same bytes keep one copy. `Stat` lets a later client
  skip sending bytes the server has (a request carrying the digest first); the first
  version always streams.
- **Order with the record:** bytes are stored first, then the transaction that appends
  `file.version_added` names the digest. A crash between the two leaves bytes nothing
  references, never a version without bytes. A sweep removes unreferenced bytes older
  than 24 hours, so an upload in flight is never swept. Referenced bytes are never
  deleted: the record is append-only.
- **Where the rest lives:** file names, versions, `maintained`, `about`, approvals and
  links are events and read models in the database, through `board.Store`, in the same
  transaction as every other write. The blob store holds bytes and nothing else, so
  swapping it never touches the rules.

### Adapters

| Adapter | URL | Status | Notes |
| --- | --- | --- | --- |
| Disk | `disk:///data/aboard/files` | Ships with files (slice 4) | `sha256/ab/cd/<hex>` under the folder, temporary files in `tmp/` on the same disk, `fsync` then rename; folders 0700 and files 0600, owned by the server's user, checked at start like the data folder (D199) |
| S3-compatible | `s3://bucket/prefix?endpoint=…&region=…` | Later (VISION's "Later" column) | Credentials only from the environment (`AWS_ACCESS_KEY_ID` …), never in the URL; a URL with a user or password is refused. Signed download URLs come with it |
| Bytes inside SQLite | none | Not shipped | Weighed below |

**Why not "bytes inside SQLite" for small servers.** Its appeal: one file to back up,
and bytes in the same transaction as the record, so nothing is ever orphaned. Against
it: every database backup copies every byte again (the backup before each migration
(D184, D199) would copy 50 MB files three times over), the write-ahead log grows with
each upload, a 50 MB value has to be streamed through SQLite's incremental blob calls or
held in memory, and a later move to Postgres would carry the bytes into Postgres too.
The disk adapter gets the same safety from content addressing (bytes first, then the
record; never deleted while referenced), and a disk folder of immutable files is the
easiest thing there is to back up incrementally. So the disk adapter ships first and
alone.

### Configuration

Two settings, each a URL, chosen independently:

| Setting | Flag | Variable | Default |
| --- | --- | --- | --- |
| Database | `--db` | `ABOARD_DB` | `sqlite://<data>/aboard.db` |
| File store | `--files` | `ABOARD_FILES` | `disk://<data>/files` |

- With `--data` (or `ABOARD_DATA`) alone, nothing changes for anyone: both default under
  it, as today's database does. The local server is the same, under Aboard's data folder.
- A flag wins over its variable, and both are read only by `aboard serve` (with or
  without `--team`), as D199's variables are.
- `postgres://…` and `s3://…` are reserved: today they fail at start with
  `storage_unsupported`, naming the adapters this build has.

**Valid combinations:**

| Database | Files | Replicas | Status |
| --- | --- | --- | --- |
| SQLite | Disk | One | Today |
| SQLite | S3 | One | Later |
| Postgres | Disk | One (the disk is one machine's) | Later |
| Postgres | S3 | Several | Later: the cluster recipe |

A disk store on a network file system is refused for the same reason SQLite on one is
(D199): the recipes say so, and the start check refuses a folder that reports a network
file system where the platform can tell.

**What the server says.** At start, one log line naming the adapters and nothing secret:
`{"msg":"storage","db":"sqlite","files":"disk","files_dir":"/data/aboard/files"}` (an
S3 store would log its endpoint host and bucket, never keys). `GET /v1/info` gains
`storage: {db: "sqlite", files: "disk"}`, the kinds only, and `features`, the list D151
planned, so a client can tell a server with files from one without.

### Moving, backing up and restoring

- **Backup, SQLite and disk** (today's single box and the Fly and Kubernetes volume
  recipes): back up the database first, then the files folder. Because bytes are stored
  before the record names them and never deleted while referenced, every version the
  database backup names is in a files copy taken after it. The files folder only grows
  and never changes in place, so `rsync` or any incremental copy is enough. A volume
  snapshot of `/data/aboard` takes both at once, and the recipes recommend it. The
  automatic backup before a migration (D184, D199) copies the database only: migrations
  never touch bytes.
- **Restore:** put back the database and the files folder, start the server, and run
  `aboard storage check`, which reads every version the database names and reports any
  whose bytes are missing or don't match their digest (exit 3, `check_failed`).
- **Moving between file stores** (disk to S3, later): `aboard storage copy --from URL --to
  URL` copies every blob, verifies each by digest on the way, skips what the target
  already has, and can be run again; then the operator changes `ABOARD_FILES` and runs
  `aboard storage check`. Because blobs are content-addressed, the copy can run while the
  server runs; a last run after stopping it catches what arrived meanwhile.
- **Moving databases** (SQLite to Postgres, later) is an export of the record and a
  rebuild of the read models, its own design with the Postgres adapter.
- **Recipes need:** a volume big enough for the database, three backups of it, and the
  files; and, for the cluster recipe, an S3 bucket and Postgres.

### The contract suite

`board/blobtest.Run(t, func() board.Blobs)`, which every adapter passes (D144) and which
moves out of `internal` with the second adapter (D78), checks:

- put then open gives back the same bytes and the right digest and size;
- the same bytes twice keep one copy, including two puts at once;
- exactly `max` bytes succeed and `max+1` fail with `ErrTooLarge`, storing nothing;
- a reader that fails halfway stores nothing, and `Walk` doesn't list it;
- stat and open of a missing digest say not found;
- bytes changed behind the store's back fail on read with `ErrCorrupt`;
- a stream larger than memory buffers goes through without being held whole;
- `Walk` lists everything put; `Delete` removes one and only one.

The SQLite store keeps passing `boardtest`, which gains the task, ask, line and file
read models. A Postgres adapter would pass the same suite.

## The brief

What we want: a board says in one place what it's for and where it stands, and says
how much has happened since someone last wrote it.

How Aboard does it: **the brief is the maintained file named `brief.md`.** No new noun:
it's written with `aboard file put brief.md --base N` (or the board view's editor),
versioned like any file, and anyone on the board who may write files may write it,
people and agents.

- **Freshness facts** come with it on the board (`brief` on `GET /v1/boards/{board}`):
  version, who wrote it and when, and since then how many messages, tasks done and asks
  answered. The board view shows "by claude-2 · 2 h ago · 41 messages since".
- **The steward is a convention.** The charter may name an agent who keeps the brief
  current ("claude-2 keeps brief.md current"). Nothing enforces it; "Ask the steward to
  update it" is an ordinary message the board view fills in.
- **The keeper's nudge** goes to whoever wrote the latest version, if it's an agent (see
  [nudges](#nudges)).
- A new agent's join output names the brief (D40's board brief gains it) and
  `aboard brief` prints it with its freshness line.

## The record

Everything that changes the board's content is an event in its hash chain; everything
about "now" is bookkeeping. The envelope and hashing don't change: new types and new
fields in `data`, covered by `data_hash` like every payload (spec/events.md).

| Type | Written when | `data` |
| --- | --- | --- |
| `board.task_prefix_set` | The first task on a board is made (just before its `task.created`, same transaction), or an owner changes the prefix | `before` (null the first time), `after` |
| `task.created` | `POST …/tasks` | `task_id`, `ref`, `number`, `title`, `about` |
| `task.started` | `…/start`, and `task new` that starts | `task_id`, `ref`, `member_id` (the new owner), `previous_owner` (null unless a person re-assigned it) |
| `task.joined` | `…/join` | `task_id`, `ref`, `member_id` |
| `task.updated` | `PATCH …/tasks/{task}` | `task_id`, `ref`, and any of `title`, `about`, `stands` (with `stands_version`) |
| `task.done` | `…/done` | `task_id`, `ref`, `note`, `cancelled` |
| `task.dropped` | `…/drop`, or a seat ending | `task_id`, `ref`, `member_id`, `as` (`owner` or `helper`), `reason`, `by` (`self`, `person`, `seat_ended`) |
| `file.version_added` | `POST …/files` | `file_id`, `name`, `version`, `digest`, `size`, `media_type`, `base_version`, `maintained`, `about` (task ids) |
| `file.updated` | `PATCH …/files/{file}` | `file_id`, and any of `maintained`, `about` |
| `file.approved` | `PUT …/approval` | `file_id`, `version`, `digest` |
| `file.approval_removed` | `DELETE …/approval` | `file_id`, `version` |

`message.posted` gains, all optional and absent from older events:

- `about`: `[{task_id, ref, how}]`;
- `ask`: `{to, options, blocking, going_with, going_at, task_id}`;
- `answer`: `{ask_id, option, withdrawn}`;
- `files`: `[{file_id, version, digest}]`.

**Visibility.** Task and file events are board content every member reads, like a
title. Under `addressed` visibility, a message's `about`, `ask`, `answer` and `files`
are withheld with the rest of its payload; the task's own counts then count only what
the reader may see.

**Not in the record:** lines, the current task's line, the state word, presence,
read positions, freshness counts and nudges.

## Permissions

The existing permission list already has what tasks and files need (`create_tasks`,
`claim_tasks`, `upload_files`); asks and answers are posts (`post`). No new permission.

| Action | Agents | People on the board | Notes |
| --- | --- | --- | --- |
| Open a task | With `create_tasks` | Always | |
| Start, join a task | With `claim_tasks` | Always | Start needs the task open, or already yours |
| Edit title, About | The opener or owner | Always | |
| Where it stands | Owner and helpers | Always | |
| Done | The owner | Always | `--cancelled` too |
| Drop | Their own part | Anyone's | |
| Change the task prefix | An agent whose person owns the board (as a title, D176) | Board owners | |
| Ask | With `post` | Always | One recipient; an agent's default is its person |
| Answer | The one asked | The one asked, or the asker's person | |
| Withdraw | The asker | The asker | |
| Set a line | Its own (`/v1/me/line`) | Their own agents' only | Bookkeeping |
| Put a file version, edit `maintained` and `about`, write the brief | With `upload_files` | Always | |
| Approve a file version | Never (`human_token_required`) | Their own approval | |
| Turn nudges off | Never | Board owners (policy) | |

- **Guests and their agents** do board content as their role allows (tasks, asks,
  answers, files, lines), like posting; they never change the prefix or the policy
  (`guest_not_allowed`).
- **Archived boards** refuse every new event (409 `board_archived`); lines, like
  presence, are bookkeeping and still work.
- **A role limited to task types** (`claim_tasks: [experiment]`) claims any task until
  tasks carry types; the limit is kept in the board file.
- **Admins of the server** get nothing new: tasks, asks and files are board content, read
  only by those on the board (D180).

## The agent's side

What we want: an agent that remembers nothing but the skill's four rules, and forgets
even those, still produces the data the board needs, and is told the next step whenever
it slips, in a line it can act on, without being interrupted or spending a turn.

### Four ideas, four rules: what the skill teaches

The skill grows by one short section, and everything else is discoverable from command
output and errors. The ideas, each a noun the board view shows:

| Idea | Commands |
| --- | --- |
| **Tasks** | `aboard task list · show · new · start · join · note · done · drop` |
| **Ask** | `aboard ask [@name] "question" ["option" …] [--going-with "X" --at 16:00]` |
| **Paused** | `aboard paused "…" --until 14:20` (and `aboard working "…"`) |
| **Say and reply** | `aboard say`, `aboard say --reply`, tagged with your current task |

The rules, word for word as the skill will say them:

1. **Tasks:** run `aboard task start <id>` (or `aboard task new "…"`) before you work,
   and `aboard task done "…"` when you finish. Keep Where it stands current with
   `aboard task note "…"`.
2. **Ask:** when you need a decision, `aboard ask` with the options. Add `--going-with`
   when you can safely go ahead; otherwise end your turn: the answer wakes you.
3. **Paused:** before anything that makes you wait (CI, a review, a long run),
   `aboard paused "…" --until <time>`.
4. **Say:** talk on the board as before. Your messages are about your current task;
   add `--task` only for another one.

Nothing else is taught. `task show`, `--no-task`, `ask --withdraw`, `ask --open`,
`working --clear`, `file approve` and the brief are found from output, hints and
`aboard help`.

### Defaults that do the work

| The agent does | The board gets, without another command |
| --- | --- |
| `task start CHK-17` | Owner, current task, line "Working on: Rotate the staging key", every later message about CHK-17 |
| `task new "…"` | The task, its id, About, and all of the above |
| `say "…"` | `about: CHK-17` (current); a reply inherits its thread's tasks; `CHK-12` in the text links it too |
| `ask "…" "A" "B"` | Goes to its person, about CHK-17, blocks it; Blocked and Needs you appear; the answer wakes it |
| Updates its todo list (Claude Code) | Its line follows the item in progress |
| `task done "…"` | Closed, line cleared, current task cleared, next task suggested |
| Its session ends | Working line cleared, state `disconnected`; the task stays its own and current for its next session |

### Nudges

Each nudge is one line, at most 200 bytes, in Aboard's own words (from the
`deliverytext` package, so the CLI and the daemon say the same). Each:

- **is cheap:** a fact and the command to run, nothing to read first;
- **is rate-limited:** at most once per window per seat, as listed; the CLI keeps when it
  last showed each in its state folder (`nudges.json`, per seat id), the daemon in
  memory per session (a daemon restart may show one again, once);
- **never blocks:** never a non-zero exit, never a wake, never between a tool call and
  its result; it rides on output or context that's already being sent;
- **can be turned off per board:** the board policy key `nudges: off` (owners only,
  recorded as a policy change, shown in `aboard status` and the board view's Rules)
  stops every nudge below on that board except the reorientation note, which is state,
  not advice;
- **is in `--json` too**, as `nudges: [{code, text}]`, since agents read JSON.

| Code | When | Where it appears | Wording | Rate |
| --- | --- | --- | --- | --- |
| `tasks_not_picked_up` | The agent has no current task and the board has open tasks | `aboard inbox` header; the session-start note; `aboard status` | `2 tasks not picked up: aboard task list` | Inbox: once per 30 min per seat. Note and status: every time (facts) |
| `no_task_posts` | The agent posted 3 or more messages in a row about no task, with no current task (`posts_without_task` from the server) | `aboard say` output | `Tip: your last 3 messages are about no task. Start one with aboard task start CHK-17, or open one: aboard task new "…"` (names the oldest open task; without one, only `task new`) | Once per 30 min per seat |
| `next_task` | `task done` succeeded and an open task exists | `aboard task done` output | `Next not picked up: CHK-20 Document the v2 webhooks (aboard task start CHK-20)` | Every done |
| `pause_late` | The agent's paused line passed its `until` | The next tool boundary of a running turn (in the waiting notice's element), else the next turn's start | `Aboard: you said you were paused on CI run #4812 until 14:20; it's 14:31. Say where you are: aboard working "…", or aboard paused "…" --until <time>.` | Once per paused line. An idle agent isn't woken: its person sees `late` |
| `line_stale` | A working line set by command is 2 h old and no task event or plan update came since | The next turn's start | `Aboard: your Working on line is 2 h old ("Migrating the fixtures"). Update it with aboard working "…", or clear it with aboard working --clear.` | Once per line |
| `ask_instead` | An agent's message (not an ask, no `--expect-reply`) says it's blocked or waiting on someone: "I'm blocked", "blocked on", "waiting for/on you", "need your decision/approval/input", "can you decide/confirm" (a fixed, case-blind word list in the CLI; no model) | `aboard say` output | `Tip: to get a decision, ask: aboard ask "…" "option" "option". It marks CHK-17 Blocked and the answer wakes you.` | Once per hour per seat |
| `stands_stale` | The agent posts about its current task and Where it stands is 8 or more messages behind (and 15 min old), or was never written after 8 | `aboard say` output | `CHK-17 · Where it stands is 9 messages old: aboard task note "…"` | At 8, then every 8 more |
| `ask_options` | An ask was sent with no options | `aboard ask` output | `Tip: next time add 2 to 4 options after the question; @leo answers with one key, or in their own words.` | Once per day per seat |
| `brief_stale` | The agent wrote the brief's latest version, and since it 30 messages were posted or 3 tasks done, and it's an hour old | The next turn's start | `Aboard: brief.md on checkout-v2 is 41 messages and 3 tasks old since your version 6. Update it if you keep it: aboard brief.` | Once per version and threshold |
| `first_task` | The first 3 times a seat starts a task | `task start` and `task new` output | `Your messages are about CHK-17 until it's done; add --task to say otherwise.` | 3 times per seat |
| `reorient` | A session takes a seat or comes back: `pair`, `join`, `resume`, a harness resume, and Claude Code's compaction (`SessionStart` with source `compact`) | The session-start note; the bind commands' output | See [failure path 3](#3-its-session-died-mid-task) | Every time; not turned off by `nudges: off` |

What was weighed and left out:

- **A reminder in the next delivery for task-less chatter.** `say` output reaches the
  agent at the moment it posts, costs no delivery bytes, and can't be mistaken for a
  message; a delivery-time reminder would arrive later, padded onto someone else's
  words. So `no_task_posts` lives in `say`.
- **The server detecting "I'm blocked".** A word list in the server would put a
  heuristic on the write path and in the record's neighbourhood (D79's spirit); in the
  CLI it's a hint the agent can ignore, and only the CLI shows it.
- **A nudge when an ask goes unanswered for long.** That's the person's Inbox, not the
  agent's business; the agent was told to end its turn or go with a default.

### Errors that name the next step

New codes, in the house style (`Error (code): message` then `Hint:`):

| Code | When | Hint |
| --- | --- | --- |
| `no_current_task` (CLI) | `task note`, `task done` or `task drop` with no ID and no current task | `Start one with aboard task start CHK-17, or name it: aboard task note CHK-17 "…"` (`details.tasks`: the agent's own open tasks, then the oldest not picked up) |
| `task_not_found` | No task with that id on the board, or one the caller can't see | `aboard task list` |
| `task_taken` (409) | `start` on a task someone else owns | `CHK-12 is claude-2's. Help with aboard task join CHK-12, or pick another: aboard task list` |
| `task_closed` (409) | `start`, `join`, `note` or `done` on a done or cancelled task | `Open a follow-up: aboard task new "…" --about "After CHK-12: …"` |
| `not_on_task` (403) | `note` or `done` by an agent that isn't the owner or a helper | `Join it first: aboard task join CHK-12` |
| `task_prefix_taken` (409) | A prefix another board uses or used | `Pick another, such as CHK2` |
| `ask_invalid` (422) | More than 4 options, an option over 80 characters, `going_at` without `going_with`, an ask with no recipient from a person, or more than one | Names the problem and the shape: `aboard ask [@name] "question" ["option" …]` |
| `not_asked` (403) | An answer `option`, or a withdrawal, from someone who may not | `Only @leo, who was asked, or the asker's person can answer #93. Reply without --option to comment.` |
| `ask_closed` (409) | Answering or withdrawing a withdrawn ask | `#93 was withdrawn. Ask again if it still matters.` |
| `line_until_past` (422) | `--until` already passed | `Use a later time, or a duration: --until 20m` |
| `file_changed` (409) | `base` isn't the latest version | `brief.md is at v5 (by claude-2, 3 min ago). Get it with aboard file get brief.md, merge your change, then aboard file put brief.md --base 5` |
| `file_not_found` | | `aboard file list` |
| `file_too_large` (413) | Over the board's file limit | `Files can be at most 50 MB here. Put a link in a message instead.` |
| `file_has_secret` (422) | A text file containing a credential | `Remove the credential and upload again; file bytes are never changed for you.` |
| `version_not_found` | | `aboard file show NAME lists its versions` |
| `stands_changed` (409) | `task note --base N` raced another note | `Where it stands changed (v4 by codex). Read it with aboard task show CHK-17, then note again.` |
| `storage_unsupported` (`aboard serve`) | `ABOARD_DB` or `ABOARD_FILES` names an adapter this build lacks | `This build stores the database in SQLite (sqlite://…) and files on disk (disk://…).` |

`aboard tasks`, `aboard paused` with no `--until` (it's required) and a time it can't
read are usage errors (exit 2) whose hint is the right command.

### A full session

claude, an agent of leo's, on `checkout-v2`. Text output as it will print; each command
also has `--json` (spec/cli.yaml).

```
$ aboard inbox
checkout-v2 · nothing new for claude
2 tasks not picked up: aboard task list

$ aboard task list
checkout-v2 · 4 open, 3 done (aboard task list --done)
Not picked up
  CHK-17  Rotate the staging Stripe key
  CHK-20  Document the v2 webhooks
In progress
  CHK-12  Move payment intents to the v2 API · claude-2, with codex
  CHK-19  Ramp to 10% · codex · Blocked: waiting on @reviewer for 5 min (#88)

$ aboard task start CHK-17
CHK-17 is yours on checkout-v2: Rotate the staging Stripe key
Working on: Rotate the staging Stripe key
Your messages are about CHK-17 until it's done; add --task to say otherwise.

$ aboard say "Found the key in two CI configs; rotating both."
Sent #91 to all on checkout-v2 · about CHK-17
@claude-2, @codex see it at their next turn. @leo sees it on the board or in their inbox.

$ aboard task note "Both configs found; vault access is the last step."
CHK-17 · Where it stands updated (v2)

$ aboard ask "Request vault access for me, or hand CHK-17 to priya?" "Request access" "Hand it to priya"
Asked @leo #93 on checkout-v2 · about CHK-17
CHK-17 is Blocked until @leo answers. The answer wakes you: end your turn, or work on something else.
```

The agent ends its turn. leo answers with key 1 in the Inbox. The answer is a reply to
#93, so it concerns claude and wakes it:

```
<aboard-messages board="checkout-v2" count="1">
<aboard-message board="checkout-v2" from="@leo" sender="owner" seq="97" reply-to="93" about="CHK-17" answers="93" option="1">
Request access
</aboard-message>
</aboard-messages>
Aboard: @leo answered your ask #93 with option 1, "Request access". CHK-17 is no longer Blocked.
```

```
$ aboard ask --going-with "rotate at 16:00" --at 16:00 "Rotate during the 16:00 ramp?"
Asked @leo #98 on checkout-v2 · about CHK-17
Going with "rotate at 16:00" at 16:00 unless @leo says otherwise. Nothing is blocked.

$ aboard paused "CI run #4812" --until 14:20
checkout-v2 · claude · Paused on: CI run #4812 · until 14:20 · CHK-17

$ aboard working "Rotating the key in the second config"
checkout-v2 · claude · Working on: Rotating the key in the second config · CHK-17

$ aboard task done "Rotated both keys; the old key is revoked."
CHK-17 done on checkout-v2. Working on: cleared.
Next not picked up: CHK-20 Document the v2 webhooks (aboard task start CHK-20)
```

In `--json`, `task done` prints `{"task":{…"state":"done"…},"line":null,"next":{"ref":"CHK-20",…},"nudges":[{"code":"next_task","text":"…"}]}` (TaskOutput).

### Failure paths

#### 1. It forgot to start a task

claude starts on the work straight away. Its line is set by its todo list (Claude Code),
so the board still shows "working · Running the migration", but on no task.

```
$ aboard say "Migration script runs clean on staging."
Sent #120 to all on checkout-v2
@claude-2 sees it at its next turn. @leo sees it on the board or in their inbox.
$ aboard say --to @claude-2 "Can you check the rollback path?"
Sent #121 to @claude-2 on checkout-v2
@claude-2 gets it when its turn ends.
$ aboard say "Rollback works too."
Sent #122 to all on checkout-v2
@claude-2 sees it at its next turn. @leo sees it on the board or in their inbox.
Tip: your last 3 messages are about no task. Start one with aboard task start CHK-20, or open one: aboard task new "…"

$ aboard task note "Migration and rollback both pass."
Error (no_current_task): You have no current task on checkout-v2.
Hint: Start one with aboard task start CHK-20, or open one with aboard task new "Run the payments migration", then note it.

$ aboard task new "Run the payments migration" --about "Move the v1 rows to v2 before the ramp."
CHK-23 is yours on checkout-v2: Run the payments migration
Working on: Run the payments migration
Your messages are about CHK-23 until it's done; add --task to say otherwise.
```

The three untagged messages stay untagged (the record never rewrites a message); the
agent can link them by naming `CHK-23` in its next message, which is `named` in `about`.
If it never starts a task, its person sees it in Worth a look once it's idle without one.

#### 2. It asked without options

First it doesn't ask at all: it says it's stuck in an ordinary message.

```
$ aboard say "I'm blocked until you decide what to do about the flaky upload test."
Sent #129 to all on checkout-v2 · about CHK-20
@leo sees it on the board or in their inbox.
Tip: to get a decision, ask: aboard ask "…" "option" "option". It marks CHK-20 Blocked and the answer wakes you.
```

(The tip shows only when the agent has no open ask on that task.) It asks, but without
options:

```
$ aboard ask "What should I do about the flaky upload test?"
Asked @leo #130 on checkout-v2 · about CHK-20
CHK-20 is Blocked until @leo answers. The answer wakes you: end your turn, or work on something else.
Tip: next time add 2 to 4 options after the question; @leo answers with one key, or in their own words.
```

The ask works as it is: it's in leo's Needs you, CHK-20 shows Blocked, and leo answers
in words in the board view, which wakes claude like any answer. Options are a
convenience for the person, never a requirement.

#### 3. Its session died mid-task

claude's terminal closed while it owned CHK-17, paused on CI until 14:20.

- The daemon reports `no_session`; the server clears no paused line (only a working
  one), so the board shows claude `late` at 14:21, and Worth a look lists "CHK-17 in
  progress, claude disconnected 30 min" for leo.
- Nothing is reassigned. leo may "Give back" CHK-17 (drop it) so another agent can start
  it, or bring claude back.
- **The same session resumes** (`claude --resume`, D157): the session-start note is the
  reorientation, built by the daemon from the agent's inbox `work` and its open asks:

```
Aboard: this session is claude on checkout-v2 again, as it was before it closed. Delivery mode: focused. …
You're on CHK-17 Rotate the staging Stripe key (owner). Where it stands, 52 min ago: "Both configs found; vault access is the last step."
Your line says: Paused on CI run #4812 until 14:20 (late). Waiting on @leo: ask #98 (going with "rotate at 16:00" at 16:00). 1 answer arrived while you were away; it comes with this turn.
```

- **A new session takes the agent over** (`aboard resume claude`): the same lines follow
  `resume`'s output. **Compaction** in Claude Code (session start with source
  `compact`) gives the same note, so a compacted agent finds its task, its line and what
  it's waiting on again without reading the board.
- **Another agent picks it up** after leo dropped it: `aboard task start CHK-17` works,
  and `task show CHK-17` gives it About, Where it stands and the whole conversation.

The note is at most 600 bytes: the task's ref, title and Where it stands cut to one
line, the line, and counts of asks; never message bodies.

## API changes

All additive (spec/openapi.yaml). New paths:

| Method and path | Operation | Notes |
| --- | --- | --- |
| `GET /v1/boards/{board}/tasks` | `listTasks` | `state` (`active`, the default: open and in progress; `open`, `in_progress`, `done`, `cancelled`, `all`), `owner`, `mine`, `limit`; not picked up first |
| `POST /v1/boards/{board}/tasks` | `createTask` | `{title, about?, start?}`; `start` makes the caller the owner in the same transaction (an agent's default in the CLI) |
| `GET /v1/boards/{board}/tasks/{task}` | `getTask` | `{task}` accepts `CHK-17`, the number, or `tsk_…` |
| `PATCH /v1/boards/{board}/tasks/{task}` | `updateTask` | `{title?, about?, stands?, stands_base?}` |
| `POST /v1/boards/{board}/tasks/{task}/start` | `startTask` | Atomic claim; sets the caller's current task and line |
| `POST /v1/boards/{board}/tasks/{task}/join` | `joinTask` | |
| `POST /v1/boards/{board}/tasks/{task}/done` | `finishTask` | `{note, cancelled?}` |
| `POST /v1/boards/{board}/tasks/{task}/drop` | `dropTask` | `{member?, reason?}`: a person may name whose part to drop |
| `GET /v1/asks` | `listAsks` | `board`, `to_me`, `from_me`, `state`, `task`, `limit`; a person's covers their boards |
| `PUT`, `DELETE /v1/me/line` | `setLine`, `clearLine` | Agent tokens |
| `PUT`, `DELETE /v1/boards/{board}/members/{member}/line` | `setMemberLine`, `clearMemberLine` | The agent's person, with their own key or browser |
| `GET`, `POST /v1/boards/{board}/files` | `listFiles`, `putFile` | `POST` streams the bytes; `name`, `base`, `maintained`, `about`, `media_type` as query parameters |
| `GET`, `PATCH /v1/boards/{board}/files/{file}` | `getFile`, `updateFile` | `{file}` is the name or `fil_…` |
| `GET /v1/boards/{board}/files/{file}/versions/{version}` | `getFileVersion` | The bytes, with `ETag` the digest |
| `PUT`, `DELETE /v1/boards/{board}/files/{file}/approval` | `approveFile`, `removeFileApproval` | People only; `{version}` |

Changed (fields and parameters only added):

- `PostMessageRequest`: `about`, `ask`, `answer`, `files`.
- `Message`: `about`, `ask` (with derived `state` and the answer's `seq`), `answer`,
  `files`.
- `GET /v1/boards/{board}/messages`: `task`.
- `Board`: `task_prefix`, `tasks_open`, `asks_to_me`, `brief`.
- `UpdateBoardRequest`: `task_prefix`.
- `Policy` and `PolicyChange`: `nudges` (`on`, `off`).
- `Member`: `line`, `state`, `current_task`. `PresenceEvent` on the stream: `line` and
  `state`, sent when either changes.
- `Inbox`: `work` (`AgentWork`: current task with Where it stands' age, open tasks,
  posts without a task, the agent's open asks, its line, the brief's freshness for its
  keeper, and whether nudges are on), which the CLI's nudges and the daemon's notes read
  with the inbox they already fetch.
- `Me`: `current_task` for an agent.
- `ServerInfo`: `features`, `storage`.
- New error codes as listed in [Errors](#errors-that-name-the-next-step).

## CLI

New commands and flags (spec/cli.yaml has every `--json` shape):

| Command | `--json` | Exit |
| --- | --- | --- |
| `aboard task list [--done] [--mine] [--all] [--limit N]` | TaskListOutput | 0 |
| `aboard task show ID` | TaskShowOutput | 0, 1 `task_not_found` |
| `aboard task new "title" [--about "…"] [--no-start]` | TaskOutput | 0 |
| `aboard task start ID` · `join ID` | TaskOutput | 0, 1 `task_taken`, `task_closed` |
| `aboard task note "…" [--task ID] [--base N]` | TaskOutput | 0, 1 `no_current_task`, `stands_changed` |
| `aboard task done "note" [--task ID] [--cancelled]` | TaskOutput | 0, 1 |
| `aboard task drop [ID] [--reason "…"]` | TaskOutput | 0, 1 |
| `aboard ask [@name] "question" ["option" …] [--going-with X [--at T]] [--task ID \| --no-task]` | AskOutput | 0, 1 `ask_invalid` |
| `aboard ask --withdraw MSG ["why"]` · `aboard ask --open` | AskOutput · AskListOutput | 0 |
| `aboard say … [--task ID \| --no-task] [--option K] [--attach FILE]` | SayOutput (gains `nudges`) | as today |
| `aboard working "…"` · `aboard working --clear` · `aboard paused "…" --until T` | LineOutput | 0, 1 `line_until_past`; 2 for a missing `--until` |
| `aboard read --task ID` | ReadOutput | as today |
| `aboard file list` · `show NAME` · `get NAME [--version N] [--out PATH]` · `put PATH [--name NAME] [--base N] [--maintained] [--task ID]` · `approve NAME [--version N] [--remove]` | FileListOutput · FileOutput · FileGetOutput · FileOutput · FileApprovalOutput | 0, 1 `file_changed` … |
| `aboard brief` | BriefOutput | 0, 1 `file_not_found` (no brief yet: the hint says how to write one) |
| `aboard board prefix PREFIX` | BoardPrefixOutput | 0, 1 `task_prefix_taken` |
| `aboard storage check [--db URL] [--files URL]` | StorageCheckOutput | 0, 3 `check_failed` |
| `aboard storage copy --from URL --to URL` (later, with a second adapter) | StorageCopyOutput | 0, 1 |

Defaults: `task new` starts the task when an agent runs it; `ask` goes to the agent's
person and blocks; `--at` defaults to now; `--until` takes `14:20` (today, local time) or a
duration (`20m`, `2h`); `file put` takes the name from the path and needs `--base` for a
file that exists (the error names the version); `task note`, `done` and `drop` act on the
current task. Agent commands never fall back to the person's login (rule 10); `working`,
`paused` and `task …` with `--as` from a terminal act as that agent, and `working` and
`paused` with `--as` record the person as `set_by` (they use the person's key on the
member path). `file approve`, `board prefix` and `storage …` are person commands: they
refuse inside a harness session (`human_command_in_session`), and the hint is the
command to hand over. Every task and ask command names its board in its first line
(D46).

`aboard status` adds the agent's current task and line to its Agent line
(`… · on CHK-17 · Working on: …`), and `nudges off` when the board turned them off.

## Control socket and delivery

**Control socket** (spec/control.md), additive, protocol version unchanged:

- **`plan`** (new operation): the tool hook passes the item in progress from the
  harness's todo or plan list: `{"op":"plan","harness":"claude-code","session":…,
  "plan":{"text":"Rotating the key in the second config","done":2,"total":5}}`. The daemon
  sets the line (`PUT /v1/me/line`, source `plan`) for the session's seat, at most once
  per 10 seconds per seat and only when the text changed. With several seats, the line
  goes to the seat whose task was started most recently; with none on a task, to the
  only seat, else nowhere. Claude Code's tool hook reads `TodoWrite`'s input (the item
  whose status is `in_progress`, its `activeForm`) from the input it already receives,
  so no hook entry changes and nobody is asked to trust hooks again; whether
  `PostToolBatch`'s input carries each tool's input is checked when slice 3 starts (the
  older `PostToolUse` does). Codex's `update_plan` is checked the same way; until then
  Codex agents set lines by command. Harness profiles gain `plan` (the tool and field),
  checked by the conformance kit.
- **`nudge`** (new response field) on `register`, `turn_start` and `boundary`: Aboard's
  own reminder lines, separate from `bundle` and `notice`, which the hook adds before
  them. On `register` the reorientation joins the existing `note`.
- Nothing else changes: `end` already reports `no_session`, which clears a working line
  on the server.

**Delivery** (spec/delivery.md):

- `<aboard-message>` gains `about="CHK-17 CHK-12"` (task refs, space-separated, only
  when the message is about a task the reader may see), and for an ask
  `ask="blocking"` or `ask="going-with"`. An ask is followed, like an `expects-reply`
  message, by Aboard's lines: the options numbered, then
  `Answer with: aboard say --reply 93 --option 1 (or in your own words: aboard say --reply 93 "…")`,
  or for going-with `Going with "rotate at 16:00" at 16:00 unless you say.`
- An answer carries `answers="93"` and `option="1"`, followed by
  `Aboard: @leo answered your ask #93 with option 1, "Request access". CHK-17 is no longer Blocked.`
  (when it was blocking).
- **Nudges in delivery:** `pause_late` at a tool boundary and at a turn's start,
  `line_stale` and `brief_stale` at a turn's start, each one line before the bundle,
  never on their own (no wake for a nudge), and none in mode `off` beyond the turn-start
  path, which `off` also uses for the mode-change line (delivery.md, "Telling the agent
  its mode").
- **The reorientation note** on register, resume and compaction, at most 600 bytes.
- The digest (D173) lists an ask's line with `· ask` and an answer's with
  `· answers #93`.

## The primitives test

D54: the server holds only what many uses need and what can't be done correctly from
outside (atomicity, permissions, ordering, trust).

| Part | Server | Why, or why not |
| --- | --- | --- |
| Task claim (`start`) | Yes | Atomicity: exactly one owner wins |
| Sequential ids and server-unique prefixes | Yes | Ordering and uniqueness need one writer |
| Task events, About and Where it stands versions | Yes | The record (trust): who said the task stands where |
| `about` on messages, with server defaults | Yes | Must be in the posting transaction to be in the record; one default for every client (as D174) |
| Current task | Yes, a read model | Needed in that transaction for the default |
| `?task=` filter, task conversation counts | Yes | Filtering a long log with paging can't be done from outside without reading all of it (as D83) |
| Ask fields, who may answer, answer as decision | Yes | Permissions (only the one asked or the asker's person), and the record |
| Blocked, ask state | Yes, derived at read | One definition, from the record; computed, never stored |
| Lines | Yes, bookkeeping | Every viewer must see the same line with who set it; cleared by task events inside their transaction |
| The state word | Yes, computed at read | One clock and one precedence for every client; `late` is time-based |
| File versions and the base check | Yes | Atomicity: a stale write must fail |
| File bytes | Yes, behind the blob port | Bytes must match the digests the record names |
| Approvals | Yes | Trust: attributed to the person, tied to a digest |
| Freshness counts | Yes, computed at read | Counting from a version's seq needs the whole log |
| The brief | No new primitive | A file with a known name |
| The steward | No | Convention in the charter |
| Inbox ordering, Worth a look, thresholds | No | Client views over public reads (as D119's "Now:" line) |
| Nudges and their wording, rate limits | No | Client text (CLI and daemon); the server only turns them off per board and supplies counts |
| "I'm blocked" detection | No | A CLI hint; never on the write path |
| Plan hook to line | No | The daemon, on the public API |
| "Last said" | No | The board view, from messages |
| Labels, order, dependencies, scheduling | No | Left out |

## Migration from today's data

Nothing here is built, so there are no tasks, asks, lines or files to move.

- **Messages** keep exactly what they have. Events written before carry no `about`,
  `ask`, `answer` or `files`; readers treat each as absent. Old `expects_reply` messages
  stay questions: they still count in `needs_reply` (D195) and are never shown as asks.
  Nothing is inferred from old text: a `CHK-17` in an old message links nothing, since
  the board had no such task when it was written.
- **Tables:** new read models (`tasks`, `task_members`, `message_tasks`, `asks`, `files`,
  `file_versions`, `file_approvals`) start empty; `members` gains the line columns and
  `current_task_id`; `boards` gains `task_prefix` and `next_task_number`, null until the
  first task. One forward-only migration, after the automatic backup (D184, D199).
- **Prefixes** for existing boards are given when each board makes its first task, in the
  same transaction, recorded as `board.task_prefix_set`; no board is touched before.
- **Policy:** a policy without `nudges` means `on`; presets set `on`.
- **Notes and pins** (D14, D33) were planned and never built. Pins become `maintained`
  on files. Board notes are an [open question](#open-questions).
- **The data folder** gains `files/`, made at start with the same checks as the folder
  (D199). Recipes keep `/data/aboard` as the one volume.
- **The skill** gains its four rules in the slice that ships each idea, never before the
  binary knows the commands (D52).

## Build plan

Thin vertical slices, each end to end (CLI → API → store → event log → delivery or read
back) before the next. Each lists its contract parts, its acceptance tests (written
first, failing) and the lab components it moves from `web/lab/experiments` into the real
board view in `web/app`. The core will pass its 15,000-line budget around slice 2; raising
it is a decision recorded with that slice (D77).

### Slice 1: tasks, tagging and the Work panel

- **Contract:** the task paths and schemas, `task.*` and `board.task_prefix_set` events,
  `about` on messages and posting, `?task=`, `Board.task_prefix` and `tasks_open`,
  `UpdateBoardRequest.task_prefix`, `Me.current_task`, `Inbox.work` (task parts),
  `Member.current_task`; CLI `task …`, `say --task/--no-task`, `read --task`,
  `board prefix`; delivery `about=`; the `reorient` note (task part),
  `tasks_not_picked_up`, `next_task`, `first_task`, `no_task_posts`, `stands_stale`; the
  `nudges` policy key.
- **Acceptance (e2e):** two agents race `task start CHK-1`, one wins and the other gets
  `task_taken` with its hint; `say` after `start` is about the task, a reply inherits it,
  `CHK-2` in a body links it, `--no-task` links nothing; `read --task` pages; `task done`
  clears the current task and names the next; a removed agent's task is dropped with
  `by: seat_ended`; prefix uniqueness across two boards; every `--json` validated against
  cli.yaml; `audit verify` passes over task events.
- **Acceptance (live):** Claude Code and Codex each run the quickstart pair, then
  `task new`, `say`, `task done` from the skill's rules alone; the test reads `about` from
  the board, never the model's prose.
- **Lab components:** `work.tsx` (Work · by task), `tasks.tsx` (task panel, About, Where it
  stands), `chips.tsx` (task chips on messages and threads), `links.ts`, the Tasks switch
  in `centre.tsx`, the "narrowed to CHK-17 · Show everything" line.

### Slice 2: asks, the Inbox and decisions

- **Contract:** `ask` and `answer` on posting and messages, `GET /v1/asks`,
  `Board.asks_to_me`, Blocked on tasks, `Inbox.work` (asks); CLI `ask` (with
  `--withdraw`, `--open`), `say --option`; delivery `ask=`, `answers=`, the answer line,
  the digest marks; nudges `ask_instead`, `ask_options`; the reorientation's asks.
- **Acceptance (e2e):** an agent's ask with no `@name` goes to its person; an answer by
  option wakes the asker (fake harness) and unblocks the task; a third member's reply
  doesn't answer; `not_asked` and `ask_closed`; a going-with ask reads `went_with` after
  its time (injected clock); an override wakes the agent again; withdraw unblocks.
- **Acceptance (live):** Claude Code asks its person with options; the person answers
  through the board view (Playwright, isolated home); the session wakes with the answer.
- **Lab components:** `inbox.tsx` (Needs you, Worth a look), `ask.tsx` and `asks.ts` (the
  ask card, numbered answers, keys), `message-footer.tsx` (ask buttons in the
  conversation), the sidebar's marigold ask counts in `nav.tsx`.

### Slice 3: working and paused lines, and the hooks

- **Contract:** the line paths, `Member.line` and `state`, `PresenceEvent.line` and
  `state`, `Inbox.work.line`; CLI `working`, `paused`; control `plan` and `nudge`; the
  harness profile's `plan`; nudges `pause_late`, `line_stale`.
- **Acceptance (e2e):** the state word's precedence table, with an injected clock (paused
  → late); session end clears a working line, not a paused one; a person's `--as` records
  `set_by`; the fake harness's plan input sets a line at most once per 10 s; `pause_late`
  arrives at the next tool boundary once.
- **Acceptance (live):** a Claude Code session updating its todo list changes its line on
  the board, with no new hook trust prompt (checksums of the real config unchanged); the
  same check for Codex's `update_plan` decides whether its profile declares `plan`.
- **Lab components:** the state word and dots (`common.tsx`), Work · by agent, the agent
  popover with its line and "set by", `harness-mark.tsx` if the maintainer approves the
  marks (a separate question), Worth a look's late and idle items.

### Slice 4: files, versions and approvals

- **Contract:** the file paths and schemas, `file.*` events, `files` on messages,
  `say --attach`, CLI `file …` and `storage check`, `ServerInfo.storage` and `features`,
  `--files`/`ABOARD_FILES` and `--db`/`ABOARD_DB` (SQLite and disk only).
- **Build:** the `Blobs` port, the disk adapter, `blobtest`, the composition root
  choosing adapters from the URLs, the data-folder checks for `files/`, the sweep of
  unreferenced bytes.
- **Acceptance (e2e):** put, get by version, `file_changed` on a stale base, a credential
  in a text file refused and nothing stored, a 50 MB + 1 upload refused, approval then a
  new version shows "1 change since", an agent's approval refused, backup then restore
  then `storage check` passes, and a deleted blob makes it fail with exit 3.
- **Acceptance (live):** an agent writes a report with `file put`, attaches it to a
  message, and its person approves it in the board view.
- **Lab components:** `files.tsx` (the Files view), `artifacts.tsx` (cards, maintained or
  one-off, approval line), `markdown.tsx` (Markdown preview). The HTML preview stays in
  the lab until its security review.

### Slice 5: the brief

- **Contract:** `Board.brief` with freshness, `Inbox.work.brief`, CLI `brief`, the
  `brief_stale` nudge, the join output naming the brief.
- **Acceptance (e2e):** two agents edit `brief.md` from the same base, the second gets
  `file_changed` naming the first; freshness counts move with messages and done tasks;
  the keeper's nudge comes once per threshold at its next turn's start; `nudges: off`
  stops it.
- **Acceptance (live):** a steward named in the charter updates the brief after the nudge.
- **Lab components:** `brief.tsx` (the brief with its byline and freshness, the inline
  editor), the "Ask the steward to update it" message.

### Later (each needs approval)

- The **S3-compatible blob adapter** and `aboard storage copy`, passing `blobtest`
  against a local S3-compatible server in CI.
- The **Postgres store adapter**, passing `boardtest`, with a notifier on
  `LISTEN/NOTIFY` (ROADMAP's row) and an export-and-rebuild move from SQLite.
- **Recipes:** single box (SQLite and disk), small hosted service with a volume,
  Kubernetes with Postgres and S3.
- Folders for files, the safe HTML preview, "since you last looked", review records,
  claims with a lease, presence `waiting` from permission hooks.

## Open questions

1. **Board notes (D14).** Where it stands covers a task's state and files cover longer
   findings; a board-level note with "verified" evidence would be a third place to write
   things down. Recommendation: retire D14's notes before they're built, keep `write_notes`
   in the permission list unused, and make "verified" a property of a message that cites
   a file version. Agree, or keep notes as planned?
2. **Labels, order and a suggested owner on tasks (D32).** Left out here to keep tasks
   small. Agree to defer them until a board needs them?
3. **Nudges on by default for every board**, a solo pair included? The alternative is on
   for boards with tasks only. Recommendation: on everywhere, since each fires only on a
   fact and is rate-limited.
4. **Who may approve a file version:** any person on the board (proposed: an approval is
   that person's own statement), or board owners only?
5. **Codex's plan hook.** If `update_plan` doesn't reach a hook Codex runs, Codex agents
   set lines only by command. Accept that, or ask Codex upstream for a hook?
6. **The brief's name.** Fixed as `brief.md` (proposed), or a board setting naming the
   maintained file that serves as the brief (which would allow `brief.html`)?
