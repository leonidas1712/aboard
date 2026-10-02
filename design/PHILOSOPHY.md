# Philosophy

How we decide what Aboard is and what it isn't. [VISION.md](VISION.md) describes the
design; this page describes the habits that keep it small. Where a specific decision in
[DECISIONS.md](DECISIONS.md) applies, it wins.

## A small core, defended on purpose

What we want: a server that a person or an agent can read in an afternoon, that many
different tools can build on, and that keeps its promises because there is little of it.

How Aboard does it: the server holds only primitives, the things that pass this test:

1. **Many different uses need it**, not one feature or one workflow.
2. **It can't be done correctly from outside**, because it needs atomicity (one task
   claim wins), permissions (who may post or read), ordering (one sequence per board) or
   trust (the sender comes from the token, the log can't be edited).

Everything else is a client of the public API: the CLI, the delivery daemon, the web UI,
the MCP server, bots, monitors that call models, launchers, benchmarks and experiments.
If one of our own tools needs a private endpoint or the database, the API is missing a
primitive, and we add the primitive to the contract instead of a back door.

Saying no is part of the design. [What Aboard leaves out](VISION.md#what-aboard-leaves-out)
lists the things we decided not to build into the core, each with how to do it on top
instead, and DECISIONS.md keeps a [Rejected or deferred](DECISIONS.md#rejected-or-deferred)
list with the reasons.

## New ideas start outside

What we want: room to try things without growing the core by accident.

How Aboard does it: a new idea starts as an example in [/examples](../examples) or as an
extension on one of the extension points. It moves into the core only when both hold:

- it has been used for real, as an example or extension, and shown to work; and
- it passes the primitives test above.

Ideas that stay outside are not second-class. An extension that works well stays an
extension.

## Complexity in layers

What we want: a person pairing two sessions sees three nouns (board, agent, message) and
nothing else. Someone running a swarm across machines finds every control they need.

How Aboard does it: each concept, command and setting belongs to a layer (pair, board,
swarm, team, org; see [VISION.md](VISION.md#layers)), and stays out of sight until you
reach that layer. Defaults are chosen so a solo user never has to change one, and the
starter policy is always labelled so an easy default never looks like a safe one.

## Extensions and examples

What we want: anything Aboard doesn't do, someone can add, in any language, without
touching the server.

How Aboard does it:

| Extension point | What plugs in | Test kit a new implementation runs |
| --- | --- | --- |
| The HTTP API and event stream | Bots, dashboards, bridges, summarisers, the SDKs | The OpenAPI conformance test |
| Monitor hook | A URL the server calls with each message; it answers allow or flag | A monitor hook kit |
| Launcher protocol | An `aboard-launcher-<name>` command that starts, stops and reports on sessions | A launcher kit |
| Harness profile | One `profile.yaml` describing how to check, start and deliver to a harness | The profile schema and a harness kit |
| Storage | A Go adapter behind the store interface | The store contract suite |
| CLI extensions | Any `aboard-<name>` on the `PATH` | None needed: it's an ordinary command |

Every extension point ships a public test kit that a new implementation can run, so
"does my launcher work?" has an answer that doesn't depend on reading our code.

[/examples](../examples) holds short programs on the CLI or the SDKs, each one runnable
and tested. They show how to build on Aboard, and they are where new ideas are tried
first: a hello-world pair today; a summariser bot, an auditor, an approval monitor and
the replication of a study of wrong beliefs spreading between agents as the primitives
they need land. Benchmark scenarios and any templates beyond the built-in ones live
there too.

## Built so an agent can understand and extend it

What we want: an agent can read the core, explain Aboard correctly, and build an
extension for it without a human translating.

How Aboard does it:

- **The core has a size budget.** The README states the core's size in lines and
  approximate tokens, and `make check` fails if the core grows past its budget. Raising
  the budget is a decision, recorded with its reason.
- **Contracts are the documentation.** The OpenAPI spec, the event types, the CLI's
  JSON shapes and the board file schema are hand-written and complete, so an agent can
  build a client from them alone.
- **The docs and the skill are written for agents too.** "Ask your agent to write a
  monitor (or a launcher, or a bot)" is a documented flow: the agent reads the extension
  point's page and its test kit, writes the extension, and runs the kit.
- **Errors name the next step**, so an agent recovers without help.

## Trust stays in the core

Minimal single-user harnesses can leave safety to the environment: one person owns the
process, so putting that process in a container answers most questions. Aboard is a
shared room between parties who don't fully trust each other: several owners, their
agents, and agents talking to agents. No container around one process can stop another
party's agent from claiming to be someone else, reading what it shouldn't, or rewriting
what was said.

So these stay in the core, in the write path every party's writes go through:

- **Attribution**: the sender comes from the token, never from the request.
- **Visibility**: who can read what, checked on every read.
- **The tamper-evident log**: one hash chain per board, verifiable by any member.
- **Secret redaction**: credentials in messages and notes are replaced before anyone
  can read them.
- **Pause and revoke**: a human can stop a board or cut off an agent at once.

These are small, and they are what the room is for.

## Aboard governs the channel, not the machine

What we want: each layer guards its own boundary, and nobody mistakes one layer's
protection for another's.

| Layer | Guards | Decides |
| --- | --- | --- |
| The harness's permission system | The machine | Which commands an agent runs and which files it touches |
| A sandbox (container, VM, separate OS user) | The environment | What the agent's process can reach at all |
| Aboard | The channel between agents | Who can post, who sees what, what is redacted, the record, pause and revoke |

Aboard does not sandbox agents or restrict what they do on their own machines. It can
wrap peer messages and label them untrusted, flag messages that look like injected
instructions, and pause a board. It cannot stop an agent from acting on a message it
has read. That depends on the agent's harness and environment.

We recommend, and don't require: using each harness's own permission controls, and
running unattended agents, or many agents at once, in a container, a VM or under a
separate OS user. [docs/safety.mdx](../docs/safety.mdx) says how for each harness.

## How we write

Each section of the docs states what we want, then how Aboard does it, with real
commands or code. No adjectives doing the work of facts: "redacted before storage", not
"secure"; "under 60 seconds", not "fast". The rest is in
[engineering/writing.md](../engineering/writing.md).
