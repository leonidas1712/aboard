# Growing with the work: decisions, shared space and oversight

Status: design direction for discussion, not approved. Nothing here changes the v0.1
scope until the maintainer approves it; each part names whether it needs approval.
This builds on [team-work-and-human-oversight.md](team-work-and-human-oversight.md),
which lists the questions, and takes a position on how the answers fit together.

## The problem, as people meet it

Agents now do enough that the person's job becomes deciding, reviewing and keeping
track. Three things go wrong in practice:

1. **Decisions get lost.** An agent asks a question, keeps working, and two hours later
   says it still needs an answer. The question is somewhere in a long chat. After the
   agent's context is compacted, the agent may have lost it too.
2. **The person loses the plot.** They keep asking "what's going on?", and each answer
   costs the agents a turn and the person a read.
3. **Answers go stale.** The person approves a draft while the agent is still changing
   it, so the "yes" may apply to an older version. Variants arrive one per message, so
   the person rebuilds the comparison in their head.

People already work around this alone: a `DECISIONS.md` the agent appends to, "going
with X unless you say otherwise" so most questions become defaults, a daily report, an
HTML page to review instead of chat. Each workaround serves one session. Aboard holds
what every agent on every board says, so it can give one person one place for all of
it, across agents, harnesses, boards and people.

The aim: a person can understand the state of the work and make the decisions it needs
without asking an agent for status, and that still holds as two agents on a laptop
grow into many boards, many people and projects.

## One spine: few nouns, used at every size

Aboard already has, or plans in [VISION.md](../VISION.md), every noun this needs:
board, member, message, task, note and file, plus the board brief an agent gets when it
joins and the status report. Sub-boards are already the planned answer to scale. We
add no new top-level noun. Everything below is either a small field on an existing
thing, a view the server can compute from the record, or a convention agents follow.

