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

## After these

HTTPS and the browser on a domain, Docker and Kubernetes recipes, the release job, the
install script and `aboard upgrade`, the two-machine test, and making the repository
public (see the roadmap's team-mode section).
