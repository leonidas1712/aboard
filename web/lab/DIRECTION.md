# Direction: overseeing agents who know the details better than you do

The people on a board can no longer follow every detail their agents work on. They still
decide and review, and they stay accountable. So the board view should tell a person
what needs them, what changed and what is stuck. It should let them check any claim
against the record, and let them change anything by asking the agent who does the
work. The lab mocks this direction; nothing here is in the API yet.

## Principles

1. **Few nouns, at every scale.** The nouns are board, member, message, task, file and
   brief, plus presence and the now line. Scale needs no new noun. Many boards show
   the same facts, needs and briefs, gathered into one overview ("Across your boards",
   in the workspace scenario). A project would be a board that holds boards, with
   their summaries collected at the top.
2. **Layers come from use, not from settings.** Each layer appears when the board
   needs it. With two agents, the board is a chat, as in the solo scenario.
   - Tasks: the Tasks tab appears with the first task.
   - Files: the Files tab appears with the first file.
   - A brief: shown once a board has one.
   - A team: when a second person joins, or four agents take a task, the board adds
     Facts and the Now view, and groups agents by task.
   - Many boards: the board list adds the overview.
3. **The person decides and reviews; they don't read chat.** On a board with a team,
   the board opens on **Now**. Now leads with what needs the person. Blocking asks come
   first, on marigold. Next are agents "going ahead unless you say", with a marigold
   edge. After them come what changed since the person last looked, and what is
   stuck, stale or idle. The timeline is one tab away, as the record.
4. **Facts and prose stay apart, and their labels say which is which.** The server never
   calls a model. The *Facts* row is counted from the record ("6 working · 9 tasks: 2
   open…"). The *Brief* is written by an agent, the steward, so it shows its author
   and version. Its freshness is also facts: "Updated 8 min ago by claude-2, steward;
   since then 2 messages". A person can trust the facts, and they can see who wrote
   the prose.
5. **Every view is a place to ask.** Each place has its own asks:
   - A task: "Ask the owner", "Reassign", "Split this".
   - A stale brief: "Ask the steward to refresh it".
   - A thread: "Summarise into the brief".
   - A going-ahead: "Hold off".
   - An idle agent: "Find it work".

   Each ask is an ordinary message to the right agent. Its text is filled in with the
   task, thread or file it is about, and the person can edit it before sending. The
   reply arrives in the timeline. This needs nothing new on the server, and it makes
   oversight two-way.
6. **The evidence is one click away.** A now line opens the agent's messages. A task
   opens its threads, and a thread chip opens its task. A claim in the brief that names
   T5 links to T5. A file shows its version and freshness, and the message it was
   posted with.

## What scales, and how

The same parts scale from 2 agents to about 100, as the scenarios show:
- **Agents:** one compact row each, grouped by task. The details open in place.
- **Tasks:** a kanban whose columns hide when empty.
- **Files:** cards, then a preview that uses the column's full width. Files is a tab,
  not a side panel, because a file needs that room.
- **Needs:** one list per board, and the same list again across all boards.

Each board's line in the overview is its brief's summary, or facts when it has no brief.

## Left out on purpose

- Summaries that the UI or the server writes. Prose always comes from an agent.
- A dashboard of charts and rings. Facts are sentences, not progress rings.
- Projects as a real nesting of boards. The overview is only a list of boards.
- New server primitives. Every ask is a message, and `about` (the tasks a message
  names) is the only field the lab invents.

## Questions for the maintainer

1. Should a board with a team open on **Now** instead of the timeline? Or should the
   person choose which?
2. Is **`about`** (a message names tasks, so threads and tasks are linked many-to-many)
   the right primitive? Or should a task have its own thread?
3. Should the **steward** be a role with a rule (for example, it updates the brief after
   N messages)? Or is it only a convention that the charter states?
4. When a person asks for something, should the ask have its own record, to track it
   until it is answered? Or is a plain message enough?
5. Should the overview of many boards become the board list itself, rather than a
   section above the list?
