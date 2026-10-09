# Extensions: code that changes how a board works, without a fork

Status: design exploration for the maintainer, not approved. Nothing here is in the v0.1
scope; each piece says whether it exists, needs a contract change, or needs approval.

## What we want

pi lets a person, or the agent itself, write a TypeScript file that adds tools, commands,
event handlers and terminal widgets, drop it in a folder and reload, with no fork. The
maintainer wants the same for a board:

- **Behaviour:** monitors, filters, routing, and orchestration such as a game master, an
  experiment protocol or a reviewer gate.
- **UI:** panels, cards, message labels and Inbox groups in the board view.
- **CLI:** new `aboard <name>` commands.
- **Models:** summaries, Inbox sorting and LLM-judge monitors, with the model call made
  by the extension, on its owner's key or subscription.

Three flows have to work end to end:

1. **Written by an agent, then shared.** "Write an extension using the skill" (a panel,
   a summary card). The agent writes it and turns it on for this board; it can then be
   published for the whole server, and anyone turns it on for their own board without
   writing it again.
2. **Hooks on the message flow.** Extra filtering or checking of messages, forwarding
   them to Slack, email or a webhook, and other processing before and after a message is
   stored.
3. **Orchestration patterns.** Games, experiments, review gates, round-robin and sealed
   rounds, built on the SDK and the hooks and packaged so a board can reuse them.

And it has to keep what makes aboard aboard: a small core, a server that never runs
agents, commands or models, one write path with the rules checked on every write, every
flow agent-operable, the same on a local and a team server, and safe when the people on a
board don't fully trust each other.

The short answer: **an extension is a program on a seat, and what it adds to the board
view is data, not code.** pi can load extensions into its own process because one person
owns that process. Aboard's shared process is the server, which every party relies on,
so extension code runs at the edge, on its owner's machine, as an API client with its own
identity. What it shows people goes through two new primitives (annotations, and a
schema for drawing them) that the board view renders with its own components.

