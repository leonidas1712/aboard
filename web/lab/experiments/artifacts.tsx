"use client";

// EXPERIMENTAL, lab only: a file on the board, opened in the side panel. It is shown on
// a light page so it reads as the agent's work, not part of the app: HTML in a frame
// that may run nothing and reach nothing (sandbox="", no allow-same-origin, no
// scripts), Markdown formatted, images shown. Who made it, when, its version and where
// it is used sit around the page, never inside it. "Open full screen" shows it over the
// whole board; "Reply about this" sends the author a message about it.

import { Download, FileCode, FileImage, FileText, Maximize2, X } from "lucide-react";
import { useState } from "react";
import { type Message, post } from "@/app/api";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import type { Artifact } from "../scenario";
import { fullScreen, openTask, openThread, scenario, showWork, useLab, useUi } from "../store";
import { ago, useNow } from "./common";
import { Markdown } from "./markdown";

const formats = { html: "HTML", md: "Markdown", svg: "Image" } as const;
const mime = { html: "text/html", md: "text/markdown", svg: "image/svg+xml" } as const;

export function formatOf(a: Artifact): string {
  return formats[a.format];
}

export function FileIcon({ a, className }: { a: Pick<Artifact, "format">; className?: string }) {
  const Icon = a.format === "svg" ? FileImage : a.format === "html" ? FileCode : FileText;
  return <Icon className={cn("size-4 shrink-0 text-muted", className)} strokeWidth={1.5} aria-hidden />;
}

