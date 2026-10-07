"use client";

// An agent's mark in the task views is its harness: an icon on the agent's identity
// tint, so Claude Code, Codex and omp agents read apart at a glance. What tells two
// agents of one harness apart sits on top: the tint, and from 24px up a small badge with
// the agent's initials. People keep their initials mark.
//
// The icons are simple original marks that evoke each harness, drawn in currentColor so
// they take every theme: an asterisk for Claude Code, a hexagon with a prompt for Codex,
// a pi for omp, and a prompt for anything else. They are not the vendors' logos.

import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { SenderMark } from "./timeline";
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

export type Marked = { name: string; kind: "agent" | "human"; harness?: string | null };

/**
 * AgentMark is a member's mark: for an agent its harness icon on its identity tint, with
 * an initials badge at the md size; for a person their initials. sm is 20px, md 24px.
 */
export function AgentMark({ member, identity, size = "md" }: { member: Marked; identity: number; size?: "sm" | "md" }) {
  if (member.kind === "human") {
    return (
      <SenderMark
        name={member.name}
        kind="human"
        identity={identity}
        className={size === "sm" ? "size-5 rounded-[5px] text-[10px]" : "size-6 rounded-[6px] text-[11px]"}
      />
    );
  }
  const harness = member.harness && icons[member.harness] ? member.harness : "other";
  return (
    <span
      aria-hidden
      title={`${member.name} · ${harnessName(member.harness) ?? "an agent"}`}
      data-harness={harness}
      className={cn(
        "harness-mark relative flex shrink-0 items-center justify-center select-none",
        size === "sm" ? "size-5 rounded-[5px]" : "size-6 rounded-[6px]",
      )}
      style={{ background: `var(--id-${identity}-bg)`, color: `var(--id-${identity}-fg)` }}
    >
      <svg viewBox="0 0 24 24" className={size === "sm" ? "size-[72%]" : "size-[62%]"} aria-hidden>
        {icons[harness]}
      </svg>
      {size === "md" && (
        <span
          className="harness-badge absolute -right-1 -bottom-1 flex h-[11px] min-w-[11px] items-center justify-center rounded-[4px] border bg-surface px-[2px] text-[8px] leading-none font-bold text-ink tabular-nums"
          style={{ borderColor: `var(--id-${identity}-fg)` }}
        >
          {markOf(member.name, "agent")}
        </span>
      )}
    </span>
  );
}
