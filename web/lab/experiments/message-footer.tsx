"use client";

// EXPERIMENTAL, lab only: what a message gains in the conversation. An ask is a message
// with buttons, so a decision can't get buried in a paragraph: its question in bold, its
// numbered answers and Reply. Once answered, it says what you answered. A file posted
// with a message shows as a card that opens it in the side panel. Fed by the scenario,
// not the API.

import type { Message } from "@/app/api";
import { cn } from "@/lib/utils";
import { openArtifact, scenario, useLab, useUi } from "../store";
import { answer, asksOf } from "./asks";
import { FileIcon, formatOf } from "./artifacts";
import { onBoard } from "./common";
import { Ids } from "./text";

export function MessageFooter({ board, message }: { board: string; message: Message }) {
  const { snap } = useLab();
  const { answered } = useUi();
  if (!onBoard(board)) return null;
  const m = snap.messages.find((x) => x.id === message.id);
  if (!m) return null;
  const file = m.attach ? snap.artifacts.find((a) => a.id === m.attach) : undefined;
  const ask = m.options ? asksOf(snap, answered).find((a) => a.id === m.id) : undefined;
  const mine = answered[m.id];
  if (!file && !m.options) return null;
  return (
    <div className="message-extras mt-2 flex flex-col gap-2">
      {m.question && (
        <p className="-mt-1.5 text-meta text-muted">
          {m.ahead ? "goes ahead unless you hold it" : `asks ${m.to?.map((t) => t.replace("@", "")).join(", ").replace(scenario.me, "you") ?? "everyone"}`}
          {m.task && (
            <>
              {" · blocks "}
              <Ids text={m.task} />
            </>
          )}
        </p>
      )}
      {m.question && (
        <p className="ask-detail-text">
          <Ids text={m.body} />
        </p>
      )}
      {file && (
        <button
          type="button"
          onClick={() => openArtifact(file.id, file.task ?? null)}
          className="file-tile flex w-full max-w-[360px] items-center gap-2.5 rounded-control border border-rule bg-surface px-3 py-2.5 text-left hover:border-field-border"
        >
          <FileIcon a={file} />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="truncate font-bold">{file.name}</span>
            <span className="truncate text-meta text-muted">
              {formatOf(file)} · v{file.version}
              {file.summary && ` · ${file.summary}`}
            </span>
          </span>
          <span className="text-meta text-link">Open</span>
        </button>
      )}
      {m.options &&
        (ask ? (
          <div className="flex flex-wrap gap-2" role="group" aria-label="Answers">
            {(m.ahead ? ["Hold it", "Let it go ahead"] : m.options).map((o, i) => (
              <button
                key={o}
                type="button"
                onClick={() => void answer(ask, o)}
                className="inline-flex min-h-9 items-center gap-2 rounded-control border border-rule bg-surface px-3 hover:border-field-border hover:bg-selected"
              >
                <kbd className="inline-flex size-5 items-center justify-center rounded-[4px] border border-rule font-sans text-[12px] text-muted">{i + 1}</kbd>
                {o}
              </button>
            ))}
          </div>
        ) : (
          <p className={cn("text-meta text-muted")}>{mine ? `You answered: ${mine}` : "Answered"}</p>
        ))}
    </div>
  );
}
