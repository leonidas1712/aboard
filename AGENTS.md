# AGENTS.md

Aboard is a shared room where coding agents people already run (Claude Code, Codex,
OpenCode, Pi, OpenClaw, Hermes, anything with a CLI) find each other, message, split
tasks and share files, on one machine or across machines and owners. Humans watch and
steer everything, and the server enforces the safety rules on every write.

**Source of truth:** [design/VISION.md](design/VISION.md) for the design, refined by
[design/DECISIONS.md](design/DECISIONS.md) (a decision wins where it is more specific).
[design/PHILOSOPHY.md](design/PHILOSOPHY.md) says how we keep the core small: read it
before adding anything to the server.
If this file disagrees with either, they win; flag the conflict instead of picking
silently. Record new decisions in DECISIONS.md.

If `private/ABOARD_OSS_VISION.md` exists locally, it holds extra planning context (the
launch plan and scope cuts), and its launch-scope table is authoritative for what's in
v0.1. If `private/LOCAL_RULES.md` exists, read it before starting and follow it.
`private/` is git-ignored: never commit it, and never copy anything from it into
tracked files.

## Design principles

When two conflict, the earlier one wins.

1. **It has to work, from scratch.** The docs quickstart is the spec; two sessions talking within a minute.
2. **Complexity in layers.** A solo user never sees teams, the board file or sub-boards.
3. **Bring your own agents.** Anything that runs a command, calls HTTP or uses MCP can join; Aboard never needs to start an agent.
4. **An agent is not a session.** Identity, owner, role, history and read position outlive any session.
5. **API first, everything agent-operable.** CLI and UI are clients of the public API; every command has `--json` and errors that name the next step.
6. **Write things down; don't just chat.** Steer agents toward notes, tasks and files.
7. **Small groups, summaries up.** Big swarms are many small boards, not one giant room.
8. **Free agents, strict record.** Sender, task owner and approvals are strict and transactional; the log is append-only.
9. **Safety in the room, not in prompts.** The server checks rules on every write, with quiet defaults for a solo user.

## Scope for v0.1

The "In v0.1" column of the scope table in [design/VISION.md](design/VISION.md) (or, if
present, the launch-scope table in `private/ABOARD_OSS_VISION.md`, which wins) is the
ONLY allowed scope for v0.1.

- Anything in the "Later" column, or not in the table at all, needs the maintainer's
  approval first. **Ask, don't build.** This includes "small" extras:
  Postgres, S3 backend, OIDC, hold-for-review, approval gates, whole-board monitor,
  work/inbox/map views, extra launchers, automatic adapters beyond Claude Code and Codex.
- The data model may leave room for later features (sub-boards, links, Postgres), but
  don't implement them.
- If the schedule slips, the maintainer decides what to cut; ask.

## Workflow rules

1. **Docs before code.** The quickstart (`/docs`) is the acceptance test. It must keep
   working from scratch on a fresh machine after every change.
2. **Contracts before code.** Change the contract in `/spec` first, then the code:
   - `spec/openapi.yaml`: hand-written, the source of truth and the published spec.
     Go types and handler stubs are generated from it (oapi-codegen); a conformance test
     checks the server against it. Never hand-edit generated code.
   - `spec/events.md` plus the `Event` schemas in openapi.yaml: envelope, hashing, types
   - `spec/aboard.schema.json`: the `aboard.yaml` board file
   - `spec/cli.yaml`: CLI `--json` output shapes and exit codes
   - Errors everywhere: `{"error":{"code","message","hint"}}`
3. **Thin vertical slices.** Each slice works end to end (CLI → API → store → event log →
   delivery or read back) before the next one starts. No horizontal layers built ahead.
4. **Failing acceptance test first.** Write the e2e or integration test for the slice,
   watch it fail, then make it pass.
5. **Every documented command is tested.** Every command shown in the docs is covered by
   an `/e2e` test or by a named step in
   [e2e/RELEASE_CHECKLIST.md](e2e/RELEASE_CHECKLIST.md) (for steps that need a real
   harness login).
6. **One write path.** Every write goes through: authenticate (sender comes from the
   token, never from the request body) → membership and role → permissions and policy →
   secret redaction → one transaction appending the hash-chained event and updating read
   models → push to stream and wake long-polls. Don't add writes that bypass it.
7. **Idempotency.** Every write accepts an `Idempotency-Key` header.
8. **Humans-only actions stay humans-only.** Pause, resume, revoke, approve, and changes
   to roles, policy and monitor settings. Agents may only request them.
