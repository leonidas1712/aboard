"use client";

// EXPERIMENTAL, lab only: what a message gains in the timeline. A message can name tasks,
// and a thread links to every task its messages name, many to many: the message that
// starts a thread shows a quiet chip for each of them, and a reply shows the tasks it
// names itself ("About T5"). A chip opens its task on the Tasks tab. A file posted with
// the message shows as a file tile that opens its preview. A thread about tasks can be
// handed to the brief's steward to summarise. Fed by the scenario, not the API.

import type { Message } from "@/app/api";
import { tasksOfThread, taskRef } from "../scenario";
import { postedAbout } from "../fake-api";
import { openArtifact, openTask, scenario, useLab } from "../store";
import { FileIcon, formatOf } from "./artifacts";
import { Ask } from "./ask";
import { onBoard } from "./common";

export function MessageFooter({ board, message }: { board: string; message: Message }) {
  const { snap } = useLab();
  if (!onBoard(board)) return null;
  const m = snap.messages.find((x) => x.id === message.id);
  const root = message.thread_root === null;
  const ids = m ? (root ? tasksOfThread(snap.messages, m.id) : (m.about ?? [])) : postedAbout(message.id);
  const tasks = ids.map((id) => snap.tasks.find((t) => t.id === id)).filter((t) => !!t);
  const file = m?.attach ? snap.artifacts.find((a) => a.id === m.attach) : undefined;
  const steward = scenario.steward;
  const summarise = root && m && tasks.length > 0 && message.reply_count > 0 && steward && snap.brief;
  if (tasks.length === 0 && !file) return null;
  return (
    <div className="message-extras mt-1.5 flex flex-col gap-1.5">
      {file && (
        <div className="file-tile flex w-full max-w-[360px] items-center gap-2.5 rounded-control border border-rule bg-surface px-3 py-2.5">
          <FileIcon a={file} />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="truncate font-bold">{file.name}</span>
            <span className="text-meta text-muted">
              {formatOf(file)}, version {file.version}
            </span>
          </span>
          <button
            type="button"
            className="min-h-9 shrink-0 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
            onClick={() => openArtifact(file.id)}
          >
            Open
          </button>
        </div>
      )}
      {tasks.length > 0 && (
        <div className="task-chips flex flex-wrap items-center gap-x-1.5 gap-y-1 text-meta text-muted">
          <span>{root && tasks.length > 1 ? "Tasks" : "About"}</span>
          {tasks.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => openTask(t.id)}
              title={`Open ${taskRef(t.id)} on the Tasks tab`}
              className="task-chip inline-flex max-w-[260px] items-baseline gap-1 rounded-[6px] border border-rule px-1.5 text-muted transition-colors duration-[140ms] ease-out hover:border-field-border hover:text-ink"
            >
              <span className="font-bold tabular-nums">{taskRef(t.id)}</span>
              <span className="truncate">{t.title}</span>
            </button>
          ))}
          {summarise && (
            <Ask
              className="ml-1 opacity-0 transition-opacity duration-[140ms] group-focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100"
              label="Summarise into the brief"
              to={steward}
              about={ids}
              text={`Please add what this thread settled to the brief, citing it: "${m.body.slice(0, 80)}${m.body.length > 80 ? "…" : ""}"`}
            />
          )}
        </div>
      )}
    </div>
  );
}
