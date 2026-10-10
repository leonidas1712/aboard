# The UI lab

The lab is the real web UI (the board list and the board view) running against an
in-memory fake of the public API and its event stream, with named scenarios and a few
experimental views. You use it to look at proposed features, and change them, before
the API has them. It needs no aboard server.

```sh
make lab          # http://localhost:3100/?lab=team (LAB_PORT to change)
make lab-export   # web/lab-out/ (static files) and web/lab-out/aboard-ui-lab.html (one page)
make lab-shots    # every scenario at desktop and phone widths, in web/lab/screenshots/
```

`aboard-ui-lab.html` holds everything it needs. Open it from disk, or put it on any host.

A floating panel at the bottom left picks the scenario and its time step. **Play** moves
through the steps. A later step happens live: new messages arrive on the stream and
presence changes. An earlier step reloads the page. The address keeps the moment:
`?lab=<scenario>&step=<n>`, then `inbox=1` for the Inbox, `board=<name>` for a board,
or `list=1` for the list of boards. On a board, `view=tasks` or `view=files` opens that view, `task=<id>`
opens a task in the side panel, and `artifact=<id>` opens a file there. Add
`panel=closed` to fold the lab panel. With no place named, the lab opens on the Inbox
when there is an ask, and on the scenario's board when there isn't.

## How it stays out of the real UI

The lab enters the app through one module name, `aboard-lab` (see `app/lab-seam.ts`).
A normal build resolves this name to `app/lab-off.ts`, which exports `lab = null`. Only
`ABOARD_LAB=1` (the npm scripts `lab` and `lab:export`) resolves it to `lab/entry.tsx`
(see `next.config.mjs`). This means no lab code is in the module graph of `web/out`,
which the binary embeds. The real UI's stylesheet does not scan `web/lab`, and the lab
builds its own styles in `lab.css`. `make web-lab-check`, part of `make web-check`,
searches `web/out` for the lab's marker and its words, and fails if it finds them.

The fake (`fake-api.ts`) replaces `window.fetch` for `/v1/` paths. Because of this,
`app/api.ts` and every component run unchanged. It hashes its events like the server,
so the record check passes. It fakes only what the UI calls. When the UI starts to call
a new endpoint, add it there to match `spec/openapi.yaml`.

## Add a scenario

1. Copy `scenarios/solo.ts` to `scenarios/<id>.ts`, and change it. A scenario is plain
   data (the types are in `scenario.ts`): the people (`me` is the viewer), the agents,
   the board, and `steps`. Each step changes only what it names: presence, working and waiting lines,
   tasks (by id), the brief and new messages. Times are minutes from the start.
2. Add it to the list in `scenarios/index.ts`.
3. Add a line for it to `shots.spec.ts`, so `make lab-shots` takes its picture.

## Add a mocked feature

1. Put the data that the feature needs in the scenario types (`scenario.ts`). Fold the
   data in `snapshot()`, if the steps change it.
2. Write its component in `experiments/`. Start the file with an `EXPERIMENTAL, lab
   only` comment that says what the feature does. Read the data with `useLab()`, and
   never from the API.
3. If the component must appear in a place that has no slot yet, add an optional slot
   to `Lab` in `app/lab-seam.ts` and one `lab?.Slot` line where the real UI draws it.
   Then fill the slot in `entry.tsx`. A slot is a type and one optional call. Real code
   never imports from `web/lab`.

When a feature goes into the product, its contract goes into `spec/` first. Then the
real component replaces the experimental one, and its slot goes away.

## The mocked features now

[DIRECTION.md](DIRECTION.md) explains why these features exist and what they need from
the server.

- **Inbox** (`experiments/inbox.tsx`, `asks.ts`): every ask from every board, and what is
  worth a look. Number keys answer the selected ask.
- **Sidebar** (`experiments/nav.tsx`): the Inbox count, each board's red dot and unread
  count, and a stub of ⌘K.
- **Brief** (`experiments/brief.tsx`): the steward's writing, with a byline. Open, it
  shows Goal, Approach, Who's doing what, Blocked on, Next and Sources.
- **Tasks and threads** (`experiments/chips.tsx`, `links.ts`): task chips on messages
  and thread rows, task thread lists, and narrowing the conversation to one task
  (`filter=<id>` in the address).
- **Files** (`experiments/files.tsx`): every file, with its context.
- **Conversation | Tasks | Files** (`experiments/centre.tsx`, `tasks.tsx`). The columns are
  Needs you, In progress, Waiting and Not picked up. Free agents are listed beside
  them. Done folds away.
- **Side panel** (`experiments/work.tsx`, `artifacts.tsx`): the board's Work, a task
  (its note, open question, files, people, Tell the team and mentions), or a file on
  a light page.
- **In the conversation** (`experiments/message-footer.tsx`, `text.tsx`): an ask has
  buttons, a file shows as a card, and task ids are links.
- **Agent-driven onboarding** (`experiments/onboarding/`, `onboarding.ts`, the
  `onboard-inviter` and `onboard-joiner` scenarios): approvals in the Inbox (Allow once,
  Allow always, Decline), the allowance in Settings (`settings=1`, reached from the
  account menu), notices about invites an agent made, pairing requests with Choose an
  agent and Copy prompt, the board's pairing line, the invite page (`join=1`), and how
  the record reads a join an agent did for its person. The data follows the API's
  shapes in `spec/openapi.yaml`; `item=<id>` opens one Inbox item, and
  `allow=add-people,invite-people` starts with that allowance.
