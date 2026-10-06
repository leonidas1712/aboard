"use client";

// EXPERIMENTAL, lab only: the board view's centre. Under the "Now:" line (counted
// facts) comes the brief (an agent's writing, with its byline), then one switch between
// two views of the same board: Conversation, the record, and Tasks, the work laid out by
// what the person would act on. The switch appears with the board's first task. The
// conversation stays mounted under Tasks, so its scroll and read position hold.

import { type ReactNode, useEffect } from "react";
import type { Member, MemberRef } from "@/app/api";
import { cn } from "@/lib/utils";
import { type View, showView, useLab, useUi } from "../store";
import { Brief } from "./brief";
import { onBoard } from "./common";
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
  const tasks = snap.tasks.filter((t) => t.state !== "done").length;
  const shown: View = snap.tasks.length > 0 ? ui.view : "conversation";
  return (
    <>
      <div className={column}>
        <Brief />
        {snap.tasks.length > 0 && (
          <div role="tablist" aria-label="Board views" className="board-switch mb-2 inline-flex animate-fade-in rounded-control border border-rule bg-surface p-0.5">
            {(["conversation", "tasks"] as View[]).map((v) => (
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
                {v === "conversation" ? "Conversation" : "Tasks"}
                {v === "tasks" && <span className="font-normal text-muted tabular-nums">{tasks}</span>}
              </button>
            ))}
          </div>
        )}
      </div>
      <div id="view-conversation" role={snap.tasks.length > 0 ? "tabpanel" : undefined} className={cn("flex min-h-0 flex-1 flex-col", shown !== "conversation" && "hidden")}>
        {children}
      </div>
      {shown === "tasks" && (
        <div id="view-tasks" role="tabpanel" aria-labelledby="tab-tasks" className="flex min-h-0 flex-1 flex-col">
          <TaskBoard agents={members.filter((m) => m.kind === "agent")} />
        </div>
      )}
    </>
  );
}
