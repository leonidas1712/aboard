"use client";

// EXPERIMENTAL, lab only: the brief, under the "Now:" line. The Now line is counted
// facts; the brief is an agent's writing (the steward's), so it always carries a byline:
// who wrote it, when, and how many messages have come since. Closed, it shows that and
// where things stand in a sentence or two. Open, it shows Goal, Approach, Who's doing
// what, Blocked on, Next and Sources, with task ids linking to their tasks, and three
// actions: ask the steward to update it, see what changed since, open it as a file.
// No steward, no brief. Fed by the scenario's steps, not the API.

import { History } from "lucide-react";
import { useState } from "react";
import { count, exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { at, openArtifact, openThread, scenario, showView, useLab } from "../store";
import { Ask } from "./ask";
import { ago, briefStaleAfter, minutesSince, useNow } from "./common";
import { Ids } from "./text";

export function Brief() {
  const { snap } = useLab();
  const now = useNow();
  const [open, setOpen] = useState(false);
  const b = snap.brief;
  if (!b) return null;
  const stale = minutesSince(b.t, now) > briefStaleAfter;
  const since = snap.messages.filter((m) => m.t > b.t).length;
  const sections: [string, string][] = [
    ["Goal", b.goal],
    ["Approach", b.approach],
    ["Who's doing what", b.who],
    ["Blocked on", b.blocked],
    ["Next", b.next],
    ["Sources", b.sources],
  ];
  return (
    <section aria-label="Brief" data-open={open || undefined} className={cn("brief mb-3 rounded-box border bg-surface px-4 py-3", stale ? "border-dashed border-field-border" : "border-rule")}>
      <div className="flex flex-wrap items-baseline gap-x-2">
        <h2 className="text-meta font-bold text-ink">Brief</h2>
        <p className="min-w-0 flex-1 text-meta text-muted" title={`Updated ${exactTime(new Date(at(b.t)).toISOString())}`}>
          by {b.by === scenario.me ? "you" : b.by} · updated {ago(b.t, now)} · {count(since, "message", "messages")} since
          {stale && (
            <span className="text-[var(--late)]">
              {" · "}
              <History className="inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden /> may be out of date
            </span>
          )}
        </p>
        <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="min-h-8 text-meta text-link hover:underline">
          {open ? "Show less" : "Show full brief"}
        </button>
      </div>
      <p className={cn("mt-1 text-now", !open && "line-clamp-2 sm:line-clamp-none")}>
        <Ids text={b.summary} />
      </p>
      {open && (
        <div className="animate-fade-in">
          <dl className="mt-3 grid gap-x-8 gap-y-3 border-t border-rule pt-3 sm:grid-cols-2">
            {sections.map(([title, text]) => (
              <div key={title} className="flex flex-col gap-0.5">
                <dt className="text-meta font-bold text-muted">{title}</dt>
                <dd>
                  <Ids text={text} />
                </dd>
              </div>
            ))}
          </dl>
          <p className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-meta">
            {scenario.steward && (
              <Ask label={`Ask ${scenario.steward} to update it`} to={scenario.steward} text={`Please bring the brief up to date: ${count(since, "message", "messages")} since your last update. Cite the threads and tasks you draw on.`} />
            )}
            <button type="button" className="min-h-8 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => {
                const first = snap.messages.find((m) => m.t > b.t);
                if (first) openThread(first.id);
                else showView("conversation");
              }}
            >
              See what changed since
            </button>
            <button type="button" className="min-h-8 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openArtifact("brief")}>
              Open as artifact
            </button>
          </p>
        </div>
      )}
    </section>
  );
}
