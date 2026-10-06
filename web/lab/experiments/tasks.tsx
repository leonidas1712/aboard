"use client";

// EXPERIMENTAL, lab only: the Tasks view, the board's work laid out by what the person
// would act on: Needs you, In progress, Blocked, Not picked up. Done folds away. Blocked
// is never set by hand: a task is Blocked while it has an open blocking ask, and Needs
// you when that ask is to you. Each card lists everyone on it, owner first (the agent
// responsible for it, not whoever opened it), with what they are doing right now, so a
// card is also who works with whom. Free agents (idle or disconnected, on no task) sit
// beside the work nobody has taken. The whole card opens its task in the side panel;
// its inner controls keep their own action.

import { Clock, MessagesSquare } from "lucide-react";
import { useState } from "react";
import type { Member } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import type { ScenarioTask } from "../scenario";
import { current, openAgent, openArtifact, openTask, scenario, uiAnswered, useLab, useUi } from "../store";
import { FileIcon } from "./artifacts";
import { Ask } from "./ask";
import { type Ask as AskItem, type Block, agentState, asksOf, blocksOf, statusOf, toneClass } from "./asks";
import { OwnerLabel, linkCount } from "./chips";
import { Mark, active, ago, useNow } from "./common";

/** needsYou is true for a task an open ask to the person blocks. */
export function needsYou(t: ScenarioTask, _asks: AskItem[], blocks: Block[] = currentBlocks()): boolean {
  return blocks.some((b) => b.task === t.id && b.on === scenario.me);
}

/** blockerOf is the open ask that blocks a task on someone other than the person, if any. */
export function blockerOf(t: ScenarioTask, blocks: Block[] = currentBlocks()): Block | undefined {
  return blocks.find((b) => b.task === t.id && b.on !== scenario.me);
}

function currentBlocks(): Block[] {
  const { snap, answered } = { ...current(), answered: uiAnswered() };
  return blocksOf(snap, answered);
}

