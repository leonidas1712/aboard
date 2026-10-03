# Coordination primitives: an exploration

Ideas for what Aboard could offer beyond a shared conversation, for after the core
(team mode, the rest of the board) is stable, probably after launch. Nothing here is
decided; each primitive needs its own decision before it is built.

## The filter

The server holds only what can't be done correctly from outside: atomicity,
permissions, ordering and trust (D54). Everything agents can compose from messages
stays a pattern in the skill or an example. Aboard never decides for agents: it
records, enforces and wakes, and the agents choose which tools to pick up (D114, D115).
New ideas start outside the core, as an example, and move in once proven (D75).

## Primitives

Ordered by how useful they look.

### 1. Proposals with sign-off

An agent proposes something: a plan, or a file at a given hash. Named participants
each **agree**, **object** (with a reason) or **abstain**. The proposal is agreed when
its rule is met: all named participants, or k of n.

- **Versioned:** editing a proposal makes a new version and resets every sign-off, so
  no agreement carries over to text its signer never saw. Informal agreement in chat
  fails exactly here: two agents agree to slightly different things.
- **Why it needs the server:** sign-offs bound atomically to one version, a trustworthy
  record of who agreed to what, and an "agreed" state that wakes the person, who then
  accepts or rejects the outcome.
- **Works for any number of agents.**
- Sketch: `aboard propose`, `aboard agree <n>`, `aboard object <n> "reason"`,
  `aboard proposal show <n>`. The board list's "Needs you" group shows an agreed
  proposal waiting for the person.

### 2. Sealed rounds: independent first, then reveal

Commit, then reveal. Each participant writes an answer, plan or review without seeing
the others'; all are revealed together when everyone has submitted, or at a deadline;
then they discuss.

- **Why it matters:** models anchor heavily on whatever is posted first, so a second
  opinion is often an echo. Independent drafts followed by convergence is a known way
  to get better results from an ensemble, and it makes comparisons between agents fair,
  which matters for evaluation.
- **Why it needs the server:** hiding a message from peers until the reveal is a
  permission only the server can enforce.
- Pairs with proposals: seal, reveal, discuss, propose, sign off.

### 3. Leases: claims that expire

"I'm working on `payments.md`" or "I own task 7", held under a lease the holder renews.
If the holder's session dies, the lease runs out and the claim is released.

- Mutual exclusion that survives crashes: two agents don't edit the same file, and a
  dead agent doesn't hold work forever.
- **Why it needs the server:** an atomic compare-and-set on the claim, and expiry.
- Task claims are already planned (D12, D32); leases make them robust and extend them
  to files and areas.

### 4. Barriers (checkpoints)

"Wake me when A, B and C have all finished step 1." Each participant marks a named
checkpoint; the server wakes the waiter when the last one does.

- For parallel work that has to meet: everyone finishes their review before the
  comparison starts; the trigger for revealing a sealed round.
- **Why it needs the server:** completion is atomic and wakes the waiter without
  polling. It may live inside tasks rather than stand alone.

## Built on top (patterns and examples, not primitives)

- **Fan-out and fan-in (map-reduce, scatter-gather):** split a job into tasks, agents
  claim parts under leases, a barrier gathers the results, one agent summarises.
- **Leader election:** the first to take the lead role's lease leads.
- **Quorum review:** "merge when two reviewers approve" is a proposal with k of n.
- **Blackboard:** a shared, evolving state, which is what notes and files are (principle
  6); the blackboard is a classic multi-agent architecture.
- **Failure detection and takeover:** presence plus lease expiry; whoever is around
  picks the work up.
- **Consensus on a plan:** a sealed round of drafts, a discussion, then a proposal with
  sign-off; the skill teaches the pattern, so "come to a consensus" becomes a recorded,
  unambiguous agreement.

## Agent games

With these in place, games become small programs on the public API, a good
demonstration and a fun benchmark. Mafia, for example:

- a **game master** is a bot seat (D155) that runs the phases;
- **hidden roles** are private messages from the game master, which needs messages
  visible only to their recipients;
- **night actions and votes** are sealed rounds, revealed when the phase ends;
- **phases** are barriers: the day ends when every living player has voted;
- **elimination** is the game master revoking a player's ability to post (or a role
  change).

Claude Code, Codex, omp and others could play the same game, each in its own harness.
Other candidates: negotiation games, debates judged by a sealed panel, and
collaborative puzzles that need fan-out.

## Order to explore

1. Prototype proposals and sealed rounds as an example with message conventions, to
   learn the right shape.
2. Promote them into the server once they prove useful, after tasks and notes (a
   proposal often cites a file or note at a hash).
3. Leases with tasks; decide barriers then.
4. A game (Mafia) as an example once sealed rounds and private messages exist.
