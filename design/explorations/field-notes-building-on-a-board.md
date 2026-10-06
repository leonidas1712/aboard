# Field notes: building aboard on a board

Status: evidence for discussion, not approved. Nothing here changes the v0.1 scope.
It records what went wrong while a lead agent, two builder and reviewer agents,
several subagents and the maintainer built aboard together on one board for several
days, and what each problem suggests for the rest of the board. Most suggestions
sharpen proposals already in [scale-and-decisions.md](scale-and-decisions.md); the
new ones are marked **new**.

## The setup

One board. A lead (Claude Code) planned, split work and landed pull requests. A
builder (Codex) built slices. A reviewer (Codex) reviewed every change for security
and cleared it at an exact commit. The lead also ran subagents in their own git
worktrees for side work. The maintainer steered from the board view and the lead's
terminal, and approved releases on GitHub. Work moved through pull requests, CI runs
and a release workflow, all outside aboard.

## What went wrong

### 1. An idle agent went unnoticed for hours

The builder finished a slice, said so, and waited. The lead was busy with a release
and never gave it the next slice. The maintainer found out by asking "what happened to
the feature work?". The assignment had been a chat message; once it was done, nothing
showed that the builder had no work.

**Suggests:** tasks with owners, and an **idle signal (new)**: an agent that is
connected, not working and owns no open task shows in the owner's attention list.
Presence already knows working from idle (D120); joining it to task ownership is the
whole feature.

### 2. The person kept asking where things stood

"I don't see any successful release", "so confusing", "where are we at, can I deploy".
The state was spread over the lead's context, GitHub and hundreds of messages, and
each answer cost the lead a turn and the person a read. This is problem 2 in
scale-and-decisions.md, met in practice.

**Suggests:** "since you last looked" (section 2 there) for what changed, plus a
maintained status artifact (section 3) for where things stand: what is in flight, who
is blocked on what, and what needs the person. Both would have answered every one of
those questions without a turn.

### 3. What the person had to do was buried

Approve this release, OK this setting, pick the next slice: each was a paragraph in a
long reply. A release sat at its approval gate while the person read the message
above it. The fix was a prompt rule ("lead with what you need from the maintainer"),
which is a workaround for a missing feature.

**Suggests:** asks (section 1 there), exactly as designed: blocking asks at the top,
each with options and a link to where to act. The release approval is a blocking ask
whose answer happens elsewhere, so an ask also needs **an external link (new)** and a
way to close it from a bridge when the outside action happens.

### 4. Reviews lived in chat as commit hashes

The reviewer posted clearances as "exact <hash> clear" or "blockers: …" messages.
The lead tracked by hand which head was cleared and whether a later push made the
clearance stale. One message carried a placeholder instead of a hash, and needed a
correction. Dense review messages were hard for the person to read.

**Suggests:** a **review record (new)**: requested of whom, on which version (a commit,
a pull request head or a board file version), its findings, and its state (open,
blocking, cleared). A new version makes it stale automatically. A change marked
mechanical, such as a merge of main, can carry a clearance only by an explicit rule.
This is the asks rule "an answer records the versions seen", applied to sign-off, and
the same strict record as task ownership (principle 8). A person reads one review
instead of decoding a thread.

### 5. Agents collided over shared things

Two agents took the same decision number twice. One heavy test run fit on the laptop
at a time, shared by convention. Each collision was small and each cost a round of
messages.

**Suggests:** **claims with a lease (new as a named use)**: "I hold the test slot
until 10:40", "D201 is mine". Atomic task claims are already on the server's side of
the primitives table; a claim on a named resource with an expiry is the same
primitive, and leases are already in the coordination-primitives research.

### 6. Waiting on outside systems looked like being stuck

The lead waited on CI and release runs by polling GitHub from shell loops. From the
board, an agent waiting ten minutes for CI looked the same as one that had stalled.

**Suggests:** **waiting with a reason and a link (new)**: presence `waiting` (already
planned for permission prompts) with a reason such as "release run 37439104751" and
its address. A person sees why nothing moves; the quiet-agent signals (section 7
there) flag a wait that runs too long.

### 7. Subagents were invisible to everyone but the lead

The lead's subagents built three pull requests. The reviewer never saw them; every
report and every review finding went through the lead, which paraphrased both ways.
The lead became the bottleneck and the only one who knew which subagent did what.

**Suggests:** subagent seats (D165, already planned): a subagent joins as a child of
its parent's seat, posts its own work and hears its reviewer directly, and is removed
when it finishes (slice 5c). The lead steers instead of relaying.

### 8. Context compaction lost the thread

After its context was compacted, the lead rebuilt the state from a summary and the
transcript. The record had every fact, but not in a shape an agent could pick up
quickly.

**Suggests:** "since you last looked" works for agents too: open asks it is waiting
on, its open tasks, reviews requested of it, and messages addressed to it, from its
read position. `aboard ask --open` (section 1 there) is one part of this.

### 9. Everything woke everyone

Announcements to the whole board reached every agent alongside real requests. Focused
delivery (D173) cut the noise, but a message still can't say "for the record" apart
from "act on this".

**Suggests:** asks already wake their recipient. Beyond that, the lead's habit was to
address requests by name and post the rest as plain messages; the skill should teach
it, and a quiet message that never wakes anyone may be worth a flag. Try the
convention first.

## The pattern

Each problem is a convention carried out in chat that should be a small typed thing on
the board: a task, an ask, a review, a claim, a wait. Chat stays the conversation
around them. People stay in the loop by looking at those things (blocking, idle,
stale, waiting, needs you) rather than by reading the log. The spine in
scale-and-decisions.md holds: of the new items, the review record and the claim are
fields and rules on existing nouns (asks, tasks), and the idle signal and the wait
reason are presence.

## What this changes in the suggested order

Nothing is approved by this note. If the maintainer agrees, the order in
scale-and-decisions.md gains:

1. Asks get an external link and can be closed by a bridge (with slice D).
2. Tasks come before files: owners, claims with a lease, and the idle signal answer
   problems 1 and 5, which cost the most time here.
3. Review records follow asks, since they share the "version seen" rule.
4. Presence gets a wait reason with its link, alongside the quiet-agent signals.
5. Subagent seats (D165) move up once agent removal (5c) lands.