export function TaskBoard({ agents }: { agents: Member[] }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const [done, setDone] = useState(false);
  const asks = asksOf(snap, answered);
  const blocks = blocksOf(snap, answered);
  const tasks = [...snap.tasks].sort((a, b) => b.t - a.t);
  const live = tasks.filter((t) => t.state !== "done");
  const needs = (t: ScenarioTask) => needsYou(t, asks, blocks);
  const blocked = (t: ScenarioTask) => !needs(t) && !!blockerOf(t, blocks);
  const columns = [
    { key: "needs", title: "Needs you", list: live.filter(needs) },
    { key: "doing", title: "In progress", list: live.filter((t) => !needs(t) && !blocked(t) && t.state !== "open") },
    { key: "blocked", title: "Blocked", list: live.filter(blocked) },
    { key: "open", title: "Not picked up", list: live.filter((t) => !needs(t) && !blocked(t) && t.state === "open") },
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
                {c.key !== "needs" && (
                  <span aria-hidden className={cn("size-2 rounded-full", c.key === "doing" ? "border-2 border-accent" : c.key === "blocked" ? "bg-ink" : "border border-dashed border-muted")} />
                )}
                {c.key === "needs" && c.list.length > 0 ? (
                  <span className="rounded-[4px] bg-attention px-1.5 text-ink">
                    {c.title} <span className="tabular-nums">{c.list.length}</span>
                  </span>
                ) : (
                  <>
                    {c.title}
                    <span className="font-normal text-muted tabular-nums">{c.list.length}</span>
                  </>
                )}
              </h3>
              <ul className="flex flex-col gap-2">
                {c.list.map((t) => (
                  <li key={t.id}>
                    <TaskCard task={t} asks={asks} now={now} needs={c.key === "needs"} blocker={c.key === "blocked" ? blockerOf(t, blocks) : undefined} />
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
                            <span className={cn("text-meta", toneClass[s.tone])}>
                              {s.tone === "late" && <Clock className="mr-1 inline size-3 -translate-y-px" strokeWidth={2} aria-label="Late or idle" />}
                              {s.text}
                            </span>
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

function TaskCard({ task: t, asks, now, needs, blocker }: { task: ScenarioTask; asks: AskItem[]; now: number; needs: boolean; blocker?: Block }) {
  const { snap } = useLab();
  const ask = asks.find((a) => a.task === t.id && !a.ahead);
  const fileList = snap.artifacts.filter((a) => a.task === t.id);
  const people = [t.owner, ...(t.with ?? [])].filter((n): n is string => !!n);
  const links = linkCount(snap, t.id);
  const done = t.state === "done";
  let line: string | null = null;
  if (needs) line = ask ? ask.question : (t.reason ?? null);
  else if (blocker) line = `${blocker.from || "Its owner"} asked ${blocker.on}: ${blocker.question}`;
  else if (t.state === "open") line = `No owner · opened by ${t.by ?? "someone"} ${ago(snap.opened[t.id] ?? t.t, now)}`;
  else if (t.state === "claimed") line = "Claimed, not started";
  const steward = scenario.steward;
  return (
    // One clickable card: the title's button stretches over the whole card, and the inner
    // controls sit above it, so each keeps its own action and stays reachable.
    <article
      className={cn(
        "task-card relative flex flex-col gap-2.5 rounded-box border border-rule px-3.5 py-3 transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected has-[.card-open:focus-visible]:outline-2 has-[.card-open:focus-visible]:outline-accent",
        done ? "bg-transparent" : "bg-surface",
      )}
      data-task={t.id}
      data-needs-you={needs || undefined}
    >
      <button
        type="button"
        onClick={() => openTask(t.id)}
        className="card-open flex flex-col gap-0.5 text-left outline-none after:absolute after:inset-0 after:rounded-box after:content-['']"
        title={`Open ${t.id}`}
      >
        <span className="text-meta text-muted tabular-nums">{t.id}</span>
        <span className={cn("leading-snug", done ? "font-normal" : "font-bold")}>{t.title}</span>
      </button>
      {line && (
        <p className={cn("text-meta", needs ? "text-ink" : "text-muted")}>
          {needs && <span className="font-bold">Waiting on you: </span>}
          {blocker && <span className="font-bold text-ink">Blocked: </span>}
          {line}
        </p>
      )}
      {people.length > 0 && (
        <ul className="flex flex-col gap-2 border-t border-rule pt-2.5">
          {people.map((n, i) => {
            const human = scenario.people.some((p) => p.name === n);
            const s = human ? { text: n === scenario.me ? "that's you" : "on it", tone: "quiet" as const } : statusOf(snap, n, asks, now);
            const late = s.tone === "late";
            return (
              <li key={n} className="grid grid-cols-[20px_minmax(0,1fr)] gap-x-2">
                <Mark name={n} />
                <span className="flex min-w-0 flex-col">
                  <span>
                    {human ? (
                      n
                    ) : (
                      <button type="button" className="relative z-10 hover:underline" onClick={() => openAgent(n)} title={`${n}'s details`}>
                        {n}
                      </button>
                    )}
                    {i === 0 && t.owner === n && (
                      <>
                        {" "}
                        <span className="relative z-10">
                          <OwnerLabel task={t} />
                        </span>
                      </>
                    )}
                    {!human && !done && <span className="text-meta text-muted"> · {agentState(snap, n, now)}</span>}
                  </span>
                  {!done && (
                    <span className={cn("line-clamp-2 text-meta", toneClass[s.tone])}>
                      {late && <Clock className="mr-1 inline size-3 -translate-y-px" strokeWidth={2} aria-label="Late" />}
                      {s.text}
                    </span>
                  )}
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {t.state === "open" && steward && (
        <span className="relative z-10">
          <Ask label={`Ask ${steward} to assign it`} to={steward} text={`${t.id} (${t.title}) has no owner. Please assign it, and say who and why.`} />
        </span>
      )}
      <div className="flex flex-col gap-1 border-t border-rule pt-2 text-meta text-muted">
        {links && (
          <button type="button" onClick={() => openTask(t.id)} title={`Open ${t.id}, with its conversation`} className="task-threads relative z-10 inline-flex min-h-7 items-center gap-1 self-start underline decoration-1 underline-offset-[3px] hover:text-ink">
            <MessagesSquare className="size-3.5" strokeWidth={1.75} aria-hidden />
            {links} in conversation
          </button>
        )}
        <p className="flex flex-wrap items-center gap-x-1.5">
          {fileList.map((a) => (
            <button
              key={a.id}
              type="button"
              onClick={() => openArtifact(a.id, t.id)}
              className="relative z-10 inline-flex max-w-[180px] items-center gap-1 rounded-[6px] border border-rule px-1.5 hover:border-field-border hover:text-ink"
              title={`Open ${a.name}`}
            >
              <FileIcon a={a} className="size-3" />
              <span className="truncate">{a.name}</span>
            </button>
          ))}
          <span>{ago(t.t, now)}</span>
        </p>
      </div>
    </article>
  );
}
