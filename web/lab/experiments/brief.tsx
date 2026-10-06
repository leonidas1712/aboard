"use client";

// EXPERIMENTAL, lab only: the brief, a short pinned note on what the board is for right
// now, under the "Now:" line. It says who set it, when, and how many messages have come
// since, which is the freshness cue: past briefStaleAfter its edge turns dashed and it
// says it may be out of date. A long brief shows two lines until opened. With no brief,
// a board with a team on it gets one quiet line; a solo or new board shows nothing.
// Fed by the scenario's steps, not the API.

import { History, Pin } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { count, exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { at, scenario, useLab } from "../store";
import { ago, briefStaleAfter, minutesSince, useNow } from "./common";

export function Brief({ agents }: { agents: number }) {
  const { snap } = useLab();
  const now = useNow();
  const [open, setOpen] = useState(false);
  // Whether the text is longer than its two lines, measured, so "Show all" only shows when it hides something.
  const [long, setLong] = useState(false);
  const text = useRef<HTMLParagraphElement>(null);
  const b = snap.brief;
  useLayoutEffect(() => {
    const el = text.current;
    if (!el) return;
    const measure = () => setLong((was) => (open ? was : el.scrollHeight > el.clientHeight + 1));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [b, open]);
  if (!b) {
    if (scenario.people.length < 2 && agents < 4) return null;
    return <p className="brief-empty pb-2 text-meta text-muted">No brief yet: nothing says what this board is for right now.</p>;
  }
  const stale = minutesSince(b.t, now) > briefStaleAfter;
  const since = snap.messages.filter((m) => m.t > b.t).length;
  return (
    <section
      aria-labelledby="brief-title"
      data-stale={stale || undefined}
      className={cn("brief mb-3 rounded-box border bg-surface px-3.5 py-2.5 animate-fade-in", stale ? "border-dashed border-field-border" : "border-rule")}
      key={`${b.t}:${b.by}`}
    >
      <div className="flex flex-wrap items-baseline gap-x-2">
        <h2 id="brief-title" className="flex items-center gap-1.5 text-meta font-bold text-muted">
          <Pin className="size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden />
          Brief
        </h2>
        <p className="text-meta text-muted" title={`Set ${exactTime(new Date(at(b.t)).toISOString())}`}>
          {b.by === scenario.me ? "you" : b.by}, {ago(b.t, now)}
          {since > 0 && ` · ${count(since, "message", "messages")} since`}
          {stale && (
            <>
              {" · "}
              <History className="inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden />
              {" may be out of date"}
            </>
          )}
        </p>
      </div>
      <p ref={text} className={cn("mt-0.5", !open && "line-clamp-1 sm:line-clamp-2")}>
        {b.text}
      </p>
      {long && (
        <button
          type="button"
          className="min-h-8 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
        >
          {open ? "Show less" : "Show all"}
        </button>
      )}
    </section>
  );
}
