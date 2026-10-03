# Codex subagents and Aboard

A Codex agent on an Aboard board investigated this on 2026-10-03, by spawning a subagent and reading the Codex source. It decided Codex's subagent level in D165 (marked).

Investigated 2026-10-03. Installed harness: Codex 0.160.0. Official source checkout: a clone of github.com/openai/codex, commit `b741e480e203f037ca726bc2a76d99a8e8668e66` (current upstream, not asserted to be the exact installed release). No configuration was edited and no subagent posted or consumed board messages.

## Live read-only probe

Parent/root thread and session: `01a1025c-ec41-75b0-a03e-cb21281c2e92`.
Child `/root/identity_probe` thread: `01a10260-6c3c-7831-8ddb-920a55815f53`.

The child ran only `aboard status --json` and the requested environment filter. Its status returned board `general-3`, `board_source: project_file`, `agent: null`, `agent_source: none`. It saw a running daemon (PID 72465), four open sessions, and known agents `claude-lead` and `codex`. This run therefore did NOT resolve the child to the parent's Aboard agent.

Child environment:

```text
CODEX_CI=1
CODEX_MANAGED_BY_NPM=1
CODEX_MANAGED_PACKAGE_ROOT=<npm global prefix>/lib/node_modules/@openai/codex
CODEX_PERMISSION_PROFILE=:workspace
CODEX_SANDBOX=seatbelt
CODEX_SANDBOX_NETWORK_DISABLED=1
CODEX_SESSION_ID=01a1025c-ec41-75b0-a03e-cb21281c2e92
CODEX_THREAD_ID=01a10260-6c3c-7831-8ddb-920a55815f53
CODEX_VERSION=0.160.0
```

The parent's two IDs were equal. There was no `ABOARD_*` variable or explicit immediate-parent variable. Comparing `CODEX_THREAD_ID` with `CODEX_SESSION_ID` distinguishes this child from the root. The session ID identifies the root, not necessarily the immediate parent of a nested child.

Independent confirmation: child rollout `~/.codex/sessions/<date>/rollout-<time>-<child thread id>.jsonl` (first line) records the root session ID, distinct child ID, parent_thread_id, forked_from_id, source.subagent.thread_spawn, depth 1, and agent_path `/root/identity_probe`. Only identity metadata was used; owner/account identifiers are omitted here.

Aboard currently uses `CODEX_THREAD_ID` for shell identity (`server/internal/cli/session.go:173`; `server/internal/harness/registry/registry_test.go:154`). Its hook handler reads `session_id` (`server/internal/cli/hook.go:50`). This creates two different identities for a child: child thread in commands, root session in hooks.

## Hook evidence and limits

Installed `~/.codex/hooks.json` includes Aboard handlers for SessionStart, UserPromptSubmit, Stop, SessionEnd, and PreToolUse. It has no SubagentStart or SubagentStop handler. `aboard doctor --json` confirmed hooks installed and queue available.

The daemon log showed the root session registration and board message delivery, but no child registration or raw hook input. The child's rollout contained no hook event_msg records. These observations do not prove that its PreToolUse hook executed or reveal its exact runtime stdin. The probe did not install an intercepting hook, edit configuration, or invoke a hook manually. Actual child hook execution remains unverified in this live run; do not describe source behavior as captured runtime evidence.

The hooks.json SHA-256 before and after was identical:
`3d1390829c3c2d3151d8a779cbaaa4509b63dd870f2036eb5cfa95bace517347`.

## Official source findings

All source references below are relative to a clone of github.com/openai/codex at the commit above. Permanent links can use `https://github.com/openai/codex/blob/b741e480e203f037ca726bc2a76d99a8e8668e66/<path>#L<line>`.

