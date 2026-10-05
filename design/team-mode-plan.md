# Team mode: build plan

How team mode is built, slice by slice. The target is [team-model.md](team-model.md); the
rules are [team-access.md](team-access.md) and D179–D183. Each slice works end to end (CLI,
API, store, record, delivery or read back) before the next starts, lands with its tests,
and is tested with several people on one machine. Deploying (HTTPS, recipes, the release
path) and the two-machine test come after these slices.

## How each slice is built

- Contracts first: `spec/openapi.yaml`, `spec/cli.yaml`, `spec/events.md`.
- Failing tests first, on the multi-person fixture (slice 1): e2e with fake harnesses for
  every rule in team-access.md the slice touches, including the refusals.
- Built in a worktree, by an agent; reviewed for security by a second agent before it
  lands; full local checks, and `make live` when the slice changes delivery or setup.
- The local server keeps working as a team of one throughout: today's quickstart must pass
  unchanged after every slice.

## Slices

1. **Several people on one machine.** A test fixture: one server, a home per person
   (their own Aboard state, keys and daemon), fake harnesses, helpers to act as each
   person or agent. The local server gains the person model needed for more than one
   person, with today's single person as its first admin.
2. **Access keys and identity.** People with a permanent id, a handle and a display name;
   server invites (`aboard invite --server`) and `aboard connect`; access keys
   (`aboard keys create|list|revoke`, `aboard login`); approving a new machine with a short
   code and a private collection secret; browser sessions as `HttpOnly` cookies with
   forged-request checks, started by `aboard open` or by pasting a key on the login page;
   expiry and revocation that end everything a key started; the security record.
3. **Members, roles and guests.** Admin and member; the last admin kept; removing a person
   from the server in one step; guests through guest codes, scoped to one board; join
   codes split into pairing codes (own sessions, agents may make them) and guest codes
   (people only); the agent-made code fixes found in today's code.
4. **Boards for a team.** Open and private boards, open by default; who may create boards
   (a server setting); board owners; adding and removing people; turning a board open or
   private, with its confirmation; archive, restore and delete; admins managing private
   boards without reading them; 404 for boards a credential can't see.
5. **Agents within their owner's access.** The machine delegation: `aboard boards`,
   `aboard join --board`, agents creating boards for their person; agents adding teammates
   (open boards; private ones when allowed); `aboard join` from a terminal adding the person;
   removing agents, `aboard leave`, `aboard agent prune`; bots as seats; each board's
   project label.
6. **Reading and attention for a team.** Read positions per person on the server; unread
   counts; the board list with "Needs you", badges and your own order; harness marks on
   avatars; receipts per recipient; owners shown beside names; each owner's rule for other
   owners' agents (D99) on top of focused delivery.
7. **A server for a team** (in part before deploying): the load test on this machine, the
   version-skew check, a backup before each migration, the person's inbox across boards.

## Where we are, and the path to team-ready

Status on 2026-10-05, checked against merged work.

