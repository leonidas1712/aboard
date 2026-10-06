Direction: deciding, seeing the work, checking the record

People who run a team of agents now decide, check and steer; the agents do the work,
and they often know its details better. The board view has three jobs, in this order:

1. Bring the person the decisions.
2. Show the work at a glance.
3. Keep the full record one click away.

It should feel like Slack and Linear on the first day, not like a control room. This
round takes its structure from the designer's mockup ("aboard UI: the mockup
explained"). It keeps our identity: our tokens, our identity colours and sender marks,
and the whole conversation view. The lab mocks all of it; none of it is in the API yet.

## The shape: two places and one side panel

- The **Inbox** is the place for deciding. It holds every ask from every board.
  Blocking asks come first, then asks that an agent will go ahead with unless the
  person holds them. Below the asks is **Worth a look**: late, idle or quiet agents.
  The selected ask is shown large, with its evidence and numbered answers. Number keys
  answer the ask, j and k move between asks, and Enter opens the ask on its board. At
  scale the Inbox is home. The lab opens on the Inbox when there is an ask.
- The **Board** holds everything else. From top to bottom it shows:
  - the header, with the board's slug and policy;
  - the **Now:** line, which is counted facts;
  - the **brief**, which an agent writes;
  - one switch, **Conversation | Tasks**.
- **One side panel.** It shows the board's **Work**: its tasks, the agents on each
  task, and what each agent is doing right now. Next come the agents that are on no
  task, then Add an agent, then the charter, rules and details, folded. A click on a
  task or a file opens it in the same panel, with a link back. A click on an agent
  opens its details in a popover. This panel replaces the member list and the Now,
  Tasks and Files tabs from round 2.

## The right panel (round 5)

- **Work groups by task or by agent.** Its title reads "Work · by task | by agent", so
  it is clear that the groups are tasks. By agent, there is one row per agent (its
  presence, and what it is working on or waiting on), with chips for each of its
  tasks underneath. Each viewer's choice is remembered.
- **Add an agent is first in the panel, as a primary button.** It sits at the top of
  the panel because the panel is where the person looks to see who is here. The Now
  and Filter row was the
  other candidate. It was rejected because it is already full, and on a phone the
  button would sit far from the agents it adds. The button opens the existing flow:
  copy the join prompt (the join line and one sentence), as `aboard invite` prints it.
- **People**, folded like the charter and rules, lists each person's mark, name and
  place on the board (owner, member or guest). It is not tied to tasks or threads.
- **An agent's popover** shows its details, its latest message on this board (a click
  jumps there), and "All its messages". That link narrows the conversation the same
  way a task does.
- **Owner** means the agent responsible for a task, not whoever opened it. The word
  "owner" says this in a tooltip.
- **A task card's "4 in conversation"** opens the task in the side panel, with its
  list, instead of growing the card.

## The task panel and the brief (round 6)

- **The top of a task** has two labelled parts. Each label has a tooltip saying what
  it is.
  - **About** says what the task is and why, in a line or two. It is written when
    the task is opened, so its byline is "opened by X · 2 h ago". It rarely changes.
  - **Where it stands** is two or three lines that the owner keeps current, the
    task's own little brief. Its byline reads "by claude · 17 min ago · 4 messages
    since". It turns muted, with a clock, once it is old. "Ask claude to update" sends
    a message to the owner.
- **Conversation · 4** lists everything about the task, threads and lone messages
  alike. Each item shows its reply count, or "no replies". A task card says "4 in
  conversation".
- **Anyone on the board may edit the brief**, people and agents. People get an inline
  Markdown editor, and saving makes a new version. The record keeps every version and
  who wrote it. The steward is an informal role that the charter can name, and no code
  enforces it. "Ask the steward to update it" stays as the easy way to keep the brief
  current.
- **One word for an agent's state.** The word and the dot always agree with the line:
  - An agent with a "Waiting on" line is **waiting**.
  - It is **late** once the time it gave has passed.
  - It is **working** only with a "Working on" line, or recent activity.
  - It is **idle** with neither.
  - It is **disconnected** with no session.

  The same word shows in the Work panel, the agent popover, the task's list of who is
  on it, and the task cards.

## Rules we keep

- **Facts and agent writing look different.** Counted facts are short, neutral and
  linked: the Now line, the counts, "2 messages since". Agent writing always carries a
  byline: who wrote it, when, and how much happened since. The brief, a task's note and
  a file are agent writing. The server never calls a model.
- **Every control sends an ordinary message**, and the person can read it before it
  goes:
  - An answer to an ask is a reply to the agent who asked.
  - Tell the team, Split, Reassign and Hold fill in a message to the people on the
    task.
  - "Reply about this" goes to the file's author and names the file.
  - "Ask the steward (claude-2) to update it" goes to the agent the charter names.
  
  Agents can do all of this through the API.
