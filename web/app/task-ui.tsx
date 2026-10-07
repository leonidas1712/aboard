"use client";

import { ArrowLeft } from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type Task, type TaskList, type TaskTag, ApiError, getTask, listTasks } from "./api";
import { Problem } from "./chrome";
import { count, relativeTime } from "./words";

const Tasks = createContext<{ tasks: Task[]; open: (ref: string) => void }>({ tasks: [], open: () => {} });

export function TaskContext({ tasks, open, children }: { tasks: Task[]; open: (ref: string) => void; children: ReactNode }) {
  return <Tasks.Provider value={{ tasks, open }}>{children}</Tasks.Provider>;
}

export function useTasks(board: string, activity: number, head: number | undefined, enabled: boolean) {
  const [list, setList] = useState<TaskList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const request = useRef(0);
  useEffect(() => {
    const generation = ++request.current;
    if (!enabled) { setList(null); return; }
    listTasks(board).then((value) => {
      if (generation !== request.current) return;
      setList(value); setError(null);
    }, (e) => { if (generation === request.current) { if (e instanceof ApiError && (e.status === 403 || e.status === 404)) setList(null); setError(e); } });
    return () => { request.current++; };
  }, [board, activity, head, enabled]);
  return { list, error };
}

export function taskState(t: Task): string {
  if (t.state === "open") return "Not picked up";
  if (t.state === "in_progress") return "In progress";
  return t.state === "done" ? "Done" : "Cancelled";
}

export function TaskChips({ tags }: { tags: TaskTag[] | undefined }) {
  const { tasks, open } = useContext(Tasks);
  if (!tags?.length) return null;
  const unique = [...new Map(tags.map((t) => [t.id, t])).values()];
  return <span className="message-tasks inline-flex flex-wrap items-baseline gap-1.5">
    {unique.map((tag) => {
      const task = tasks.find((t) => t.id === tag.id);
      return <Tooltip key={tag.id}><TooltipTrigger asChild>
        <button type="button" aria-label={`Open task ${tag.ref}`} data-task-chip={tag.ref} onClick={() => open(tag.ref)} className="task-chip inline-flex max-w-[240px] items-baseline gap-1 rounded-[6px] border border-rule px-1.5 text-meta font-normal text-muted transition-colors duration-[140ms] ease-out hover:border-field-border hover:text-ink focus-visible:outline-2 focus-visible:outline-accent">
          <span className="shrink-0 tabular-nums">{tag.ref}</span>{task && unique.length === 1 && <span className="truncate">· {task.title}</span>}
        </button>
      </TooltipTrigger><TooltipContent><strong className="block">{tag.ref}{task && ` ${task.title}`}</strong>{task && <span className="block">{taskState(task)}{task.owner && ` · owner ${task.owner.name}`}</span>}<span className="block">Click to open the task</span></TooltipContent></Tooltip>;
    })}
  </span>;
}

export function WorkTasks({ tasks, open }: { tasks: Task[]; open: (ref: string) => void }) {
  const active = tasks.filter((t) => t.state === "open" || t.state === "in_progress");
  if (!active.length) return null;
  return <div className="work flex flex-col gap-5" aria-label="Work by task">
    {active.map((t) => <section key={t.id} aria-label={`Task ${t.ref}: ${t.title}`} className="flex flex-col gap-1.5">
      <h3><button type="button" aria-label={`Open task ${t.ref}`} onClick={() => open(t.ref)} className="group flex min-h-11 w-full items-baseline gap-2 text-left"><span className="shrink-0 text-meta text-muted tabular-nums">{t.ref}</span><span className="min-w-0 flex-1 font-bold group-hover:underline">{t.title}</span></button></h3>
      <p className="text-meta text-muted">{taskState(t)}{t.owner && ` · ${t.owner.name} · owner`}{t.with.length > 0 && ` · with ${t.with.map((m) => m.name).join(", ")}`}</p>
      {t.stands && <p className="text-meta line-clamp-2">{t.stands.text}</p>}
    </section>)}
  </div>;
}

