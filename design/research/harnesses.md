# Harness research

Notes from researching how other harnesses and agent tools work, kept so later harness
work starts from them. Each section says when it was researched; the tools change
often, so check a claim against the current source before relying on it. Decisions
that came from these notes: D160, D164–D167, and the omp work.

## Hermes (Nous Research, Python), 2026-10-03

Source: github.com/NousResearch/hermes-agent and hermes-agent.nousresearch.com docs.

- **Session id:** `HERMES_SESSION_ID` is exported to every tool subprocess (terminal,
  execute_code, persistent shell, container backends, delegated subagents).
- **Hooks:** Python plugin hooks (`on_session_start`, `pre_llm_call`, `post_llm_call`,
  `pre_tool_call`, `post_tool_call`, `on_session_end`); shell hooks in `config.yaml`
  (`hooks:`) with the same events plus `subagent_stop`, JSON on stdin
  (`hook_event_name`, `session_id`, `cwd`, `profile`). `pre_llm_call` can return
  `{"context": …}`, appended to the turn's user message. The first run of a shell hook
  asks for consent, stored in `~/.hermes/shell-hooks-allowlist.json`
  (`hooks_auto_accept: true` skips it). Gateway hooks live in `~/.hermes/hooks/<name>/`.
- **No idle wake:** no stop-style hook that keeps the agent working, and no verified
  way to queue input into an idle session.
- **Skills:** SKILL.md from `.hermes/skills` or `.agents/skills` (project),
  `~/.hermes/skills`, and `skills.external_dirs`.
- **Config:** `~/.hermes`, overridden by `HERMES_HOME`; profiles are separate homes
  (`~/.hermes/profiles/<name>`).
- **Resume:** `--resume <id|title>`, `--continue`.
- **Sandbox:** `terminal.backend` local (default), docker, ssh, singularity or modal;
  approval modes smart, manual or off. Unverified: Docker network defaults, and whether
  its environment filtering strips `ABOARD_*` variables.
- **Plugins:** `plugin.yaml` plus `__init__.py` with `register(ctx)`, opt-in with
  `hermes plugins enable`.
- **Fit:** inject at turn start through a `pre_llm_call` shell hook; an idle agent still
  needs the skill and `aboard inbox --wait`. Its Go package would merge YAML hook
  entries, explain the consent step and handle profiles.

## OpenClaw (TypeScript), 2026-10-03

Source: docs.openclaw.ai.

- A personal agent driven from messaging apps through a long-running **Gateway**
  process. A session is a routing key (`sessionKey`), not a terminal.
- **Session id:** plugin hook handlers get `ctx.agentId`, `ctx.sessionKey`,
  `ctx.sessionId`, `ctx.runId`. Commands the agent runs get only
  `OPENCLAW_SHELL=exec` (and `OPENCLAW_CHANNEL_CONTEXT` for channel runs): no session id.
- **Hooks (TS plugins inside the Gateway):** `before_agent_run`, `before_prompt_build`,
  `agent_turn_prepare` (can return `prependContext`/`appendContext`), `before_tool_call`,
  `agent_end`, `session_end`; no `session_start`. A durable queue:
  `api.session.workflow.enqueueNextTurnInjection`. Waking: `runEmbeddedAgent` from a
  plugin, or the Gateway's `POST /hooks/agent` and `/hooks/wake` (bearer token).
- **Skills:** SKILL.md from `<workspace>/skills`, `<workspace>/.agents/skills`,
  `~/.agents/skills`, `<state-dir>/skills`, bundled.
- **Config:** `~/.openclaw` (`OPENCLAW_STATE_DIR`), `openclaw.json`
  (`OPENCLAW_CONFIG_PATH`).
- **Sandbox:** the Docker backend defaults to no network and doesn't inherit the
  environment.
- **Fit:** a TS plugin supplies identity and receives deliveries (the extension
  connection, or a push to the Gateway). Unverified: whether a hook can set environment
  variables for a command.

## omp (oh-my-pi, TypeScript on Bun), 2026-10-03

Source: the oh-my-pi repository, version 18.5.1 (`packages/coding-agent`).

- **No shell hooks:** only TypeScript extensions (a default factory taking the extension
  API), run by Bun with no build step, loaded from `<cwd>/.omp/extensions/` and
  `~/.omp/agent/extensions/`, with no trust dialog.
- **Session id:** none in commands' environment. Every command gets `OMPCODE=1` **and
  `CLAUDECODE=1`**, so detection must check `OMPCODE` first. An extension may set
  `process.env` in `session_start`/`session_switch`/`session_branch` handlers; later
  commands of the main agent see it. Use `ctx.sessionManager.getSessionId()` (a UUIDv7),
  not the provider-facing id in `session_stop`, which `/fresh` changes.
- **Subagents** run in the same process (`ctx.agent.kind` is `main` or `sub`) and share
  its environment, so their commands must be marked.
- **Events:** `session_start` (no startup-or-resume field), `session_switch` (reason
  `resume`), `session_shutdown` (2 s), `agent_start`/`agent_end`, `turn_start`/`turn_end`,
  `session_stop` (stop-hook style, but every handler times out after 30 s, so it can't
  wait for messages), `tool_call` (can add context).
