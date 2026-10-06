"use client";

// EXPERIMENTAL, lab only: the brief, the board's maintained summary (what the project
// is, what's going on, who does what, blockers, next), under the "Now:" line. Closed,
// it is one row: its summary line and how fresh it is. Open, it shows the whole brief,
// formatted, in place. Its freshness is facts, not a guess: who updated it, when, and
// what happened since (messages, tasks done, decisions). Past briefStaleAfter its edge
// turns dashed and it says it may be out of date. It is also the pinned maintained
// artifact in the Artifacts tab. A board with a team and no brief gets one quiet line;
// a solo or new board shows nothing. Fed by the scenario's steps, not the API.

import { ChevronRight, History, Pin } from "lucide-react";
import { useState } from "react";
import { count, exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { at, openArtifact, openTask, scenario, useLab } from "../store";
import { ago, briefStaleAfter, minutesSince, useNow } from "./common";
import { Ask } from "./ask";
import { Markdown } from "./markdown";

/** freshness says who last updated the brief, when, and what happened since, as facts. */
export function freshness(by: string, t: number, now: number, s: ReturnType<typeof useLab>["snap"]): { lead: string; since: string } {
  const messages = s.messages.filter((m) => m.t > t).length;
  const done = s.tasks.filter((x) => x.state === "done" && x.t > t).length;
  const decisions = s.messages.filter((m) => m.decision && m.t > t).length;
  const parts = [
    messages > 0 && count(messages, "message", "messages"),
    done > 0 && count(done, "task done", "tasks done"),
    decisions > 0 && count(decisions, "decision", "decisions"),
  ].filter(Boolean);
  return {
    lead: `Updated ${ago(t, now)} by ${by === scenario.me ? "you" : by}${by === scenario.steward ? ", steward" : ""}`,
    since: parts.length ? `since then ${parts.join(", ")}` : "nothing new since",
  };
}

export function Brief({ agents }: { agents: number }) {
  const { snap } = useLab();
  const now = useNow();
  const [open, setOpen] = useState(false);
  const b = snap.brief;
  if (!b) {
    if (scenario.people.length < 2 && agents < 4) return null;
    return <p className="brief-empty pb-2 text-meta text-muted">No brief yet: nothing says what this board is for right now.</p>;
  }
  const stale = minutesSince(b.t, now) > briefStaleAfter;
  const f = freshness(b.by, b.t, now, snap);
  return (
    <section
      aria-label="Brief"
      data-stale={stale || undefined}
      data-open={open || undefined}
      className={cn("brief mb-3 rounded-box border bg-surface animate-fade-in", stale ? "border-dashed border-field-border" : "border-rule")}
      key={`${b.t}:${b.by}`}
    >
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="group flex min-h-11 w-full items-start gap-2 rounded-box px-3.5 py-2.5 text-left transition-colors duration-[140ms] ease-out hover:bg-selected"
      >
        <span className="flex w-[52px] shrink-0 items-center gap-1 pt-px text-meta font-bold text-muted" title="Written by an agent, the board's steward, and kept current by it.">
          <Pin className="size-3.5 shrink-0" strokeWidth={1.75} aria-hidden />
          Brief
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-x-2 sm:flex-row sm:items-baseline">
          <span className={cn("min-w-0", !open && "truncate")}>{b.summary}</span>
          <span className="shrink-0 text-meta whitespace-nowrap text-muted" title={`Updated ${exactTime(new Date(at(b.t)).toISOString())}`}>
            {stale && <History className="mr-1 inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-label="May be out of date" />}
            {f.lead}
          </span>
        </span>
        <ChevronRight
          className="mt-[5px] size-3.5 shrink-0 text-muted transition-transform duration-200 ease-out group-aria-expanded:rotate-90"
          strokeWidth={1.75}
          aria-hidden
        />
      </button>
      {open && (
        <div className="brief-body quiet-scroll flex max-h-[min(45vh,420px)] animate-fade-in flex-col gap-3 overflow-y-auto border-t border-rule px-3.5 pt-2.5 pb-3">
          <p className="text-meta text-muted">
            {f.lead}; {f.since}
            {stale && ". It may be out of date"}. Version {snap.briefVersion}.
          </p>
          <Markdown text={b.body} refs={(r) => (snap.tasks.some((t) => t.id === r.toLowerCase()) ? () => openTask(r.toLowerCase()) : null)} />
          <p className="flex flex-wrap gap-x-4 text-meta">
            <button type="button" className="min-h-8 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openArtifact("brief")}>
              Open brief.md in Files
            </button>
            {scenario.steward && (
              <Ask
                label={stale ? "Ask the steward to refresh it" : "Ask the steward to update it"}
                to={scenario.steward}
                text={`Please bring the brief up to date: ${f.since}. Cite the threads and tasks you draw on.`}
              />
            )}
          </p>
        </div>
      )}
    </section>
  );
}