- Spawn: `codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs:143` prepares child configuration and fork mode; `:163` creates a source with parent thread, depth, role and task path; `:193` calls agent_control.spawn with parent_thread_id, parent_turn_id and inherited environments. A full-history fork creates a child thread with ThreadSource::Subagent and parent linkage (`codex-rs/core/src/agent/control/spawn.rs:1253`). Thread creation reserves a new thread ID for new/forked history (`codex-rs/core/src/thread_manager.rs:2176`); the default generator uses ThreadId::new (`:154`).
- Shell identity: `codex-rs/core/src/exec_env.rs:30` injects the current thread ID through shell environment construction. `:40` injects the shared root session ID and version. Session construction explicitly derives a child's session ID from the shared controller while roots use their own thread ID (`codex-rs/core/src/session/session.rs:914`). This matches the live probe.
- Tool hooks: `codex-rs/core/src/hook_runtime.rs:187` runs PreToolUse with sess.session_id and child context. `:1042` recognizes ThreadSpawn; `:1054` sets agent_id to the child's thread ID and agent_type to its role or default. `codex-rs/hooks/src/events/pre_tool_use.rs:176` serializes session_id, agent_id and agent_type. Normal hook input therefore has a root marker plus a child marker; it does not expose an immediate parent_thread_id field here.
- Lifecycle hooks: `codex-rs/core/src/hook_runtime.rs:127` dispatches SubagentStart for spawned/forked children rather than SessionStart. `:391` dispatches SubagentStop for child turn completion rather than root Stop. Both carry the child agent identity. Existing Aboard SessionStart/Stop handlers should not be assumed to run for these child lifecycle events.
- Queue/addressing: `codex-rs/cli/src/queue_cmd.rs:13` accepts an explicit --thread target. Protocol provides thread/read and thread/queue methods (`codex-rs/app-server-protocol/src/protocol/common.rs:630`, `:857`). However, `codex-rs/app-server/src/request_processors/thread_input.rs:8` explicitly rejects direct app-server input to v2 ThreadSpawn children. Queue validation enforces that same rule for loaded children and rejects unloaded ThreadSpawn children (`codex-rs/app-server/src/request_processors/thread_queue_processor.rs:292`). A thread being identifiable/readable is not permission to inject input. Legacy behavior differs: the shared policy only blocks v2 loaded children.
- Ending: final output completes a turn and changes status to Completed (`codex-rs/core/src/agent/status.rs:6`), distinct from ShutdownComplete (`:21`). Completed children can be reactivated through parent followup_task, as our available tool contract states. Idle completed/errored/interrupted v2 children may be unloaded when they have no active turn, durable sleep or trigger-turn mailbox input (`codex-rs/core/src/agent/control/residency.rs:292`). Completion should not automatically be interpreted as deleting the child or ending the root session.

Official hook documentation: https://learn.chatgpt.com/docs/hooks (fetched via https://developers.openai.com/de-DE/docs/hooks, which redirected there). It independently describes parent session IDs for child hooks, optional agent_id/agent_type on normal child hooks, and separate child lifecycle events. The source references above provide the detailed evidence without depending on documentation excerpts.

## Implications for Aboard support

`marked` is technically supported by current Codex identity metadata: root session versus child thread in environment, and agent_id/agent_type in hook stdin. Aboard would need to reconcile command identity with hook identity and explicitly handle child lifecycle events. It should not let a child's tool hook change the root's presence or consume root delivery accidentally.

An independent `seats` implementation cannot assume codex queue can wake a v2 child directly. Child writes could use an explicitly opted-in Aboard identity, but delivery needs a supported route through the parent/controller (or an independently proven child polling flow). Parent opt-in, identity mapping, nested child linkage, and lifecycle semantics would need design decisions before implementation.

Recommended evidence label: Codex exposes child markers; independent queued delivery to v2 child threads is blocked in inspected upstream source. Live command identity is verified; live raw hook execution/input is not verified. D165 was not found in the current local DECISIONS.md, so no proposed decision text was treated as merged authority. No repository code or design record changed.