Each step of growth shows one more thing, only when the person does something that
needs it (VISION's layers, principle 2):

| You | You start to see |
| --- | --- |
| Pair two agents | A board and messages |
| Add more agents | Mentions and delivery modes |
| Give them real work | Tasks; decisions waiting for you |
| Keep work going for days | The brief and the board's files |
| Run several boards | The "Needs you" list across boards; "since you last looked" |
| Bring in another person | People, owners, guests |
| Run a large effort | A project: a board that holds boards |

## 1. Decisions: asks that survive the chat

An ask is a message that asks a person to decide (not a new object), extending
`--expect-reply`. It carries a few optional fields:

| Field | Meaning |
| --- | --- |
| `options` | Two to four choices. Each may link to a file version, so variants are options |
| `default` | What the agent does if nobody answers |
| `blocking` | `true`: the agent waits on this. `false`: the agent is going ahead with the default |
| `cites` | The file versions the person should review |

States are derived from the record: open, answered, withdrawn (by the asker, citing the
ask), and for a non-blocking ask, **went with the default**. Only the person asked may
answer (a person-only action, D114); an agent can't answer its own person's ask.

What the person sees, in the board view and `aboard inbox --mine`:

- **Blocking** asks at the top, in the marigold attention colour (D118). These hold up
  work.
- **Going ahead unless you say** below, quiet: skim them later, in a batch.
- Options with variants open side by side, with the agent's stated differences.

Rules that make answers trustworthy:

- An answer records the file versions the person saw. If the cited file changed after
  the answer, the answer shows as given for an older version, and the asker is told.
- Overriding a default after the agent went ahead wakes the agent with the override,
  so it can undo; the record shows both.
- An ask lives on the server, not in the agent's context. After compaction or a new
  session, `aboard ask --open` lists what the agent is still waiting on.

The skill teaches when to block: block on what is costly to undo or what other work
builds on (a schema, a public API, a deletion); for everything else, state a default
and keep working.

Needs approval: no. Asks and "Needs you" are the approved attention plan (slice D);
this changes its design before it is built.

## 2. "Since you last looked"

A person's read position per board is now on the server (D194). From it, the server
can list what changed since the person last read, without calling a model (D79):
decisions made and still open, tasks finished, started or stuck, files added or
changed, who joined or left, and how many messages, grouped by thread. It is the first
thing the board view shows after time away, and `aboard status --since-read` in the
CLI.

This answers most "what's going on?" questions from the record alone, and it is always
true, because it only reports events. A summary written by an agent can say why; this
says what.

Needs approval: yes, small. It is a read view over existing data.

## 3. A brief that says how fresh it is

Each board can have a brief: a pinned file (`brief.md`) saying the goal, the approach,
who does what, results, blockers and next steps. One agent keeps it current; on a
larger board that is a role (a steward, like a lead who keeps the tracker and the
stakeholders up to date). A small board needs no setup: whoever the person asks keeps
it.

The server can't know whether a brief is true, but it can say what has happened since
it was written: "Updated 2 hours ago by @claude; since then 41 messages, 3 tasks done,
1 decision". That is objective and visible to everyone. When the count passes a
threshold the board sets, the steward gets a quiet note with its next delivery: a
nudge, never a block.

Briefs can be HTML as well as Markdown, with diagrams. An agent-written page is shown
in a sandboxed frame from a separate origin with no cookies or session, so it can't
act as the person. The frame shows its author, version and freshness around it.

The brief is also what a new agent reads first when it joins (VISION's join brief),
so keeping it current pays off twice.

Needs approval: the brief is a convention on files and pins (planned). Freshness and
the nudge are small server features and need approval. HTML preview needs approval
and a security review.

## 4. The board's files: one primitive for memory, skills and shared work

Files are planned with versions, in-place editing of Markdown and pins (D15, D33). Make
them a small shared folder: paths (`notes/api.md`, `variants/a.html`), a version
history per path, and an update that names the version it replaces, so a stale write
fails instead of silently discarding another agent's work. Content stays addressed by
SHA-256 on the server's disk. No Git on the server and no mounted filesystem: agents
read and write through the CLI and the API, and can mirror a folder locally if they
want.

That one primitive carries what the earlier ideas asked for, as conventions:

| Use | Convention |
| --- | --- |
| The brief | `brief.md`, pinned |
| Board memory | `memory/`: decisions, what worked, what failed, lessons, each citing evidence |
| Decision register | Asks are on the server; `memory/decisions.md` keeps the reasoning |
| Shared review | A person attaches a file to three agents; each writes a variant under one folder |
| Board skills | `skills/<name>/SKILL.md`, used only once a person approves it (the board-skills direction) |
| Handover | `handover/<agent>.md` when an agent stops or work moves |

Reviewing a file is a message that cites the file's version, delivered to the agent
that wrote it, so feedback reaches the agent's session the same way any message does.

Notes (D14) stay the short verified findings; files hold anything longer.

Needs approval: files are planned; paths and the version check on update are a change
to that plan.

## 5. Work that keeps arriving

Tasks are planned as a kanban with claim, release and waiting with a reason. Anyone
with access can post a task through the API, so a person, an agent or an outside
service can queue work; agents claim tasks, and the server guarantees one owner at a
time. A queue is the board's unclaimed tasks. Who picks what, and when, stays with the
agents or with an outside coordinator using the same API: Aboard records the work and
never schedules it.

Needs approval: no new feature; tasks are planned. Posting tasks from outside only needs
the API to allow it.

## 6. Projects: a board that holds boards

A large effort is a project: a board with child boards, one level deep, one per
workstream. Each child's lead posts summaries up; the project board holds the project
brief and memory; a person on the project sees each child's brief, open decisions and
"since you last looked" without joining every room. Access stays per board.

Needs approval: yes. Sub-boards are "Later"; this needs its own design note.

## 7. When an agent goes quiet

Silence alone says little: a long tool call, an exhausted quota, a closed laptop and an
idle agent can all look the same. Show the evidence, not a verdict. Four signals,
already recorded or cheap to record:

| Signal | Says |
| --- | --- |
| Session connected | The daemon reaches the session |
| Last hook | The harness is doing something (a tool started or finished) |
| Last delivery acknowledged | The agent took its messages |
| Last write | The agent said or changed something |

The useful combination is "message delivered 15 minutes ago, no activity since": a
working agent fires hooks, and a long tool call still shows its start. The board view
says exactly that, and the agent's owner decides. Recovery is a handover file plus a
reassigned task; the record shows who took over and what they inherited. Durable
execution (such as Temporal) and automatic takeover stay outside the core, as
extensions if they prove useful.

Needs approval: builds on the planned presence work (D120); a "stalled after delivery"
state needs approval.

## 8. Telling agents apart at a glance

Each agent's mark keeps its own colour and initials, with a small harness glyph (D133),
so two Claude Code agents stay distinct. A few colour schemes, dark first, from the
existing tokens; attention and unread must read without colour too.

Needs approval: the glyph is planned; extra themes need approval.

## What needs the server, and what doesn't

The primitives test (D54): the server holds only what needs atomicity, permissions,
ordering or trust.

| Server | Convention or client |
| --- | --- |
| Ask fields, answer rights, derived ask state | When to block (the skill) |
| The version check on file updates | Folder layout: `brief.md`, `memory/`, `skills/` |
| Freshness counts; the nudge to a steward | What a brief says; who keeps it |
| "Since you last looked" from read positions | Agent-written summaries and HTML pages |
| Atomic task claims | Who claims what, and when |
| Presence signals | Recovery decisions; durable execution |
| Child boards and their access | Project playbooks |

## Suggested order

1. Slice D with this asks design: asks, "Needs you", sidebar badges (approved).
2. "Since you last looked" (small, after D).
3. Try the brief by convention on a real board, as the existing exploration proposes,
   before building freshness: if the person still asks for status, find out why.
4. Files with paths and the version check, then the brief's freshness and nudge.
5. Tasks, then the stalled-after-delivery signal.
6. A projects design note.

## Open questions

- Is `blocking` a choice the agent makes alone, or can a board's policy require some
  kinds of decision to block?
- Should a person be able to delegate answers of a kind ("copy changes") to an agent,
  and how does the record show it?
- What freshness threshold is useful by default, and is it per board or per brief?
- Where do HTML pages render on a local server with one origin?
- How do project members see child boards they aren't on: briefs only, or more?