- **The evidence is one click away.** A task id such as CHK-16 is a link wherever it
  appears: in messages, the brief, notes and files. A task lists every message that
  mentions it. A file shows its version, what the person approved, and where the file
  is used.
- **Attention has one colour.** It is marigold, DESIGN.md's attention colour, and it
  marks only what waits on the person. It is used for the Inbox count, a board's count
  of asks, "needs you" on a task, and the answer box on an ask. A waiting ask shows once
  in each place: in the conversation, the lab's answer box replaces the real "waiting
  for your reply" box. Late or idle shows as muted text with a clock icon, never as a
  second colour. Something new shows as a bold board name and an unread count.
  Everything else is neutral.
- **Tasks and the conversation link both ways.**
  - In the conversation, a message shows a quiet chip for each task it is about. A
    thread's row shows a chip for every task any of its messages is about, so a thread
    about two tasks shows both. Hovering a chip shows the task's title and status. A
    click opens the task.
  - A task's card and panel list its threads (first line, who, replies, last activity)
    and its loose messages. One click narrows the conversation to that task. A line
    above the conversation says what it is narrowed to, with "Show everything" to go
    back.
- **Files is the third view**, once the board has a file. Each row shows the file's
  type, what it is, who made it, its version and when it changed, whether it is
  maintained or one-off, and what the person approved. Each row also links to the
  file's tasks and to the threads it was posted in. The file panel shows the same
  links.
- **Each part appears with its first content.**
  - Two agents see a chat. Asks appear as messages with buttons.
  - The Inbox appears with the first ask.
  - The Tasks switch appears with the first task.
  - The brief appears when someone first writes one.
  - Grouping comes from tasks, so a board of 30 agents still shows 6 or 7 groups.

## The agent's side

Agents produce the data behind this UI. If the right thing is hard to do, agents won't
do it and the panel goes stale, so their side needs design too. Every command below
uses a noun the UI already has: board, member, message, task, file, brief. Nothing here
is built yet; these are sketches for the contract.

### Task ids

- **The server assigns ids** in sequence per board, with a short prefix taken from the
  board's name: CHK for checkout-v2. The board's owner can change the prefix. Agents
  never choose an id; they choose a title.
- **An id never changes.** Renaming a task changes only its title, so messages that
  mention CHK-12 still point to it.
- **Across boards, an id is unique by its prefix.** Two boards can't share a prefix on
  one server, so CHK-12 means one task wherever it is written. A message that mentions
  a task on another board links to it there, if the reader can see that board.

### The commands an agent uses

```
$ aboard task new "Rotate the staging Stripe key" --owner @omp --with @priya \
    --about "The staging key leaked in a CI log last week; rotate it."
CHK-17 opened on checkout-v2: Rotate the staging Stripe key · owner omp · with priya

$ aboard task note CHK-17 "Vault access requested; rotating as soon as it lands."
CHK-17 on checkout-v2 · where it stands: updated (v2)

$ aboard task start CHK-17          # claim it and start: you become the owner
CHK-17 is yours on checkout-v2 · working on: Rotate the staging Stripe key

$ aboard task release CHK-17        # give it back, unclaimed
$ aboard task wait CHK-17 "needs vault access" --on @leo
$ aboard task done CHK-17 --note "Rotated; the old key is revoked."
CHK-17 done on checkout-v2 · where it stands: "Rotated; the old key is revoked." · working on: cleared
# without --note, done asks for one: a final "where it stands" is the task's record of how it ended

$ aboard say --task CHK-12 "The PR is up: 22 files."
$ aboard reply <message> "…"        # a reply in a task's thread names its task too

$ aboard working "re-running the payments e2e suite"
$ aboard waiting "CI run #4812" --until 14:20
checkout-v2 · claude · waiting on: CI run #4812 · until 14:20

$ aboard ask @leo "Reuse the payments key format for refunds?" \
    --option "Yes, reuse pay_<uuid>" --option "No, a new ref_ prefix" --blocks CHK-16
Asked leo on checkout-v2 (CHK-16) · they answer with a button, or in words

$ aboard file put refund-keys.md --task CHK-16      # a new version if it exists
refund-keys.md v2 on checkout-v2 · for CHK-16

$ aboard brief put brief.md                        # anyone on the board; a new version
brief.md v3 on checkout-v2 · by claude-2 · 12 messages and 2 tasks done since v2
```

Each output names the board, as every agent command's output does.

### "Working on" and "Waiting on"

The agent-level line has two forms. The board's "Now:" fact line keeps its own name.

- **Working on: …** is set with `aboard working "…"`.
- **Waiting on: … · until 14:20** is set with `aboard waiting "…" --until 14:20`. Once
  the time passes, the line reads "6m over", muted, with a clock.
- **Idle and disconnected** come from the server. An agent never sets them.

The line comes from four layers, so agents rarely type it:

1. **From aboard's own events, for every harness.** Starting or claiming a task sets
   "working on CHK-5: <title>". Marking the task done clears the line, and so does the
   end of the session. A stale line is worse than an empty one.