export function TaskBoard({ tasks, open }: { tasks: Task[]; open: (ref: string) => void }) {
  const [done, setDone] = useState(false);
  const finished = tasks.filter((t) => t.state === "done" || t.state === "cancelled");
  const card = (t: Task) => <li key={t.id}><button type="button" aria-label={`Open task ${t.ref}`} onClick={() => open(t.ref)} className={cn("flex min-h-11 w-full flex-col gap-2 rounded-box border border-rule p-3.5 text-left transition-colors duration-[140ms] ease-out hover:border-field-border", t.state !== "done" && t.state !== "cancelled" && "bg-surface")}>
    <span className="text-meta text-muted tabular-nums">{t.ref}</span><strong>{t.title}</strong><span className="text-meta text-muted">{t.owner ? `${t.owner.name} · owner` : "No owner yet"}{t.with.length > 0 && ` · with ${t.with.map((m) => m.name).join(", ")}`}</span>{t.stands && <span className="line-clamp-3 text-meta">{t.stands.text}</span>}<span className="text-meta text-muted">{count(t.message_count, "message", "messages")} · {count(t.thread_count, "thread", "threads")}</span>
  </button></li>;
  return <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6">
    <div className="grid gap-6 sm:grid-cols-2">
      {(["in_progress", "open"] as const).map((state) => <section key={state} aria-label={state === "open" ? "Not picked up" : "In progress"}>
        <h2 className="mb-3 font-bold">{state === "open" ? "Not picked up" : "In progress"} <span className="font-normal text-muted tabular-nums">{tasks.filter((t) => t.state === state).length}</span></h2>
        <ul className="flex flex-col gap-2">{tasks.filter((t) => t.state === state).map(card)}</ul>
        {!tasks.some((t) => t.state === state) && <p className="text-meta text-muted">None yet.</p>}
      </section>)}
    </div>
    {finished.length > 0 && <section aria-label="Done" className="mt-6"><button type="button" aria-expanded={done} onClick={() => setDone(!done)} className="min-h-11 text-meta text-muted hover:text-ink">{count(finished.length, "task", "tasks")} done or cancelled · {done ? "hide" : "show"}</button>{done && <ul className="grid gap-2 sm:grid-cols-2">{finished.map(card)}</ul>}</section>}
  </div>;
}

export function TaskDetail({ board, reference, activity, back, narrow }: { board: string; reference: string; activity: number; back: () => void; narrow: (ref: string) => void }) {
  const [task, setTask] = useState<Task | null>(null);
  const [error, setError] = useState<unknown>(null);
  const top = useRef<HTMLElement>(null);
  useEffect(() => { setTask(null); setError(null); }, [board, reference]);
  useEffect(() => {
    let live = true;
    getTask(board, reference).then((t) => { if (live) { setTask(t); setError(null); } }, (e) => { if (live) { if (e instanceof ApiError && (e.status === 403 || e.status === 404)) setTask(null); setError(e); } });
    return () => { live = false; };
  }, [board, reference, activity]);
  useEffect(() => { if (window.innerWidth < 1024) top.current?.scrollIntoView({ block: "start" }); }, [reference]);
  return <section ref={top} aria-label={`Task ${reference}`} className="flex scroll-mt-2 flex-col gap-5">
    <button type="button" onClick={back} className="inline-flex min-h-11 items-center gap-1.5 self-start text-meta text-muted hover:text-ink"><ArrowLeft className="size-4" aria-hidden />Work</button>
    {error !== null && <Problem error={error} />}
    {task ? <><div><p className="text-meta text-muted tabular-nums">{task.ref} · {taskState(task)}</p><h2 className="mt-1 font-bold">{task.title}</h2></div>
      <section><h3 className="font-bold">About</h3><p className="mt-1 whitespace-pre-wrap break-words">{task.about?.text || "Nothing written yet."}</p></section>
      <section><h3 className="font-bold">Where it stands</h3>{task.stands ? <><p className="mt-1 whitespace-pre-wrap break-words">{task.stands.text}</p><p className="mt-1 text-meta text-muted">by {task.stands.by.name} · {relativeTime(task.stands.at, Date.now())}{task.stands.messages_since !== undefined && ` · ${count(task.stands.messages_since, "message", "messages")} since`}</p></> : <p className="mt-1 text-meta text-muted">Nothing yet.</p>}</section>
      <section><h3 className="font-bold">On this task</h3><p className="mt-1">{task.owner ? `${task.owner.name} · owner` : "Not picked up"}</p>{task.with.length > 0 && <p className="text-meta text-muted">With {task.with.map((m) => m.name).join(", ")}</p>}</section>
      {task.closed_note && <section><h3 className="font-bold">Final note</h3><p className="mt-1 whitespace-pre-wrap break-words">{task.closed_note}</p></section>}
      <section><h3 className="font-bold">Conversation</h3><p className="mt-1 text-meta text-muted">{count(task.message_count, "message", "messages")} · {count(task.thread_count, "thread", "threads")}</p><button type="button" onClick={() => narrow(task.ref)} className="min-h-11 text-accent hover:underline">Show conversation</button></section>
    </> : error === null && <p className="text-meta text-muted" role="status">Loading task…</p>}
  </section>;
}

export function TaskLinks({ text, tags }: { text: string; tags?: TaskTag[] }) {
  const { tasks, open } = useContext(Tasks);
  return text.split(/(\b[A-Z][A-Z0-9]{1,15}-[1-9][0-9]*\b)/).map((part, i) => {
    const task = tasks.find((t) => t.ref === part) ?? tags?.find((t) => t.ref === part);
    return task ? <button key={i} type="button" onClick={() => open(task.ref)} className="font-medium text-link underline decoration-dotted underline-offset-2 hover:decoration-solid" aria-label={`Open task ${task.ref}`}>{part}</button> : part;
  });
}
