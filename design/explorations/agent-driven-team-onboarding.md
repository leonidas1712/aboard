# Agent-driven team onboarding and administration

A design for #268, proposed for the release after 0.1.4. Its decision is D222 in
DECISIONS.md; this page is the detail. Nothing here is built yet.

## Why

Testing team mode end to end showed that every step works on its own, but people have
to stitch them together by hand: an admin runs `aboard invite --server` in a terminal,
sends a link, the colleague runs `aboard connect` in a terminal, picks a handle, someone
adds them to a board with `aboard board add @handle` (and gets the handle wrong after a
rename), then each person tells an agent to `aboard join --board`, and nobody knows
whether delivery actually works until they try. A failure partway leaves people unsure
what succeeded, and some delete their account to start over.

Most of that friction comes from one rule: commands that use the person's login refuse
to run inside an agent session (`human_command_in_session`, D85), and D114 lists
accepting an invite, logging in and managing people as person-only. Those rules guard
against an agent being talked into changing access. They are right about the risk and
wrong about the remedy: the person ends up copying commands their agent prepared.

## The flow we want

The colleague-facing guide in #268 is the target. In short:

1. **Inviter:** "Invite my colleague and make a pairing-test board." Their agent creates
   the board, one invite that carries the board membership and a pairing request, and
   gives them one link to send privately.
2. **Colleague:** pastes the link into their own agent: "Set up Aboard and join using
   this link." The agent installs Aboard if needed, redeems the invite (creating their
   account, or using their existing one), sets up the harness, accepts the pairing
   request in this session and joins the board. It asks only what it can't decide: the
   name teammates should see, or whether to use an existing account.
3. **Unavoidable human steps** are only the harness's own (trusting hooks, a restart),
   named precisely, after which "Continue Aboard setup" resumes.
4. **Both agents run a short handshake.** Only a real round trip between the two
   sessions reports "delivery verified"; an offline peer leaves it pending.
5. **Pairing again** with an existing teammate needs no invite: a pairing request to the
   person, accepted by whichever of their sessions they choose.

## 1. Agent admin: allowances and approvals

An agent may now ask the server to do admin work for its person. The server decides,
from the person's own permissions and their **allowance**, whether to do it now or to
hold it as an **approval** for the person.

- **Allowance** (auto mode): a person's standing, revocable list of admin actions their
  agents may take for them without asking. Off by default. Categories:
  - `invite-people`: create a server invite (only if the person may invite);
  - `add-people`: add a person to a board the person can add people to;
  - `create-boards`: create boards (agents can already do this through `pair` and D205).
  Never in an allowance: making someone an admin, changing roles, removing people,
  revoking keys, changing a board's policy. Those always need an approval.
- **Approval** (ask me): an admin request the allowance doesn't cover is held, not
  refused. The person sees it and allows it once, allows it and adds the category to
  their allowance ("always"), or declines. The agent is told it's pending and how the
  person approves it.
- **Checks on every write, in the one write path:** the agent is the person's own agent;
  the person holds the permission themselves, now (a member who can't invite can't
  delegate inviting); the allowance or the approval covers this exact action. The record
  shows both: who authorized (the person, with the allowance or approval id) and who
  acted (the agent).
- **What an allowance can't defend against:** an agent talked into an action by a board
  message can still use the person's allowance. That is why the allowance is opt-in, is
  limited to additive actions, and every use is recorded and shown. Destructive and
  privilege-raising actions always ask. A board message is never itself authority.

### Everything through the API and the CLI, not only the board view

People set allowances and decide approvals in the board view, from the CLI, or through
the API, so an agent can always tell its person exactly what to run:

```
aboard approvals                      # pending approvals (an agent sees its own requests)
aboard approvals allow <id> [--always]
aboard approvals decline <id>
aboard allowance                      # show
aboard allowance set invite-people on|off
```

`approvals allow|decline` and `allowance set` are the person's: they refuse inside a
session, as D85 commands do. An agent runs `aboard approvals --json`, and when something
of its own is pending, tells its person: "You have 1 pending approval: allow it with
`aboard approvals allow apr_…`, or in the board view's Inbox." Nudges and notifications
for approvals come later.

API (sketch): `GET /v1/me/allowance`, `PUT /v1/me/allowance` (person token);
`GET /v1/me/approvals`, `POST /v1/me/approvals/{id}/allow` (with `always`),
`POST /v1/me/approvals/{id}/decline` (person token); an agent's admin request returns
`202` with the approval when held, `200` or `201` when the allowance covered it.

## 2. One invite that carries the board

A server invite gains optional fields: the board (or boards) to join and the role there,
and a pairing request. Redeeming it is still one step on the server: consume the invite,
create or resolve the person, add the memberships, attach the pairing request. A
redeemed invite can't be redeemed again, but a retry after a lost response finds the
same result (see section 4).

