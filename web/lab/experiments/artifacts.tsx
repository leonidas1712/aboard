"use client";

// EXPERIMENTAL, lab only: the board's files, as the Artifacts tab. Two groups: Artifacts
// (what agents made) and Content (what people attached). Maintained artifacts (the
// brief, a status page) come first, pinned, and say when they were last kept current;
// one past briefStaleAfter is marked as maybe out of date. A card opens a preview in
// place: HTML in a sandboxed frame that may run nothing and reach nothing (no
// allow-same-origin, no scripts), Markdown formatted, images shown. Who made it, its
// version and freshness sit around the frame, never inside it. Fed by the scenario's
// steps, not the API.

import { ArrowLeft, Download, FileCode, FileImage, FileText, History, Pin } from "lucide-react";
import type { ReactNode } from "react";
import { exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import type { Artifact } from "../scenario";
import { at, openArtifact, openTask, openThread, scenario, useLab, useUi } from "../store";
import { ago, briefStaleAfter, minutesSince, useNow } from "./common";
import { Markdown } from "./markdown";

const formats = { html: "HTML page", md: "Markdown", svg: "SVG image" } as const;
const mime = { html: "text/html", md: "text/markdown", svg: "image/svg+xml" } as const;

export function formatOf(a: Artifact): string {
  return formats[a.format];
}

export function FileIcon({ a, className }: { a: Artifact; className?: string }) {
  const Icon = a.format === "svg" ? FileImage : a.format === "html" ? FileCode : FileText;
  return <Icon className={cn("size-4 shrink-0 text-muted", className)} strokeWidth={1.5} aria-hidden />;
}

function download(a: Artifact) {
  const url = URL.createObjectURL(new Blob([a.body], { type: mime[a.format] }));
  const link = document.createElement("a");
  link.href = url;
  link.download = a.name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

const who = (n: string) => (n === scenario.me ? "you" : n);
const stale = (a: Artifact, now: number) => !!a.maintained && minutesSince(a.t, now) > briefStaleAfter;

export function ArtifactsView() {
  const { snap } = useLab();
  const { artifact } = useUi();
  const now = useNow();
  const open = snap.artifacts.find((a) => a.id === artifact);
  return (
    <div className="artifact-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1100px] px-4 sm:px-6">
        <div className="border-t border-rule pt-4 pb-8">{open ? <Preview a={open} now={now} /> : <List artifacts={snap.artifacts} now={now} />}</div>
      </div>
    </div>
  );
}

function List({ artifacts, now }: { artifacts: Artifact[]; now: number }) {
  const order = (a: Artifact, b: Artifact) => Number(!!b.maintained) - Number(!!a.maintained) || b.t - a.t;
  const made = artifacts.filter((a) => a.kind === "artifact").sort(order);
  const content = artifacts.filter((a) => a.kind === "content").sort(order);
  return (
    <div className="flex flex-col gap-6">
      {[
        { title: "Artifacts", help: "What agents made", list: made },
        { title: "Content", help: "What people attached", list: content },
      ].map(
        (g) =>
          g.list.length > 0 && (
            <section key={g.title} aria-labelledby={`files-${g.title}`} className="flex flex-col gap-2">
              <h3 id={`files-${g.title}`} className="flex items-baseline gap-2 text-meta font-bold text-muted">
                {g.title}
                <span className="font-normal">
                  {g.list.length} · {g.help}
                </span>
              </h3>
              <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {g.list.map((a) => (
                  <li key={a.id}>
                    <Card a={a} now={now} />
                  </li>
                ))}
              </ul>
            </section>
          ),
      )}
      {artifacts.length === 0 && <p className="text-muted">No files on this board yet.</p>}
    </div>
  );
}

function Fresh({ a, now }: { a: Artifact; now: number }) {
  const old = stale(a, now);
  return (
    <span title={`Updated ${exactTime(new Date(at(a.t)).toISOString())}`}>
      {old && <History className="mr-1 inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden />}
      {a.maintained ? "kept current, updated " : "updated "}
      {ago(a.t, now)}
      {old && ", may be out of date"}
    </span>
  );
}

function Card({ a, now }: { a: Artifact; now: number }) {
  return (
    <article
      className={cn(
        "artifact-card flex h-full flex-col gap-1.5 rounded-box border bg-surface px-3.5 py-3",
        stale(a, now) ? "border-dashed border-field-border" : "border-rule",
      )}
      data-artifact={a.id}
    >
      <div className="flex items-start gap-2">
        <FileIcon a={a} className="mt-1" />
        <h4 className="min-w-0 flex-1 truncate font-bold" title={a.name}>
          <button type="button" className="max-w-full truncate text-left hover:underline hover:decoration-1 hover:underline-offset-[3px]" onClick={() => openArtifact(a.id)}>
            {a.name}
          </button>
        </h4>
        {a.maintained && (
          <span className="flex shrink-0 items-center gap-1 text-meta text-muted" title="Pinned, and kept current by its author">
            <Pin className="size-3.5" strokeWidth={1.75} aria-hidden />
            Maintained
          </span>
        )}
      </div>
      {a.summary && <p className="text-meta">{a.summary}</p>}
      <p className="text-meta text-muted">
        {formatOf(a)} · by {who(a.by)} · version {a.version}
      </p>
      <p className="text-meta text-muted">
        <Fresh a={a} now={now} />
      </p>
      <div className="mt-auto flex gap-4 pt-1">
        <button type="button" className="min-h-9 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openArtifact(a.id)}>
          Open
        </button>
        <button
          type="button"
          className="inline-flex min-h-9 items-center gap-1 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
          onClick={() => download(a)}
        >
          <Download className="size-3.5" strokeWidth={1.75} aria-hidden />
          Download
        </button>
      </div>
    </article>
  );
}

function Preview({ a, now }: { a: Artifact; now: number }) {
  const { snap } = useLab();
  const posted = snap.messages.find((m) => m.attach === a.id);
  let body: ReactNode;
  if (a.format === "html") {
    body = (
      // sandbox="" lets the page run nothing and reach nothing, not even this page.
      <iframe
        title={a.name}
        sandbox=""
        srcDoc={a.body}
        className="artifact-frame h-[min(70vh,640px)] w-full rounded-control border border-rule bg-white"
      />
    );
  } else if (a.format === "svg") {
    body = (
      <img
        alt={a.summary ?? a.name}
        src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(a.body)}`}
        className="max-h-[70vh] max-w-full rounded-control border border-rule bg-white"
      />
    );
  } else {
    body = (
      <div className="rounded-box border border-rule bg-surface px-4 py-3">
        <Markdown text={a.body} refs={(r) => (snap.tasks.some((t) => t.id === r.toLowerCase()) ? () => openTask(r.toLowerCase()) : null)} />
      </div>
    );
  }
  return (
    <div className="artifact-preview flex flex-col gap-3">
      <button type="button" className="inline-flex min-h-9 items-center gap-1.5 self-start text-meta text-link hover:underline" onClick={() => openArtifact(null)}>
        <ArrowLeft className="size-3.5" strokeWidth={1.75} aria-hidden />
        All files
      </button>
      <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-col">
          <h3 className="flex items-center gap-2 text-title font-bold break-all">
            <FileIcon a={a} className="size-5" />
            {a.name}
          </h3>
          <p className="text-meta text-muted">
            {a.maintained && "Maintained · "}
            {formatOf(a)} · by {who(a.by)} · version {a.version} · <Fresh a={a} now={now} />
          </p>
          {posted && (
            <p className="text-meta">
              <button type="button" className="text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(posted.id)}>
                Posted with {who(posted.from)}&apos;s message
              </button>
            </p>
          )}
        </div>
        <button
          type="button"
          className="inline-flex min-h-10 items-center gap-1.5 rounded-control border border-field-border px-3 text-meta hover:bg-selected"
          onClick={() => download(a)}
        >
          <Download className="size-4" strokeWidth={1.75} aria-hidden />
          Download
        </button>
      </header>
      {body}
    </div>
  );
}
