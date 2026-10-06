"use client";

// EXPERIMENTAL, lab only: the board view's centre with the mocked features. Under the
// "Now:" line, the board's state in two labelled rows, so a person can tell what is
// counted from what is written: Facts (counted from the record) and the Brief (written
// by an agent, the steward). Then tabs, each appearing only once the board has what it
// shows, so a solo board stays a chat (D123):
//
// - Now, the home of a board with a team and real work: what needs you, what changed,
//   what is stuck. It opens first there; the timeline is one tab away.
// - Timeline, the record, always.
// - Tasks, once the board has a task.
// - Files, once the board has a file. A tab rather than a side panel, because a file
//   needs the room: cards side by side, and a preview as wide as the column.
//
// The conversation stays mounted under the other tabs, so its scroll and read position
// hold. ?view=now|tasks|artifacts opens on a tab.

import { type ReactNode, useEffect } from "react";
import type { Member, MemberRef } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import { type View, scenario, showView, useLab, useUi } from "../store";
import { ArtifactsView } from "./artifacts";
import { Brief } from "./brief";
import { onBoard, useNow } from "./common";
import { NowView, needsOf } from "./now-view";
import { TaskBoard } from "./tasks";

const column = "mx-auto w-full max-w-[848px] px-4 sm:px-6";

/** teamLayer is true once a board has a team at work: another person, or four agents and a task. */
export function teamLayer(agents: number, tasks: number): boolean {
  return scenario.people.length > 1 || (agents >= 4 && tasks > 0);
}

export function Centre({
  board,
  members,
  identity,
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
  const now = useNow();
  const agents = members.filter((m) => m.kind === "agent");
  const team = teamLayer(agents.length, snap.tasks.length);
  const tabs: View[] = [
    ...(team ? (["now"] as View[]) : []),
    "timeline",
    ...(snap.tasks.length > 0 ? (["tasks"] as View[]) : []),
    ...(snap.artifacts.length > 0 ? (["artifacts"] as View[]) : []),
  ];
  // A team board opens on Now; anything that asks for another view moves there.
  const shown: View = ui.n === 0 && !ui.explicit ? (team ? "now" : "timeline") : tabs.includes(ui.view) ? ui.view : "timeline";

  // A thread asked for from a task or a file opens in the timeline once it shows.
  useEffect(() => {
    if (!ui.thread) return;
    const id = ui.thread;
    const timer = setTimeout(() => onShow(id), 60);
    return () => clearTimeout(timer);
  }, [ui.thread, ui.n, onShow]);

  if (!onBoard(board)) return <>{children}</>;
  const { asks, waiting } = needsOf(snap.messages, snap.tasks, now);
  const needs = asks.length + waiting.length;
  const notDone = snap.tasks.filter((t) => t.state !== "done").length;
  const labels: Record<View, ReactNode> = {
    now: (
      <>
        Now
        {needs > 0 && <span className="rounded-[4px] bg-attention px-1 text-meta font-normal text-ink">{needs} needs you</span>}
      </>
    ),
    timeline: "Timeline",
    tasks: (
      <>
        Tasks <span className="font-normal text-muted tabular-nums">{notDone}</span>
      </>
    ),
    artifacts: (
      <>
        Files <span className="font-normal text-muted tabular-nums">{snap.artifacts.length}</span>
      </>
    ),
  };
  return (
    <>
      <div className={column}>
        {team && <Facts agents={agents} />}
        <Brief agents={agents.length} />
        {tabs.length > 1 && (
          <div role="tablist" aria-label="Board views" className="board-tabs -mx-3.5 flex animate-fade-in overflow-x-auto">
            {tabs.map((v) => (
              <button
                key={v}
                type="button"
                role="tab"
                id={`tab-${v}`}
                aria-selected={shown === v}
                aria-controls={`view-${v}`}
                onClick={() => showView(v)}
                className={cn(
                  "-mb-px inline-flex min-h-11 shrink-0 items-center gap-2 border-b-2 px-3.5 transition-colors duration-[140ms] ease-out",
                  shown === v ? "border-accent font-bold text-ink" : "border-transparent text-muted hover:text-ink",
                )}
              >
                {labels[v]}
              </button>
            ))}
          </div>
        )}
      </div>
      <div
        id="view-timeline"
        role={tabs.length > 1 ? "tabpanel" : undefined}
        aria-labelledby={tabs.length > 1 ? "tab-timeline" : undefined}
        className={cn("flex min-h-0 flex-1 flex-col", shown !== "timeline" && "hidden")}
      >
        {children}
      </div>
      {shown !== "timeline" && (
        <div id={`view-${shown}`} role="tabpanel" aria-labelledby={`tab-${shown}`} className="flex min-h-0 flex-1 flex-col">
          {shown === "now" && <NowView />}
          {shown === "tasks" && <TaskBoard identity={identity} />}
          {shown === "artifacts" && <ArtifactsView />}
        </div>
      )}
    </>
  );
}

/** Facts is the board's state counted from its record: nobody wrote it, so it is always true. */
function Facts({ agents }: { agents: Member[] }) {
  const { snap } = useLab();
  const by = (p: string) => agents.filter((a) => (a.presence ?? "no_session") === p).length;
  const tasks = snap.tasks;
  const n = (s: string[]) => tasks.filter((t) => s.includes(t.state)).length;
  const parts = [
    `${by("working")} working`,
    by("idle") > 0 && `${by("idle")} idle`,
    by("no_session") > 0 && `${by("no_session")} disconnected`,
    tasks.length > 0 &&
      `${count(tasks.length, "task", "tasks")}: ${[
        n(["open"]) && `${n(["open"])} open`,
        n(["claimed", "working"]) && `${n(["claimed", "working"])} in progress`,
        n(["waiting"]) && `${n(["waiting"])} waiting`,
        n(["done"]) && `${n(["done"])} done`,
      ]
        .filter(Boolean)
        .join(", ")}`,
    snap.artifacts.length > 0 && count(snap.artifacts.length, "file", "files"),
  ].filter(Boolean);
  return (
    <p className="facts mb-1.5 flex gap-2 px-3.5 text-meta" title="Counted from the board's record by the server. Nobody wrote these words.">
      <span className="w-[52px] shrink-0 font-bold text-muted">Facts</span>
      <span className="min-w-0 text-ink">{parts.join(" · ")}</span>
    </p>
  );
}
