# How the code is shaped

Aboard's rules live in one core that knows nothing about how data is stored, sent or
shown. Storage, HTTP, the CLI, harness hooks and files on disk are adapters that plug
into that core. This is ports and adapters ("hexagonal architecture"); the words below
are all you need.

**The test:** every rule of Aboard can be read in `board`, `rules` and `events` alone,
without a line of SQL, HTTP or CLI code.

## The pieces

| Piece | What it is | In Aboard |
| --- | --- | --- |
| Domain | The product's rules, in the product's words ([glossary.md](glossary.md)) | `board` (the service and its ports), `rules`, `events` |
| Port | A small interface the domain declares for something it needs | `board`'s store and notifier interfaces; `delivery`'s adapter, journal and server interfaces |
| Adapter, driven | Plumbing that implements a port | `store/sqlite`, `notify`, `delivery/idlehook`, `delivery/codex` |
| Adapter, driving | A caller that turns requests into domain calls and back | `api` (HTTP), `cli`, the delivery daemon |

**Dependencies point inward.** Adapters import the domain; the domain never imports an
adapter. If `board` imports `sqlite`, the arrow is wrong.

**One composition root names concrete adapters.** `server/internal/server` (and the
CLI's `serve` and `daemon` commands that call it) opens the SQLite store, builds the
notifier and hands them to the domain. Nothing else imports an adapter. The root only
wires: any logic there, such as "create the server's identity on first start", takes a
port, not `*sqlite.Store`, so another store plugs in without editing it.

## Rules

1. **A rule lives next to the data it protects, once.** "One member claims a task" is
   enforced in `board`, not in a handler, the CLI and a query. Ask: could someone break
   the rule by calling the code a different way? If yes, it's in the wrong place.
2. **Driving adapters hold no rules.** The API and CLI translate and call the service.
   No "just to be safe" permission check in a handler: pick the domain and trust it.
3. **The domain doesn't know about transport.** No HTTP statuses, exit codes, JSON tags
   or SQL in domain types. The domain returns its own errors; the edge translates them.
4. **Things that must agree change together.** A board's event sequence and hash chain,
   a task's state and its claimant: each group is loaded, changed through the service and
   saved in one transaction. Keep these groups as small as the rule needs.
5. **Ports are defined by the code that uses them, small, and named for the job.**
   `AppendEvent`, `MembersOf`, `Inbox`; not `Get`, `Save`, `Find(query)`. The delivery
   daemon needs to read an inbox and acknowledge it, so its interface has those two
   methods, not the whole store.
6. **Ports don't leak.** A port never takes SQL fragments or returns `*sql.Rows` or
   driver errors. The adapter turns "unique constraint failed" into the domain's
   "name taken".
7. **Accept interfaces, return structs.**
8. **An interface needs a reason.** A second implementation, or an outside system to
   replace in tests: storage, notifier, clock, randomness, harness adapters, launchers,
   monitors, file storage, login providers. Most internal helpers don't get one.

```go
// Do: board declares what it needs, in its own words.
package board

type Tx interface {
	BoardByName(name string) (Board, error)
	AppendEvent(e events.Event) error
	Inbox(reader Member, limit int) ([]Message, error)
}

// Don't: a generic repository that hides what the domain needs.
type Repository interface {
	Find(table string, where string, args ...any) ([]map[string]any, error)
}
```

## Contexts

Parts of Aboard with their own model talk through explicit interfaces, mostly the public
API, never through each other's tables. They are packages in one binary, not services.

| Context | Owns | Talks to the others through |
| --- | --- | --- |
| Board | Boards, members, roles, policy, messages, tasks, notes, the event log | It is the core |
| Delivery | Sessions, bindings, the journal, bundles, harness adapters | The public API, like any client |
| Identity | Human logins, agent tokens, invites, servers | Today inside `board`; split out when team mode needs it |
| Files | Bytes, hashes, versions, storage backends | Built with files |

Split a context out when two parts start using one word differently, not before.

## Layout

```
server/internal/board            domain service and the ports it needs
server/internal/board/boardtest  the contract suite every store adapter must pass
server/internal/rules            pure policy: permissions, presets, visibility
server/internal/events           pure: event envelope, canonical hashing, chain checks
server/internal/store/sqlite     adapter: board's store ports (and api's idempotency port)
server/internal/notify           adapter: board's notifier port, in-process
server/internal/api              driving adapter: HTTP, generated from openapi.yaml
server/internal/cli              driving adapter: talks only to the API client
server/internal/delivery         context: daemon, journal port, adapter port, sessions
server/internal/delivery/idlehook adapter: a hook that waits while the session is idle (Claude Code)
server/internal/delivery/codex   adapter: Codex's queue and app server
server/internal/delivery/extension adapter: an extension inside the harness holds a connection to the daemon (omp)
server/internal/harness          what Aboard knows about each harness: profile, generic implementation, Harness interface
server/internal/harness/claudecode, harness/codex, harness/omp   each harness's quirks, over the generic implementation
server/internal/harness/registry the one list of harnesses; the CLI and the daemon loop over it
```

The harness layer is a client-side context like delivery: the CLI and the daemon reach
every harness through `harness.Harness`, and only `harness/registry` names them. A
harness's data is its profile in `adapters/<harness>/profile.yaml`; see
[adding-a-harness.md](adding-a-harness.md).

## Testing by layer

- **Domain rules** (`rules`, `events`, pure functions): table-driven unit tests.
- **Every adapter of a port** passes that port's contract suite (for storage,
  `boardtest.Run`). That suite is what makes swapping SQLite for Postgres safe.
- **The service** is tested through the API with a real store (integration tests).
- **The wiring** is proven end to end through the CLI (e2e).
- **Mocking our own service is a smell**: the thing under test probably holds a rule it
  shouldn't.

## What goes wrong

- **Ceremony**: layers, factories and an interface per struct for a three-line rule.
- **Rules in two places**: they drift.
- **One giant aggregate**: loading and saving a whole board on every write.
- **Splitting too early**: contexts invented before the code shows the seams.
