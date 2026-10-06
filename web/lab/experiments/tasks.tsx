"use client";

// EXPERIMENTAL, lab only: the board's tasks as a kanban inside the board view. Five
// states fold into four columns (Open, In progress, Waiting, Done): claimed and working
// share In progress and the card says which. Each card names its owner and the people
// and agents working with it, so a card is also who works with whom. A task waiting on
// you takes the attention colour, the only colour here; a done card drops its fill.
// Columns stack on a narrow screen. Fed by the scenario's tasks, not the API.

import { useState } from "react";
import type { MemberRef } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import type { ScenarioTask, TaskState } from "../scenario";
import { scenario, useLab } from "../store";
import { Who, ago, useNow } from "./common";

const columns: { key: string; title: string; states: TaskState[] }[] = [
  { key: "open", title: "Open", states: ["open"] },
  { key: "doing", title: "In progress", states: ["claimed", "working"] },
  { key: "waiting", title: "Waiting", states: ["waiting"] },
  { key: "done", title: "Done", states: ["done"] },
];

const doneShown = 4;

export function TaskBoard({ identity }: { identity: (m: MemberRef) => number }) {
  const { snap } = useLab();
  const now = useNow();
  const [allDone, setAllDone] = useState(false);
  // Newest change first in every column.
  const tasks = [...snap.tasks].sort((a, b) => b.t - a.t);
  const shown = columns.filter((c) => tasks.some((t) => c.states.includes(t.state)));
  return (
    <div className="task-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1280px] px-4 sm:px-6">
      <div
        className="task-board grid gap-x-4 gap-y-6 border-t border-rule pt-4 pb-8 sm:grid-cols-2 xl:grid-cols-[repeat(var(--n),minmax(0,1fr))]"
        style={{ "--n": shown.length } as React.CSSProperties}
      >
        {shown.map((c) => {
          const list = tasks.filter((t) => c.states.includes(t.state));
          const done = c.key === "done";
          const visible = done && !allDone ? list.slice(0, doneShown) : list;
          return (
            <section key={c.key} aria-labelledby={`tasks-${c.key}`} className="task-column flex min-w-0 flex-col gap-2">
              <h3 id={`tasks-${c.key}`} className="flex items-baseline gap-2 pb-1 text-meta font-bold text-muted">
                {c.title}
                <span className="font-normal tabular-nums">{list.length}</span>
              </h3>
              <ul className="flex flex-col gap-2">
                {visible.map((t) => (
                  <li key={t.id}>
                    <TaskCard task={t} identity={identity} now={now} />
                  </li>
                ))}
              </ul>
              {done && list.length > doneShown && (
                <button
                  type="button"
                  className="min-h-11 self-start text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
                  onClick={() => setAllDone(!allDone)}
                >
                  {allDone ? "Show fewer" : `Show ${count(list.length - doneShown, "more done task", "more done tasks")}`}
                </button>
              )}
            </section>
          );
        })}
      </div>
      </div>
    </div>
  );
}

function stateLine(t: ScenarioTask): string {
  switch (t.state) {
    case "open":
      return "Open, not picked up";
    case "claimed":
      return "Claimed, not started";
    case "working":
      return "Working";
    case "waiting":
      return "Waiting";
    case "done":
      return "Done";
  }
}

function TaskCard({ task: t, identity, now }: { task: ScenarioTask; identity: (m: MemberRef) => number; now: number }) {
  const mine = t.state === "waiting" && t.waitingOn === scenario.me;
  const done = t.state === "done";
  // Text on the attention fill is always ink, labels included.
  const label = cn("text-meta", mine ? "text-ink" : "text-muted");
  return (
    <article
      className={cn(
        "task-card flex flex-col gap-2 rounded-box border px-3.5 py-3 transition-colors duration-200 ease-out",
        mine ? "border-transparent bg-attention" : done ? "border-rule bg-transparent" : "border-rule bg-surface",
      )}
      data-state={t.state}
      data-needs-you={mine || undefined}
    >
      <h4 className={cn("leading-snug font-bold", done && "font-normal")}>{t.title}</h4>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-2 gap-y-1">
        <dt className={label}>{done ? "Done by" : "Owner"}</dt>
        <dd className="min-w-0">{t.owner ? <Who name={t.owner} identity={identity} /> : <span className={label}>none yet</span>}</dd>
        {(t.with?.length ?? 0) > 0 && (
          <>
            <dt className={label}>With</dt>
            <dd className="flex min-w-0 flex-wrap gap-x-3 gap-y-1">
              {t.with!.map((n) => (
                <Who key={n} name={n} identity={identity} />
              ))}
            </dd>
          </>
        )}
        {t.label && (
          <>
            <dt className={label}>Label</dt>
            <dd>{t.label}</dd>
          </>
        )}
      </dl>
      {t.state === "waiting" && (
        <p className="task-waiting">
          <span className="font-bold">Waiting on {t.waitingOn === scenario.me ? "you" : (t.waitingOn ?? "someone")}</span>
          {t.reason && <>: {t.reason}</>}
        </p>
      )}
      <p className={label}>
        {stateLine(t)} · {ago(t.t, now)}
      </p>
    </article>
  );
}
