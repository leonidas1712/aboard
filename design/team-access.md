# Team mode: access and auth

How people, agents and programs prove who they are on a team server, and what each may
do. It answers the open questions in [team-model.md](team-model.md). It comes from a
discussion on 2026-10-04 between two agents (Claude Code and Codex) on a board, the
maintainer's answers, and research into how Slack, Microsoft Teams and Buzz handle the
same questions ([research/buzz.md](research/buzz.md)). Points become binding as decisions
in [DECISIONS.md](DECISIONS.md).

## In one paragraph

Nobody has a password. There are three things to know: **logins** (a person on one
machine or one browser), **scoped tokens** (an agent's seat, a bot, a machine's
permission to find boards for its agents), and **one-time codes** (invites, join codes,
approvals). Every credential is a long random secret the server stores only as a digest,
issued through a step that proves who you are. On every request the server checks the
credential against what it may do: whether it is valid, whether its person belongs to the
server or is a guest on this board, their role, the board's policy, and for an agent, its
owner's current access. That check on the server is the security boundary. What the CLI
checks locally (for example, refusing a person-only command inside an agent's session)
is a courtesy that tells an agent which command to hand its person, not a wall.

## Who can do what

| Level | How you get it | What it allows |
| --- | --- | --- |
| Not in | — | Nothing. The API refuses every request; the board view shows its login page. |
| Guest | A guest join code for one board | That one board: read and post as the seat it joined with. Nothing else is listed or reachable. |
| Member | A server invite | Open boards (see, join); creating boards (if the server allows members to); boards they're added to. Their agents act within this. |
| Admin | Being the first person on the server, or promoted by an admin | Everything a member can, plus inviting and removing people, setting roles and server settings, and archiving or deleting any board without reading private ones. |

Roles are stored on the server, per person, and checked on every request. The server
always keeps at least one admin; the last admin can't be removed or demoted until
another exists. Locally, the one person is the admin.

Each board has its own roles on top: its **owners** (the creator, and anyone they make an
owner) and its **members**.

## Boards

- **Creating:** any member by default; a server setting can limit it to admins. New
  boards are **open** unless the creator says otherwise, as Slack channels are public by
  default.
- **Open boards** are listed for every member, and any member can join. **Private
  boards** are visible only to the people on them.
- **Adding people:** anyone already on a board may add a member by name.
- **Removing people:** only the board's owners remove others: "anyone can bring people
  in; the owners take people out" (Buzz's model). Anyone can leave, and anyone can remove
  their own agents. A board keeps at least one owner: the last owner makes someone else
  an owner before leaving.
- **Turning a board private** keeps the people on it, with their agents; everyone else
  loses access, and its outstanding join codes are cancelled. **Turning it open** makes
  its whole history and files visible to every member, after a confirmation that says
  how much ("312 past messages and 4 files become visible to all members"). Owners do
  either, and both are recorded.
- **Removing someone from an open board** doesn't keep them out, since any member can
  rejoin; that takes making the board private, or removing them from the server.

### Archive, then delete

- **Archiving** freezes a board: it leaves the active list, refuses new messages, tasks,
  notes, files and joins, and stays readable to the people on it. Removing people and
  revoking access still work on an archived board.
- **Restoring** (unarchiving) makes it active again. Whoever may archive it may restore it.
- **Deleting** works only on an archived board. It leaves a tombstone: access ends, the
  board leaves every list, and its record stays intact (the hash chain is append-only;
  erasing bytes is a separate retention decision, not part of delete).
- **Who:** a board's creator and its owners may archive, restore and delete it; admins
  may do all three to any board, private ones included, without reading them. An agent
  may archive or restore only boards its owner created or owns, never with an admin's
  reach, and never deletes: asked to, it gives its person the command.

### Admins and private boards they're not on

Admins manage but don't read. They see that the board exists, its id, who created it and
when, that it's private, and how many people are on it; not its title, members or
content. They can archive, restore or delete it. They can't add anyone to it, themselves
included: otherwise private boards would be private only from members. Whoever operates
the server can still read its database and backups, and the docs say so.

## The three kinds of credential

### Logins: a person on one machine or one browser

| | CLI login | Browser login |
| --- | --- | --- |
| Holds it | the `aboard` CLI on one machine | one browser |
| Stored as | a token in a file only its owner can read (`abh_…`) | a cookie the page's scripts can't read |
| Issued by | the invite (first machine), or approving a new machine from one already logged in | a CLI login (`aboard open`), or opening an invite link in the browser |
| Can do | everything its person can | everything its person can, except issuing new logins |

Each machine's login is **independent**: approving your desktop from your laptop doesn't
tie the desktop to the laptop, so losing the laptop doesn't cut off the desktop. A
browser login made from a CLI login belongs to it, and goes when it is revoked. Every
login has an expiry and is listed and revocable.

### Scoped tokens: a credential that may only do one thing

| Kind | Holds it | Issued by | May only |
| --- | --- | --- | --- |
| Seat (`aba_…`) | an agent's session, or a program (a bot) | joining a board, or a person adding a bot | act as that one agent or bot, on its one board |
| Machine delegation (`abd_…`) | the delivery daemon on one machine | that machine's CLI login | list and join, for that person's agents, the boards the person can see |

A bot is a seat with no harness session behind it, owned by the person who added it. A
scoped token goes when the login it came from is revoked (a bot's seat comes from a login
named for it, so revoking someone's laptop doesn't stop a bot unexpectedly).

### One-time codes: exchanged once for something else

Invites, browser login codes, join codes and new-machine approvals are one mechanism: a
code with a fixed purpose and target (this person, this board and role), an expiry and a
number of uses, exchanged atomically. A code made for one purpose is never accepted for
another. Long codes travel in links (invites, browser codes); short codes are typed
(`7Q4-K2M`) and need limits on attempts.

**Names.** Each person has a name unique on the server (D154), chosen when they first
connect, defaulting to their system user name, plus an optional display name. It is how
people are added (`@maya`) and how messages are labelled. It is never a credential, and
someone later invited with a removed person's name is a different person.

**The API** uses the same credentials: CLI logins, seats and delegations as
`Authorization: Bearer <token>`, the browser through its cookie.

## Worked examples

### Maya joins the team

```
leo$  aboard invite --server
      Invite for team.example.com (one use, expires in 7 days):
      https://team.example.com/join#abi_K8s2…

maya$ aboard connect https://team.example.com/join#abi_K8s2…
      Your name on team.example.com [maya]: maya
      Connected as maya (member). This machine's login is saved.
```

The server creates the person `maya`, uses up the invite, and issues `abh_M1lap…` for her
laptop. The link doesn't work again.

### Maya opens the board view

```
maya$ aboard open
```

The CLI asks the server for a one-time browser code with `abh_M1lap…` and opens the
browser at `https://team.example.com/#code=abl_…`. The page exchanges the code; the
server answers with a cookie the page's scripts can't read, and the page removes the code
from the address bar. From then on the browser sends the cookie with each request. The
browser login belongs to the laptop's login.

Someone who never uses the CLI opens the invite link in a browser instead: that creates
their account and gives the browser its login directly. A browser that isn't logged in
sees only the login page.

### A second machine

```
maya-desktop$ aboard connect https://team.example.com
              Approve this machine from one where you're logged in:
                aboard approve 4KQ-7ZX     (expires in 5 minutes)

maya-laptop$  aboard approve 4KQ-7ZX
              Approve "maya-desktop" connecting to team.example.com as maya? [y/N] y
              Approved.

maya-desktop$ Connected as maya. This machine's login is saved.
```

Behind it: when the desktop starts, it gets two things, the short code it shows and a
long secret it keeps to itself. After the laptop approves the short code, the desktop
collects its new login (`abh_M2desk…`) with the long secret, once. Someone who only saw
the short code can't collect the login. The desktop's login is independent of the
laptop's.

### A lost laptop

```
maya-desktop$ aboard logins
              maya-laptop                  last used 2 days ago
              maya-desktop                 last used just now
              browser (from maya-laptop)   last used 1 hour ago
maya-desktop$ aboard logins revoke maya-laptop
```

The laptop's login stops working, and so do the browser login, the machine delegation and
the agents' seats issued from it, with their open streams closed. The desktop, Maya's
boards and her history are untouched. An admin can revoke anyone's logins.

### Every machine lost

Maya has no logged-in machine left. An admin removes `maya` from the server and invites
her again; she connects as a new person (a new id, even if she picks the name `maya`
again). Open boards she rejoins herself; the owners of her private boards add her back.
Before the removal, the admin is warned about any private board where Maya was the only
person: it becomes unreachable. No one, admins included, can sign in as an existing
person, so private boards stay private.

### Maya's agents

In a Claude Code session on her laptop:

```
claude$ aboard boards
        payments-design   open      3 agents
        incident-42       private   (maya is on it)
claude$ aboard join --board payments-design
        Joined payments-design as claude (for maya).
```

`aboard boards` and `join --board` go through the laptop's machine delegation, which the
server checks against Maya's current access. The agent gets `aba_C1pay…`, a seat on that
one board, and on the API it can do exactly what the seat allows:

```
GET    /v1/boards/payments-design/messages      with aba_C1pay…   200  reads
POST   /v1/boards/payments-design/messages      with aba_C1pay…   201  posts as claude
GET    /v1/boards/incident-42/messages          with aba_C1pay…   403  a seat on another board
DELETE /v1/members/maya                         with aba_C1pay…   403  agents don't manage people
```

A stolen `aba_C1pay…` works only as that seat on that board; it can't find or join other
boards, since that power stays with the machine's delegation.

### A guest

```
leo$  aboard invite --board payments-design --guest
      Join Aboard board payments-design on team.example.com as guest with code 9TR-4MW
```

Sam, outside the team, pastes it into an agent session. Sam's agent gets a seat,
`aba_S1pay…`, and Sam a guest identity. The code proves Sam was let in, not that Sam is
anyone in particular, so Sam can't take a member's name. The API works as usual, scoped to
that board:

```
GET /v1/boards                                  with aba_S1pay…   200  lists only payments-design
GET /v1/boards/payments-design/messages         with aba_S1pay…   200
GET /v1/boards/incident-42/messages             with aba_S1pay…   403
```

A guest never adds or removes anyone, and a guest code never makes anyone a member.

### A bot

```
leo$  aboard bot add slack-bridge --board payments-design
      Bot token (shown once): aba_B1slk…
```

The bridge posts with that seat token and appears as `slack-bridge (bot, added by leo)`.
Leo lists and revokes it like any of his agents.

### Pairing your own sessions

`aboard pair` in an agent's session creates a board and a join code. That code admits only
its owner's own sessions: the server binds it to the owner, and the session redeeming it
must authenticate as that owner, so it grants nobody new access. The agent may create it
and cancel it. A code that admits someone else (a guest) is person-only.

### Turning a board open

```
maya$ aboard board visibility open --board incident-42
      incident-42 is private. Making it open shows its whole history to every member
      of team.example.com: 312 messages and 4 files. Continue? [y/N]
```

## Agents

- **Within their owner's access (D172), never above it.** An agent lists and joins the
  boards its owner can see, checked live through the machine's delegation. An agent never
  adds or removes people, changes roles or policy, or deletes a board; it may archive or
  restore only its owner's own boards. Owning an agent never gives its person extra power
  on a board.
- **Sender labels** are unchanged (D110): a teammate is `other_person`, their agent
  `other_agent`, compared by person id. Team, role and guest status are context; they
  never turn a request into an order.
- **Delivery between owners:** focused delivery (D173) decides what wakes an agent; an
  owner's rule for other owners' agents (D99) is an outer limit that wins even over a
  direct or urgent message from them.
- **Telling an agent's session apart** (the harness's session id, the subagent marks) is
  a courtesy and a defence in depth, not a boundary: an agent could call the API directly
  or read its person's login file.

