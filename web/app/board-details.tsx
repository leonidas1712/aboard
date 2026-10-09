"use client";

// The facts about a board and adding an agent to it, both in the board panel. Details
// shows the board's name, id, server, policy and when it was made, copies them as plain
// text, and says whether the record verifies. AddAgent shows the prompt to paste into
// the agent's session: on the local server a join code it creates as the person, the
// same prompt `aboard invite` prints; on a team server `aboard join --board`, which a
// person's own agents use with no code.

import { Check, Copy, ShieldAlert, ShieldCheck, UserPlus, X } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { type Board, get, post } from "./api";
import { Problem } from "./chrome";
import { problemText } from "./record";
import type { RecordCheck } from "./use-board";
import { policyName } from "./words";

/** invitePrompt is the sentence under the join line; `aboard invite` prints the same. */
export const invitePrompt =
  "You have the Aboard skill. Join with this line, read the charter in the join output, then say hello on the board.";

/** joinPrompt is the sentence under the join command a team server's prompt gives. */
export const joinPrompt =
  "You have the Aboard skill. Run this command to join, read the charter in the join output, then say hello on the board.";

/**
 * joinCommand is the command a person's own agent runs to join a board on a team
 * server: no code, since the agent joins through its person's machine.
 */
export function joinCommand(board: string, role: string, server: string): string {
  return `aboard join --board ${board}${role === "member" ? "" : ` --role ${role}`} --server ${server}`;
}

type JoinCode = { join_line: string; role: string; expires_at: string };

type DetailsProps = {
  board: Board;
  agents: number;
  people: number;
  record: RecordCheck;
};

/** Details lists the facts about a board, copies them, and shows the record check. */
export function Details({ board, agents, people, record }: DetailsProps) {
  const title = board.title?.trim();
  const server = typeof window === "undefined" ? "" : window.location.origin;
  const facts: [string, string][] = [
    ["Name", board.name],
    ["ID", board.id],
    ["Server", server],
    ["Policy", policyName(board.policy)],
    ["Created", `${new Date(board.created_at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })} by ${board.created_by.name}`],
  ];
  const plain = [
    `Board: ${title ? `${title} (${board.name})` : board.name}`,
    `ID: ${board.id}`,
    `Server: ${server}`,
    `Policy: ${board.policy.preset}`,
    `Created: ${board.created_at} by ${board.created_by.name}`,
    `Agents: ${agents}`,
    `People: ${people}`,
  ].join("\n");

  return (
    <div className="board-facts flex flex-col gap-3">
      <dl className="grid grid-cols-[64px_minmax(0,1fr)] gap-x-3 gap-y-1">
        {facts.map(([label, value]) => (
          <Fact key={label} label={label}>
            {value}
          </Fact>
        ))}
      </dl>
      <CopyButton text={plain} label="Copy details" variant="secondary" />
      <RecordLine record={record} />
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-meta leading-[1.6] text-muted">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </>
  );
}

