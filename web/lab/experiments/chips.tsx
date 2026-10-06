"use client";

// EXPERIMENTAL, lab only: the link between tasks and the conversation, both ways.
// A message shows a quiet chip for each task it is about, on its first line; a thread's
// row shows a chip for every task any of its messages is about, so a thread about two
// tasks shows both. Hovering a chip says the task's title and where it stands; a click
// opens the task in the side panel. The other way, a task lists its threads and loose
// messages (ThreadList), and one click narrows the conversation to them.

import { MessagesSquare } from "lucide-react";
import type { Message } from "@/app/api";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { count, relativeTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { type ScenarioTask } from "../scenario";
import { boardMessages } from "../fake-api";
import { openTask, openThread, scenario, useLab, useUi } from "../store";
import { asksOf } from "./asks";
import { Mark, onBoard, useNow } from "./common";
import { aboutTask, tasksOf, threadOf } from "./links";

/** stateOf says where a task stands, in a few words. */
export function stateOf(t: ScenarioTask, needs: boolean): string {
  if (needs) return "Waiting on you";
  switch (t.state) {
    case "open":
      return "Not picked up";
    case "claimed":
      return "Claimed, not started";
    case "waiting":
      return `Waiting on ${t.waitingOn}`;
    case "done":
      return "Done";
    default:
      return "In progress";
  }
}

/** OwnerLabel is the word "owner" beside a task's owner, which says in a tooltip what it means. */
export function OwnerLabel({ task }: { task: ScenarioTask }) {
  const opener = task.by ?? scenario.steward ?? task.owner;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="owner-label cursor-help text-meta text-muted underline decoration-dotted decoration-1 underline-offset-[3px]">
          owner
        </span>
      </TooltipTrigger>
      <TooltipContent side="top" align="start">
        Responsible for the task{opener && opener !== task.owner ? `; opened by ${opener === scenario.me ? "you" : opener}` : ""}
      </TooltipContent>
    </Tooltip>
  );
}

/** TaskChip is a task's id and title, quietly; a click opens it in the side panel. */
export function TaskChip({ id, short, className }: { id: string; short?: boolean; className?: string }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const t = snap.tasks.find((x) => x.id === id);
  if (!t) return null;
  const needs = (t.state === "waiting" && t.waitingOn === scenario.me) || asksOf(snap, answered).some((a) => a.task === t.id && !a.ahead && a.board === scenario.board.name);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={() => openTask(t.id)}
          data-task-chip={t.id}
          className={cn(
            "task-chip inline-flex max-w-[240px] items-baseline gap-1 rounded-[6px] border border-rule px-1.5 align-baseline text-meta font-normal text-muted transition-colors duration-[140ms] ease-out hover:border-field-border hover:text-ink",
            className,
          )}
        >
          <span className="shrink-0 tabular-nums">{t.id}</span>
          {!short && <span className="truncate">· {t.title}</span>}
        </button>
      </TooltipTrigger>
      <TooltipContent side="top" align="start" className="max-w-[300px]">
        <span className="block font-bold">
          {t.id} {t.title}
        </span>
        <span className="block">
          {stateOf(t, needs)}
          {t.owner && ` · owner ${t.owner === scenario.me ? "you" : t.owner}`}
        </span>
        <span className="block opacity-80">Click to open the task</span>
      </TooltipContent>
    </Tooltip>
  );
}

/** MessageMeta is the timeline's slot on a message's first line: a chip per task it is about. */
export function MessageMeta({ board, message, grouped }: { board: string; message: Message; grouped: boolean }) {
  const { snap } = useLab();
  if (!onBoard(board)) return null;
  const m = boardMessages().find((x) => x.id === message.id) ?? { body: message.body, about: [] };
  const own = tasksOf(m, snap);
  // A message that starts a thread leaves its chips to the thread's row, which shows the
  // tasks of the whole thread, so a task shows once per place.
  if (own.length === 0 || threadOf(snap, message.id)) return null;
  const chips = own.map((id) => <TaskChip key={id} id={id} short={own.length > 1} />);
  if (grouped) return <p className="message-tasks flex flex-wrap gap-1.5 pt-0.5">{chips}</p>;
  return <span className="message-tasks ml-2 inline-flex flex-wrap gap-1.5 align-baseline">{chips}</span>;
}

/** ThreadMeta is the timeline's slot on a thread's row: a chip for every task the thread is about. */
export function ThreadMeta({ board, root }: { board: string; root: Message }) {
  const { snap } = useLab();
  if (!onBoard(board)) return null;
  const thread = threadOf(snap, root.id);
  if (!thread || thread.tasks.length === 0) return null;
  return (
    <span className="thread-tasks inline-flex flex-wrap items-center gap-1.5 text-meta text-muted">
      {thread.tasks.length > 1 ? `about ${thread.tasks.length} tasks` : "about"}
      {thread.tasks.map((id) => (
        <TaskChip key={id} id={id} short={thread.tasks.length > 1} />
      ))}
    </span>
  );
}

/** ThreadList is a task's threads (first line, who, replies, last activity) and its loose messages. */
export function ThreadList({ task, className }: { task: string; className?: string }) {
  const { snap } = useLab();
  const now = useNow();
  const { threads, loose } = aboutTask(snap, task);
  const ago = (ms: number) => relativeTime(new Date(ms).toISOString(), now);
  return (
    <ul className={cn("thread-list flex flex-col gap-1", className)}>
      {threads.map((t) => {
        const others = t.tasks.filter((x) => x !== task);
        return (
          <li key={t.root.id}>
            <button
              type="button"
              onClick={() => openThread(t.root.id)}
              className="grid w-full grid-cols-[20px_minmax(0,1fr)] gap-x-2 rounded-control px-1.5 py-1.5 text-left hover:bg-selected"
              title="Open this thread in the conversation"
            >
              <Mark name={t.root.from} />
              <span className="flex min-w-0 flex-col">
                <span className="line-clamp-1 text-meta">
                  <strong>{t.root.from === scenario.me ? "You" : t.root.from}:</strong> {t.root.body}
                </span>
                <span className="text-meta text-muted">
                  <MessagesSquare className="mr-1 inline size-3 -translate-y-px" strokeWidth={1.75} aria-hidden />
                  {count(t.replies, "reply", "replies")} · last {ago(t.last)}
                  {others.length > 0 && ` · also ${others.join(", ")}`}
                </span>
              </span>
            </button>
          </li>
        );
      })}
      {loose.map((m) => (
        <li key={m.id}>
          <button
            type="button"
            onClick={() => openThread(m.id)}
            className="grid w-full grid-cols-[20px_minmax(0,1fr)] gap-x-2 rounded-control px-1.5 py-1.5 text-left hover:bg-selected"
            title="Show this message in the conversation"
          >
            <Mark name={m.from} />
            <span className="flex min-w-0 flex-col">
              <span className="line-clamp-1 text-meta">
                <strong>{m.from === scenario.me ? "You" : m.from}:</strong> {m.body}
              </span>
              <span className="text-meta text-muted">
                <MessagesSquare className="mr-1 inline size-3 -translate-y-px" strokeWidth={1.75} aria-hidden />
                no replies · {ago(m.at)}
              </span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

/** linkCount is how many items of conversation are about a task, threads and lone messages alike, or null for none. */
export function linkCount(snap: ReturnType<typeof useLab>["snap"], task: string): number | null {
  const { threads, loose } = aboutTask(snap, task);
  const n = threads.length + loose.length;
  return n > 0 ? n : null;
}