Who may create one is unchanged (the person must be allowed to invite); with the
allowance, their agent may too.

## 3. Pairing requests

A first-class resource addressed to a **person**, not a session:

- fields: id, server, board, inviter and initiating agent, recipient person (or the
  invite it rides on until redeemed), selected recipient session, proposed work, created
  and expiry times;
- states: awaiting account, awaiting a session, awaiting the other endpoint, verifying,
  ready, declined, cancelled, expired;
- the recipient picks the session: by accepting it in a session ("accept it here"), by
  **Choose an agent** in their Inbox, or by **Copy prompt** in their Inbox and pasting it
  into any session. Activity alone never picks a session;
- acceptance is atomic and idempotent: a second session accepting gets the current
  state or a clear conflict, and "use this session instead" is an explicit replacement;
- accepting authorizes participation in the stated work only. It gives the inviter no
  control over the recipient's agent, and the proposed-work text is content, not orders.

CLI: `aboard pairing request @person --board B "proposed work"` (inviter side),
`aboard pairing` (list), `aboard pairing accept [<id>] --here`, `aboard pairing decline`.

**Pairing with your own next session** is the same request addressed to yourself
(`aboard pairing request me …`). In one session: "make a board for the auth review and a
pairing request for my next session"; in a new session (another harness, a cloud
session, another machine): "find my pairing request and accept it". The new session
lists your open requests, asks which if there are several, accepts with `--here`, joins
the board, runs the handshake and starts on the proposed work. No allowance is involved:
both sessions act for the same person on their own board. Today the second session can
already `aboard join --board` (D172), but has to be told the board, and nothing checks
the two sessions reach each other or hands over what to do.

## 4. Setup that resumes

`aboard setup <invite-link | pairing request>` (and "Continue Aboard setup") runs the
steps in order, records each on this machine as it completes, and on a rerun starts at
the first incomplete one:

1. Aboard installed and writable without sudo (check ownership first; report the exact
   path and a scoped fix otherwise);
2. account connected and its key saved (new account from the invite, or the existing
   account through new-machine approval, D188);
3. board memberships from the invite present;
4. harness set up (`aboard init` for the current harness), with any trust or restart
   step named precisely;
5. the pairing request accepted in this session, and its seat joined;
6. delivery verified by the handshake.

Each step is safe to retry: redemption is keyed by the invite so a lost response doesn't
create a second account, memberships and seats are reused when present, and a failed
key save is recovered through the existing account rather than by deleting it. The
report always says which of the six steps are done.

The agent may run setup and redeem the invite itself: pasting a one-use invite meant for
you into your own agent is your consent. The key is written to Aboard's own state file;
the agent never sees it, and setup's output never prints it.

## 5. The handshake

When both endpoints of a pairing request are in sessions, each side sends a short ping
carrying a correlation id, and expects the other side's reply carrying it back, through
real delivery into the real sessions. Only both round trips complete mark the request
**ready**. An offline peer leaves it **verifying**, saying which side is awaited; a
timeout is a recoverable failure with the next step named.

## Changes to existing decisions

- **D85**: `connect` with an invite, and `setup`, may run inside a session (the invite is
  the consent). Admin commands may run in a session when an allowance or an approval
  covers them. Approving, setting allowances, logging in with a pasted key, managing
  keys and `servers use` stay person-only.
- **D114**: the person-only list shrinks to: logging in with a key, managing keys,
  setting allowances, deciding approvals, and the destructive or privilege-raising
  actions, which an agent may request but only a person may approve.
- **D181** ("an agent never manages people"): an agent may manage people within its
  person's permissions and allowance, or with an approval.

## Slices

1. **Contracts:** allowance, approvals, invite fields, pairing requests, setup states and
   the handshake in openapi.yaml, cli.yaml and delivery.md.
2. **Allowance and approvals** end to end: an agent creates a server invite under the
   allowance, or as an approval the person allows from the CLI; board view Inbox cards.
3. **Invite carries the board;** `setup` for a brand-new colleague through steps 1 to 4.
4. **Pairing requests** and acceptance in a session; Inbox Choose an agent and Copy
   prompt.
5. **The handshake** and the ready state, with live tests for Claude Code and Codex.
6. **Recovery:** existing accounts, another machine, interrupted setup at each step,
   failed key saves, without sudo.
7. **The colleague guide** in docs, tested end to end.

## Open questions

- Should `create-boards` be in the allowance at all, given agents can already create
  boards through `pair` and D205?
- Do approvals expire, and after how long?
- Should a person be able to give an allowance to one agent rather than all of theirs?
- Where do approvals live for a person on several servers: per server (simplest)?
