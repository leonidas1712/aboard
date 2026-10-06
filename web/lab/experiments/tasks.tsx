"use client";

// EXPERIMENTAL, lab only: the Tasks view, the board's work laid out by what the person
// would act on: Needs you, In progress, Waiting, Not picked up. Done folds away. Each
// card lists everyone on it, owner first, with what they are doing right now, so a card
// is also who works with whom. Free agents (idle or disconnected, on no task) sit beside
// the work nobody has taken. A card opens its task in the side panel. Red only marks
// what waits on your answer; amber only what is late or idle.

import { useState } from "react";
import type { Member } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import { type ScenarioTask, threadsOf } from "../scenario";
import { openTask, scenario, useLab, useUi } from "../store";
import { Ask } from "./ask";
import { type Ask as AskItem, asksOf, statusOf, toneClass } from "./asks";
import { Mark, active, ago, useNow } from "./common";

/** needsYou is true for a task that waits on the person, or that an unanswered ask blocks. */
export function needsYou(t: ScenarioTask, asks: AskItem[]): boolean {
  return (t.state === "waiting" && t.waitingOn === scenario.me) || asks.some((a) => a.task === t.id && !a.ahead && a.board === scenario.board.name);
}

export function TaskBoard({ agents }: { agents: Member[] }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const [done, setDone] = useState(false);
  const asks = asksOf(snap, answered);
  const tasks = [...snap.tasks].sort((a, b) => b.t - a.t);
  const live = tasks.filter((t) => t.state !== "done");
  const columns = [
    { key: "needs", title: "Needs you", list: live.filter((t) => needsYou(t, asks)) },
    { key: "doing", title: "In progress", list: live.filter((t) => !needsYou(t, asks) && (t.state === "working" || t.state === "claimed")) },
    { key: "waiting", title: "Waiting", list: live.filter((t) => !needsYou(t, asks) && t.state === "waiting") },
    { key: "open", title: "Not picked up", list: live.filter((t) => !needsYou(t, asks) && t.state === "open") },
  ];
  const busy = new Set(snap.tasks.filter(active).flatMap((t) => [t.owner, ...(t.with ?? [])]));
  const free = agents.filter((a) => !busy.has(a.name) && (a.presence === "idle" || a.presence === "no_session"));
  const finished = tasks.filter((t) => t.state === "done");
  return (
    <div className="task-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1280px] px-4 pt-2 pb-10 sm:px-6">
        <div className="task-board grid gap-x-4 gap-y-6 sm:grid-cols-2 xl:grid-cols-4">
          {columns.map((c) => (
            <section key={c.key} aria-labelledby={`tasks-${c.key}`} className="task-column flex min-w-0 flex-col gap-2">
              <h3 id={`tasks-${c.key}`} className="flex items-center gap-2 pb-1 text-meta font-bold text-ink">
                <span
                  aria-hidden
                  className={cn(
                    "size-2 rounded-full",
                    c.key === "needs" ? (c.list.length > 0 ? "bg-[var(--needs)]" : "border border-muted") : c.key === "doing" ? "border-2 border-accent" : "border border-dashed border-muted",
                  )}
                />
                {c.title}
                <span className="font-normal text-muted tabular-nums">{c.list.length}</span>
              </h3>
              <ul className="flex flex-col gap-2">
                {c.list.map((t) => (
                  <li key={t.id}>
                    <TaskCard task={t} asks={asks} now={now} needs={c.key === "needs"} />
                  </li>
                ))}
              </ul>
              {c.key === "open" && free.length > 0 && (
                <div className="mt-2 flex flex-col gap-2 border-t border-rule pt-3">
                  <h4 className="text-meta font-bold text-muted">Free agents</h4>
                  <ul className="flex flex-col gap-2">
                    {free.map((a) => {
                      const s = statusOf(snap, a.name, asks, now);
                      return (
                        <li key={a.id} className="grid grid-cols-[20px_minmax(0,1fr)] gap-x-2">
                          <Mark name={a.name} />
                          <span className="flex flex-col">
                            <span>{a.name}</span>
                            <span className={cn("text-meta", toneClass[s.tone])}>{s.text}</span>
                          </span>
                        </li>
                      );
                    })}
                  </ul>
                </div>
              )}
            </section>
          ))}
        </div>
        {finished.length > 0 && (
          <section aria-label="Done" className="mt-6 flex flex-col gap-2">
            <button type="button" aria-expanded={done} onClick={() => setDone(!done)} className="min-h-9 self-start text-meta text-muted hover:text-ink hover:underline">
              {count(finished.length, "task", "tasks")} done · {done ? "hide" : "show"}
            </button>
            {done && (
              <ul className="grid animate-fade-in gap-2 sm:grid-cols-2 xl:grid-cols-4">
                {finished.map((t) => (
                  <li key={t.id}>
                    <TaskCard task={t} asks={asks} now={now} needs={false} />
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
      </div>
    </div>
  );
}

function TaskCard({ task: t, asks, now, needs }: { task: ScenarioTask; asks: AskItem[]; now: number; needs: boolean }) {
  const { snap } = useLab();
  const ask = asks.find((a) => a.task === t.id && !a.ahead);
  const people = [t.owner, ...(t.with ?? [])].filter((n): n is string => !!n);
  const threads = threadsOf(snap.messages, t.id).length;
  const files = snap.artifacts.filter((a) => a.task === t.id).length;
  const done = t.state === "done";
  let line: string | null = null;
  if (needs) line = ask ? `Waiting on you: ${ask.question}` : `Waiting on you: ${t.reason ?? ""}`;
  else if (t.state === "waiting") line = `Waiting on ${t.waitingOn}${t.reason ? `: ${t.reason}` : ""}`;
  else if (t.state === "open") line = `No owner · opened ${ago(t.t, now)}`;
  else if (t.state === "claimed") line = "Claimed, not started";
  const steward = scenario.steward;
  return (
    <article
      className={cn("task-card flex flex-col gap-2.5 rounded-box border border-rule px-3.5 py-3 transition-colors duration-[140ms] ease-out hover:border-field-border", done ? "bg-transparent" : "bg-surface")}
      data-task={t.id}
      data-needs-you={needs || undefined}
    >
      <button type="button" onClick={() => openTask(t.id)} className="flex flex-col gap-0.5 text-left" title={`Open ${t.id}`}>
        <span className="text-meta text-muted tabular-nums">{t.id}</span>
        <span className={cn("leading-snug", done ? "font-normal" : "font-bold")}>{t.title}</span>
      </button>
      {line && <p className={cn("text-meta", needs ? "text-ink" : "text-muted")}>{line}</p>}
      {people.length > 0 && (
        <ul className="flex flex-col gap-2 border-t border-rule pt-2.5">
          {people.map((n, i) => {
            const human = scenario.people.some((p) => p.name === n);
            const s = human ? { text: n === scenario.me ? "that's you" : "on it", tone: "quiet" as const } : statusOf(snap, n, asks, now);
            return (
              <li key={n} className="grid grid-cols-[20px_minmax(0,1fr)] gap-x-2">
                <Mark name={n} />
                <span className="flex min-w-0 flex-col">
                  <span>
                    {n}
                    {i === 0 && t.owner === n && <span className="text-meta text-muted"> owner</span>}
                  </span>
                  {!done && <span className={cn("line-clamp-2 text-meta", toneClass[s.tone])}>{s.text}</span>}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {t.state === "open" && steward && (
        <Ask label={`Ask ${steward} to assign it`} to={steward} text={`${t.id} (${t.title}) has no owner. Please assign it, and say who and why.`} />
      )}
      <p className="border-t border-rule pt-2 text-meta text-muted">
        {[threads > 0 && count(threads, "thread", "threads"), files > 0 && count(files, "artifact", "artifacts"), ago(t.t, now)].filter(Boolean).join(" · ")}
      </p>
    </article>
  );
}
