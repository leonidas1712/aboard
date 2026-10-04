# Buzz (block/buzz)

Notes from reading Buzz's source and documents on 2026-10-04 (desktop app v0.5.26). Buzz
moves quickly, so check a claim against its current source before relying on it. How
Aboard positions itself against Buzz and similar products is in
[../positioning.md](../positioning.md).

## What it is

Buzz is Block's open-source (Apache 2.0) "workspace where humans and agents build
together, on a relay you own". It started in March 2026 and is developed at a fast pace by
a large team. It aims to replace a team's chat, code hosting, CI dashboards and release
tools with one self-hosted substrate, with agents as full members. Its stated scale target
is 10,000 people and 50,000 agents per community.

- **Built on Nostr.** Every message, reaction, workflow step, approval and git event is a
  signed event; the event `kind` decides what it is. A community is one relay, selected by
  the request's host.
- **Stack.** A Rust relay (Axum, WebSocket and an HTTP bridge), Postgres, Redis for fan-out
  and presence, S3 for media; a Tauri desktop app, a Flutter mobile app in progress, a small
  web client; an agent-first JSON CLI (`buzz`) and an operator CLI. Deployed with Docker
  Compose or Helm; a hosted operator can run many communities, with tenant isolation
  modelled in TLA+ and Tamarin.
- **Surfaces.** Stream channels (chat with threads), forums, DMs, canvases, a git forge
  (patches, issues, pull requests, reviews; "branches are channels"), YAML workflows
  (triggered by messages, reactions, schedules or webhooks), voice huddles, an agent
  activity feed, search, moderation, and enterprise single sign-on.

## How agents take part

- **Buzz runs the agents.** Its desktop app creates "managed agents": you choose a harness,
  model, keys and MCP servers, and it spawns `buzz-acp`, which starts and supervises 1 to 32
  agent processes over ACP (Agent Client Protocol, JSON-RPC over stdio). Agents can also run
  remotely on Kubernetes and stop themselves when idle.
- **Harnesses.** Built in: Goose, Claude Code (through an ACP adapter), Codex (through an
  ACP adapter) and Buzz's own small agent. Presets: Cursor, Pi, oh-my-pi, OpenCode, Amp,
  Hermes Agent, OpenClaw and others; custom definitions too. Every harness must speak ACP;
  an interactive session a person already has open in a terminal cannot join as it is.
- **Identity.** An agent is a keypair; its owner signs an attestation authorising it, so the
  record separates "written by the agent" from "authorised by its owner". One identity may
  run many sessions (per channel or per thread), and the prompt tells each that it is "one
  session of your agent identity".
- **Delivery.** The relay pushes events over WebSocket to the harness, which queues them per
  channel and sends them to the agent as one prompt. By default ("steer"), a message that
  arrives mid-turn cancels the running turn and re-prompts with what the agent was working
  on plus the new message; partial work is lost. Delivery is best-effort, and the harness
  keeps no durable state. Agents respond only to their owner by default (an author gate:
  owner-only, an allowlist, anyone, or nobody); owners can cancel or stop an agent from the
  chat (`!cancel`, `!shutdown`).
- **Testing.** Integrations are tested with a deterministic fake ACP agent; real harnesses
  only through opt-in tests and a manual check.

## Record, access and safety

- Events are signed and verified, but the events table is not append-only: deletions,
  edits, replaceable kinds and retention remove or replace events. A separate audit log is
  hash-chained per community and records about ten coarse actions.
- Channel membership is the only access gate; channels are open, private or guest-scoped.
  Community roles are owner and admin; channels have owner, admin, member and guest, and
  agents are marked as bots.
- No secret redaction in message content; rate limiting is a stub; workflow approval gates
  are not wired yet; a design for binding agents to an audience against confused-deputy
  problems is a draft. Notifications are off by default ("you opt in to noise").

## Worth learning from

- The mention editor's written contract, and mentioning an agent that isn't in the channel
  offering to invite it.
- Notifications off by default; presence as a renewed lease meaning "available to talk",
  not "the process is alive".
- The agent activity view: each step as verb, object and outcome; failures stand out and
  reads fade; the raw stream on demand.
- One session per thread, and telling a session it is one of its identity's sessions.
- The owner's attestation of an agent key.
- A "welcome team" of agents greeting a new person in a welcome channel.
- Formal models of tenant isolation.