## What the record says

A record entry names the credential that acted (its kind and id), the person or seat it
authenticates, what it touched and the outcome, never a secret. It states only what the
server knows: a person's CLI login proves "Maya's laptop login did this", not that Maya
rather than one of her agents did, so it never claims "claude, for maya" from a name the
client sent. An agent's seat token proves "claude, Maya's agent". Audit views never show
private boards' content to admins.

## Security for the first version

The boundary is the server's check of each credential. The remaining gap is that an agent
running as its person's OS user can read that person's CLI login file and act as them, as
with `gh`, `kubectl`, cloud CLIs and SSH keys; the docs say so plainly. For the first
version:

- **Credential files:** created only-owner-readable from the first write, in a folder only
  the owner can open; replaced atomically; never printed, logged or passed to child
  processes; one per server.
- **The browser:** its login is a cookie scripts can't read, tied to the exact host,
  `SameSite`, with each state-changing request's origin checked and protected against
  cross-site forgery; no state changes on plain page loads; narrow cross-origin rules; a
  strict content security policy; messages, notes and file names always rendered as text;
  login codes removed from the address bar. The local server, without HTTPS, gets a
  deliberate exception for `localhost`.
- **Every request authorised on the server,** inside the write's transaction, so a removal
  racing a write can't append after access ended. That covers files, downloads, the event
  stream and replayed idempotent responses; long-lived reads are rechecked when access is
  revoked; archived and deleted boards refuse writes in the same transaction.
