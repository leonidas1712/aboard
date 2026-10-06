"use client";

// EXPERIMENTAL, lab only: the board view's centre with the mocked features. Under the
// "Now:" line: the brief, then tabs (Timeline, Tasks) once the board has a task, as
// design/UI.md plans (D123). The conversation stays mounted under the Tasks tab, so its
// scroll position and read position hold. ?view=tasks opens on the tasks.

import { type ReactNode, useState } from "react";
import type { Member, MemberRef } from "@/app/api";
import { cn } from "@/lib/utils";
import { scenario, useLab } from "../store";
import { Brief } from "./brief";
import { onBoard } from "./common";
import { TaskBoard } from "./tasks";

const column = "mx-auto w-full max-w-[848px] px-4 sm:px-6";

type View = "timeline" | "tasks";

export function Centre({
  board,
  members,
  identity,
  children,
}: {
  board: string;
  members: Member[];
  identity: (m: MemberRef) => number;
  children: ReactNode;
}) {
  const { snap } = useLab();
  const [view, setView] = useState<View>(() =>
    typeof window !== "undefined" && new URLSearchParams(window.location.search).get("view") === "tasks" ? "tasks" : "timeline",
  );
  if (!onBoard(board)) return <>{children}</>;
  const tasks = snap.tasks;
  const hasTasks = tasks.length > 0;
  const shown: View = hasTasks ? view : "timeline";
  const notDone = tasks.filter((t) => t.state !== "done").length;
  const needsYou = tasks.filter((t) => t.state === "waiting" && t.waitingOn === scenario.me).length;
  const agents = members.filter((m) => m.kind === "agent").length;
  const tab = (v: View, label: ReactNode) => (
    <button
      type="button"
      role="tab"
      id={`tab-${v}`}
      aria-selected={shown === v}
      aria-controls={`view-${v}`}
      onClick={() => setView(v)}
      className={cn(
        "-mb-px inline-flex min-h-11 items-center gap-2 border-b-2 px-3.5 transition-colors duration-[140ms] ease-out",
        shown === v ? "border-accent font-bold text-ink" : "border-transparent text-muted hover:text-ink",
      )}
    >
      {label}
    </button>
  );
  return (
    <>
      <div className={column}>
        <Brief agents={agents} />
        {hasTasks && (
          <div role="tablist" aria-label="Board views" className="board-tabs flex animate-fade-in gap-1">
            {tab("timeline", "Timeline")}
            {tab(
              "tasks",
              <>
                Tasks
                <span className="font-normal text-muted tabular-nums">{notDone}</span>
                {needsYou > 0 && <span className="rounded-[4px] bg-attention px-1 text-meta font-normal text-ink">{needsYou} needs you</span>}
              </>,
            )}
          </div>
        )}
      </div>
      <div
        id="view-timeline"
        role={hasTasks ? "tabpanel" : undefined}
        aria-labelledby={hasTasks ? "tab-timeline" : undefined}
        className={cn("flex min-h-0 flex-1 flex-col", shown !== "timeline" && "hidden")}
      >
        {children}
      </div>
      {shown === "tasks" && (
        <div id="view-tasks" role="tabpanel" aria-labelledby="tab-tasks" className="flex min-h-0 flex-1 flex-col">
          <TaskBoard identity={identity} />
        </div>
      )}
    </>
  );
}
