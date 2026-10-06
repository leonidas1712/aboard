"use client";

// EXPERIMENTAL, lab only: the left panel. The Inbox on top, once there is an ask, with
// the only solid red count on the page. Then the boards, each with its unread count and
// a red dot when something on it waits on the person; a board with unread messages is
// bold. Then "Find or do anything" (⌘K), a stub of one box to jump anywhere.

import { Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { Board } from "@/app/api";
import { boardLabel } from "@/app/words";
import { cn } from "@/lib/utils";
import { labHref, openTask, scenario, useLab, useUi } from "../store";
import { asksOf } from "./asks";

export function Nav({ current, boards }: { current: string | null; boards: Board[] | null }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const [palette, setPalette] = useState(false);
  const asks = asksOf(snap, answered);
  const everAsked = asks.length > 0 || Object.keys(answered).length > 0;
  const inInbox = current === null;
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette(true);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  const row = "flex min-h-10 items-center gap-2 rounded-control px-2.5 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-selected";
  return (
    <div className="lab-nav -mx-2.5 flex flex-col gap-4">
      {everAsked && (
        <a href={labHref({ inbox: "1", board: null, view: null, task: null, artifact: null })} aria-current={inInbox ? "page" : undefined} className={cn(row, "font-bold", inInbox && "bg-selected")}>
          <span className="flex-1">Inbox</span>
          {asks.length > 0 && (
            <span className="inbox-count min-w-6 rounded-full bg-[var(--needs)] px-1.5 text-center text-meta font-bold text-on-ink tabular-nums" title={`${asks.length} waiting on you`}>
              {asks.length}
            </span>
          )}
        </a>
      )}
      <section aria-label="Boards" className="flex flex-col gap-0.5">
        {everAsked && inInbox && <h3 className="px-2.5 pb-1 text-meta font-bold text-muted">Boards</h3>}
        {boards === null ? (
          <div className="mx-2.5 h-10 animate-pulse rounded-control bg-selected" />
        ) : (
          boards
            .filter((b) => b.lifecycle !== "archived")
            .map((b) => {
              const waiting = asks.filter((a) => a.board === b.name).length;
              const unread = b.unread ?? 0;
              const here = b.name === current;
              return (
                <a
                  key={b.id}
                  href={labHref({ board: b.name, inbox: null, view: null, task: null, artifact: null })}
                  aria-current={here ? "page" : undefined}
                  className={cn(row, here && "bg-selected")}
                >
                  <span className={cn("min-w-0 flex-1 truncate", (unread > 0 || here) && "font-bold")}>{boardLabel(b)}</span>
                  {(waiting > 0 || unread > 0) && (
                    <span className="flex shrink-0 items-center gap-1.5 rounded-full bg-selected px-1.5 text-meta text-muted tabular-nums">
                      {waiting > 0 && <span className="size-1.5 rounded-full bg-[var(--needs)]" aria-label={`${waiting} waiting on you`} />}
                      {unread > 0 && <span title={`${unread} unread`}>{unread}</span>}
                    </span>
                  )}
                </a>
              );
            })
        )}
      </section>
      <button type="button" onClick={() => setPalette(true)} className={cn(row, "text-meta text-muted")}>
        <kbd className="rounded-[4px] border border-rule px-1 font-sans text-[12px]">⌘K</kbd>
        Find or do anything
      </button>
      {palette && <Palette boards={boards ?? []} onClose={() => setPalette(false)} />}
    </div>
  );
}

/** Palette is a stub of ⌘K: it finds boards and tasks by name, and goes there. */
function Palette({ boards, onClose }: { boards: Board[]; onClose: () => void }) {
  const { snap } = useLab();
  const [q, setQ] = useState("");
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  const items = [
    { label: "Go to the Inbox", go: () => (window.location.href = labHref({ inbox: "1", board: null })) },
    ...boards.map((b) => ({ label: `Open ${boardLabel(b)}`, go: () => (window.location.href = labHref({ board: b.name, inbox: null })) })),
    ...snap.tasks.map((t) => ({
      label: `Open ${t.id} ${t.title}`,
      go: () => {
        if (new URLSearchParams(window.location.search).get("board") === scenario.board.name) openTask(t.id);
        else window.location.href = labHref({ board: scenario.board.name, inbox: null, task: t.id });
      },
    })),
  ].filter((i) => i.label.toLowerCase().includes(q.toLowerCase()));
  return (
    <div className="fixed inset-0 z-[60] flex items-start justify-center bg-[color-mix(in_oklab,var(--background)_60%,transparent)] px-4 pt-[15vh]" onClick={onClose}>
      <div
        role="dialog"
        aria-label="Find or do anything"
        className="flex w-full max-w-[560px] flex-col overflow-hidden rounded-box border border-field-border bg-surface"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
          if (e.key === "Enter" && items[0]) {
            items[0].go();
            onClose();
          }
        }}
      >
        <label className="flex items-center gap-2 border-b border-rule px-3.5">
          <Search className="size-4 text-muted" strokeWidth={1.75} aria-hidden />
          <span className="sr-only">Find or do anything</span>
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find a board or a task, or type a command" className="min-h-12 flex-1 bg-transparent text-body text-ink outline-none" />
        </label>
        <ul className="max-h-[50vh] overflow-y-auto py-1">
          {items.slice(0, 12).map((i) => (
            <li key={i.label}>
              <button
                type="button"
                className="w-full px-3.5 py-2 text-left hover:bg-selected"
                onClick={() => {
                  i.go();
                  onClose();
                }}
              >
                {i.label}
              </button>
            </li>
          ))}
        </ul>
        <p className="border-t border-rule px-3.5 py-2 text-meta text-muted">A stub: commands such as &ldquo;hold codex&rdquo; would send a message, like every other control.</p>
      </div>
    </div>
  );
}