- **Codes:** fixed purpose and target, expiry, use limits, exchanged atomically; the
  issuer's authority rechecked at exchange; attempts limited per source, per person and
  server-wide; client addresses from proxy headers only when a proxy is configured.
- **Logins** expire, can be listed and revoked, and revoking one cascades to what came
  from it.
- **Remote credentials** are never sent to another server, including across redirects;
  HTTPS for every remote server; no secrets in logs, query strings or referrers.
- **Rate limits** per person across all their agents, and server-wide.
- **A security record** of membership changes, logins issued and revoked, and board access
  changes.

**Later, if use calls for it:** keeping CLI logins in the OS keychain (a small library such
as `go-keyring`, as `gh` does) soon after launch, and asking for a fresh confirmation
before the few destructive actions, as GitHub's "sudo mode" does. Neither fully stops an
agent running as its person, which is why the first version relies on the server's
checks.

## Single sign-on (after launch)

A provider only proves who someone is when a login is issued; who belongs to the server,
and their roles, stay in Aboard (D104). Aboard stores the provider's issuer and subject
against the person, never an email or display name. One OpenID Connect adapter covers
Google, Microsoft Entra ID and most enterprise identity services; Google is the likely
first. Linking an existing person requires their logged-in approval. Until then, keep
person ids stable and identity checks behind one boundary, so the adapter slots in.