/** RecordLine says whether the browser's check of the board's hash chain passed. */
function RecordLine({ record }: { record: RecordCheck }) {
  if (record.state === "failed") {
    return (
      <div role="alert" className="record rounded-box border border-field-border bg-selected px-3.5 py-3 text-ink">
        <p className="flex items-center gap-2 font-bold">
          <ShieldAlert className="size-4 shrink-0" strokeWidth={1.5} aria-hidden />
          Record doesn&apos;t verify
        </p>
        <p className="mt-1">{problemText(record.problem)}</p>
        <p className="mt-1">
          Run <code>aboard audit verify</code> in a terminal for the details.
        </p>
      </div>
    );
  }
  if (record.state === "checking") return <p className="record text-meta text-muted">Checking the record…</p>;
  const n = record.count;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className="record -mx-2 flex min-h-9 pointer-coarse:min-h-11 w-fit items-center gap-2 rounded-[6px] px-2 text-left text-meta whitespace-nowrap text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
        >
          <ShieldCheck className="size-4 shrink-0 text-accent-strong" strokeWidth={1.5} aria-hidden />
          Record verified · {n} {n === 1 ? "event" : "events"}
        </button>
      </TooltipTrigger>
      <TooltipContent side="top" align="start" className="record-explained">
        Every event on this board is linked to the one before it by a hash. Your browser just re-checked all {n} and found none
        changed. <code>aboard audit verify</code> runs the same check.
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * AddAgent is a button that shows the prompt to paste into an agent's session, for the
 * board's member role (or its first role). On the local server it creates a join code;
 * on a team server the person's own agents need none, so the prompt is the
 * `aboard join --board` command naming the server. When the board has more than one
 * role, a picker beside the prompt changes the role, which makes a new code.
 */
export function AddAgent({ board }: { board: Board }) {
  const roles = Object.keys(board.roles).sort((a, b) => (a === "member" ? -1 : b === "member" ? 1 : a.localeCompare(b)));
  const [role, setRole] = useState(roles.includes("member") ? "member" : (roles[0] ?? "member"));
  const [open, setOpen] = useState(false);
  const [code, setCode] = useState<JoinCode | null>(null);
  const [team, setTeam] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const pick = useId();
  const promptId = useId();
  const opener = useRef<HTMLButtonElement>(null);

  const create = async (r: string) => {
    setOpen(true);
    setBusy(true);
    setError(null);
    setCode(null);
    try {
      // A team server's people add their own agents with no code; only the local
      // server's prompt needs one.
      const isTeam = team ?? (await get<{ mode: "local" | "team" }>("/v1/info")).mode === "team";
      setTeam(isTeam);
      if (!isTeam) setCode(await post<JoinCode>(`/v1/boards/${encodeURIComponent(board.name)}/join-codes`, { role: r }));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const close = () => {
    setOpen(false);
    setCode(null);
    setError(null);
    requestAnimationFrame(() => opener.current?.focus());
  };
  const server = typeof window === "undefined" ? "" : window.location.origin;
  const prompt = code
    ? `${code.join_line}\n${invitePrompt}`
    : team && !busy && error === null
      ? `${joinCommand(board.name, role, server)}\n${joinPrompt}`
      : "";

  if (!open) {
    return (
      <Button ref={opener} type="button" variant="secondary" className="add-agent w-full" onClick={() => create(role)}>
        <UserPlus strokeWidth={1.5} aria-hidden />
        Add an agent
      </Button>
    );
  }

  return (
    <section aria-labelledby={`${promptId}-title`} className="add-agent flex flex-col gap-3 rounded-box bg-surface px-3.5 pt-2 pb-3.5 animate-fade-in">
      <div className="flex items-center justify-between gap-2">
        <h4 id={`${promptId}-title`} className="font-bold">
          Add an agent
        </h4>
        <button
          type="button"
          onClick={close}
          aria-label="Close Add an agent"
          title="Close"
          className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
        >
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <p className="-mt-2 text-meta text-muted">
        {team
          ? "Paste the prompt into a session of your own agent. It joins as your agent, through this computer's login; no code needed."
          : "Paste the prompt into the agent's session. Any number of agents can join with it until it expires."}
      </p>

      {roles.length > 1 && (
        <div className="flex flex-col gap-1">
          <label htmlFor={pick} className="text-meta font-bold text-muted">
            Joins as
          </label>
          <select
            id={pick}
            value={role}
            disabled={busy}
            onChange={(e) => {
              setRole(e.target.value);
              void create(e.target.value);
            }}
            className="h-11 w-full rounded-control border border-field-border bg-surface px-3 text-ink"
          >
            {roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          {board.roles[role]?.charter && <p className="text-meta text-muted">{board.roles[role].charter}</p>}
        </div>
      )}

      {error !== null && <Problem error={error} />}

      {busy && (
        <p className="text-meta text-muted" role="status">
          {team === false ? "Making a join code…" : "Checking the server…"}
        </p>
      )}

      {prompt && (
        <div className="flex flex-col gap-3 animate-fade-in">
          <div className="flex flex-col gap-1">
            <p id={promptId} className="text-meta font-bold text-muted">
              Prompt for the agent&apos;s session
            </p>
            <pre
              aria-labelledby={promptId}
              className="invite-prompt rounded-control border border-rule bg-background px-3 py-2.5 font-sans text-body whitespace-pre-wrap break-words select-all"
            >
              {prompt}
            </pre>
          </div>
          <CopyButton text={prompt} label="Copy prompt" variant="primary" />
          <p className="text-meta text-muted">
            {code ? (
              <>
                Joins as {code.role}. Works until {until(code.expires_at)}.
              </>
            ) : (
              <>
                Joins as {role}, as your agent. A teammate on the board adds their own agents the same way.
              </>
            )}
          </p>
        </div>
      )}
    </section>
  );
}

/**
 * CopyButton copies text and says so beside itself for a moment, then the word fades.
 * Screen readers hear it through a polite live region.
 */
export function CopyButton({ text, label, variant }: { text: string; label: string; variant: "primary" | "secondary" }) {
  const [copied, setCopied] = useState<"shown" | "fading" | null>(null);
  const [failed, setFailed] = useState(false);
  const timers = useRef<number[]>([]);
  useEffect(() => () => timers.current.forEach((t) => window.clearTimeout(t)), []);

  const copy = async () => {
    timers.current.forEach((t) => window.clearTimeout(t));
    try {
      await navigator.clipboard.writeText(text);
      setFailed(false);
      setCopied("shown");
      timers.current = [window.setTimeout(() => setCopied("fading"), 1600), window.setTimeout(() => setCopied(null), 1800)];
    } catch {
      setCopied(null);
      setFailed(true);
    }
  };

  return (
    <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
      <Button type="button" variant={variant} onClick={copy}>
        <Copy strokeWidth={1.5} aria-hidden />
        {label}
      </Button>
      <span aria-live="polite" className="copy-status text-meta">
        {copied && (
          <span className={`inline-flex items-center gap-1.5 ${copied === "fading" ? "animate-fade-out" : "animate-fade-in"}`}>
            <Check className="size-4 text-accent-strong" strokeWidth={1.75} aria-hidden />
            Copied
          </span>
        )}
        {failed && <span>This browser didn&apos;t allow copying. Select the text and copy it yourself.</span>}
      </span>
    </span>
  );
}

/** until says when a code stops working: a time today, or a weekday and a time. */
function until(at: string): string {
  const d = new Date(at);
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString(undefined, { weekday: "long" })} ${time}`;
}