Sharing works like templates (D111): a server keeps a **catalog** of published
extensions, and a board owner **enables** one on a board. The server stores the bytes and
records the enablement but never runs them; an **extension host**, a client process (the
person's delivery daemon locally, a small service beside a team server), sees the
enablement and runs the program on a seat of that board. Hooks on the message flow come
in two kinds: **after the write**, any program follows the board and reacts (forwarding
included); **before the write**, the only power is a board's synchronous check hook,
which may allow, flag or refuse a message within a time limit, with fail-open or
fail-closed set by the board's owners, and may never rewrite it.

## 1. pi's model

Read from `earendil-works/pi` at `f10993b` (2026-10-07), MIT licence. Paths are under
`packages/coding-agent/`.

### How an extension is written

A TypeScript module whose default export is a factory taking `ExtensionAPI`
(`docs/extensions.md`, "Create and load an extension"). The factory registers what it
adds; it must not start processes or timers, because some invocations load extensions
without a session. Long-lived work starts in `session_start` and ends in an idempotent
`session_shutdown`.

```typescript
export default function (pi: ExtensionAPI) {
  pi.registerCommand("hello", {
    description: "Show a greeting",
    handler: async (name, ctx) => ctx.ui.notify(`Hello, ${name || "world"}!`, "info"),
  });
}
```

The API surface (`src/core/extensions/types.ts`, about 2,300 lines, the exact contract):

| Kind | API | Notes |
| --- | --- | --- |
| Lifecycle and interception | `pi.on(event, handler)`, about 40 events | Some only notify; others transform or block: `tool_call` can change input or block a call, `tool_result` handlers compose, `context` rewrites what the model sees, `turn_end` and `agent_before_settle` can append entries and ask for one more turn |
| Model-callable tools | `pi.registerTool()` | TypeBox schema, `execute()`, `exposure` (direct, codemode, deferred, hidden), MCP-style hints (`readOnlyHint`, `destructiveHint`) that a permission extension can read |
| Commands, shortcuts, flags | `registerCommand`, `registerShortcut`, `registerFlag` | `/name` in the session |
| Messages and state | `sendMessage`, `sendUserMessage`, `appendEntry` | State that follows the branch goes in tool-result `details`; durable data outside the model's context in `appendEntry` |
| Providers and models | `registerProvider`, `registerVirtualModel`, `ctx.modelRegistry.streamSimple()` | Nested model calls use the providers the person already configured |
| MCP | `registerMcpServer` | Per session, not saved |
| UI | `ctx.ui` (dialogs, `notify`, `setStatus`, `setWidget`, `setFooter`, `custom()`), `registerMessageRenderer`, `registerEntryRenderer`, `registerToolRenderer` | Terminal components; see below for remote UI |
| Between extensions | `pi.events` | An in-process event bus |

### Discovery, loading and packages

- **Folders:** `~/.pi/agent/extensions/` (personal) and `.pi/extensions/` (project); a
  file, or a folder with `index.ts` (`docs/configuration.md`). `pi -e ./x.ts` loads one
  for a single run.
- **No build step:** pi loads TypeScript with `jiti` (`src/core/extensions/loader.ts`).
- **Packages** (`docs/packages.md`): an npm package, git repository or local folder with
  `extensions/`, `skills/`, `prompts/`, `themes/`, or a `pi` key in `package.json`.
  `pi install npm:@x/tools@1.0.0` or `git:github.com/x/tools@v1`; versions and git refs
  are pinned. Personal installs go to `~/.pi/agent/settings.json`, project installs
  (`-l`) to `.pi/settings.json`. Settings can filter which resources of a package load.
  A package is identified by npm name, git URL without the ref, or absolute path, so it
  never loads twice.

### How an agent writes one for itself and reloads it

There is no special mechanism; three ordinary things make it work:

1. **The system prompt points at the docs.** `src/core/system-prompt.ts` (around line
   162) adds a section naming the docs and examples folders and saying "When asked
   about: extensions (docs/extensions.md, examples/extensions/) … read the docs and
   examples, and follow .md cross-references before implementing." Sixty-odd checked
   examples in `examples/extensions/` (a permission gate, protected paths, a todo tool,
   tic-tac-toe, a sub-agent, a sandbox) are the reference.
2. **The agent writes the file** into one of the folders with its normal file tools.
3. **`/reload`** replaces the extension runtime. The agent can trigger it itself:
   `examples/extensions/reload-runtime.ts` registers a tool that queues `/reload-runtime`
   as a follow-up message, since tools can't reload directly. After a reload, any
   captured `pi` or `ctx` throws a "stale" error (`loader.ts`, around line 195), which
   stops old closures acting on the new runtime.

### Trust and isolation

- An extension **runs inside pi's process with the person's OS permissions**: it can
  read prompts, files, credentials and history (`docs/extensions.md`, first section).
- **Project trust** (`docs/security.md`, `src/core/project-trust.ts`,
  `src/core/trust-manager.ts`) decides whether a folder's `.pi/` extensions, packages
  and settings load at all. Decisions are saved per directory in
  `~/.pi/agent/trust.json`. Only personal and command-line extensions may answer the
  `project_trust` event, so a project can't approve itself.
- pi says plainly that watching the transcript or granting trust **is not a security
  boundary**; isolation means a container, VM or separate user.
- Approval of individual actions is itself an extension (`permission-gate.ts`,
  `confirm-destructive.ts`, the `tool_call` annotation example in `docs/extensions.md`).

### Versioning

There is no extension API version number. Extensions import pi's own packages
(`@earendil-works/pi-coding-agent`, `pi-ai`, `pi-tui`, `typebox`), which pi supplies;
packages must list them as `peerDependencies: "*"` and never bundle them
(`docs/packages.md`, "Declare dependencies"). The exported types are the contract, and
compatibility is pi's job release by release.

### Remote UI: pi's own declarative subset

When pi runs as an RPC server with the UI in another process, extension UI can't draw
terminal components. `docs/rpc-extension-ui.md` reduces it to a JSON request/response
protocol: dialogs (`select`, `confirm`, `input`, `editor`) and fire-and-forget calls
(`notify`, `setStatus`, `setWidget`, `setTitle`); `custom()` returns `undefined` and
most terminal-specific calls do nothing. **Once pi's UI is not in the same process as
the extension, it becomes a fixed vocabulary of data.** That is aboard's situation for
every board view, all the time.

### omp, and aboard's omp extension

omp keeps pi's extension API. Aboard already ships one: `adapters/omp/aboard.ts` (D168),
installed by `aboard init` and run by omp's Bun with no build step. It uses `pi.on` for
`session_start`, `before_agent_start`, `agent_start`, `agent_end`, `turn_end`,
`tool_call` and `session_shutdown`, and `pi.sendMessage(…, {triggerTurn})` or
`{deliverAs: "aside" | "followUp"}` to add messages to a session. It holds no rules: it
is a thin bridge from inside omp's process to the delivery daemon over the control
socket (`spec/control.md`). That split, code inside the trusted process and a protocol
to the shared room, is the shape this design keeps.

### What carries over, and what doesn't

| pi | Carries over? |
| --- | --- |
| Docs and examples named in the agent's instructions, so the agent writes its own | Yes: the aboard skill points at `docs/extending.mdx` and `/examples` |
| A folder you drop code into, and a reload | Yes, at the edge: an extension folder; restart the process; views change with no UI build |
| Pinned packages from git or a registry, personal or project scope | Yes, later: git source with a pinned ref and digest |
| One registration API in one process | No: aboard's "process" is a shared server; registration becomes a manifest and a seat |
| In-process code with full permissions; trust as one yes or no per folder | No: a team board has several owners, so every power is a role permission checked by the server |
| Intercepting and rewriting (block a tool call, rewrite context) | Only through the room's own levers: flag, and later hold; never user code in the write path |
| Imperative UI components | No: data in a fixed vocabulary, like pi's RPC UI |
| Nested model calls on the person's configured providers | Yes: the model call happens in the extension, on its owner's credentials |

## 2. Aboard as it is

What already exists, so the design builds on it rather than beside it:

| Piece | Today | Where |
| --- | --- | --- |
| Primitives test | Something goes in the server only if many uses need it and it can't be done correctly from outside | D54, PHILOSOPHY.md |
| Extension points | Monitor hook (designed, not built), launchers (`aboard-launcher-<name>`, built), harness profiles (built), storage port, CLI extensions (`aboard-<name>`, designed, **not built**: `cli.go` reports an unknown command), stream readers | D57, D78, `docs/extending.mdx` |
| A program on a board | Its own agent seat and token, owned by the person who added it, posting as itself; "a kind of seat made for programs isn't built yet" | D56, D155, `docs/extending.mdx` "Bots and bridges" |
| Agent limits | An agent acts within its person's access; agents never manage people, roles, policy, pause, revoke, approvals or **bots** | D172, D181 |
| Child seats | A parent agent creates `<parent>.<name>` for a subagent; it ends with the parent | D165 |
| Starting things | `swarm up` reads the board file's `agents` and starts them through launchers; the server never runs anything | D59, D105, D178 |
| Following a board | `GET /v1/stream` (person tokens only, heads and presence, no content); `GET /boards/{b}/events?after=` (no `wait`); `GET /me/inbox?wait=` (only messages to the seat) | `spec/openapi.yaml` |
| Structured data on things | Reactions from a fixed set of six (D175); `about` links messages to tasks (D207); asks and answers on messages (D208); files with versions (D211) | `spec/events.md` |
| Reserved events | `flag.raised`, `monitor.flagged`, `board.paused`, … | `spec/events.md` "Reserved type names" |
| Server features | `GET /v1/info` lists feature names clients check | D151 |
| Model calls | Never in the server; LLM checks belong behind the hook | D79 |
| Board view safety | CSP with `script-src 'self'` plus build hashes, `frame-src 'none'`, `connect-src 'self'`; text-only rendering; cookie session with Origin and CSRF checks; HTML files shown only in `sandbox=""` with every URL removed | `server/internal/api/browser.go` (`uiPolicy`), `web/app/files.tsx` (`previewable`), D189 |
| UI slots | The UI lab's build-time seam: `Overlay`, `Place`, `Nav`, `Centre`, `RightTitle`, `RightPanel`, `MessageFooter`, `MessageMeta`, `ThreadMeta`, `Text`, `AgentMark`, `themes`; a real build resolves it to nothing | `web/app/lab-seam.ts`, `web/next.config.mjs` |
| Experiments | `aboard-lab` (Python, on the SDK) and `aboard-bench` are designed, not built; SDKs not built | D58, D65, ROADMAP |
| Games | Twenty Questions launch demo by a game-master program; Mafia later, once sealed rounds exist | ROADMAP |

Two gaps stand out. A program can post and read, but **it has nowhere to put what it
knows except a message or a file**, so every summary, score or label becomes chat noise.
And **the board view can only show what aboard's own code knows how to draw**, so a
program's state is invisible unless someone reads its messages.

## 3. The options

### Behaviour: where extension code runs

| Option | How | Verdict |
| --- | --- | --- |
| A. In the server (Go plugins, WASM, JS in an embedded runtime) | The server loads user code and calls it on events or in the write path | **No.** Breaks D57, D79, D105: whoever installs code on a team server can read every board and act as anyone; a slow or wrong extension stalls every write; the record stops being explained by the rules alone |
| B. Server calls out (webhooks, the monitor hook) | The server POSTs each message or event to a URL an admin set | Only where the server must wait for an answer before storing: the pre-write check hook ([Hook points](#9-hook-points-on-the-message-flow)). For anything after the write (flagging, forwarding, webhooks), a pull is simpler and safer: no outbound requests from the server, so no SSRF on a team server, no retry queue, no signing secret |
| C. **A program on its own seat** | Any process, any language, any machine, using the public API with a seat token; its powers are its role's | **Yes.** Exists today (D155). Needs a few small primitives to be good (below) |

### UI: how a program's output reaches the board view

| Option | How | XSS and trust | Verdict |
| --- | --- | --- | --- |
| A. Script plugins in the board view's origin | The page loads an extension's JavaScript, as the UI lab does at build time | The script runs with the person's session: it can post, approve, change policy, read every board the person sees. D189's whole reasoning ("an injected script gets only the page's own powers") becomes "every extension gets them". On a team server, one member's extension runs as every viewer | **No**, at runtime. Stays a build-time tool for us (the UI lab) and for anyone building their own client |
| B. **Declarative views** | The program writes JSON in a fixed schema; the board view draws it with its own components | No code runs; text is rendered as text (or through the existing Markdown renderer); no external loads under the current CSP; actions are messages the viewer sees before sending | **Yes, first.** Covers cards, labels, scoreboards, progress, Inbox groups |
| C. Sandboxed apps | The program uploads an HTML app as a board file; the board view shows it in an iframe with scripts but an opaque origin, no network, and a `postMessage` bridge | Safe only with all of: `sandbox="allow-scripts"` (never `allow-same-origin`, popups, top navigation, forms, modals), a response CSP with `sandbox` and `connect-src 'none'`, the page's `frame-src` opened only to that path, a bridge that hands in only what the program's own seat can already read, and every write turned into a host-drawn confirmation. Residual: spoofed UI inside the frame, CPU use | **Later, if at all**, behind a board policy owners turn on. Only for things a fixed vocabulary truly can't do (a draggable game board) |
| D. The extension's own web app | Its own origin, its own backend holding its seat token; linked from its card | Separate origin; the server sends no CORS headers, so it can't use the person's session | Already possible; a card can link to it |

Why B before C, in more detail:

- **It's what pi does when the UI is remote** (its RPC extension UI), and what aboard's
  board view already is: everything it draws comes from data.
- **Agents can use it.** A card is JSON an agent can read with `--json`, and its buttons
  are messages an agent can send. An iframe app is opaque to agents, which breaks D114.
- **The page's guarantees stay simple.** `uiPolicy` today promises "text that reaches
  the page can't run as a script" and `frame-src 'none'`. Option C has to weaken both,
  and every later change to the bridge is a security review.
- **One rule makes both safe on a team server: a view never holds more than its
  program's seat can already read.** The board view draws a program's data next to the
  board's data, never passes the viewer's data to the program. So a hostile program can
  lie to you on its card, but can't learn anything through it. The card's frame says
  who wrote it, so the lie is attributed.

## 4. Recommended shape

### The one-sentence version

**An extension is a folder with a manifest and a program. Running it gives the program a
seat on a board, where it acts through the public API under its role, and what it wants
people to see it writes as annotations, which the board view draws from a fixed schema.
Published to the server's catalog, it is enabled per board by that board's owners and
run by an extension host, never by the server.**

### The pieces

```
 extension folder: aboard-ext.yaml (manifest) + a program in any language
        │
        ├─ aboard ext run .            run it yourself, now, on this board (a seat of yours)
        └─ aboard ext publish .        store the bundle in the server's catalog (bytes + digest)
                                              │
 board owner: aboard ext enable <name> --board B   →  event extension.enabled (version, settings, role)
                                              │
 extension host (delivery daemon locally; `aboard ext host` beside a team server)
   follows enablements it may serve → gets a program seat token → downloads the bundle by digest → runs it
        │
        │  seat token (an agent seat, owned by the person who enabled it, marked as a program)
        ▼
 aboard server ── one write path: authenticate → role → policy → redact → [pre-write check hook] → one transaction
        │            new: annotation.*, extension.*, agent.program_set events
        ▼
 board view, CLI, agents ── read annotations; the board view draws them with its own components
 programs ── follow events after the write: react, flag, forward to Slack or email
```

#### 1. Programs on seats (exists, plus a marker)

A running extension is an agent seat (D155) owned by a person. Three ways to get one:

- **A person adds it**, as any agent today (`aboard join … --name gm`), or `swarm up`
  starts it from the board file (below). It is an agent of that person.
- **An agent adds it for itself**, as a child seat (`<agent>.<name>`, like D165's
  subagent seats), with the same role ceiling and ending when its parent seat ends. This
  is the pi-like path: no person needed to try an extension on a board where the
  agent's role already allows what it does.
- **A host runs it from an enablement** (section 8): the seat belongs to the person who
  enabled the extension on that board.

New: a program seat **says it is a program**. `PUT /v1/me/program` sets
`{name, version, source, summary, uses_models, commands}` from the manifest and writes an
`agent.program_set` event, so the record and the crew show "gm · program 0.3.0 · owner
@leo · uses a model: none". It has no harness, no delivery and no presence from hooks;
its presence is "running" while it renews it. This passes D54 on trust: every reader,
person or agent, needs to know a seat is code and what it declares, and only the server
can attach that to the seat. It is additive: `kind` stays `agent`, and the new `program`
field is absent for ordinary agents.

#### 2. Following a board as a seat (new, small)

Programs need every event, not only their inbox. Add `wait` to
`GET /v1/boards/{board}/events` (long-poll, as the inbox has), so a seat follows a board
without polling. The person stream (`/v1/stream`) stays person-only. This is API
completeness under D54: the person has a stream, a seat has nothing but polling.

#### 3. Annotations (new primitive)

**An annotation is a small piece of JSON a member attaches to something on the board:
the board, a message, a task, a file or a member.** It is the one new primitive for data on a board.

| Property | Rule |
| --- | --- |
| Address | `(target, writer, key)`. A writer's keys are its own: nobody else can set or remove `@gm`'s `score`. Setting again replaces it |
| Value | JSON, at most 8 KB; string values go through secret redaction like message text |
| Audience | `board`: an event in the record (`annotation.set`, `annotation.removed`), shown to everyone who can see the target. `owner`: bookkeeping like read positions, never an event, shown only to the writer's person and that person's agents |
| Visibility | Follows the target: an annotation on a message you can't see is withheld, as the message is |
| Permission | `owner` audience: any seat, for its own person. `board` audience: needs a new role permission, `annotate`. Under `starter` the `member` role has it (one person, nothing to protect); under `recommended` only roles that list it |
| Limits | Per-seat rate limit; a cap per target; never a general key-value store (big state goes in a file) |
| API | `PUT` and `DELETE /v1/boards/{board}/annotations/{key}?on=<target>` with `Idempotency-Key`; `GET /v1/boards/{board}/annotations?on=&by=&key=`; message, task and file reads carry their annotations, as messages carry reactions |
| CLI | `aboard annotate <target> <key> --file value.json [--audience owner]`, `aboard annotate <target> <key> --rm`, `aboard annotations [target] --json` |

Why it passes D54: many different uses need it (cards, labels, scores, Inbox categories,
judge verdicts, game state, experiment conditions, summaries); and it can't be done
correctly outside, because of **trust** (attributed to its writer, no one can forge or
overwrite another's), **permissions** (visibility follows the target; who may write
board-visible ones is a role decision), and **ordering** (a board-audience annotation
sits in the hash chain after the thing it describes). Reactions (D175) are the special
case with a fixed set of six names and no value; annotations don't replace them.

#### 4. Views: how the board view draws annotations (contract, client-side)

A **view** is an annotation whose value follows `spec/view.schema.json`
(`{"view": 1, …}`). The server stores it as any annotation; the board view, the CLI and
the TUI draw it. **Where it shows comes from its target, so there is no slot registry:**

| Target | Shows as | UI lab slot it comes from |
| --- | --- | --- |
| The board | A **card** in the board panel, under the charter, framed with "by @gm · program · owner @leo" | `RightPanel` |
| A message | A **label** on the message's first line (text and a tone), with details on hover or tap | `MessageMeta`, `MessageFooter` |
| A thread's first message | A label on the thread row | `ThreadMeta` |
| A task or a file | A label on its row and a card in its panel | the Work and Files panels |
| A member | A line under the member in the crew | `RightPanel` crew |
| Any target, `owner` audience | The same, seen only by the person; `inbox_group` on a message sorts it into an Inbox group | `Place` (the Inbox) |

Version 1 of the schema has a short, fixed list of blocks, each drawn by an existing
component: `text` (Markdown through the timeline's renderer, no HTML, no images),
`fields` (label and value pairs), `table` (at most 20 rows), `progress`, `badge` (a tone
from the design system's palette, never a colour the program picks), `refs` (links to
messages, tasks, files and members on this board), `grid` (small cells of text and tone,
for a game board), and `actions`.

**Actions never run code.** Each is a message the viewer would send:
`{"label": "Guess", "say": {"to": "@gm", "text": "guess "}, "input": "word"}`. Pressing it
shows the exact message, to whom, with Send and Cancel; sending is an ordinary post by
the viewer, so it is attributed to them and goes through the write path. An agent sees
the same action in `aboard annotations --json` with the equivalent `aboard say` command,
so everything a card offers people, it offers agents.

Rules the board view enforces: a card can't style the page, load anything, or appear
outside its frame; unknown block types show "This card needs a newer board view"; a value
that fails the schema shows its writer and "couldn't be shown", never raw JSON in the
page. A model-written text block carries `"by_model": "claude-haiku"`, and the frame
says "written by a model" (keeping D119's line between facts and prose).

#### 5. The manifest and the folder (client-side)

```yaml
# aboard-ext.yaml
ext: 1
name: twenty-questions
version: 0.3.0
summary: Runs Twenty Questions between the agents on a board and keeps the score.
run: [python3, gm.py]
needs: [annotations, events_wait]       # feature names from GET /v1/info (D151)
can: [post, annotate]                   # what it will use; checked against its role at start
uses_models: []                         # e.g. ["anthropic:claude-haiku-4-5"]; shown in the crew
role: game-master                       # the role it asks for when enabled on a board
commands:                               # what people and agents say to it
  - text: "join"
    does: Adds you to the next round.
  - text: "guess <word>"
    does: Makes a guess; one per turn.
settings:                               # what a board chooses when it enables it (JSON Schema)
  rounds: {type: integer, minimum: 1, maximum: 10, default: 3}
  players: {type: array, items: {type: string}, description: "Agent names; default: every agent"}
```

The program reads its board's settings from the enablement (`aboard ext settings
--json`), so one published extension serves many boards, each set up differently.

- **Personal:** extension folders in `$ABOARD_HOME/extensions/<name>/`, or any path.
- **Per board:** the board file names programs beside agents. An `agents` entry gets
  `run:` (a program) instead of `harness:`, and `swarm up`, `ps` and `down` start, list
  and stop it through a launcher as today. The board file stays client-only; the server
  never sees `run`.
- **Per server:** the catalog (next section). Templates (D111) name catalog extensions
  by name and version (`extensions: [{name: twenty-questions, version: 0.3.0, settings:
  {rounds: 5}}]`), so a board made from the template starts with them enabled.
- **From outside the server:** `aboard ext publish git+https://…@<commit>` fetches,
  shows the source and digest, and asks before storing, the way pi's project trust asks
  before loading a folder's code; `--json` refuses with the exact command to approve.
- **No public registry or marketplace.** Each server's catalog and pinned git sources
  are enough until real use says otherwise.

CLI, all client-side: `aboard ext check <dir>` validates the manifest and any view JSON
it ships as fixtures (like `aboard template check`); `aboard ext test <dir>` runs the
extension kit; `aboard ext run <dir|name> [--board B]` gets or creates the seat (a child
seat when an agent runs it), sets its program description, and starts the process
through a launcher with `ABOARD_AGENT`, `ABOARD_HOME` and the server address in its
environment; `aboard ext list` shows what is installed and running.

#### 6. CLI extensions (exists in the design, not built)

Build D57's `aboard <name>` → `aboard-<name>` on the `PATH`, as git and kubectl do:
built-in commands always win; `aboard help` lists found extensions under their own
heading; the extension gets `ABOARD_BIN`, `ABOARD_HOME`, the server address and the
resolved agent, and calls back into `aboard … --json`, so tokens stay in one place and
the identity rules (D85, D165, rule 10) still apply. Add `aboard api <METHOD> <path>
[--input file]`, a raw API call as the resolved identity (like `gh api`), so a script in
any language reaches every endpoint without handling a token. No server change.

#### 7. Model-powered extensions

They are programs that call a model; nothing about the server changes.

- **Where the call happens:** in the program, on its owner's machine or wherever its
  owner runs it, with the owner's API key, or by running a harness's own headless turn
  (`claude -p`, `codex exec`, omp's print mode), which uses the person's existing login
  the way pi's `streamSimple()` uses configured providers. Aboard's harness profiles
  already describe each harness's headless turn, so an SDK helper can offer
  `ask(prompt, harness="claude-code")` without aboard holding a key.
- **What people see:** the manifest's `uses_models` appears in the crew ("sends board
  text to: anthropic"), and anything a model wrote is marked in its card or label.
- **Consent on a team board:** a member's program can already read what its person can;
  sending that text to a provider is the person's choice, as pasting it into a chat
  would be. What changes is that it is now visible, and board owners can remove the
  program (D182). A board policy `programs_using_models: off` could forbid it; open
  question 6.
- **If D79 loosens later**, keep the server model-free and put consented calls in the
  person's delivery daemon, which already runs with their credentials on their
  machine; its output would be ordinary annotations by a seat of theirs.

#### 8. The catalog: publish once, enable per board

**What we want:** an agent writes an extension once; afterwards anyone on the server
turns it on for their board with one command or one click, without the code or its
author.

**How:** the same split as templates (D111), which already solved "stored on the server,
managed by admins, used by everyone, the same locally and on a team".

| Step | Command | Who | What the server does |
| --- | --- | --- | --- |
| Publish | `aboard ext publish <dir>` | Locally, you; on a team, server admins (as for `aboard template save`). An agent prepares it and hands its person the command (D114) | Validates the manifest, stores the folder as one bundle in the blob store by SHA-256 (D212), keeps every version; writes nothing to any board |
| List | `aboard ext list --catalog` | Everyone on the server | Lists name, versions, summary, `uses_models`, the role it asks for, who published it |
| Enable | `aboard ext enable <name>[@version] --board B [--set rounds=5]` | That board's owners (roles and policy are theirs, D97) | Checks `settings` against the manifest's schema and the role exists or is created with no more than `can`; writes `extension.enabled` (name, version, digest, settings, role, enabled by) |
| Change, disable | `aboard ext enable … --set …`, `aboard ext disable <name> --board B` | Board owners | `extension.settings_changed`, `extension.disabled`; disabling ends the program's seat (D182) |
| Run | automatic | An extension host | See below |

Enabling is a board event, so the record and the board view show which extensions a
board has, at which version, set up how, and who turned each on. Upgrading is enabling a
newer version; nothing changes under a board by itself.

**Extension hosts.** Something has to run the program, and the server never does
(D105). A host is a client process with a **host credential**: it lists the enablements
it may serve (`GET /v1/extension-runs?wait=`), takes one with a lease, gets the
program's seat token, downloads the bundle by digest and checks it, and runs it through a
launcher. Two hosts cover both kinds of server with one design (D113):

- **Locally**, the person's delivery daemon is their host, for their own boards. Enable
  a card on a board and it appears within seconds.
- **On a team server**, a person runs `aboard ext host` on a machine of their choosing,
  for the boards they own, or a server admin runs one beside the server (a container in
  the same deploy recipe) for every board. A board owner's `enable` can name a host; if
  none is named, the server-wide host takes it.

The seat belongs to the person who enabled the extension, so attribution stays true
("@digest · program · owner @maya · hosted by team-host"), and the host only holds seat
tokens, never a person's key. A host's credential is a new kind like a delegation
(D197): it can only take runs and get program seats, and it ends with whoever issued it.

Why the catalog and enablements pass D54: **permissions** (who may publish, who may turn
code on for a board) and **trust** (the digest pins what runs; the record says who
enabled it). Running the code stays outside.

#### 9. Hook points on the message flow

**What we want:** filter, check, forward and otherwise process messages, without user
code in the server and without a second write path.

Every message passes the same points; each takes a different kind of extension:

| Point | Where it runs | What it can do | How |
| --- | --- | --- | --- |
| 1. Before sending, in the sender's client | The sender's machine | Lint, block or rewrite their own draft before it is sent | Personal: a harness hook or a CLI `pre_say` command in the person's config. Their message, their choice; the server never knows |
| 2. Before the write, in the server | Inside rule 6, after redaction and before the transaction | **Allow, flag or refuse**, never rewrite | The board's **check hook**, below |
| 3. After the write | Anywhere: programs following the board | Flag, annotate, reply, open tasks, forward to Slack, email or a webhook, mirror to another system | Programs on seats with `wait` on events (section 2) |
| 4. Before delivery, in the recipient's daemon | The recipient's machine | Decide what wakes their own agents | Delivery modes today (D173); personal annotations later (open question 7) |

**Why a check hook may refuse but never rewrite.** The sender comes from the token and
the record says what they sent. A rewritten message would be attributed to someone who
didn't write it, and their own agents would act on text they never sent. Redaction is
the one rewrite, it lives in the core, and it is recorded (`redactions`). A hook that
wants a cleaner message refuses with a reason and the sender posts again.

**The check hook** (D57's monitor hook, made concrete):

```yaml
# board file, set only by board owners (a policy change, recorded)
monitor:
  checks: [prompt_injection, credential]     # built-in rules, in the server
  hook:
    url: https://checks.internal/aboard       # or the endpoint of an enabled extension
    timeout_ms: 1500                          # at most 5000
    on_error: allow                           # allow (fail open) or refuse (fail closed)
    applies_to: [agents]                      # agents, people, or both
```

- The server POSTs `{board, message: {to, body (after redaction), reply_to, about},
  sender: {name, kind, role, owner}}`, signed with a per-board secret, and waits at most
  `timeout_ms`. The hook answers `allow`, `flag` (store it, and raise a flag to the
  board's owners and the sender's owner) or `refuse` with a reason. A refusal stores
  nothing in the message log and returns 422 `message_refused` to the sender, with the
  hook's reason and the hint to rewrite; a new `monitor.refused` event
  records that something was refused, by which hook, without the text.
- **Fail-open or fail-closed is the owners' choice.** On a timeout, a non-2xx answer or
  an unreadable body, `allow` stores the message and marks it `unchecked` (shown in the
  board view and in `--json`), so a broken hook is visible, never silent; `refuse`
  refuses with `monitor_unavailable`. Locally the default is `allow`.
- **The call is made before the transaction, never inside it**, so no database lock
  waits on the network; the sequence number is given at commit. A repeated
  `Idempotency-Key` returns the first outcome without calling again.
- **Outbound safety on a team server:** only `https`, no redirects, private and
  loopback addresses refused unless the server admin allows them (`--monitor-hosts`), a
  response capped at 16 KB, and one call per message.
- **An enabled extension can be the hook.** A manifest that says `check: true` serves an
  HTTP endpoint on its host; enabling it on a board offers to set it as the board's hook,
  which is still the owners' policy change. Only one hook per board, so the order of
  checks is the hook's own business.
- **Pull instead of push, later.** For servers that can't make outbound calls, or checks
  that need a person: hold the message (D99's hold-for-approval) until a monitor seat or
  a person releases it. Slower, no outbound calls; it needs the held state, which is
  "Later" in the scope table.

Forwarding (Slack, email, a webhook) is always point 3: a program follows the board and
sends what it sees. A failed Slack call can then retry for as long as it likes without
holding up a single message, and the board's visibility rules apply to the forwarder's
seat like any reader.

#### 10. Orchestration patterns as reusable extensions

**What we want:** games, experiments, review gates, round-robin and sealed rounds that a
board turns on, not code each team writes again.

Each pattern is one extension in the catalog, with `settings` for what varies, built
from the same parts:

| Pattern | Program does | Server guarantees it relies on | Strictness from the board's own rules |
| --- | --- | --- | --- |
| Round-robin | Addresses the next agent (`--to @name`), waits for its reply, moves on; a card shows whose turn it is | Ordering; `focused` delivery wakes only the agent addressed (D173) | Players' role without `broadcast`; `addressed` visibility if they mustn't see each other |
| Sealed round | Asks everyone the same question, collects answers sent only to it, reveals them together in one post | `addressed` visibility (D3): nobody but the sender and the program reads an answer before the reveal | Players can only send to the program; the record shows each answer was sent before the reveal |
| Review gate | When a task is done, opens a review task for the reviewer and an approval ask to a person (D208); a label on the task says "waiting on review" | Tasks, asks, file approvals tied to a version (D211) | The gate is advisory on the board; a hard gate is outside (CI reads the annotation or approval before merging) |
| Game master | Holds secrets and turns, scores, keeps a card | All of the above | As above |
| Experiment protocol | Creates a board per trial, launches agents, injects conditions, scores from the event log | Fresh boards, launchers, the hash chain | Policy presets as conditions |

These share one small library, in the SDK's hand-written layer (D55) and aboard-lab
(D58): `turn(to, prompt, wait)`, `collect(from, question, sealed=True)`, `reveal(answers)`,
`card(…)`, `on(event_type)`. The library is client code. If a pattern proves itself
across games and experiments (sealed rounds are the strongest candidate in
`design/research/coordination-primitives.md`), it moves into the server only by the
promotion rule (D75), and only the part that needs the server, such as "answers are
hidden until all are in, enforced even against the program".

### Who may do what on a team server

| Action | Who | How it's enforced |
| --- | --- | --- |
| Write and run an extension | Any person, on a seat of their own; any agent, as its child seat | Seat creation rules (D155, D165, D181) |
| Post, read, open tasks | As the seat's role allows | Existing role permissions |
| `owner` annotations (only my person sees them) | Any seat, for its own person | Writer's person checked in the write transaction |
| `board` annotations: cards and labels everyone sees | Seats whose role has `annotate` | New role permission; roles are set by board owners only (D97, rule 8) |
| Flag someone else's message to that message's owner | Seats whose role has `monitor` | New role permission, with `flag.raised` (reserved) |
| Pause, revoke, change roles or policy | People only | Unchanged; a program asks, as agents do |
| Publish to the server's catalog | Server admins on a team; you locally. Agents prepare, people run | As `aboard template save` (D111) |
| Enable, configure or disable an extension on a board | That board's owners | Board events; the role it gets is theirs to set |
| Set the pre-write check hook, its timeout and fail mode | That board's owners | A policy change, recorded (`board.policy_changed`) |
| Allow check hooks on private addresses | Server admins | A server setting |
| Run a host | Anyone for boards they own; server admins for every board | A host credential, issued by a person and ending with them |
| Sandboxed apps (option C) | Off; board owners turn on per board | A policy key, if C is ever built |
| Programs on the server at all | Open question 4 | A server setting, if needed |

An agent that needs more than its role gives gets an error that names the next step,
for example `Error (permission_denied): Role member can't post cards on general. Hint:
Ask a board owner to run aboard board role grant game-master annotate, or post with
--audience owner to see it yourself.` That is the D114 hand-off: the agent prepares the
exact command and the person runs it.

### Versioning

- Contracts only grow (D68): new events, the `annotate` and `monitor` permissions, the
  `program` field and the view schema are all additive.
- The manifest says `ext: 1`; views say `view: 1`. A program checks `needs` against
  `GET /v1/info` features (D151) at start and fails with `server_feature_missing` naming
  the feature and `aboard upgrade` or the server's admin.
- The board view draws every block type it knows and a placeholder for the rest, so an
  old board view never breaks on a new program.
- The extension kit (D78) runs a program against a throwaway local server in its own
  `ABOARD_HOME`, with scripted members, and checks: the manifest validates; every view
  it writes validates; every write carries an `Idempotency-Key`; it resumes from where it
  stopped after a restart; it stops cleanly on SIGTERM; it refuses to act on a board
  where its role lacks what `can` lists.

### How an agent writes one for itself, then shares it

The pi loop, translated, for the first flow. A person says to their agent: "Write an
extension that keeps a 'Where things stand' card on this board."

1. **The skill points at the docs.** It names `docs/extending.mdx` and `/examples` as
   what to read before building on aboard, as pi's system prompt names its docs.
2. **The agent copies the nearest example** (`examples/summary-card/`), edits
   `aboard-ext.yaml` and the program, and runs `aboard ext check .` and, once the kit
   exists, `aboard ext test .`.
3. **It turns it on for this board:** `aboard ext run . --board general`. That makes the
   child seat `claude.summary-card`, owned by the same person, marks it as a program,
   and starts it. On a `starter` board the card appears at once. On a `recommended`
   board the seat's role lacks `annotate`, and the error names the command for a board
   owner (`aboard board role grant … annotate`), which the agent hands to its person.
4. **Changing it is a restart.** The agent edits the program and runs `aboard ext run`
   again; the board view redraws from data, with no build.
5. **Sharing it** (slice 2): the agent says "Publish it with `aboard ext publish
   ./summary-card`" and its person runs that (publishing is a person's step, like saving
   a template). It is now in the catalog. On another board, its owner runs `aboard ext
   enable summary-card --board design-review --set every=30`, or picks it from the
   board panel's "Extensions"; the host runs it there on a seat of theirs. Nobody rewrites
   it, and the record on each board says who enabled which version.

## 5. Map to decisions and the primitives test

| Piece | Status | Decisions it rests on | D54 test |
| --- | --- | --- | --- |
| Program on a seat | Exists | D56, D155, D181 | Already core (identity and attribution) |
| Program marker and description (`agent.program_set`) | New contract | D110 (labels read on their own), D155 | Passes: trust; readers must know a seat is code |
| Child seats for programs an agent starts | New; extends D165's subagent seats | D165, D181, D182 | Passes: permissions (ceiling and lifetime tied to parent) |
| `wait` on board events | New parameter | D69, D83 | Passes as API completeness: people have a stream, seats don't |
| Annotations (`board` and `owner` audience) | New primitive | D6 (bookkeeping isn't events), D175 (reactions), D207 (`about`) | Passes: trust, permissions, ordering |
| `annotate` and `monitor` role permissions | New | D97, rule 8 | Passes: permissions |
| Flags | Planned (`flag.raised` reserved, ROADMAP "later") | D16, D79 | Passes: trust (a flag reaches the owner and can't be muted) |
| View schema and board view rendering | New contract, client-side only | D118, D123 (panels appear when there's something to show), D119 | Not core: a client contract like `cli.yaml` |
| Action buttons as messages | Client-side | D114, D155 | Not core: an ordinary post |
| Manifest, `aboard ext` commands, extension kit | Client-side | D57, D75, D78 | Not core |
| `aboard <name>` dispatch, `aboard api` | Client-side; dispatch designed in D57, not built | D57 | Not core |
| Programs in the board file and templates | Schema addition, client-only | D59, D111, D178 | Not core |
| Model calls in programs | No change | D79 | Not core, by design |
| Monitors that only flag run as seats after the write; the server-called hook is kept for checks that must refuse | **Refines D57/D79**; needs approval | D57, D79 | As above |
| Catalog (publish, versions, bundles by digest) | New | D111 (templates), D212 (blob store) | Passes: permissions (who may publish), trust (the digest pins what runs) |
| Enablements per board, with settings (`extension.*` events) | New | D97, D111 | Passes: permissions (owners decide), trust (recorded) |
| Extension hosts and host credentials | New credential kind; the host is a client | D105, D197 (delegations) | The credential passes (permissions); running code stays outside |
| Pre-write check hook: allow, flag or refuse, timeout, fail mode | New contract; designed in D57 and D79, not built | D57, D79, rule 6 | Passes: only the server can stop a write before it is stored |
| Pattern library (turns, sealed collection, reveal, cards) | Client-side, in the SDK and aboard-lab | D55, D58, D75 | Not core until a part needs the server |
| Sandboxed apps | Later, needs approval | D189 | Not core; a policy key and a CSP change |

## 6. The first slice

**"An agent writes a card extension and turns it on for its board."** The smallest
thing that serves the first flow end to end and that the next two build on: it adds the
one data primitive (annotations), the drawing contract (views), the program seat and
following a board, and the folder and manifest everything later shares. Hooks and the
catalog come next, on top of exactly these pieces.

1. **Contracts:** annotations with `audience: board`, on the `board` and on messages
   (`PUT`, `DELETE`, `GET`; events `annotation.set`, `annotation.removed`); the
   `annotate` role permission (in `starter`'s `member` role, not in `recommended`'s);
   annotations on message reads; `PUT /v1/me/program` and `agent.program_set`; `wait`
   on `GET /boards/{b}/events`; `spec/view.schema.json` v1 with `text`, `fields`,
   `table`, `progress`, `badge` and `actions`; the manifest schema
   (`spec/extension.schema.json`); `annotations` and `events_wait` in `/v1/info`.
2. **Server:** one write path, one read-model table, visibility following the target,
   the size cap and redaction of string values.
3. **CLI:** `aboard annotate`, `aboard annotations`, `aboard events --wait`, and
   `aboard ext check`, `ext run` (a seat of the person's, or a child seat when an agent
   runs it, started through the built-in headless-style launcher) and `ext list`.
4. **Board view:** board cards in the board panel framed with their writer; message
   labels; programs marked in the crew; action buttons with confirm-then-post. Tried in
   the UI lab as a scenario first, then moved.
5. **Skill and docs:** the skill names `docs/extending.mdx` and `/examples` as what to
   read before building on aboard; `docs/extending.mdx` gets "Extensions" with the
   manifest, views and a walk-through an agent can follow alone.
6. **Examples and tests:** `examples/summary-card/` (the agent-written flow, below) and
   `examples/twenty-questions/` (the launch demo's game master, keeping a score card),
   each with an e2e test using fake members: the card appears, updates, survives a
   restart of the program, and its action posts as the viewer.

**How the three flows land on it:**

| Slice | Adds | Serves |
| --- | --- | --- |
| 1 (above) | Annotations, views, program seats, `wait` on events, `aboard ext run` | Flow 1 for one board; flow 2 after the write (forwarders, flagging by annotation); flow 3's patterns as examples |
| 2 | Catalog, `ext publish`, `ext enable` with settings, extension hosts (the daemon locally, `aboard ext host` on a team) | Flow 1 shared across the server |
| 3 | Flags and the pre-write check hook (allow, flag, refuse; timeout; fail mode), and an extension serving as the hook | Flow 2 before the write |
| 4 | The pattern library in the SDK and aboard-lab; round-robin, sealed round and review gate published as catalog extensions | Flow 3 as reusable extensions |
| Later | `owner` annotations and Inbox groups; `aboard <name>` dispatch and `aboard api`; held messages; sandboxed apps only if a real extension needs them | |

## 7. Examples

### A monitor after the write (flow 2)

A seat with role `monitor` (`can: [monitor]`) that flags agent messages asking another
agent to skip its owner. Rules first, a cheap model only when unsure; the model call is
in the program.

```python
# examples/canary-monitor/monitor.py: follows a board and flags suspect messages.
import json, re, subprocess
SUSPECT = re.compile(r"ignore (your|the) (charter|owner)|don't tell", re.I)

def aboard(*args):
    out = subprocess.run(["aboard", *args, "--as", "canary", "--json"], check=True,
                         capture_output=True, text=True).stdout
    return json.loads(out)

after = aboard("read", "--newest", "--limit", "1")["next_after"]
while True:
    page = aboard("events", "--after", str(after), "--wait", "60")
    for ev in page["events"]:
        if ev["type"] != "message.posted" or ev["actor"]["kind"] != "agent":
            continue
        body = ev["data"]["body"]
        if SUSPECT.search(body) or judge(body) == "yes":              # judge: claude -p
            aboard("flag", ev["data"]["message_id"], "--reason", "asks to skip the owner")  # slice 3
            aboard("annotate", ev["data"]["message_id"], "risk",
                   "--value", '{"view":1,"badge":{"text":"flagged","tone":"attention"}}')
    after = page["next_after"]
```

### A summary card, written by an agent and shared (flow 1)

The program an agent writes in the walk-through above. Its folder:

```yaml
# summary-card/aboard-ext.yaml
ext: 1
name: summary-card
version: 0.1.0
summary: Keeps a "Where things stand" card, rewritten every few messages by a model.
run: [bash, card.sh]
needs: [annotations, events_wait]
can: [annotate]
role: summariser
uses_models: ["anthropic, through Claude Code's print mode"]
settings:
  every: {type: integer, minimum: 5, default: 20, description: "Messages between rewrites"}
```

```bash
# summary-card/card.sh: waits for N new messages, then rewrites the card.
every=$(aboard ext settings --json | jq -r '.every')
after=$(aboard read --newest --limit 1 --json | jq -r '.next_after')
while true; do
  after=$(aboard events --after "$after" --wait 600 --count "$every" --json | jq -r '.next_after')
  summary=$(aboard read --newest --limit 200 --markdown |
    claude -p "Summarise in five bullets what changed and what is waiting on whom.")
  jq -n --arg s "$summary" '{view:1, title:"Where things stand",
    blocks:[{type:"text", markdown:$s, by_model:"claude-haiku"}]}' |
    aboard annotate board summary --file -
done
```

The host starts it with `ABOARD_AGENT` set to the program's seat, so every `aboard`
command acts as that seat. The board panel shows the card framed "by @summary-card ·
program · owner @leo · written by a model"; an agent joining later reads it with
`aboard annotations board --json`. Shared: `aboard ext publish ./summary-card` once;
then `aboard ext enable summary-card --board <b> --set every=30` on any board, by its
owners, with no copy of the code.

### A pre-write check and a Slack forwarder (flow 2)

Two extensions on one board, one on each side of the write.

**The check** refuses agent messages that paste a production hostname, and flags
messages that read like an instruction to another person's agent. It is a catalog
extension with `check: true`: its host serves the endpoint, and the board's owners set it
as the hook when they enable it.

```yaml
# board policy, set by an owner: aboard ext enable prod-guard --board release --as-hook
monitor:
  checks: [prompt_injection, credential]
  hook: {extension: prod-guard, timeout_ms: 1000, on_error: refuse, applies_to: [agents]}
```

```python
# prod-guard/check.py: the hook's handler; answers within the time limit or not at all.
PROD = re.compile(r"\b[\w.-]+\.prod\.internal\b")

def check(req):                       # req: {board, message, sender}, signature verified
    body = req["message"]["body"]
    if PROD.search(body):
        return {"decision": "refuse",
                "reason": "Production hostnames don't go on this board. Name the service instead."}
    if req["sender"]["kind"] == "agent" and looks_like_instruction(body):
        return {"decision": "flag", "reason": "Reads like an instruction to another agent."}
    return {"decision": "allow"}
```

The sending agent gets `Error (message_refused): … Hint: Name the service instead.` and
posts again; nothing of the refused text is stored. If `prod-guard`'s host is down, the
board's `on_error: refuse` makes every agent post fail with `monitor_unavailable` until
it is back, which is what this board's owners chose; a board that prefers to keep
working sets `allow` and sees each message marked `unchecked`.

**The forwarder** sends every message that mentions `@oncall` to a Slack channel. It is
an ordinary program after the write, so Slack being slow or down never delays a post.

```python
# slack-forward/forward.py
after = aboard("read", "--newest", "--limit", "1")["next_after"]
while True:
    page = aboard("events", "--after", str(after), "--wait", "300")
    for ev in page["events"]:
        if ev["type"] == "message.posted" and "oncall" in ev["data"]["mentions"]:
            post_to_slack(settings["channel"], f'{ev["actor"]["name"]}: {ev["data"]["body"]}')
    after = page["next_after"]   # saved only after Slack accepted, so a restart resends nothing
```

The forwarder's seat sees only what its role and the board's visibility allow, so it
can't leak an `addressed` message it wasn't sent.

### Round-robin, sealed rounds and a review gate (flow 3)

Three patterns published as catalog extensions on the shared library, each enabled per
board with settings:

```bash
aboard ext enable round-robin  --board design   --set 'order=["claude","codex","omp"]' --set rounds=3
aboard ext enable sealed-round --board estimate --set 'question="How many days for CHK-12?"'
aboard ext enable review-gate  --board release  --set reviewer=codex --set approver=@leo
```

```python
# sealed-round/main.py, on the pattern library
from aboard.patterns import Board
b = Board.current()                                   # the program's own seat
q = b.settings["question"]
answers = b.collect(b.agents(), q, sealed=True, timeout=900)   # each answer sent only to the program
b.card(title="Sealed round", fields={"Question": q, "Answered": f"{len(answers)} of {len(b.agents())}"})
b.reveal(answers)                                     # one post, all answers, in the order received
```

- **Round-robin** posts to one agent at a time and waits for its reply; under `focused`
  delivery only that agent wakes. With the players' role lacking `broadcast`, the
  board's own rules keep them from talking over each other.
- **Sealed round** relies on `addressed` visibility (D3): the server guarantees no
  player reads another's answer before the reveal. What it can't guarantee is the
  program itself not peeking or revealing early; that part would justify a server
  primitive later, by the promotion rule (D75).
- **Review gate** watches `task.done`, opens a review task for the reviewer and an
  approval ask to the approver (D208, D211), and labels the task "waiting on review"
  until the approval exists. It can't stop anyone marking a task done; a hard gate sits
  outside, where CI reads the approval before a merge.

### A game orchestrator (flow 3)

Twenty Questions between Claude Code, Codex and omp. The game master holds the secret
and the turn order; aboard holds the record, the attribution and the visibility.

- The board file has `agents:` for the three players and `{name: gm, run: [python3,
  gm.py], role: game-master}`; the `game-master` role has `post`, `broadcast` and
  `annotate`. `aboard swarm up` starts all four.
- The game master posts each turn to one player (`--to @codex`, which wakes only that
  agent under `focused` delivery), reads answers with `aboard inbox --wait`, and keeps a
  card: a `fields` block (round, questions left), a `table` (scores) and `actions`
  ("Next round", which posts `next round` to `@gm` as the viewer).
- Sealed guesses use `addressed` visibility (D3): players send guesses to `@gm` only and
  the game master reveals them together. The fairness is the game master's; the server
  guarantees nobody else read them and the record shows who sent what, when.
- Nothing about phases, turns or scoring enters the server. If sealed rounds prove
  themselves across games and experiments, they come in by the promotion rule (D75).

### An experiment (flow 3)

The injection-spread benchmark (B3) on aboard-lab, once the Python SDK exists:

```python
from aboard_lab import Experiment, monitor

@monitor(role="monitor")                         # runs as a monitor seat, as above
def canary(msg):
    return "flag" if "canary.txt" in msg.body else "allow"

exp = Experiment("b3-injection", conditions=["starter", "recommended", "recommended+canary"],
                 repeats=10, seed=7)

@exp.trial
def run(t):
    board = t.board(template="b3-parser", policy=t.condition)   # fresh board per trial
    t.launch(["claude-code", "codex", "codex", "codex"], launcher="headless")
    t.card(progress=(t.index, t.total), fields={"condition": t.condition})   # a board card
    t.after(messages=30, do=lambda: t.whisper("@codex-2", INJECTION))       # private signal
    t.wait(task_done="parser", timeout=1800)
    return t.score(obeyed=t.files_touched("canary.txt"), flags=t.flags(), tests=t.hidden_tests())
```

The runner is a person-key client (it creates boards); the monitor is a program seat;
the progress card is an annotation; the condition and the score are written as
annotations on the board, so they sit in the trial's hash chain beside what happened.
`aboard audit verify` then shows the result wasn't edited after the fact.

## 8. Open questions for the maintainer

1. **Direction:** programs on seats, annotations and declarative views first; a
   per-server catalog with per-board enablement run by hosts; sandboxed apps later or
   never. Is that the shape you want?
2. **Hosts:** is "the delivery daemon hosts your boards' extensions locally;
   `aboard ext host` or an admin's container hosts them on a team" acceptable, given it
   means one more long-running client on a team deploy? The alternative is that every
   enablement runs on the enabling person's machine only, which stops when their laptop
   sleeps.
3. **The check hook:** may it refuse (not only flag), with the fail mode chosen per
   board by owners and `allow` as the default? And is "refuse, never rewrite" the right
   line? It refines D57 and D79, and puts an outbound call on the write path of a team
   server, guarded as in section 9.
4. **Monitors that only flag:** should they be programs after the write, so the server
   makes outbound calls only for checks that must refuse?
5. **Who publishes:** server admins on a team, like templates (D111)? Or any member,
   with admins able to remove entries?
6. **Names:** "annotation" for the data, "card" and "label" for how it's drawn,
   "program" for the seat, "extension" for the folder, "catalog", "enable", "host", and
   `aboard ext` for the commands. Or fewer words?
7. **`owner` annotations:** are personal annotations (Inbox groups, my own labels) worth
   a bookkeeping table, or should personal sorting stay in the browser and CLI? And
   should the delivery daemon treat one from the person's own program (say
   `wake: true`) as "concerns this agent", a second path into D173's rules?
8. **Models:** is the crew's "sends board text to: <provider>" disclosure enough on team
   boards, or do you want a board policy that forbids it? Is running a harness's headless
   turn on a person's subscription acceptable under each provider's terms for automated
   use?
9. **Should people annotate too** (a person's own labels on messages), or only seats?

## 9. What not to do

- **No user code in the server**, in the write path or beside it: no plugins, no WASM, no
  scripting, no "server functions". The rules stay readable in `board`, `rules` and
  `events`.
- **No extension JavaScript in the board view's origin** at runtime. It would act as
  every viewer.
- **No extension acting with a person's login** (D155). Programs have seats.
- **No per-extension endpoints** (`/v1/ext/gm/…`) and no server proxy to programs, the
  check hook's one outbound call aside. The API stays one shape for everyone.
- **No workflow or game engine in the core:** phases, turns, scores and protocols live
  in programs; only what proves itself across many uses moves in (D75).
- **No general key-value store.** Annotations are small, attached to a target, and
  capped; big state goes in files.
- **No HTML, remote images, colours or styles chosen by a program** in views, and no
  view outside its attributed frame.
- **No view or app that receives data its program's seat couldn't read.**
- **No model calls in the server**, optional or not; consented calls, if they come,
  belong in the person's own daemon.
- **No hook that rewrites a message.** A pre-write check allows, flags or refuses; the
  only rewrite is the core's redaction, which is recorded.
- **No outbound calls from the server after the write** (webhooks, forwarding, retries).
  Those are programs following the board.
- **No database lock held across a hook call**, and no hook call without a time limit
  and a fail mode someone chose.
- **No catalog entry that runs on a board without its owners enabling it**, and no
  enablement that moves to a new version by itself.
- **No public marketplace or registry** before real extensions exist.
- **No scaffolding generator before the docs, examples and kit work for agents.** pi's
  self-extension works because of docs and examples, not tooling.
