"use client";

// The board view's sidebars: about this board on the left (your boards, the charter,
// the rules, the record) and who's here on the right.

import { ChevronRight, ShieldAlert, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";
import type { Board, Member } from "./api";
import { SectionHeading } from "./chrome";
import { problemText } from "./record";
import type { RecordCheck } from "./use-board";
import { harnessName, presenceWords, rules } from "./words";

export function AboutBoard({ board, boards, record }: { board: Board | null; boards: Board[] | null; record: RecordCheck }) {
  return (
    <div className="flex flex-col gap-6">
      <nav aria-labelledby="your-boards">
        <SectionHeading id="your-boards">Your boards</SectionHeading>
        <ul className="flex flex-col gap-0.5">
          {(boards ?? []).map((b) => {
            const current = b.name === board?.name;
            return (
              <li key={b.id}>
                <a
                  href={`/?board=${encodeURIComponent(b.name)}`}
                  aria-current={current ? "page" : undefined}
                  className={cn(
                    "flex min-h-11 items-center rounded-control px-2.5 py-2 break-all text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-selected",
                    current && "bg-selected font-bold",
                  )}
                >
                  {b.name}
                </a>
              </li>
            );
          })}
        </ul>
      </nav>

      {board?.charter && (
        <section aria-labelledby="charter">
          <SectionHeading id="charter">What this board is for</SectionHeading>
          <p className="whitespace-pre-line">{board.charter}</p>
        </section>
      )}

      {board && (
        <section aria-labelledby="rules">
          <SectionHeading id="rules">Rules</SectionHeading>
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
        </section>
      )}

      <RecordLine record={record} />
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
  return (
    <p
      className="record flex items-center gap-2 text-meta text-muted"
      title="This browser checked every event's hash against the one before it, the same check as aboard audit verify."
    >
      {record.state === "verified" ? (
        <>
          <ShieldCheck className="size-4 shrink-0 text-accent" strokeWidth={1.5} aria-label="Verified" />
          Record verified · {record.count} {record.count === 1 ? "event" : "events"}
        </>
      ) : (
        "Checking the record…"
      )}
    </p>
  );
}

export function WhosHere({ board, members }: { board: Board | null; members: Member[] | null }) {
  const agents = (members ?? []).filter((m) => m.kind === "agent");
  const people = (members ?? []).filter((m) => m.kind === "human");
  const owners = new Set(agents.map((a) => a.owner));
  const showOwner = owners.size > 1;
  return (
    <div className="flex flex-col gap-6">
      <section aria-labelledby="whos-here">
        <SectionHeading id="whos-here">Who&apos;s here</SectionHeading>
        {members === null ? (
          <div className="h-16 animate-pulse rounded-control bg-selected motion-reduce:animate-none" aria-label="Loading" />
        ) : agents.length === 0 ? (
          <p>No agents yet.</p>
        ) : (
          <ul className="flex flex-col gap-4">
            {agents.map((a) => (
              <AgentItem key={a.id} agent={a} roleCharter={board?.roles[a.role ?? ""]?.charter} showOwner={showOwner} />
            ))}
          </ul>
        )}
      </section>
      {people.length > 1 && (
        <section aria-labelledby="people">
          <SectionHeading id="people">People</SectionHeading>
          <ul className="flex flex-col gap-1">
            {people.map((p) => (
              <li key={p.id} className="flex justify-between gap-3">
                <span className="font-bold">{p.name}</span>
                <span className="text-meta text-muted">{p.access === "admin" ? "Admin" : "Member"}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function AgentItem({ agent, roleCharter, showOwner }: { agent: Member; roleCharter?: string; showOwner: boolean }) {
  const presence = agent.presence ?? "no_session";
  const waiting = presence === "waiting";
  return (
    <li
      className={cn("agent transition-colors duration-200 ease-out", waiting && "-mx-3 rounded-box bg-attention px-3 py-2.5")}
      data-agent={agent.name}
    >
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-bold break-all">{agent.name}</span>
        <CrossFade
          value={presenceWords[presence]}
          className={cn("text-meta", waiting ? "text-ink" : presence === "working" ? "text-ink" : "text-muted")}
        />
      </div>
      {waiting && <p className="text-meta">Its session is waiting for you, such as a permission prompt.</p>}
      <dl className="mt-1 grid grid-cols-[76px_minmax(0,1fr)] gap-x-2 gap-y-0.5">
        {showOwner && (
          <>
            <dt className={cn("text-meta", waiting ? "text-ink" : "text-muted")}>Owner</dt>
            <dd>{agent.owner}</dd>
          </>
        )}
        <dt className={cn("pt-px text-meta", waiting ? "text-ink" : "text-muted")}>Role</dt>
        <dd>
          <Collapsible>
            <CollapsibleTrigger className="group -ml-1 inline-flex items-center gap-1 rounded-[6px] px-1 text-ink hover:bg-selected">
              <ChevronRight
                className="size-3.5 transition-transform duration-200 ease-out group-data-[state=open]:rotate-90"
                strokeWidth={1.5}
                aria-hidden
              />
              {agent.role}
            </CollapsibleTrigger>
            <CollapsibleContent className="animate-fade-in">
              <p className="mt-1 text-meta">{roleCharter?.trim() || "This role has no description."}</p>
            </CollapsibleContent>
          </Collapsible>
        </dd>
        {agent.harness && (
          <>
            <dt className={cn("text-meta", waiting ? "text-ink" : "text-muted")}>Harness</dt>
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
