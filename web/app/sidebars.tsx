"use client";

// The board view's sidebars: about this board on the left (your boards, the charter,
// the rules, the record) and who's here on the right.

import { ChevronDown, ChevronRight, CircleQuestionMark, ShieldAlert, ShieldCheck } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { Board, Member } from "./api";
import { usePref } from "./prefs";
import { problemText } from "./record";
import type { RecordCheck } from "./use-board";
import { boardLabel, charterBlocks, harnessName, presenceWords, rules } from "./words";

/** Help is a small "?" that explains a heading in a tooltip, on hover or keyboard focus. */
function Help({ topic, children }: { topic: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className="help inline-flex size-7 shrink-0 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink"
          aria-label={`About ${topic}`}
        >
          <CircleQuestionMark className="size-3.5" strokeWidth={1.75} aria-hidden />
        </button>
      </TooltipTrigger>
      <TooltipContent side="right" align="start" className="help-text">
        {children}
      </TooltipContent>
    </Tooltip>
  );
}

/** Section is a sidebar section whose heading opens and closes it; the browser remembers which. */
function Section({ id, title, help, children }: { id: string; title: string; help?: ReactNode; children: ReactNode }) {
  const [open, setOpen] = usePref(`aboard.open.${id}`, true);
  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <section aria-labelledby={id} className="flex flex-col">
        <div className="flex items-center gap-1">
          <h3 id={id} className="min-w-0 flex-1 text-meta font-bold text-muted">
            <CollapsibleTrigger className="group -ml-2 flex min-h-9 w-[calc(100%+0.5rem)] items-center gap-1.5 rounded-[6px] px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink">
              <ChevronRight
                className="size-3.5 shrink-0 transition-transform duration-200 ease-out group-data-[state=open]:rotate-90"
                strokeWidth={1.75}
                aria-hidden
              />
              {title}
            </CollapsibleTrigger>
          </h3>
          {help && <Help topic={title}>{help}</Help>}
        </div>
        <CollapsibleContent className="pt-1 animate-fade-in">{children}</CollapsibleContent>
      </section>
    </Collapsible>
  );
}

/** Charter shows a board's charter as its author wrote it: paragraphs and lists. */
function Charter({ text }: { text: string }) {
  return (
    <div className="charter flex flex-col gap-2 rounded-box bg-surface px-3.5 py-3">
      {charterBlocks(text).map((b, i) =>
        b.kind === "paragraph" ? (
          <p key={i}>{b.text}</p>
        ) : (
          <ul key={i} className="flex list-disc flex-col gap-1 pl-5 marker:text-muted">
            {b.items.map((item, j) => (
              <li key={j}>{item}</li>
            ))}
          </ul>
        ),
      )}
    </div>
  );
}

export function AboutBoard({ board, boards, record }: { board: Board | null; boards: Board[] | null; record: RecordCheck }) {
  return (
    <div className="flex flex-1 flex-col gap-4">
      <Section id="your-boards" title="Your boards">
        <nav aria-labelledby="your-boards">
          <ul className="-mx-2.5 flex flex-col gap-0.5">
            {(boards ?? []).map((b) => {
              const current = b.name === board?.name;
              return (
                <li key={b.id}>
                  <a
                    href={`/?board=${encodeURIComponent(b.name)}`}
                    aria-current={current ? "page" : undefined}
                    className={cn(
                      "flex min-h-11 flex-col justify-center rounded-control px-2.5 py-1.5 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-selected",
                      current && "bg-selected",
                    )}
                  >
                    <span className={cn("break-words", current && "font-bold")}>{boardLabel(b)}</span>
                    {b.title && <span className="text-meta break-all text-muted">{b.name}</span>}
                  </a>
                </li>
              );
            })}
          </ul>
        </nav>
      </Section>

      {board?.charter && (
        <Section id="charter" title="Charter" help="Written by this board's admins. Every agent reads it when it joins and follows it as guidance.">
          <Charter text={board.charter} />
        </Section>
      )}

      {board && (
        <Section
          id="rules"
          title="Rules Aboard enforces"
          help="Set by the board's policy and checked by the server on every message. Agents can't break them."
        >
          <div className="flex flex-col gap-2">
            {rules(board).map((r) => (
              <p key={r}>{r}</p>
            ))}
            {board.policy.preset === "starter" && (
              <p>
                This board is on the starter policy. Before adding more agents or people, run{" "}
                <code>aboard board policy recommended</code>.
              </p>
            )}
          </div>
        </Section>
      )}

      <div className="mt-auto pt-4">
        <RecordLine record={record} />
      </div>
    </div>
  );
}

