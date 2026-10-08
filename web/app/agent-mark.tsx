"use client";

// A member's mark, wherever one shows (GEN-14): an agent's is its harness, an icon on the
// agent's identity tint, so Claude Code, Codex and omp agents read apart at a glance.
// What tells two agents of one harness apart sits on top: the tint, and from 24px up a
// small badge with the agent's initials. People keep their initials on their tint.
//
// The icons are simple original marks that evoke each harness, drawn in currentColor so
// they take every theme: an asterisk for Claude Code, a hexagon with a prompt for Codex,
// a pi for omp, and a prompt for anything else. They are not the vendors' logos.

import { lab } from "aboard-lab";
import { type ReactNode, createContext, useContext } from "react";
import { cn } from "@/lib/utils";
import { StatusDot, type Tone } from "./status";
import { harnessName, markOf } from "./words";

const icons: Record<string, ReactNode> = {
  // Four strokes through the centre: an eight-armed asterisk.
  "claude-code": (
    <g stroke="currentColor" strokeWidth="2.6" strokeLinecap="round">
      <path d="M12 3.5v17" />
      <path d="M3.5 12h17" />
      <path d="M6 6l12 12" />
      <path d="M18 6L6 18" />
    </g>
  ),
  // A hexagon with a prompt inside.
  codex: (
    <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round">
      <path d="M12 2.8l8 4.6v9.2l-8 4.6-8-4.6V7.4z" />
      <path d="M8.6 9.6l2.8 2.4-2.8 2.4M12.6 14.6h3" />
    </g>
  ),
  // A pi: a bar and two legs.
  omp: (
    <g fill="currentColor">
      <rect x="4" y="5.5" width="16" height="3.2" rx="1.2" />
      <rect x="7.4" y="7" width="3.2" height="12" rx="1.2" />
      <path d="M13.4 7h3.2v9.4c0 .9.4 1.3 1.3 1.3h1.1V20h-1.8c-2.5 0-3.8-1.2-3.8-3.6z" />
    </g>
  ),
  // Any other harness: a terminal prompt.
  other: (
    <g fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 8l4 4-4 4M12 17h6" />
    </g>
  ),
};

/**
 * Harnesses is each agent's harness by name, from the board's member list, for marks
 * drawn from a reference that doesn't carry one (a file's author, a brief's writer).
 */
const Harnesses = createContext<ReadonlyMap<string, string | null>>(new Map());
export const HarnessProvider = Harnesses.Provider;

/** px is a mark's size from its size-N class (Tailwind's 4px steps), 32px when none is given. */
function px(className: string | undefined): number {
  const n = className?.match(/(?:^|\s)size-(\d+)(?:\s|$)/)?.[1];
  return n ? Number(n) * 4 : 32;
}

/**
 * SenderMark is a member's mark at any size, set by a size-N class: for an agent its
 * harness tile, for a person their one or two letters on their identity colour. harness
 * is the agent's when the reference carries it; else the board's member list gives it.
 */
export function SenderMark({
  name,
  kind,
  identity,
  harness,
  className,
}: {
  name: string;
  kind: "agent" | "human";
  identity: number;
  harness?: string | null;
  className?: string;
}) {
  const known = useContext(Harnesses).get(name);
  // Only the UI lab draws an agent's mark another way (lab-seam.ts).
  if (lab?.AgentMark && kind === "agent") return <lab.AgentMark name={name} identity={identity} className={className} />;
  if (kind === "agent") return <HarnessTile name={name} harness={harness ?? known ?? null} identity={identity} className={className} />;
  const mark = markOf(name, kind);
  const size = px(className);
  return (
    <span
      aria-hidden
      className={cn(
        "sender-mark flex size-8 shrink-0 items-center justify-center rounded-control font-bold tracking-[0.02em] select-none before:content-[attr(data-mark)]",
        mark.length > 1 ? "text-[13px]" : "text-body",
        className,
        size <= 20 && "rounded-[5px] text-[10px]",
        size === 24 && "rounded-[6px] text-[11px]",
      )}
      style={{ background: `var(--id-${identity}-bg)`, color: `var(--id-${identity}-fg)` }}
      // The letters are drawn by CSS, so they stay out of the text around the mark.
      data-mark={mark}
    />
  );
}

/**
 * HarnessTile is an agent's harness icon on its identity tint, sized by a size-N class,
 * with its initials in a badge from 24px up.
 */
function HarnessTile({ name, harness, identity, className }: { name: string; harness: string | null; identity: number; className?: string }) {
  const known = harness && icons[harness] ? harness : "other";
  const size = px(className);
  const badge = size >= 24;
  return (
    <span
      aria-hidden
      title={`${name} · ${harnessName(harness) ?? "an agent"}`}
      data-harness={known}
      className={cn(
        "harness-mark sender-mark relative flex size-8 shrink-0 items-center justify-center rounded-control select-none",
        className,
        size <= 20 && "rounded-[5px]",
        size === 24 && "rounded-[6px]",
      )}
      style={{ background: `var(--id-${identity}-bg)`, color: `var(--id-${identity}-fg)` }}
    >
      <svg viewBox="0 0 24 24" className={size <= 20 ? "size-[72%]" : "size-[60%]"} aria-hidden>
        {icons[known]}
      </svg>
      {badge && (
        <span
          className={cn(
            "harness-badge absolute flex items-center justify-center border bg-surface px-[2px] leading-none font-bold text-ink tabular-nums before:content-[attr(data-mark)]",
            size >= 28 ? "-right-1 -bottom-1 h-[13px] min-w-[13px] rounded-[4px] text-[9px]" : "-right-1 -bottom-1 h-[11px] min-w-[11px] rounded-[4px] text-[8px]",
          )}
          style={{ borderColor: `var(--id-${identity}-fg)` }}
          data-mark={markOf(name, "agent")}
        />
      )}
    </span>
  );
}

export type Marked = { name: string; kind: "agent" | "human"; harness?: string | null };

/**
 * AgentMark is a member's mark in the compact lists (Work, task cards, the agent list),
 * with the agent's status in its corner when given. sm is 20px, md 24px.
 */
export function AgentMark({ member, identity, size = "md", status }: { member: Marked; identity: number; size?: "sm" | "md"; status?: Tone }) {
  const mark = <SenderMark name={member.name} kind={member.kind} harness={member.harness} identity={identity} className={size === "sm" ? "size-5" : "size-6"} />;
  if (member.kind !== "agent" || !status) return mark;
  // The status sits on the mark's top-right corner, cut out of the surface behind it
  // (--mark-ring, which a panel sets), clear of the initials badge.
  return (
    <span className="agent-mark-status relative inline-flex shrink-0" data-status={status}>
      {mark}
      <span aria-hidden className="absolute -top-1 -right-1 flex size-[13px] items-center justify-center rounded-full bg-[var(--mark-ring,var(--surface))]">
        <StatusDot tone={status} className="size-[9px]" />
      </span>
    </span>
  );
}