export function download(a: Artifact) {
  const url = URL.createObjectURL(new Blob([a.body], { type: mime[a.format] }));
  const link = document.createElement("a");
  link.href = url;
  link.download = a.name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** approval says what the person approved of a file, and how much changed since. */
export function approval(a: Artifact): { text: string; changed: boolean } | null {
  if (a.approved === undefined) return null;
  const since = a.version - a.approved;
  return since > 0
    ? { text: `you approved v${a.approved} · ${count(since, "change", "changes")} since`, changed: true }
    : { text: `you approved v${a.approved}`, changed: false };
}

/** Page is a file's content on its light page. */
export function Page({ a, tall }: { a: Artifact; tall?: boolean }) {
  const { snap } = useLab();
  return (
    <div className={cn("artifact-page overflow-hidden rounded-box bg-[var(--page)] text-[var(--page-ink)]", tall ? "h-full" : "")}>
      {a.format === "html" ? (
        // sandbox="" lets the page run nothing and reach nothing, not even this page.
        <iframe title={a.name} sandbox="" srcDoc={a.body} className={cn("artifact-frame block w-full border-0 bg-[var(--page)]", tall ? "h-full" : "h-[min(60vh,560px)]")} />
      ) : a.format === "svg" ? (
        <img alt={a.summary ?? a.name} src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(a.body)}`} className="mx-auto max-h-[70vh] max-w-full p-4" />
      ) : (
        <div className="max-h-[min(60vh,560px)] overflow-y-auto px-5 py-4 [&_code]:bg-[#e6e3db] [&_.text-muted]:text-[#55606a] [&_.text-link]:text-[#1f5a78] [&_li]:marker:text-[#55606a]">
          <Markdown text={a.body} refs={(r) => (snap.tasks.some((t) => t.id === r) ? () => openTask(r) : null)} />
        </div>
      )}
    </div>
  );
}

export function ArtifactPanel({ id }: { id: string }) {
  const { snap } = useLab();
  const { full } = useUi();
  const now = useNow();
  const a = snap.artifacts.find((x) => x.id === id);
  if (!a) return <p className="text-muted">This file isn&apos;t on the board.</p>;
  const uses = snap.messages.filter((m) => m.attach === a.id);
  const asks = uses.filter((m) => m.options).length;
  const ok = approval(a);
  const used = asks > 0 ? `used in ${count(asks, "ask", "asks")}` : uses.length > 0 ? `posted with ${count(uses.length, "message", "messages")}` : null;
  return (
    <div className="artifact-panel flex flex-col gap-3">
      <div className="flex items-center justify-end gap-1 text-meta">
        <button type="button" className="inline-flex min-h-8 items-center gap-1.5 rounded-control px-2 text-muted hover:bg-selected hover:text-ink" onClick={() => fullScreen(true)}>
          <Maximize2 className="size-3.5" strokeWidth={1.75} aria-hidden />
          Open full screen
        </button>
        <button type="button" className="inline-flex size-8 items-center justify-center rounded-control text-muted hover:bg-selected hover:text-ink" aria-label="Close the file" onClick={showWork}>
          <X className="size-4" strokeWidth={1.75} aria-hidden />
        </button>
      </div>
      <header className="flex flex-col gap-0.5">
        <h3 className="text-title font-bold break-all">{a.name}</h3>
        <p className="text-meta text-muted">
          Made by {a.by === scenario.me ? "you" : a.by} · {ago(a.t, now)} · {formatOf(a)} · v{a.version}
          {used && (
            <>
              {" · "}
              <button type="button" className="underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(uses[0].id)}>
                {used}
              </button>
            </>
          )}
        </p>
        {ok && <p className={cn("text-meta", ok.changed ? "font-bold text-ink" : "text-muted")}>{ok.text}</p>}
      </header>
      <Page a={a} />
      <div className="flex gap-4 text-meta">
        <button type="button" className="inline-flex min-h-8 items-center gap-1 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => download(a)}>
          <Download className="size-3.5" strokeWidth={1.75} aria-hidden />
          Download
        </button>
      </div>
      <ReplyAbout a={a} />
      {full && <FullScreen a={a} />}
    </div>
  );
}

/** ReplyAbout sends the file's author a message about it, naming the file and its version. */
function ReplyAbout({ a }: { a: Artifact }) {
  const [text, setText] = useState("");
  const [sent, setSent] = useState<Message | null>(null);
  if (a.by === scenario.me || scenario.people.some((p) => p.name === a.by)) return null;
  if (sent) {
    return (
      <p className="text-meta">
        Sent to {a.by}.{" "}
        <button type="button" className="text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(sent.id)}>
          See it in the conversation
        </button>
      </p>
    );
  }
  return (
    <form
      className="flex flex-col gap-1.5"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!text.trim()) return;
        const m = await post<Message>(`/v1/boards/${encodeURIComponent(scenario.board.name)}/messages`, {
          body: `About ${a.name} (v${a.version}${a.task ? `, ${a.task}` : ""}): ${text.trim()}`,
          to: [`@${a.by}`],
        });
        setSent(m);
      }}
    >
      <label htmlFor="reply-about" className="text-meta text-muted">
        Reply about this
      </label>
      <div className="flex gap-2">
        <input
          id="reply-about"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={`Goes to ${a.by}, naming ${a.name} v${a.version}`}
          className="min-h-10 min-w-0 flex-1 rounded-control border border-field-border bg-surface px-3 text-ink"
        />
        <button type="submit" disabled={!text.trim()} className="min-h-10 rounded-control bg-ink px-3.5 font-bold text-on-ink disabled:opacity-60">
          Send
        </button>
      </div>
    </form>
  );
}

function FullScreen({ a }: { a: Artifact }) {
  const now = useNow();
  return (
    <div role="dialog" aria-label={a.name} className="fixed inset-0 z-[60] flex flex-col gap-3 bg-background p-4 sm:p-6" onKeyDown={(e) => e.key === "Escape" && fullScreen(false)}>
      <header className="flex items-center gap-3">
        <FileIcon a={a} className="size-5" />
        <h2 className="min-w-0 flex-1 truncate text-title font-bold">
          {a.name} <span className="text-meta font-normal text-muted">by {a.by} · {ago(a.t, now)} · v{a.version}</span>
        </h2>
        <button type="button" autoFocus className="inline-flex min-h-10 items-center gap-1.5 rounded-control border border-field-border px-3 text-meta hover:bg-selected" onClick={() => fullScreen(false)}>
          <X className="size-4" strokeWidth={1.75} aria-hidden />
          Close
        </button>
      </header>
      <div className="min-h-0 flex-1">
        <Page a={a} tall />
      </div>
    </div>
  );
}
