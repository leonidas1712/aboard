"use client";

// EXPERIMENTAL, lab only: the Files view, every file on the board in one list, beside
// Conversation and Tasks once the board has a file. A row says what the file is (type,
// name, a line about it), who made it, its version and when it changed, whether it is
// maintained (pinned, kept current) or one-off, and what you approved of it. Under that,
// its context: the tasks it is for, and the messages and threads it was posted in or
// named in, each one click from the conversation. A light filter (all, maintained,
// from people) and newest first; a click on a file opens it in the side panel.

import { Pin } from "lucide-react";
import { useState } from "react";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import type { Artifact } from "../scenario";
import { openArtifact, openThread, scenario, useLab } from "../store";
import { FileIcon, approval, formatOf } from "./artifacts";
import { TaskChip } from "./chips";
import { Mark, ago, kindOf, useNow } from "./common";
import { type FileContext, contextOf } from "./links";

type Show = "all" | "maintained" | "people";

const shows: { key: Show; label: string }[] = [
  { key: "all", label: "All" },
  { key: "maintained", label: "Maintained" },
  { key: "people", label: "From people" },
];

export function FilesView() {
  const { snap } = useLab();
  const now = useNow();
  const [show, setShow] = useState<Show>("all");
  const files = [...snap.artifacts]
    .filter((a) => (show === "maintained" ? a.maintained : show === "people" ? kindOf(a.by) === "human" : true))
    .sort((a, b) => b.t - a.t);
  return (
    <div className="files-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[848px] px-4 pt-1 pb-10 sm:px-6">
        <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-meta" role="group" aria-label="Show">
          {shows.map((s) => (
            <button
              key={s.key}
              type="button"
              aria-pressed={show === s.key}
              onClick={() => setShow(s.key)}
              className={cn("min-h-8 rounded-[6px] px-2", show === s.key ? "bg-selected font-bold text-ink" : "text-muted hover:text-ink")}
            >
              {s.label}
            </button>
          ))}
          <span className="ml-auto text-muted">newest first</span>
        </div>
        <ul className="divide-y divide-rule border-y border-rule">
          {files.map((a) => (
            <li key={a.id}>
              <FileRow a={a} ctx={contextOf(snap, a)} now={now} />
            </li>
          ))}
        </ul>
        {files.length === 0 && <p className="py-6 text-muted">No files here.</p>}
      </div>
    </div>
  );
}

function FileRow({ a, ctx, now }: { a: Artifact; ctx: FileContext; now: number }) {
  const ok = approval(a);
  return (
    <article className="file-row grid grid-cols-[20px_minmax(0,1fr)] gap-x-3 py-3" data-file={a.id}>
      <FileIcon a={a} className="mt-1" />
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <button type="button" onClick={() => openArtifact(a.id)} className="min-w-0 truncate text-left font-bold hover:underline">
            {a.name}
          </button>
          <span className="text-meta text-muted">
            {a.maintained ? (
              <span title="Pinned and kept current by its author">
                <Pin className="mr-0.5 inline size-3 -translate-y-px" strokeWidth={1.75} aria-hidden />
                maintained
              </span>
            ) : (
              "one-off"
            )}
            {" · "}
            {formatOf(a)}
          </span>
        </div>
        {a.summary && <p className="line-clamp-1 text-meta">{a.summary}</p>}
        <p className="flex flex-wrap items-center gap-x-1.5 text-meta text-muted">
          <Mark name={a.by} />
          <span>
            {a.by === scenario.me ? "you" : a.by} · v{a.version} · updated {ago(a.t, now)}
          </span>
          {ok && <span className={ok.changed ? "font-bold text-ink" : undefined}>· {ok.text}</span>}
        </p>
        <Context a={a} ctx={ctx} />
      </div>
    </article>
  );
}

/** Context is where a file is used: its tasks, and the messages and threads it is in. */
export function Context({ a, ctx }: { a: Artifact; ctx: FileContext }) {
  const posted = ctx.posted[0];
  const who = (n: string) => (n === scenario.me ? "you" : n);
  if (ctx.tasks.length === 0 && !posted && ctx.mentioned.length === 0) return null;
  return (
    <p className="file-context flex flex-wrap items-center gap-x-2 gap-y-1 text-meta text-muted">
      {ctx.tasks.map((t) => (
        <TaskChip key={t} id={t} />
      ))}
      {posted && (
        <button type="button" className="underline decoration-1 underline-offset-[3px] hover:text-ink" onClick={() => openThread(posted.message.id)}>
          {posted.message.replyTo ? "posted in a thread" : "posted"} by {who(posted.message.from)}
          {posted.thread && ` · ${count(posted.thread.replies, "reply", "replies")}`}
        </button>
      )}
      {ctx.mentioned.length > 0 && (
        <button type="button" className="underline decoration-1 underline-offset-[3px] hover:text-ink" onClick={() => openThread(ctx.mentioned[0].id)}>
          named in {count(ctx.mentioned.length, "message", "messages")}
        </button>
      )}
      {a.id === "brief" && <span>kept by the steward, {scenario.steward}</span>}
    </p>
  );
}
