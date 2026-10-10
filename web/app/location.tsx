"use client";

// Where a person's own agent last ran, and the commands to pick its conversation up again.
// The server sends the location only for the viewer's own agents, so this shows nothing else.

import type { Member } from "./api";
import { CopyButton } from "./board-details";
import { exactTime, harnessName, relativeTime } from "./words";

/**
 * resumeCommands is how each harness reopens a conversation by its session id: the
 * interactive resume of its adapter profile, without the prompt.
 */
const resumeCommands: Record<string, (session: string) => string> = {
  "claude-code": (s) => `claude --resume ${shellQuote(s)}`,
  codex: (s) => `codex resume ${shellQuote(s)}`,
  omp: (s) => `omp --resume ${shellQuote(s)}`,
};

/** shellQuote makes a word safe in a POSIX shell: plain words stay as they are, the rest go in single quotes. */
export function shellQuote(s: string): string {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(s)) return s;
  return `'${s.replaceAll("'", `'\\''`)}'`;
}

/** reopenCommand is the shell command that returns to the agent's conversation, or null when its harness has no resume. */
export function reopenCommand(loc: NonNullable<Member["location"]>): string | null {
  const resume = resumeCommands[loc.harness];
  return resume ? `cd ${shellQuote(loc.folder)} && ${resume(loc.session_id)}` : null;
}

function shortSession(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…` : id;
}

/** AgentLocation is the "Where it ran" section of a person's own agent's details. */
export function AgentLocation({ agent, board }: { agent: Member; board?: string }) {
  const loc = agent.location;
  const reopen = loc ? reopenCommand(loc) : null;
  const label = "text-meta text-muted";
  const where = loc?.machine ?? "that machine";
  return (
    <section className="agent-location mt-2 flex flex-col gap-2 border-t border-rule pt-2" aria-label={`Where ${agent.name} ran`}>
      <h4 className="text-meta text-muted">Where it ran</h4>
      {loc ? (
        <>
          <dl className="grid grid-cols-[72px_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1">
            {loc.machine && (
              <>
                <dt className={label}>Machine</dt>
                <dd className="loc-machine">{loc.machine}</dd>
              </>
            )}
            <dt className={label}>Harness</dt>
            <dd className="loc-harness">{harnessName(loc.harness)}</dd>
            <dt className={label}>Folder</dt>
            <dd className="loc-folder break-all font-mono text-meta">{loc.folder}</dd>
            <dt className={label}>Session</dt>
            <dd className="loc-session font-mono text-meta" title={loc.session_id}>
              {shortSession(loc.session_id)}
            </dd>
            <dt className={label}>Last active</dt>
            <dd className="loc-active" title={exactTime(loc.last_active)}>
              {relativeTime(loc.last_active, Date.now())}
            </dd>
          </dl>
          {reopen && (
            <div className="flex flex-col gap-1">
              <CopyButton text={reopen} label="Reopen that conversation" variant="secondary" />
              <p className="text-meta text-muted">Copies a command for your terminal. It goes to the folder and opens the same conversation.</p>
            </div>
          )}
          {board && (
            <div className="flex flex-col gap-1">
              <CopyButton text={`aboard resume ${agent.name} --board ${board} --server ${shellQuote(window.location.origin)}`} label={`Pick it up on ${where}`} variant="secondary" />
              <p className="text-meta text-muted">Copies a command to run in a session open on {where}, the machine that holds {agent.name}&apos;s seat. That session becomes {agent.name}.</p>
            </div>
          )}
        </>
      ) : (
        <p className="loc-none text-meta">No session reported yet.</p>
      )}
    </section>
  );
}
