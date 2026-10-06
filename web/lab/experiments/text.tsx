"use client";

// EXPERIMENTAL, lab only: task ids in text. A message, the brief or a note that names a
// task (CHK-16) links to it, and the link opens the task in the side panel. In the
// timeline it is the plain text between a message's mentions.

import { Fragment } from "react";
import { taskIds } from "../scenario";
import { openTask, scenario, useLab } from "../store";

export function Ids({ text }: { text: string }) {
  const { snap } = useLab();
  const parts = text.split(new RegExp(`(${taskIds.source})`, "g"));
  return (
    <>
      {parts.map((p, i) => {
        const task = i % 2 === 1 ? snap.tasks.find((t) => t.id === p) : undefined;
        if (!task) return <Fragment key={i}>{p}</Fragment>;
        return (
          <button
            key={i}
            type="button"
            onClick={() => openTask(task.id)}
            title={`${task.id} ${task.title}`}
            className="task-ref text-ink underline decoration-dotted decoration-1 underline-offset-[3px] hover:decoration-solid"
          >
            {p}
          </button>
        );
      })}
    </>
  );
}

/** Text is the timeline's slot: only the scenario's own board has tasks to link. */
export function Text({ text }: { text: string }) {
  const onBoard = typeof window !== "undefined" && new URLSearchParams(window.location.search).get("board") === scenario.board.name;
  return onBoard ? <Ids text={text} /> : <>{text}</>;
}