- **Injection:** `sendMessage`/`sendUserMessage` with `deliverAs` `steer`, `followUp`,
  `nextTurn` or `aside` (lands at the next step boundary without interrupting the
  running tool); `triggerTurn: true` wakes an idle session; `ctx.isIdle()`.
- **Resume:** `omp -c`, `omp -r <id>`; same session file, so the same id.
- **Skills:** `.omp/skills` and `~/.omp/agent/skills`; it also loads a project's
  `.claude/skills` and `.agents/skills` (identical duplicates are collapsed).
- **Config:** `~/.omp`; `PI_CODING_AGENT_DIR` overrides the agent folder; logins live in
  a SQLite `agent.db` that also holds caches, so tests must never link or copy it.
- **Permissions:** the default approval mode allows everything; no sandbox.
- **Terminal:** the title shows a spinner while working and `>` when ready.
- **Fit:** the extension holds a long-lived connection to the delivery daemon (the
  extension connection in spec/control.md), supplies identity, and injects bundles.

## Orca (stablyai/orca, TypeScript desktop app), 2026-10-04

Source: github.com/stablyai/orca.

- **Many harnesses (about 45):** a launch-config table per agent (detect command,
  process name, how a prompt is injected, timing fixes such as a ready signal before
  pasting and an extra Enter for Codex, writing trust files before launch), a hook
  installer per agent with a registry and coverage tests, an event normalizer per
  provider, and a per-agent resume switch.
- **Version gating:** a map from each Claude Code hook event to the first CLI version
  that accepts it, pinned by fixtures. Orca probes `claude --version` and installs only
  events that version knows, because some Claude Code versions discard the whole
  settings file when it names one unknown hook event.
- **Evidence:** real-CLI tests are opt-in and skipped without a login; recorded adapter
  captures are replayed; recorded terminal transcripts per agent version are written up
  with expected verdicts and known misreads. No single published support matrix.
- **States:** `working`, `blocked` (a dialog owns the keyboard), `waiting` (wants a
  person: a permission prompt or a question), `done` (with interrupted and
  session-boundary flags); restored rows are unconfirmed; status goes stale after 30
  minutes.
- **State sources, most trusted first:** hooks (Claude `UserPromptSubmit`/`PreToolUse`
  → working, `PermissionRequest` → waiting until a tool event with the same
  `tool_use_id`, `Stop`/`StopFailure`/`PostCompact` → done; Codex the same, and its
  `request_user_input` → waiting); escape sequences the agent prints; terminal-title
  glyphs; per-agent JSON screen rules, each with a mandatory reason, updated from
  releases; process identity (pid and start time).
- **Input:** bracketed paste then Enter, with a receipt in stages (`input_accepted`,
  `turn_started`), idempotent retries that never resend, and waiting for idle before
  sending. A structured lane bypasses the terminal (Claude's Agent SDK stream, Codex's
  app server).
- **Fragile:** an enormous codebase of special cases; Codex submission relies on a
  blind extra Enter.
- **Checked against the published builds (2026-10-04):** Orca's table matches the
  hook event lists in each `@anthropic-ai/claude-code` build on npm, and 2.1.101 is the
  first that skips only the bad values ("The values listed above were skipped; the
  rest of the file is in effect.") instead of the whole file. Its table stops before
  `PostToolBatch` (2.1.118), and it gates only events, not options such as
  `asyncRewake` (2.1.64), which older versions silently drop. Codex, read from its
  source at each release tag, ignores unknown hook events in every version.

## herdr (herdrdev/herdr, Rust terminal multiplexer), 2026-10-04

Source: github.com/herdrdev/herdr.

- **Many harnesses (23):** a TOML detection manifest per agent (rules with state,
  priority, a screen region such as the title or the bottom 12 lines, and matchers),
  updatable from a published index, overridable locally. `herdr agent explain` shows
  which rule decided a state. Hook scripts per agent carry an integration version.
- **Support matrix:** published, with notes per agent.
- **A contract for agent makers:** the agent reports its own state with
  `herdr pane report-agent <pane> --state idle|working|blocked --seq <n>`; reports with
  a sequence no higher than the last are dropped.
- **States:** `idle`, `working`, `blocked`, `unknown`, and a derived `done` (idle, not
  yet seen). Working→idle is held for three checks 100 ms apart (at most 700 ms) unless
  idle or a dialog is visible; blocked only on a positive dialog match; a known agent
  with no match falls back to idle, labelled as such.
- **Input:** paste and Enter as one submission; refused while a dialog is up; "stalled"
  if no working state within 5 s, with a warning never to resend blindly.
- **Fragile:** screen rules drift with each terminal UI release, and an unknown new
  dialog reads as idle.

## What we took from these

- Version-gate every hook event Aboard installs, with a kit test that an older version
  gets only the safe set.
- Presence `waiting` from `PermissionRequest` hooks; presence that says how sure it is
  (unconfirmed after a restart, stale after silence, a short settle before idle).
- Delivery stages (accepted, turn started) and "stalled", never resending.
- Evidence (harness version and date) for each cell of the support matrix.
- `aboard agent explain`, and later a public contract for harness makers.