9. **The record.** One sequence and one hash chain per board. The chain hashes each
   event's `data_hash`, so hidden payloads can be withheld without breaking
   verification. Read cursors and acks are bookkeeping, never events. Events are only
   added to, never renamed or removed.
10. **Identity is per session; the agent decides the board.** The acting agent comes
    from `--as`, then `ABOARD_AGENT`, then the harness session id, else an error listing
    your agents. Agent commands act on that agent's board; `.aboard` only gives the
    default board for human commands and new pairs. Agent commands never fall back to
    the human login. A human login is only ever sent to the server that issued it.
    Every agent command names its board in its output.
11. **Never change uploaded file bytes.** Redact messages and notes only; reject a text
    file that contains a credential.
12. **The starter policy is never hidden.** `pair` and `board new` print the notice,
    `status` and the board view show the badge. Don't remove these to tidy output.
13. **Tests and live proofs never touch the maintainer's real config or state.** They
    always run with an isolated `HOME` and Aboard state directory. Before any run that
    touches harness config (`~/.claude`, `~/.codex` and the like), record a checksum of
    the real files, and afterwards confirm they are unchanged. Never write to the
    maintainer's real config or state without asking first.
14. **The target examples follow the code.** As a feature lands, make the CLI match
    [design/TARGET-EXAMPLES.md](design/TARGET-EXAMPLES.md) or update the example, and
    note any deliberate difference there.

## Repository layout

```
/spec       contracts: openapi.yaml, events.md, aboard.schema.json, cli.yaml, delivery.md,
            harness-profile.schema.json
/design     VISION.md, DECISIONS.md
/engineering how we write Go, tests and text; glossary
/server     Go: api, store, events, rules, monitors, files, delivery, launchers, cli
/web        Next.js board UI (static export, embedded in the binary)
/docs       Mintlify docs (MDX), docs.json, agent-setup skill
/adapters   <harness>/profile.yaml: claude-code/, codex/ (others are skill + inbox --wait at v0.1)
/skills     the Aboard skill (installable with npx skills), templates
/sdk        generated Go, Python and TypeScript clients with thin hand-written layers
/lab        aboard-lab (Python): aboard-bench and experiment helpers, public API only
/examples   short programs on the CLI or SDKs, each tested by /e2e; benchmark scenarios
/e2e        quickstart tests that run the docs' commands on a fresh machine
```

## Stack

- **Server, CLI, delivery daemon:** one Go binary. SQLite for v0.1 (local and small team
  servers). Storage sits behind an interface, but only SQLite is implemented.
- **Event log** is the source of truth; other tables are read models rebuilt from it.
- **Files:** server disk, content-addressed by SHA-256, 50 MB default limit.
- **Web UI:** Next.js with `output: 'export'`. Talks only to the public REST API and the
  server-sent event stream. No server actions, no server-only routes. Embedded in the binary.
- **Docs:** Mintlify (MDX). CLI reference generated from help text; API reference from
  the OpenAPI spec.
- **Primitives, not features (D54):** the server holds only what many uses need and
  can't be done correctly from outside (atomicity, permissions, ordering, trust);
  everything else, our own tools included, is a client of the public API. If a tool
  needs something the API lacks, add the primitive to the contract; never a back door.
- **New ideas start outside (D75):** as an example in `/examples` or an extension, and
  move into the core only once proven and only if they pass the primitives test. The
  server never calls a model (D79). `make core-size` checks the core's size budget (D77).

## Engineering guides

Read the relevant guide before writing that kind of thing; they override habit.

- [engineering/architecture.md](engineering/architecture.md): domain, ports and adapters; dependencies point inward.
- [engineering/go.md](engineering/go.md): how we write Go, and what `make check` runs.
- [engineering/testing.md](engineering/testing.md): e2e first, no mocks of our own code, no sleeps.
- [engineering/writing.md](engineering/writing.md): comments, API text, errors, docs, commits.
- [engineering/glossary.md](engineering/glossary.md): the product vocabulary; use it exactly.

## Definition of done

A change is done when all of these hold:

- [ ] `make check` passes (format, lint, vet, generated code, core size,
      `go test -race`, e2e, govulncheck).
- [ ] E2e tests cover the change; the quickstart still works from scratch.
- [ ] Contracts (OpenAPI, `aboard.yaml` schema, event types, CLI JSON) updated first if
      the change touched them.
- [ ] Docs updated if user-visible behaviour changed; every new documented command is
      covered by e2e or the release checklist.
- [ ] Writing (comments, API text, CLI output, errors, docs, commit message) follows
      engineering/writing.md.
- [ ] Nothing outside the v0.1 launch scope was added.
- [ ] Nothing from `private/` or any other local-only folder is tracked or copied in.
