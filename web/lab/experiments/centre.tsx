"use client";

// EXPERIMENTAL, lab only: the board view's centre. Under the "Now:" line (counted
// facts) comes the brief (an agent's writing, with its byline), then one switch between
// views of the same board: Conversation, the record; Tasks, the work laid out by what
// the person would act on, once the board has a task; Files, every file, once it has a
// file. The conversation stays mounted under the others, so its scroll and read
// position hold. Narrowed to a task, the conversation shows only that task's threads
// and messages, under a line that says so and offers the way back.

import { X } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import type { Member, MemberRef } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import { type View, filterTo, narrowTo, showView, useLab, useUi } from "../store";
import { Brief } from "./brief";
import { TaskChip } from "./chips";
import { Mark, onBoard } from "./common";
import { FilesView } from "./files";
import { aboutTask, byAgent, filterIds } from "./links";
import { TaskBoard } from "./tasks";

const column = "mx-auto w-full max-w-[848px] px-4 sm:px-6";

export function Centre({
  board,
  members,
  onShow,
  children,
}: {
  board: string;
  members: Member[];
  identity: (m: MemberRef) => number;
  onShow: (id: string) => void;
  children: ReactNode;
}) {
  const { snap } = useLab();
  const ui = useUi();

  // A thread asked for from a task, a file or the brief opens in the conversation.
  useEffect(() => {
    if (!ui.thread) return;
    const id = ui.thread;
    const timer = setTimeout(() => onShow(id), 60);
    return () => clearTimeout(timer);
  }, [ui.thread, ui.n, onShow]);

  if (!onBoard(board)) return <>{children}</>;
  const views: View[] = ["conversation", ...(snap.tasks.length > 0 ? (["tasks"] as View[]) : []), ...(snap.artifacts.length > 0 ? (["files"] as View[]) : [])];
  const shown: View = views.includes(ui.view) ? ui.view : "conversation";
  const tasks = snap.tasks.filter((t) => t.state !== "done").length;
  const filter = ui.filter && snap.tasks.some((t) => t.id === ui.filter) ? ui.filter : null;
  const agent = !filter && ui.agent && members.some((m) => m.name === ui.agent) ? ui.agent : null;
  const mine = agent ? byAgent(snap, agent) : null;
  const kept = filter ? filterIds(snap, filter) : (mine?.ids ?? []);
  const about = filter ? aboutTask(snap, filter) : null;
  const narrowed = filter ?? agent;
  const label: Record<View, ReactNode> = {
    conversation: "Conversation",
    tasks: (
      <>
        Tasks <span className="font-normal text-muted tabular-nums">{tasks}</span>
      </>
    ),
    files: (
      <>
        Files <span className="font-normal text-muted tabular-nums">{snap.artifacts.length}</span>
      </>
    ),
  };
  return (
    <>
      <div className={column}>
        <Brief />
        {views.length > 1 && (
          <div role="tablist" aria-label="Board views" className="board-switch mb-2 inline-flex animate-fade-in rounded-control border border-rule bg-surface p-0.5">
            {views.map((v) => (
              <button
                key={v}
                type="button"
                role="tab"
                id={`tab-${v}`}
                aria-selected={shown === v}
                aria-controls={`view-${v}`}
                onClick={() => showView(v)}
                className={cn(
                  "inline-flex min-h-9 items-center gap-1.5 rounded-[6px] px-3 transition-colors duration-[140ms] ease-out",
                  shown === v ? "bg-selected font-bold text-ink" : "text-muted hover:text-ink",
                )}
              >
                {label[v]}
              </button>
            ))}
          </div>
        )}
        {agent && shown === "conversation" && mine && (
          <p className="task-filter mb-2 flex flex-wrap items-center gap-2 rounded-control bg-selected px-3 py-1.5 text-meta" role="status" aria-live="polite">
            Only <Mark name={agent} /> <strong>{agent}</strong>
            <span className="text-muted">
              {[mine.threads > 0 && count(mine.threads, "thread it wrote in", "threads it wrote in"), mine.loose > 0 && count(mine.loose, "message", "messages")].filter(Boolean).join(", ")}
            </span>
            <button type="button" className="ml-auto inline-flex min-h-8 items-center gap-1 font-bold text-ink hover:underline" onClick={() => narrowTo(null)}>
              <X className="size-3.5" strokeWidth={2} aria-hidden />
              Show everything
            </button>
          </p>
        )}
        {filter && shown === "conversation" && about && (
          <p className="task-filter mb-2 flex flex-wrap items-center gap-2 rounded-control bg-selected px-3 py-1.5 text-meta" role="status" aria-live="polite">
            Only <TaskChip id={filter} />
            <span className="text-muted">
              {about.threads.length + about.loose.length} in conversation
            </span>
            <button type="button" className="ml-auto inline-flex min-h-8 items-center gap-1 font-bold text-ink hover:underline" onClick={() => filterTo(null)}>
              <X className="size-3.5" strokeWidth={2} aria-hidden />
              Show everything
            </button>
          </p>
        )}
      </div>
      {narrowed && (
        // The timeline is the real one; narrowing it hides what isn't about the task or agent.
        <style>{`[data-lab-filter] li.board-event, [data-lab-filter] .new-divider { display: none; }
[data-lab-filter] li.message:not(.reply)${kept.map((id) => `:not([data-id="${CSS.escape(id)}"])`).join("")} { display: none; }
[data-lab-filter] li.thread${kept.map((id) => `:not([data-thread="${CSS.escape(id)}"])`).join("")} { display: none; }`}</style>
      )}
      <div
        id="view-conversation"
        role={views.length > 1 ? "tabpanel" : undefined}
        data-lab-filter={narrowed ?? undefined}
        className={cn("flex min-h-0 flex-1 flex-col", shown !== "conversation" && "hidden")}
      >
        {children}
      </div>
      {shown === "tasks" && (
        <div id="view-tasks" role="tabpanel" aria-labelledby="tab-tasks" className="flex min-h-0 flex-1 flex-col">
          <TaskBoard agents={members.filter((m) => m.kind === "agent")} />
        </div>
      )}
      {shown === "files" && (
        <div id="view-files" role="tabpanel" aria-labelledby="tab-files" className="flex min-h-0 flex-1 flex-col">
          <FilesView />
        </div>
      )}
    </>
  );
}