function RecordLine({ record }: { record: RecordCheck }) {
  if (record.state === "failed") {
    return (
      <div role="alert" className="record rounded-box bg-attention px-3.5 py-3 text-ink">
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
          className="record -mx-2 flex min-h-9 w-fit items-center gap-2 rounded-[6px] px-2 text-left text-meta whitespace-nowrap text-muted transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink"
        >
          <ShieldCheck className="size-4 shrink-0 text-accent" strokeWidth={1.5} aria-hidden />
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

type WhosHereProps = {
  board: Board | null;
  members: Member[] | null;
  /** me is the person's own name. */
  me: string | null;
  /** from is the member the timeline is filtered to, if any. */
  from: string | undefined;
  /** onPick filters the timeline to a member, or clears that filter when it's already set. */
  onPick: (name: string) => void;
};

export function WhosHere({ board, members, me, from, onPick }: WhosHereProps) {
  const agents = (members ?? []).filter((m) => m.kind === "agent");
  const people = (members ?? []).filter((m) => m.kind === "human");
  const owners = new Set(agents.map((a) => a.owner));
  const showOwner = owners.size > 1;
  return (
    <div className="flex flex-col gap-6">
      {members === null ? (
        <div className="h-16 animate-pulse rounded-control bg-selected motion-reduce:animate-none" aria-label="Loading" />
      ) : agents.length === 0 ? (
        <p>No agents yet.</p>
      ) : (
        <ul className="flex flex-col gap-5" aria-label="Agents">
          {agents.map((a) => (
            <AgentItem
              key={a.id}
              agent={a}
              roleCharter={board?.roles[a.role ?? ""]?.charter}
              showOwner={showOwner}
              picked={from === a.name}
              onPick={() => onPick(a.name)}
            />
          ))}
        </ul>
      )}
      {people.length > 1 && (
        <section aria-labelledby="people">
          <h3 id="people" className="mb-1 text-meta font-bold text-muted">
            People
          </h3>
          <ul className="flex flex-col gap-1">
            {people.map((p) => (
              <li key={p.id} className="flex items-baseline justify-between gap-3">
                <NameButton name={p.name} picked={from === p.name} onPick={() => onPick(p.name)}>
                  {p.name}
                  {p.name === me && <span className="font-normal text-muted"> (you)</span>}
                </NameButton>
                <span className="text-meta text-muted">{p.access === "admin" ? "Admin" : "Member"}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

/** NameButton is a member's name that filters the timeline to them. */
function NameButton({ name, picked, onPick, children }: { name: string; picked: boolean; onPick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onPick}
      aria-pressed={picked}
      title={picked ? `Show everyone's messages` : `Show only ${name}'s messages`}
      className={cn(
        "member-filter -mx-2 min-h-9 min-w-0 rounded-[6px] px-2 text-left font-bold break-all text-ink transition-colors duration-[140ms] ease-out hover:bg-selected",
        picked && "bg-selected underline decoration-accent decoration-2 underline-offset-[5px]",
      )}
    >
      {children}
    </button>
  );
}

function AgentItem({
  agent,
  roleCharter,
  showOwner,
  picked,
  onPick,
}: {
  agent: Member;
  roleCharter?: string;
  showOwner: boolean;
  picked: boolean;
  onPick: () => void;
}) {
  const presence = agent.presence ?? "no_session";
  const waiting = presence === "waiting";
  const label = cn("text-meta", waiting ? "text-ink" : "text-muted");
  return (
    <li className={cn("agent transition-colors duration-200 ease-out", waiting && "-mx-3 rounded-box bg-attention px-3 py-2.5")} data-agent={agent.name}>
      <div className="flex items-center justify-between gap-3">
        <NameButton name={agent.name} picked={picked} onPick={onPick}>
          {agent.name}
        </NameButton>
        <span className="flex shrink-0 items-center gap-1.5">
          <span
            aria-hidden
            className={cn(
              "presence-dot size-2 rounded-full transition-colors duration-200 ease-out",
              presence === "working" ? "bg-accent" : presence === "no_session" ? "border border-muted" : waiting ? "bg-ink" : "bg-muted",
            )}
          />
          <CrossFade value={presenceWords[presence]} className={cn("text-meta", presence === "working" || waiting ? "text-ink" : "text-muted")} />
        </span>
      </div>
      {waiting && <p className="text-meta">Its session is waiting for you, such as a permission prompt.</p>}
      <dl className="mt-1 grid grid-cols-[72px_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1">
        {showOwner && (
          <>
            <dt className={label}>Owner</dt>
            <dd>{agent.owner}</dd>
          </>
        )}
        <dt className={label}>Role</dt>
        <dd>
          <Collapsible>
            <CollapsibleTrigger className="group inline-flex items-center gap-1 rounded-[6px] text-ink hover:underline hover:decoration-1 hover:underline-offset-[3px]">
              {agent.role}
              <ChevronDown
                className="size-3.5 text-muted transition-transform duration-200 ease-out group-data-[state=open]:rotate-180"
                strokeWidth={1.5}
                aria-hidden
              />
              <span className="sr-only">: what this role does</span>
            </CollapsibleTrigger>
            <CollapsibleContent className="animate-fade-in">
              <p className="mt-1 text-meta">{roleCharter?.trim() || "This role has no description."}</p>
            </CollapsibleContent>
          </Collapsible>
        </dd>
        {agent.harness && (
          <>
            <dt className={label}>Harness</dt>
            <dd>{harnessName(agent.harness)}</dd>
          </>
        )}
      </dl>
    </li>
  );
}

/** CrossFade shows a word that fades into the next one when it changes. */
function CrossFade({ value, className }: { value: string; className?: string }) {
  const [shown, setShown] = useState(value);
  const [leaving, setLeaving] = useState<string | null>(null);
  useEffect(() => {
    if (value === shown) return;
    setLeaving(shown);
    setShown(value);
  }, [value, shown]);
  return (
    <span className={cn("presence grid text-right", className)} aria-live="polite">
      {leaving !== null && (
        <span aria-hidden className="animate-fade-out [grid-area:1/1]" onAnimationEnd={() => setLeaving(null)}>
          {leaving}
        </span>
      )}
      <span key={shown} className={cn("[grid-area:1/1]", leaving !== null && "animate-fade-in")}>
        {shown}
      </span>
    </span>
  );
}
