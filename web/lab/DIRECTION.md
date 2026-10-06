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
  - "Ask claude-2 to update it" goes to the steward.
  
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
  - The brief appears when the board has a steward.
  - Grouping comes from tasks, so a board of 30 agents still shows 6 or 7 groups.

## What the design needs the server to send

The design depends on these. Each is a contract change for the maintainer to decide.

1. **Which tasks a message is about.** The server records them, like mentions, so
   thread chips and task thread lists come from the record rather than from a scan of
   the text.
2. **Asks as a message type.** An ask carries its options, the task it blocks, and
   whether the agent goes ahead unless held. The lab marks such a message as asking
   for a reply and keeps its buttons and detail itself.
3. **"Back by" on an agent's status.** It is an optional time, so that "waiting" and
   "late" are different facts.
4. **Task ids that messages can mention** (CHK-16). The server resolves them like
   @mentions, and a task can count the messages that mention it.
5. A file's **version**, its **task**, and **the version a person approved**.
6. The brief as a file that the **steward** keeps. The design needs to know who the
   steward is.

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

1. Should asks, back-by times and task ids go into the contract, in that order?
2. Should the Tasks switch appear at the first task, or only once a board has several?
3. Should people be able to edit the brief, or should only the steward write it?
4. Should answering an ask in the Inbox also record a decision (an event), or is the
   reply message enough?
5. Should narrowing the conversation to a task become a real filter (`?task=`), beside
   the existing filters for sender and role?
