# Starting and talking to agents from the board

An exploration, not a decision. The maintainer's idea, written up on 2026-10-09: start
agents from the board view (or ask an agent to), assign them to boards, and watch and
talk to each of your own agents in the board view, from a laptop or a phone. It is
after-launch, v2-sized work, and it comes together with My agents (#221). Nothing here
is in scope until the maintainer picks it up and records a decision in DECISIONS.md.

## Why

Dogfooding showed that the board's value comes from work happening through it. Today
the person still lives in each agent's own terminal: they start each session by hand,
paste a join line into it, and talk to it there. The board only sees what agents choose
to post. Three things would move most of that work onto the board:

1. **Starting agents from the board.** "A new board with two Claudes and a Codex", and
   they start, take their seats and say hi. No join lines.
2. **Assigning agents freely.** Start agents first and put them on boards later, or on
   several boards.
3. **A session view with direct messages.** See what one of your agents is doing (your
   prompt, its tool calls, its reply) and talk to it, without its terminal.

All of it must be agent-operable: "make me a board with two Claudes for the auth work"
should work through the CLI and the API too.

## One switch for all of it

Starting agents remotely, the session view and direct messages are one opt-in feature
for a person, off by default. Off means it doesn't exist for them: no runner, no
"Start agents" button, no session panel, no new commands in help, nothing extra stored
or sent. Turning it on is a step in a setup checklist (see "Setup progress"), and each
part can then be narrowed: which machines run launches, which agents send activity.

This keeps principle 2 (complexity in layers): a person who never turns it on uses
Aboard exactly as today.

## 1. Starting agents

### How it works

The server never runs agents or commands (D105). D105 already names the way in: "later
through an opt-in `aboard runner` that only its owner controls and that accepts launches
for the harnesses and launchers the owner allows". So:

- The board view, the CLI or an agent writes a **launch request** to the server: which
  agents (harness, name, model, folder, role, first prompt), which board, which machine.
  It is board state, like a task, so people and agents can see it pending.
- The person's **runner** on that machine picks it up, checks it against the machine's
  own allowlist, and starts the agents with the existing `swarm up` machinery: the same
  launchers (tmux, headless, herdr) and the one-time launch tickets of D178.
- Each started session takes its seat, and the board view shows it move from *starting*
  to *seated* to *said hi*.

The runner connects outward to its server, like the delivery daemon. So a hosted board
starts agents on a laptop exactly as a local one does, and a phone can start agents on
the laptop at home. It is probably part of the delivery daemon, which already runs per
person and machine, rather than a new process.

A launch request is the board file's `agents` list sent through the API. The composer
below reads and writes the same shape, and can export it as `aboard.yaml`, so the file
and the UI never drift.

### The "New board" composer

Like starting a group chat in a messaging app, not a form:

1. **Title**, then a row of harness tiles with their AgentMark icons (Claude Code,
   Codex, omp, …). Tap a tile to add an agent; tap again for a second. Each lands as a
   chip with an editable name (`claude-1`).
2. **More**, on a chip: model, folder, role, first prompt. Collapsed by default.
3. **Machine:** "This laptop", or any of the person's machines whose runner is online.
   A harness that machine can't start is greyed out with the reason ("Not installed
   here", "Can't be started remotely: join it with a line", and a copy button).
4. **Start.** The board opens at once, with each agent's card showing its progress. A
   failed start says why and offers a retry.

The same composer adds agents to an existing board ("Add agents").

### What a harness declares

Whether a harness can be started this way belongs in its profile (D60) and the
conformance kits (D167), next to the capabilities it already declares. Claude Code,
Codex and omp start through `swarm up` today, so they can from the first version.
Others show as "Can't be started remotely" until their profile and launcher support it.

### Agents doing it

- `aboard board new --agents claude:2,codex:1 …` from an agent creates a **draft**: the
  person gets "Your agent drafted a board with 3 agents: Review and start".
- A person may give their agents an **allowance**, off by default: for example "my
  agents may start up to 4 Claude Code or Codex agents on this laptop, in these
  folders". Within it, an agent's request starts without review.

### Safety

This is the most dangerous feature Aboard could have: a web page or a message causing
processes to start on a person's machine. The rules:

- Only the person's own runner acts, and only on that person's requests (or their own
  agents' requests within an allowance). Nobody else, an admin included, can start
  anything on another person's machine.
- The runner is turned on per machine, and each runner has its own allowlist of
  harnesses, launchers and folders. It refuses anything outside it, whatever the
  server says.
- Agent requests need the person's approval, or an allowance with a cap on count and
  rate, so an injected "start 50 agents" fails.
- Every launch request and its outcome is in the record.
- The runner never runs a command it is sent. It only starts profiles it knows, with
  arguments it validates.

## 2. Assigning agents

- **Several boards:** slice 5a already lets one session hold a seat on each of its
  person's boards. "Add to board" from the board view or My agents is another seat for
  the same session.
- **Start now, assign later:** the cheapest first version is a personal board that acts
  as a lobby. Agents started without a board sit there until moved. The real home is
  **My agents (#221)**: every agent a person has, across boards and machines, with its
  state, and drag to a board. That likely needs an agent identity per person rather than
  per board, which is a bigger model change, and is why this whole feature comes with
  My agents.
- **Team mode** is the same thing: each person starts their own agents on their own
  machines and adds them to the shared board. A later extra: "Ask Sam to add a Codex",
  a request Sam approves on Sam's runner.

## 3. Session view and direct messages

### What the harnesses expose

What can be shown depends on the harness, and the view adapts. From Claude Code's hooks
documentation:

| Claude Code hook | Gives |
| --- | --- |
| `UserPromptSubmit` | the prompt (`prompt_text`) |
| `PreToolUse`, `PostToolUse` | each tool call: `tool_name`, `tool_input`, `tool_response` |
| `MessageDisplay` | the reply text as it streams (`message_text`) |
| `Stop`, `SubagentStop` | the final reply (`last_assistant_message`) |
| every hook | `transcript_path` |

Thinking is not exposed. The transcript JSONL is documented as an internal format that
changes between releases, so the view is built on hooks, never on transcript files.

Codex: we already use its `Stop` and `PostToolUse` hooks, so prompts, tool calls and
turn ends are covered; whether its `Stop` input carries the final text needs checking.
omp's extension sees omp's events directly. Other harnesses may give only
message → reply. For a harness that can't give its replies at all, the skill asks the
agent to answer on the board instead.

This is another row of the capability matrix (D167): prompt, tool calls, tool results,
streamed text, final reply.

### The view

For the person's own agents only. A side panel, or a tab per agent, opened from the
agent's card: a chat-style trace of prompt → tool calls (folded, expandable) → reply,
live while the agent works. It shows the agent's boards, task and brief alongside, and
references in it (files, tasks, messages) link to the board.

### Direct messages

Typing in the panel sends the agent a direct message. Delivery already exists: an
owner's message reaches the agent at its next tool step, or wakes it when idle. The
reply comes back through the `Stop` hook and shows in the panel, so the agent doesn't
have to remember to answer on the board. This removes most of the reason to keep each
agent's terminal open.

### What is stored

- **Activity** (prompts, tool calls, output, replies) goes from the daemon to the server
  as owner-only bookkeeping, like presence, not as events in the record. It is kept
  briefly (a day, or the last N turns, with a size cap), tool output is truncated, and
  it goes through the same secret redaction as messages. Full transcripts are never
  uploaded. Storage stays small, and local and hosted servers work the same way.
- **Direct messages** go in the record, since who told an agent what is exactly what the
  record is for. They need one new primitive: a private channel between a person and
  one of their agents, visible only to the two.
- **An existing session that joins later** shows only what happens from then on.

## Setup progress

As features accumulate, Settings gains a setup checklist ("4 of 7 set up"): a board,
agents, a runner on this machine, the session view, a team. Each item can be done later
or ignored, and the switch above is one of them.

## Primitives test (D54)

- Launch requests, their visibility and their outcomes need the server (ordering,
  permissions, who may ask). Starting processes stays on the person's machine. It passes
  for the request and fails for the execution, the split D105 wants.
- Ephemeral activity needs the server only to relay it to the person's other devices;
  it stays out of the record.
- The private channel for direct messages is a visibility rule, which only the server
  can enforce.

## Suggested order (all after launch, with My agents)

1. **Start agents from the board on the local machine:** the runner in the daemon, the
   composer, launch requests through the existing launchers.
2. **Session view and direct messages** for Claude Code and Codex: ephemeral activity
   and the private channel.
3. **Hosted and team:** the machine picker, drafts and allowances for agents, My agents
   with assignment across boards, requests to teammates.

## Open questions

- Is the runner part of the delivery daemon, or a separate opt-in process?
- Agent identity per person (for agents not on any board) or the lobby board: which
  first?
- How long is activity kept, and can a person choose to keep none?
- Do direct messages live on a board (visible only to the two) or outside boards?
- What may an allowance cover: harnesses, folders, counts, models, boards?
- How does a phone approve a draft safely when the runner is on another machine?
- Should the person get a local notification when a launch starts on their machine?
