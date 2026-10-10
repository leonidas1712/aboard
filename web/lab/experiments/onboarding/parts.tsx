"use client";

// EXPERIMENTAL, lab only: the small pieces the onboarding mock shares: a command to
// copy, a labelled field grid, a neutral warning box, a done line, and a request's
// progress as a short list of steps.

import { Check, Circle, Copy, LoaderCircle, TriangleAlert } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/** Command is an exact command to run in a terminal, with a copy button inside its box. */
export function Command({ label, command, note }: { label: string; command: string; note?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div className="ob-command flex flex-col gap-1.5">
      <p className="text-meta font-bold text-muted">{label}</p>
      <div className="flex items-start gap-1 rounded-control border border-rule bg-background py-1 pr-1 pl-3">
        <code className="min-w-0 flex-1 py-1.5 font-mono text-[13px] leading-[1.55] break-words text-ink select-all">
          {/* Lines break between words only, never inside --server or an id. */}
          {command.split(" ").map((w, i) => (
            <span key={i}>
              {i > 0 && " "}
              <span className="whitespace-nowrap">{w}</span>
            </span>
          ))}
        </code>
        <button
          type="button"
          onClick={copy}
          aria-label={copied ? "Copied" : "Copy the command"}
          title="Copy"
          className="tap inline-flex size-9 shrink-0 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
        >
          {copied ? <Check className="size-4 text-accent-strong" strokeWidth={1.75} aria-hidden /> : <Copy className="size-4" strokeWidth={1.5} aria-hidden />}
        </button>
      </div>
      <span aria-live="polite" className="sr-only">
        {copied ? "Copied" : ""}
      </span>
      {note && <p className="text-meta text-muted">{note}</p>}
    </div>
  );
}

/** Fields is the board view's two-column grid: a label in Meta, then its value. */
export function Fields({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[76px_minmax(0,1fr)] gap-x-3 gap-y-2">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="pt-px text-meta text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Warning is a neutral box: warnings are never the accent, which means "act on this". */
export function Warning({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("ob-warning flex gap-2.5 rounded-box border border-field-border bg-selected px-3.5 py-3 text-ink", className)}>
      <TriangleAlert className="mt-0.5 size-4 shrink-0" strokeWidth={1.75} aria-label="Warning" />
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  );
}

/** Done is a short line saying what happened, with an accent-strong check. */
export function Done({ children, muted = false }: { children: ReactNode; muted?: boolean }) {
  return (
    <p role="status" aria-live="polite" className={cn("ob-done flex items-start gap-2 animate-fade-in", muted && "text-muted")}>
      {muted ? <Circle className="mt-1 size-3.5 shrink-0 text-muted" strokeWidth={2} aria-hidden /> : <Check className="mt-0.5 size-[18px] shrink-0 text-accent-strong" strokeWidth={2} aria-hidden />}
      <span className="min-w-0">{children}</span>
    </p>
  );
}

export type Stage = { label: string; state: "done" | "now" | "later" };

/**
 * Stages is a request's progress: done steps with a check, the current one with a
 * quiet turning ring (still under reduced motion: a ring), later ones as an empty ring.
 */
export function Stages({ stages, label }: { stages: Stage[]; label: string }) {
  return (
    <ol aria-label={label} className="ob-stages flex flex-col gap-2">
      {stages.map((s) => (
        <li key={s.label} className={cn("flex items-start gap-2.5", s.state === "later" && "text-muted", s.state === "now" && "font-bold")} aria-current={s.state === "now" ? "step" : undefined}>
          <span className="mt-0.5 inline-flex size-[18px] shrink-0 items-center justify-center" aria-hidden>
            {s.state === "done" ? (
              <Check className="size-[18px] text-accent-strong" strokeWidth={2} />
            ) : s.state === "now" ? (
              <LoaderCircle className="size-4 text-ink motion-safe:animate-spin motion-safe:[animation-duration:2.4s]" strokeWidth={1.75} />
            ) : (
              <Circle className="size-3.5 text-faint" strokeWidth={1.75} />
            )}
          </span>
          <span className="min-w-0">
            {s.label}
            <span className="sr-only">{s.state === "done" ? ", done" : s.state === "now" ? ", in progress" : ", not yet"}</span>
          </span>
        </li>
      ))}
    </ol>
  );
}