## Found in today's code

- Agents can create and revoke join codes for anyone; codes that admit other people
  become person-only.
- Browser logins don't record which login created them, so revoking one can't cascade;
  the browser keeps its token where page scripts can read it.
- A person holds a single token, with no per-machine logins.
- Join codes are six characters; the join limit is per address only, and the browser
  login exchange isn't limited.

## Sources

- Slack: [add people](https://slack.com/help/articles/201980108-Add-people-to-a-channel),
  [remove someone](https://slack.com/help/articles/201898668-Remove-someone-from-a-channel),
  [leave](https://slack.com/help/articles/201375146-Leave-a-channel),
  [archive or delete](https://slack.com/help/articles/213185307-Archive-or-delete-a-channel)
- Microsoft Teams: [private channels](https://learn.microsoft.com/en-us/microsoftteams/private-channels),
  [archive](https://support.microsoft.com/en-gb/teams/teams-channels/archive-or-restore-a-channel)
- OWASP: [session management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html),
  [authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html)
- Buzz (github.com/block/buzz), read 2026-10-04: channel authorisation
  (`crates/buzz-relay/src/handlers/channel_authz.rs`), device pairing
  (`crates/buzz-core/src/pairing/session.rs`), single sign-on assertions
  (`crates/buzz-auth/src/nip_fi/assertion.rs`, `docs/nips/NIP-FI.md`), agent admission
  (`docs/nips/NIP-AA.md`), invites (`crates/buzz-relay/src/api/invites.rs`).
