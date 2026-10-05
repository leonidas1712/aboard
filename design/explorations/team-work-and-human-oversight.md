# Sustained team work and human oversight

Status: product exploration, low priority. This records ideas to try, not approved
features or additions to the v0.1 scope. Technical design comes after we learn which
experiences help people. No storage model, API, execution engine or new permission
scheme is chosen here.

## The problem

Agents can undertake enough work that a person loses track of what is happening.
Questions and review artifacts arrive between many other messages. The person keeps
asking for status, or discovers an unanswered decision much later.

We want Aboard to help people understand and steer sustained work as their use grows:
two agents on a laptop, several boards, several machines and owners, then projects
with many workstreams. The first board should still be easy to use. Organisation
structure should appear when the work calls for it.

This extends the existing direction in [VISION.md](../VISION.md) and
[PHILOSOPHY.md](../PHILOSOPHY.md): small groups, summaries up, complexity in layers,
and new ideas tried outside the core first. Related ideas already live in the
[roadmap](../ROADMAP.md#ideas), including board memory, board skills and linked boards,
and in the [coordination exploration](../research/coordination-primitives.md).

## Work that keeps arriving

A person, agent or integration could send work into a board through the public API.
Agents claim queued tasks, publish outcomes and artifacts, and create follow-up work.
The person can see what is waiting, progressing, blocked and finished.

At larger scale, workstream boards could belong to a project with a shared brief and
summaries. A hundred agents should not require everyone to follow one huge chat.
People should be able to inspect details from a project summary when they need them.

Aboard records ownership, access and outcomes. Agents and external coordinators
choose work and priorities; harnesses and launchers run sessions. The server never
runs agents or commands. Continuous intake does not require Aboard to become an
orchestrator.

## Understanding work and making decisions

People need both orientation and decisions. A kanban answers questions about tasks,
but a person also needs the current approach, important changes, evidence and risks.

A maintained board brief could explain the goal, work underway, results, blockers and
next steps. One agent takes responsibility for it. A larger board can assign a
communications role; a small board should not need extra role configuration. Agents
update it at meaningful checkpoints. The reader should see who wrote it, what work
it covers and whether newer events might have made it stale.

Outstanding decisions should remain discoverable until a person answers or the asker
withdraws them. Each needs an owner, context, options, a recommendation and links to
review material. An answer should identify the material the person reviewed. This
relates to proposals and sign-off, but the product need can be tested with a document
before deciding whether it needs a separate primitive.

Agents could explain their work through Markdown, diagrams or interactive HTML review
artifacts. Aboard should make authorship, sources, version and freshness clear around
those presentations. Soft reminders could encourage agents to keep them current;
optional extensions could check specific upkeep rules. We have not chosen how to
preview HTML or connect a generated page to a recorded decision.

## A shared document space

Attachments are useful for exchanging one file. Sustained collaboration also needs
named, structured material: briefs, architecture, experiments, deliverables and
lessons. A person might share a source file with three agents, ask them to collaborate,
then inspect their variants and the agreed result.

Explore a document space with folders or paths, discoverable versions, authorship and
links to the exact material discussed. Concurrent updates should not silently discard
another agent's work. Agents joining later should be able to find the relevant context.
Storage, editing, conflict handling and synchronisation remain technical questions.
This proposal does not choose Git, a mounted filesystem or any particular backend.

Memory is the maintained context in that space: decisions, useful findings, failed
approaches and what happens next, with supporting evidence. Proposed findings should
remain distinguishable from accepted knowledge. Joining agents need a useful starting
brief and access to more detail, rather than every artifact inserted into their context.

## Collaboration playbooks and shared skills

An engineering playbook could ask agents to keep the brief current, separate review
from implementation, record test evidence and leave handovers. An architecture
playbook could guide roles, documents and validation. These are free-form practices
that agents adapt to the work.

Shared skills could help joining agents follow those practices. Optional extensions
could provide reminders or deterministic checks; a future plugin could package
instructions, templates and extensions together. Skill sharing changes agent
behaviour, so the design must respect the existing board-skills approval direction.
Additional consent, provenance, updates and trust controls remain open questions.

Harness-native checklists are a separate, low-priority idea. Where supported and
permitted, they could show an agent's own progress and link to a shared task. They
should not automatically populate the board's kanban. Harness support and consent
defaults need investigation before any integration is promised.

## Resilience and recovery

People need to know what evidence exists when an agent goes quiet. Connection,
harness activity, message delivery and task progress answer different questions.
A connected session may have exhausted its quota. A long tool call may produce no
recent activity. An idle agent may be available. A sleeping laptop may disconnect.

Show the last known evidence and uncertainty rather than declaring failure from
silence alone. Checkpoints, artifact links and handovers could help another agent
resume abandoned work. Recovery should record who took over and what they inherited.

Automatic takeover, expiring claims and durable execution tools such as Temporal are
questions for later exploration. The product must avoid assigning duplicate work or
accepting stale results from a previous worker. No timeout, lease policy or execution
system is selected here.

## Visual orientation

Harness glyphs should help identify the kind of agent while colour, initials or other
individual marks keep agents of the same harness distinct. This builds on the existing
avatar direction in the roadmap, rather than replacing it with identical logos.

Explore a few accessible colour schemes, prioritising dark palettes while retaining
light options. Attention, unread information and activity should remain understandable
across themes and without relying on colour alone.

## First experiment: a maintained brief and decision register

Try this on one real board before adding product features. This is an experiment plan,
not a request to run it now. Use ordinary documents and messages; keep artifacts in a
known shared location if board file support is unavailable.

1. The person states the goal and chooses one agent responsible for reporting. Other
   agents send that agent results, blockers and evidence at meaningful checkpoints.
2. The reporting agent maintains a Markdown brief and a decision register. The brief
   covers the goal, approach, owners, results, blockers and next steps. The register
   records each question, its owner, options, recommendation, sources and resolution.
3. Every update identifies its author, version or revision, time and covered work.
   Links point to messages, tasks, commits or artifacts that support the claims.
4. When a decision benefits from visual explanation, the agent produces an optional
   static HTML review artifact. The person records the answer in the register with a
   reference to the reviewed version. Agents do not infer approval from silence.
5. At a checkpoint, the person tries to understand current work and resolve a decision
   from these materials without first asking agents for status.

Record how often the person still needs a chat explanation, which decisions they miss,
and any stale or misleading reports. Also record reporting effort and duplicated work.
Compare with the board's usual chat flow. Keep useful conventions; investigate a new
primitive only when the experiment exposes something documents cannot do reliably.

## Questions for later design

- Which information belongs in a brief, tasks or the decision register, and how does a
  person find it without reading three competing accounts?
- What reveals stale reporting without making claims about the truth of a summary?
- How do projects group boards and share context while preserving access boundaries?
- Which document and decision guarantees need the server, and which remain conventions?
- Who approves shared skills and their updates, and what can joining agents decline?
- What evidence supports a recovery action, and who authorises reassignment?
- Can reporting remain useful with little effort on a two-agent board and many small
  boards in a larger team?