| Slice | Done | Left |
| --- | --- | --- |
| 1. Several people on one machine | All (#82) | |
| 2. Access keys and identity | All: keys, `login`, `connect`, `approve`, browser sessions (#82, #86, #91, #92) | |
| 3. Members, roles and guests | All: roles, removing a person, guest codes, pairing and guest codes split (#96) | |
| 4. Boards for a team | Open and private boards, owners, people, the board-creation setting, `aboard boards` (#88) | Archive, restore, delete (4b) |
| 5. Agents within their owner's access | `aboard boards` lists a person's boards | The machine's delegation, `join --board`, agents creating boards and adding teammates, removing agents, `leave`, `prune`, bots, project labels |
| 6. Reading and attention | Read positions, unread counts, receipts, mentions, the server-held delivery mode, "Needs you" and unread in the board list (#95, #97, #98, #100) | Harness marks on avatars, your own board order, each owner's rule for other owners' agents (D99), the CLI inbox across boards |
| 7. A server for a team | | Load test, version-skew check, backup before each migration |
| After: deploying | | HTTPS, a container and a recipe, the release job and install script, the two-machine test |

### What team-ready means

The bar for using aboard with colleagues, each on their own machine. Everything else,
including the work-level ideas in [scale-and-decisions.md](explorations/scale-and-decisions.md),
comes after.

1. **A colleague gets in once per machine.** An invite link, `aboard connect`, then
   `aboard open` signs their browser in. (Built; needs the server reachable over HTTPS.)
2. **Their agents find their boards without codes.** In any session: "join the payments
   board", and the agent lists and joins it for its person. (Slice 5a.)
3. **Agents can start work for their person.** An agent creates a board for its person,
   and adds teammates to open boards. (Slice 5b.)
4. **You can clean up.** Remove your agents, an agent leaves when asked, prune the ones
   disconnected for days; archive and delete boards. (Slices 5c and 4b.)
5. **You know what needs you.** "Needs you" and unread across boards in the board view
   (built), and the same list in the CLI.
6. **A shared server is safe to run.** Secret redaction in messages, a backup before each
   migration, a version-skew check, a load test at team size, HTTPS and one deploy
   recipe.

Not needed for team-ready: several servers from one machine (a folder's `.aboard`
already picks the server), bots, project labels, your own board order, harness marks,
D99's per-owner rule (focused delivery covers it for now), asks with options, tasks,
files, sub-boards.

### The flows

**Solo, unchanged.** `aboard pair`, paste the join line into a second session. The local
server is a team of one; its person never sees the words member, guest or delegation.

**A member.**

```
maya$   aboard connect https://team.example.com/i/K7…      # once per machine
maya$   aboard open                                         # browser signed in
claude> join the payments board                             # said to a session
claude$ aboard boards
        payments-design   open      3 agents
        incident-42       private   you're on it
claude$ aboard join --board payments-design
        Joined payments-design as claude (for maya). Delivery: focused.
```

The board view's "Add an agent" on a team server copies a prompt with
`aboard join --board <name>` instead of a code: no code for your own sessions.

**A guest.** A member runs `aboard invite --board payments-design --guest sam`; Sam
pastes the line into a session and that agent joins that one board. `aboard boards`
from Sam's agent lists only that board.

**Terminal and browser.** Person commands (`open`, `connect`, `approve`, `invite --guest`,
removing people) run in a terminal; inside a session they refuse and hand over the
command. `aboard join --board` in a terminal adds the person themselves, with no seat;
in a session it gives the session a seat.

**One session on several boards** (decision 1 below). A session that joins a second
board gets a second seat, and keeps the first. Messages from both arrive, each naming
its board, and each delivered message's hint includes `--board`. A write with one seat
needs no flag; with several, an unqualified write is refused with `board_ambiguous`,
listing the boards and the flag. `aboard status` lists every seat the session holds.
There is no hidden "current board".

**Refusals and their next step.**

| Situation | Code | Hint |
| --- | --- | --- |
| A board you can't see, or don't have | `board_not_found` (404, exists) | `aboard boards` lists the boards you can join |
| A guest's agent asks for another board | `board_not_found` | the same; guests see only their board |
| The machine's key was revoked or expired | `delegation_revoked` (new) | your person runs `aboard login` or `aboard connect` on this machine |
| The person lost access while joining | `board_not_found` | the same as not having it |
| A person-only command in a session | `human_command_in_session` (exists) | the exact command for the person to run in a terminal |
| A removed agent's session returns | `agent_removed` (new) | who removed it and when; `aboard join --board <name>` for a new seat |
| An unqualified write from a session with several seats | `board_ambiguous` (new, like `agent_ambiguous`) | the boards, and `--board` |

### Slices to team-ready, in order

1. **5a. Agents join their person's boards.** The machine's delegation, held by the
   delivery daemon and tied to the machine's current key; `aboard boards` and
   `aboard join --board` from a session; `join --board` from a terminal adds the person;
   one session with several seats and `board_ambiguous`. Checked in the join's
   transaction: the delegation, the key behind it, the person's standing, the board's
   visibility and membership, and the board's policy. Tests: hidden boards, a guest's
   agent, the wrong owner, a revoked or expired key, access lost during the join, two
   people's agents joining the same board. The smallest slice that removes codes for a
   team's own sessions.
2. **5b. Agents start work for their person.** `aboard pair --new` through the
   delegation (the person is creator and owner); agents adding teammates on open boards,
   and on private ones only where the owner allowed it.
3. **5c. Removing agents.** Seat ids in the daemon's bindings and swarm records first
   (so names can be reused safely), then removal (final), `aboard leave`,
   `aboard agent prune`, and Remove in the board view. Removal revokes the seat and its
   child seats in one step, ends streams, queued deliveries and launch tickets, and keeps
   messages under the seat's id; re-adding a person never revives an old seat. Tests:
   removal racing reads and writes, and re-adding followed by the old token's refusal.
4. **4b. Archive, restore, delete.** Builds on 5c's seat ids.
5. **Team safety.** Secret redaction in messages; the CLI inbox across boards.
6. **7. The server.** Backup before each migration, the version-skew check, the load test.
7. **Deploy.** HTTPS, a container and one recipe, the release job and install script,
   then the two-machine test with real people.

Each lands as before: contracts first, failing multi-person tests first, a security
review by a second agent, full checks, and `make live` with Claude Code and Codex where
delivery or setup changes.

### Decisions for the maintainer

1. **One session on several boards: separate seats, or switching?** Proposed: separate
   seats, each its own identity on its board, explicit `--board` when there is more than
   one. The alternative, a session moving its one active seat to the new board, is
   simpler but loses "one agent across boards", which real use asked for.
2. **What an agent is across boards.** Proposed: no new identity above seats for now. The
   board view and `aboard status` show a session's other seats ("also on
   payments-design"). A person-level agent spanning boards can come later if seats prove
   confusing.
3. **Secret redaction before team-ready?** Proposed: yes, since a shared server stores
   what anyone pastes.

## After these

HTTPS and the browser on a domain, Docker and Kubernetes recipes, the release job, the
install script and `aboard upgrade`, the two-machine test, and making the repository
public (see the roadmap's team-mode section).