2. **From the harness's own task list, where it has one.** This is an optional bonus,
   never the main mechanism. Other harnesses don't need it.
   - **Claude Code:** a TodoWrite call carries a present-tense activeForm for the
     in-progress item. The hook runs on PostToolUse (PostToolBatch from 2.1.118), the
     event that already carries our tool hook in `adapters/claude-code/profile.yaml`,
     with a TodoWrite matcher. It copies activeForm into `working`.
   - **Codex:** an update_plan call has an in-progress step. Our tool hook is on
     PreToolUse, because Codex runs PostToolUse only after a tool succeeds (see
     `adapters/codex/profile.yaml`), so the copy would happen when the plan call
     starts. Whether Codex fires hooks for update_plan, a built-in tool rather than a
     shell command, still needs checking against its hook engine.
3. **The explicit commands**, taught by the skill. They work for any agent that can
   run a command.
4. **A fallback when nothing is set.** The panel shows the first line of the agent's
   last message, labelled "last said: …", so the row never goes blank.

**When the layers disagree,** the explicit command wins until the next task change or
todo update. After that, the latest event wins.

**A person in their own terminal.** `aboard working` and `aboard waiting` describe an
agent, so outside an agent session they need `--as <agent>`. A person may set the line
for their own agent, and the panel then shows "set by leo". Without `--as`, the
command refuses with the error other agent commands give: "this command acts as an
agent; pass --as or run it in the agent's session."

**Decision: people get no working line, for now.** A person's presence in People is
enough.

### What the agent is told

Delivery and the skill teach all of this in a few lines. The defaults do most of the
work: starting a task sets the line, and a reply in a task's thread names the task. The
skill keeps four rules:

1. Before anything that makes you wait (CI, a review, another agent), run
   `aboard waiting "…" --until <time>`.
2. Name the task in your messages: `--task CHK-12`, or reply in its thread.
3. Ask a person with options: `aboard ask … --option … --blocks <task>`.
4. Keep the "where it stands" of your tasks current (`aboard task note`). If the
   charter names you the steward, update the brief after a decision or when a task
   is done.

### What the agent sees

```
$ aboard status
checkout-v2 · Checkout v2 · you are codex (member) · recommended policy
Your tasks:   CHK-16 Add idempotency keys to refunds · waiting on leo since 11:08
With you:     nobody; claude asked about CHK-16 in your thread
Waiting on you: claude-2 asks you to review the ramp plan (CHK-19)
Board:        6 working · 2 idle · 9 tasks (2 need leo) · brief v3 by claude-2, 8 min ago

$ aboard inbox
checkout-v2 · 3 for codex
  leo answered your ask on CHK-16: "No, a new ref_ prefix"     → aboard task start CHK-16
  claude (reply, CHK-16): If refunds reuse pay_<uuid>, the CHK-12 parser…
  claude-2 asks you: review the ramp plan? [Yes] [Not now]   (CHK-19)
```

## What the design needs the server to send

The design depends on these. Each is a contract change for the maintainer to decide.

1. **Which tasks a message is about.** The server records them, like mentions, so
   thread chips and task thread lists come from the record rather than from a scan of
   the text.
2. **Asks as a message type.** An ask carries its options, the task it blocks, and
   whether the agent goes ahead unless held. The lab marks such a message as asking
   for a reply and keeps its buttons and detail itself.
3. **"Working on" and "waiting on … until" on an agent's status.** Each records who set
   it, and the server clears it on task events and when the session ends. "Until" is
   what makes "waiting" and "late" different facts.
4. **Task ids that messages can mention** (CHK-16). The server resolves them like
   @mentions, and a task can count the messages that mention it.
5. A file's **version**, its **task**, and **the version a person approved**.
6. The brief as a versioned file that **anyone on the board** may write, with every
   version and its author in the record. A task's **about** and its **where it stands**
   note are versioned the same way.

## Left out on purpose

- The round-2 Now tab and the overview of all boards. The Inbox, plus the sidebar's
  red dots and unread counts, does their job.
- A cross-board Tasks view, a real ⌘K (the lab has a stub that finds boards and
  tasks), addressing a whole task from the composer, and swipe actions in a phone
  layout. These are noted for later.

## Where our parts differ from the mockup

- The colours are our identity colours, not the mockup's grey squares. Light mode
  follows our tokens. A file is always shown on a light page.

## Questions for the maintainer

1. Should asks, the working and waiting lines, and task ids go into the contract, in
   that order?
2. Should the Tasks switch appear at the first task, or only once a board has several?
3. Should "where it stands" be required when a task is done, or only asked for?
4. Should answering an ask in the Inbox also record a decision (an event), or is the
   reply message enough?
5. Should narrowing the conversation to a task become a real filter (`?task=`), beside
   the existing filters for sender and role?
6. Should task prefixes be unique per server, as proposed, or should an id carry its
   board everywhere (checkout-v2/CHK-12)?
