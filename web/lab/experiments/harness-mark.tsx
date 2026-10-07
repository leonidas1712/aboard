"use client";

// EXPERIMENTAL, lab only: an agent's avatar is its harness's icon, so Claude Code, Codex
// and omp agents read apart at a glance. What tells two agents of one harness apart
// sits on top of it: the agent's identity colour as the tile's tint, and a small badge
// with its initials in a corner (from 24px up; below that the tint and the name beside
// it do the work). People keep their initials mark.
//
// The icons are simple original marks that evoke each harness, drawn in currentColor so
// they take every theme: an asterisk for Claude Code, a hexagon with a prompt for Codex,
// a pi for omp, a prompt for anything else. They are not the vendors' logos. Official
// marks need each owner's permission first (see DIRECTION.md).

import type { ReactNode } from "react";
import { markOf } from "@/app/words";
import { cn } from "@/lib/utils";
import { scenario } from "../store";

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

/** harnessOf is an agent's harness, from the scenario, else guessed from a name like claude-3. */
export function harnessOf(name: string): string {
  const known = scenario.agents.find((a) => a.name === name)?.harness;
  if (known) return known;
  if (/^claude/.test(name)) return "claude-code";
  if (/^codex/.test(name)) return "codex";
  if (/^omp/.test(name)) return "omp";
  return "other";
}

const harnessLabel: Record<string, string> = { "claude-code": "Claude Code", codex: "Codex", omp: "omp", other: "an agent" };

/** HarnessMark is the lab's AgentMark slot: the harness icon on the identity tint, with an initials badge. */
export function HarnessMark({ name, identity, className }: { name: string; identity: number; className?: string }) {
  const harness = harnessOf(name);
  const size = Number(className?.match(/size-(\d+)/)?.[1] ?? 8);
  const badge = size >= 6;
  const initials = markOf(name, "agent");
  return (
    <span
      aria-hidden
      title={`${name} · ${harnessLabel[harness] ?? harness}`}
      data-harness={harness}
      className={cn("harness-mark relative flex size-8 shrink-0 items-center justify-center rounded-control select-none", className)}
      style={{ background: `var(--id-${identity}-bg)`, color: `var(--id-${identity}-fg)` }}
    >
      <svg viewBox="0 0 24 24" className={size >= 6 ? "size-[62%]" : "size-[72%]"} aria-hidden>
        {icons[harness] ?? icons.other}
      </svg>
      {badge && (
        <span
          className={cn(
            "absolute -right-1 -bottom-1 flex items-center justify-center rounded-[4px] border bg-surface px-[2px] leading-none font-bold tabular-nums",
            size >= 8 ? "h-[13px] min-w-[13px] text-[9px]" : "h-[11px] min-w-[11px] text-[8px]",
          )}
          style={{ borderColor: `var(--id-${identity}-fg)`, color: "var(--ink)" }}
        >
          {initials}
        </span>
      )}
    </span>
  );
}
