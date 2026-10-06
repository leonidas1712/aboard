"use client";

// EXPERIMENTAL, lab only: the brief, under the "Now:" line. The Now line is counted
// facts; the brief is writing, so it always carries a byline: who wrote this version,
// when, and how many messages have come since. Anyone on the board may edit it, people
// and agents; the steward is an informal role the charter can name, who keeps it
// current, and the record keeps every version and who wrote it. Closed, it shows where
// things stand in a sentence or two. Open, it shows Goal, Approach, Who's doing what,
// Blocked on, Next and Sources, with task ids linking to their tasks, and the actions:
// Edit, ask the steward to update it, see what changed since, open it as a file.
// Fed by the scenario's steps, not the API.

import { History } from "lucide-react";
import { useState } from "react";
import { count, exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { briefMarkdown } from "../scenario";
import { at, openArtifact, openThread, scenario, showView, useLab } from "../store";
import { Ask } from "./ask";
import { ago, briefStaleAfter, minutesSince, useNow } from "./common";
import { Markdown } from "./markdown";
import { Ids } from "./text";

/** Edited is a version of the brief the person wrote here, which the lab keeps until reload. */
type Edited = { md: string; at: number };

const link = "min-h-8 text-link underline decoration-1 underline-offset-[3px] hover:no-underline";

export function Brief() {
  const { snap } = useLab();
  const now = useNow();
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [edited, setEdited] = useState<Edited | null>(null);
  const [draft, setDraft] = useState("");
  const b = snap.brief;
  if (!b) return null;
  const version = snap.briefVersion + (edited ? 1 : 0);
  const by = edited ? scenario.me : b.by;
  const atMs = edited ? edited.at : at(b.t);
  const stale = !edited && minutesSince(b.t, now) > briefStaleAfter;
  const since = edited ? 0 : snap.messages.filter((m) => m.t > b.t).length;
  // An edited brief's summary is its first paragraph that isn't a heading.
  const summary = edited ? (edited.md.split("\n").find((l) => l.trim() && !l.startsWith("#")) ?? "") : b.summary;
  const sections: [string, string][] = [
    ["Goal", b.goal],
    ["Approach", b.approach],
    ["Who's doing what", b.who],
    ["Blocked on", b.blocked],
    ["Next", b.next],
    ["Sources", b.sources],
  ];
  const startEditing = () => {
    setDraft(edited?.md ?? briefMarkdown(b));
    setEditing(true);
    setOpen(true);
  };
  return (
    <section
      aria-label="Brief"
      data-open={open || undefined}
      data-editing={editing || undefined}
      className={cn("brief mb-3 rounded-box border bg-surface px-4 py-3", stale ? "border-dashed border-field-border" : "border-rule")}
    >
      <div className="flex flex-wrap items-baseline gap-x-2">
        <h2 className="text-meta font-bold text-ink">Brief</h2>
        <p className="min-w-0 flex-1 text-meta text-muted" title={`Version ${version}, ${exactTime(new Date(atMs).toISOString())}`}>
          by {by === scenario.me ? "you" : by} · v{version} · updated {edited ? "just now" : ago(b.t, now)} · {count(since, "message", "messages")} since
          {stale && (
            <span>
              {" · "}
              <History className="inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden /> may be out of date
            </span>
          )}
        </p>
        {!editing && (
          <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="min-h-8 text-meta text-link hover:underline">
            {open ? "Show less" : "Show full brief"}
          </button>
        )}
      </div>
      {editing ? (
        <form
          className="mt-2 flex animate-fade-in flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            setEdited({ md: draft, at: Date.now() });
            setEditing(false);
          }}
        >
          <label htmlFor="brief-edit" className="text-meta text-muted">
            Markdown. Saving makes version {snap.briefVersion + (edited ? 2 : 1)}; the record keeps every version and who wrote it.
          </label>
          <textarea
            id="brief-edit"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={14}
            className="w-full resize-y rounded-control border border-field-border bg-background px-3 py-2 font-mono text-meta text-ink"
            autoFocus
          />
          <div className="flex items-center gap-3">
            <button type="submit" className="min-h-9 rounded-control bg-ink px-3.5 font-bold text-on-ink">
              Save version {snap.briefVersion + (edited ? 2 : 1)}
            </button>
            <button type="button" className="text-meta text-link hover:underline" onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <>
          <p className={cn("mt-1 text-now", !open && "line-clamp-2 sm:line-clamp-none")}>
            <Ids text={summary} />
          </p>
          {open && (
            <div className="animate-fade-in">
              {edited ? (
                <div className="mt-3 border-t border-rule pt-3">
                  <Markdown text={edited.md} />
                </div>
              ) : (
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
              )}
              <p className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-meta">
                <button type="button" className={link} onClick={startEditing}>
                  Edit
                </button>
                {scenario.steward && (
                  <Ask
                    label={`Ask the steward (${scenario.steward}) to update it`}
                    to={scenario.steward}
                    text={`Please bring the brief up to date: ${count(since, "message", "messages")} since v${version}. Cite the threads and tasks you draw on.`}
                  />
                )}
                <button
                  type="button"
                  className={link}
                  onClick={() => {
                    const first = snap.messages.find((m) => m.t > b.t);
                    if (first && !edited) openThread(first.id);
                    else showView("conversation");
                  }}
                >
                  See what changed since
                </button>
                <button type="button" className={link} onClick={() => openArtifact("brief")}>
                  Open as artifact
                </button>
              </p>
            </div>
          )}
        </>
      )}
    </section>
  );
}
