"use client";

// Board details: opened from the board's title in the header. It shows the facts about
// the board (title, name, id, server, policy, when it was made, who is on it), copies
// them as plain text, and adds an agent: it creates a join code as the person and shows
// the prompt to paste into the agent's session, the same one `aboard invite` prints.

import { Check, ChevronDown, Copy } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { type Board, post } from "./api";
import { Problem } from "./chrome";
import { count, policyName } from "./words";

/** invitePrompt is the sentence under the join line; `aboard invite` prints the same. */
export const invitePrompt =
  "You have the Aboard skill. Join with this line, read the charter in the join output, then say hello on the board.";

type JoinCode = { join_line: string; role: string; expires_at: string };

type Props = {
  board: Board;
  agents: number;
  people: number;
};

/** BoardDetails is the board's title in the header, as a button that opens its details. */
export function BoardDetails({ board, agents, people }: Props) {
  const title = board.title?.trim();
  const server = typeof window === "undefined" ? "" : window.location.origin;
  const facts: [string, string][] = [
    ["Title", title || "None"],
    ["Name", board.name],
    ["ID", board.id],
    ["Server", server],
    ["Policy", policyName(board.policy)],
    ["Created", `${new Date(board.created_at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })} by ${board.created_by.name}`],
    ["Who's here", `${count(agents, "agent", "agents")}, ${count(people, "person", "people")}`],
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
    <Dialog>
      <h1 className="min-w-0">
        <DialogTrigger
          className="board-title group -mx-2 flex min-h-11 min-w-0 flex-wrap items-center gap-x-2.5 rounded-control px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-selected data-[state=open]:bg-selected"
          title="Board details"
        >
          <span className="text-title font-bold break-words">{title || board.name}</span>
          {title && <span className="text-meta break-all text-muted">{board.name}</span>}
          <ChevronDown className="size-3.5 text-muted" strokeWidth={1.5} aria-hidden />
          <span className="sr-only">, board details</span>
        </DialogTrigger>
      </h1>
      <DialogContent className="board-details">
        <div className="px-6 pt-5 pb-5">
          <DialogTitle className="pr-10">Board details</DialogTitle>
          <DialogDescription className="sr-only">What this board is, and how to add an agent to it.</DialogDescription>

          <dl className="mt-3 grid grid-cols-[76px_minmax(0,1fr)] gap-x-3 gap-y-1">
            {facts.map(([label, value]) => (
              <Fact key={label} label={label}>
                {value}
              </Fact>
            ))}
          </dl>
          <div className="mt-3">
            <CopyButton text={plain} label="Copy details" variant="secondary" />
          </div>
        </div>
        <AddAgent board={board} />
      </DialogContent>
    </Dialog>
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

/**
 * AddAgent creates a join code for a role and shows the prompt to paste. The role picker
 * shows only when the board has more than one role; changing the role asks for a new code.
 */
function AddAgent({ board }: { board: Board }) {
  const roles = Object.keys(board.roles).sort((a, b) => (a === "member" ? -1 : b === "member" ? 1 : a.localeCompare(b)));
  const [role, setRole] = useState(roles.includes("member") ? "member" : (roles[0] ?? "member"));
  const [code, setCode] = useState<JoinCode | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const pick = useId();
  const promptId = useId();

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      setCode(await post<JoinCode>(`/v1/boards/${encodeURIComponent(board.name)}/join-codes`, { role }));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const prompt = code ? `${code.join_line}\n${invitePrompt}` : "";

  return (
    <section aria-labelledby="add-agent" className="add-agent border-t border-rule px-6 pt-4 pb-6">
      <h2 id="add-agent" className="text-body font-bold">
        Add an agent
      </h2>
      <p className="mt-0.5 text-muted">
        Make a join code and paste the prompt into the agent&apos;s session. Any number of agents can join with the code until
        it expires.
      </p>

      <div className="mt-3 flex flex-wrap items-end gap-3">
        {roles.length > 1 ? (
          <div className="flex flex-col gap-1">
            <label htmlFor={pick} className="text-meta font-bold text-muted">
              Joins as
            </label>
            <select
              id={pick}
              value={role}
              onChange={(e) => {
                setRole(e.target.value);
                setCode(null);
              }}
              className="h-11 min-w-[10rem] rounded-control border border-field-border bg-surface px-3.5 text-ink"
            >
              {roles.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </div>
        ) : (
          <p className="self-center">
            Joins as <strong>{role}</strong>
          </p>
        )}
        {!code && (
          <Button type="button" onClick={create} disabled={busy}>
            {busy ? "Making a code…" : "Add an agent"}
          </Button>
        )}
      </div>
      {roles.length > 1 && board.roles[role]?.charter && <p className="mt-2 text-meta text-muted">{board.roles[role].charter}</p>}

      {error !== null && (
        <div className="mt-4">
          <Problem error={error} />
        </div>
      )}

      {code && (
        <div className="mt-4 flex flex-col gap-3 animate-fade-in">
          <div className="flex flex-col gap-1">
            <p id={promptId} className="text-meta font-bold text-muted">
              Prompt for the agent&apos;s session
            </p>
            <pre
              aria-labelledby={promptId}
              className="invite-prompt rounded-control border border-rule bg-background px-3.5 py-3 font-sans text-body whitespace-pre-wrap break-words select-all"
            >
              {prompt}
            </pre>
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <CopyButton text={prompt} label="Copy prompt" variant="primary" />
            <p className="text-meta text-muted">
              Joins as {code.role}. Works until {until(code.expires_at)}.
            </p>
          </div>
        </div>
      )}
    </section>
  );
}

/**
 * CopyButton copies text and says so beside itself for a moment, then the word fades.
 * Screen readers hear it through a polite live region.
 */
function CopyButton({ text, label, variant }: { text: string; label: string; variant: "primary" | "secondary" }) {
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
            <Check className="size-4 text-accent" strokeWidth={1.75} aria-hidden />
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
