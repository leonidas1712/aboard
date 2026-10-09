"use client";

// The brief: the board's maintained file brief.md or brief.html, which says what the
// board is for and where it stands. It sits under the "Now:" line with a byline of
// counted facts (which version, who wrote it, when, and what happened since). Markdown
// is drawn as elements, never as HTML; an HTML brief shows only in the sandboxed preview.
// A person edits it here: saving names the version they opened, so a version someone
// wrote meanwhile is never overwritten, and their text stays on screen until they choose
// what to do with it. Every version stays in the file's history.

import { History, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ApiError, type BriefSummary, type FileChanged, type MemberRef, fileText, putFile } from "./api";
import { SenderMark } from "./agent-mark";
import { CopyButton } from "./board-details";
import { Problem } from "./chrome";
import { HtmlFrame } from "./files";
import { Markdown } from "./markdown";
import { count, exactTime, relativeTime } from "./words";

type Format = "md" | "html";
const fileName: Record<Format, "brief.md" | "brief.html"> = { md: "brief.md", html: "brief.html" };
const formatLabel: Record<Format, string> = { md: "Markdown", html: "HTML" };

/** The keeper's nudge thresholds (D213): past 30 messages or 3 tasks done, and an hour old. */
const staleMessages = 30;
const staleTasks = 3;
const staleAge = 60 * 60_000;

function formatOf(b: BriefSummary): Format {
  return b.name === "brief.html" ? "html" : "md";
}

function nameOf(b: BriefSummary): string {
  return b.name ?? "brief.md";
}

function who(by: Pick<MemberRef, "name" | "kind">, me: string | null): string {
  return by.kind === "human" && by.name === me ? "you" : by.name;
}

function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(tick);
  }, []);
  return now;
}

/** since lists what happened on the board after the brief's version, or nothing when nothing did. */
function since(b: BriefSummary): string {
  const f = b.freshness;
  return [
    f.messages_since > 0 && count(f.messages_since, "message", "messages"),
    f.tasks_done_since > 0 && count(f.tasks_done_since, "task done", "tasks done"),
    f.answers_since > 0 && count(f.answers_since, "answer", "answers"),
  ]
    .filter(Boolean)
    .join(", ");
}

function isStale(b: BriefSummary, now: number): boolean {
  const old = now - new Date(b.at).getTime() >= staleAge;
  return old && (b.freshness.messages_since >= staleMessages || b.freshness.tasks_done_since >= staleTasks);
}

/** summary is where the brief starts: its first paragraph that isn't a heading, as plain text. */
function summary(text: string, format: Format): string {
  if (format === "html") {
    // A parsed document is inert: nothing in it runs or loads.
    const doc = new DOMParser().parseFromString(text, "text/html");
    const first = [...doc.body.querySelectorAll("p, li")].map((el) => el.textContent?.trim() ?? "").find(Boolean);
    return (first ?? doc.body.textContent ?? "").replace(/\s+/g, " ").trim();
  }
  const lines: string[] = [];
  let fence = false;
  for (const raw of text.replace(/\r\n?/g, "\n").split("\n")) {
    const l = raw.trim();
    if (l.startsWith("```")) {
      fence = !fence;
      continue;
    }
    if (fence || l.startsWith("#")) {
      if (lines.length) break;
      continue;
    }
    if (!l) {
      if (lines.length) break;
      continue;
    }
    lines.push(l.replace(/^>\s?/, "").replace(/^([-*+]|\d+[.)])\s+/, ""));
  }
  return plainInline(lines.join(" "));
}

/**
 * plainInline is a line of Markdown as it reads: emphasis, strike-through and code
 * marks dropped, a link or image as its words, escapes undone. An underscore or star
 * inside a word (snake_case, 2*3) stays, as Markdown keeps it.
 */
function plainInline(md: string): string {
  return md
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/<((?:https?|mailto):[^>\s]+)>/g, "$1")
    .replace(/`+([^`]+?)`+/g, "$1")
    .replace(/~~(\S(?:.*?\S)?)~~/g, "$1")
    .replace(/(^|[^\w*\\])(\*\*|__)(\S(?:.*?[^\s\\])?)\2(?=[^\w*]|$)/g, "$1$3")
    .replace(/(^|[^\w*\\])([*_])(\S(?:.*?[^\s\\])?)\2(?=[^\w*]|$)/g, "$1$3")
    .replace(/\\([\\`*_{}[\]()#+\-.!~>])/g, "$1");
}

/** Read is a version's text, keyed by file and version so a newer one never shows under an older byline. */
type Read = { key: string; body: string };

function keyOf(fileId: string, version: number): string {
  return `${fileId}@${version}`;
}

function useBriefText(board: string, b: BriefSummary | null | undefined, seeded: Read | null) {
  const [read, setRead] = useState<Read | null>(null);
  const [error, setError] = useState<unknown>(null);
  const key = b ? keyOf(b.file_id, b.version) : null;
  useEffect(() => {
    if (!b || !key) return;
    if (seeded?.key === key) {
      setRead(seeded);
      return;
    }
    let live = true;
    setError(null);
    fileText(board, b.file_id, b.version).then(
      (body) => live && setRead({ key, body }),
      (e) => live && setError(e),
    );
    return () => {
      live = false;
    };
    // seeded only short-cuts the read of a version this page just wrote.
  }, [board, key]);
  return { text: read && read.key === key ? read.body : null, error };
}

/**
 * Editing is what the editor holds: the text, its format, and the brief it was opened
 * from (its file and version), which a save names as the one it replaces. fileId is
 * null for a new brief.
 */
type Editing = {
  draft: string;
  opened: string;
  format: Format;
  fileId: string | null;
  base: number;
  /** from is the format of the brief opened, null for a new one. */
  from: Format | null;
  /** kept is the person's own text, set aside when they continue from a newer version. */
  kept: string | null;
};

export function Brief({
  board,
  brief,
  me,
  canEdit,
  onChanged,
  openHistory,
  identity,
}: {
  board: string;
  /** identity is a member's identity colour, for the writer's mark in the byline. */
  identity: (m: MemberRef) => number;
  /** brief is undefined on a server without files: then nothing shows. */
  brief: BriefSummary | null | undefined;
  me: string | null;
  canEdit: boolean;
  /** onChanged reads the board again, after a save or a refused one. */
  onChanged: () => void;
  /** openHistory opens the brief's file, with all its versions, in the side panel. */
  openHistory: (fileId: string) => void;
}) {
  const now = useNow();
  const [open, setOpen] = useState(false);
  const [asking, setAsking] = useState(false);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [seeded, setSeeded] = useState<Read | null>(null);
  const { text, error } = useBriefText(board, brief, seeded);
  const toggle = useId();

  if (brief === undefined) return null;

  const startEditing = (from: BriefSummary | null, body: string) => {
    setEditing({
      draft: body,
      opened: body,
      format: from ? formatOf(from) : "md",
      fileId: from?.file_id ?? null,
      base: from?.version ?? 0,
      from: from ? formatOf(from) : null,
      kept: null,
    });
    setOpen(true);
  };

  if (editing) {
    return (
      <section aria-label="Brief" data-editing className="brief quiet-scroll mb-3 max-h-[62dvh] overflow-y-auto overscroll-contain rounded-box border border-rule bg-surface px-4 py-3">
        <Editor
          board={board}
          brief={brief}
          editing={editing}
          setEditing={setEditing}
          me={me}
          now={now}
          onSaved={(written) => {
            setSeeded(written);
            setEditing(null);
            setOpen(true);
            onChanged();
          }}
          onRefused={onChanged}
          onCancel={() => setEditing(null)}
        />
      </section>
    );
  }

  if (brief === null) {
    return (
      <section aria-label="Brief" className="brief brief-empty mb-3 flex flex-col gap-3 rounded-box border border-dashed border-field-border/60 px-4 py-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-6">
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <h2 className="text-meta font-bold text-ink">No brief yet</h2>
            <p className="text-meta text-muted">
              A brief says what this board is for and where it stands, in one place anyone joining reads first. Agents write it with{" "}
              <code className="brief-code whitespace-nowrap">aboard brief put brief.md</code>
              {canEdit ? "; you can write it here, or " : ". "}
              <AskButton asking={asking} setAsking={setAsking} label={canEdit ? "ask an agent to" : "Ask an agent to write it"} inline />
              {canEdit ? "." : ""}
            </p>
          </div>
          {canEdit && (
            <Button type="button" variant="secondary" className="self-start sm:self-auto" onClick={() => startEditing(null, "")}>
              Write the brief
            </Button>
          )}
        </div>
        {asking && <AskAgent prompt={writePrompt(board)} close={() => setAsking(false)} />}
      </section>
    );
  }

  const format = formatOf(brief);
  const stale = isStale(brief, now);
  const happened = since(brief);
  const short = text === null ? null : summary(text, format);
  return (
    <section
      aria-label="Brief"
      data-open={open || undefined}
      className={cn("brief quiet-scroll mb-4 rounded-box border bg-surface px-5 py-4", open && "max-h-[62dvh] overflow-y-auto overscroll-contain", stale ? "border-dashed border-field-border" : "border-rule")}
    >
      {/* While the whole brief is open its box scrolls, so the header row stays pinned to
          the top: Show less is always in reach. */}
      <div className={cn("flex flex-wrap items-baseline gap-x-2 gap-y-0.5", open && "sticky -top-4 z-10 -mx-5 -mt-4 bg-surface px-5 pt-4 pb-1")}>
        <h2 className="text-meta font-bold text-ink">Brief</h2>
        <p className="brief-byline min-w-0 flex-1 text-meta text-muted">
          <span title={`Version ${brief.version}, written ${exactTime(brief.at)}`}>
            Updated by{" "}
            <span className="brief-writer inline-flex items-baseline gap-1">
              <SenderMark name={brief.by.name} kind={brief.by.kind} harness={brief.by.harness} identity={identity(brief.by)} className="size-4 translate-y-[3px] self-start rounded-[4px] text-[9px]" />
              <span className="text-ink">{who(brief.by, me)}</span>
            </span>{" "}
            · <time dateTime={brief.at}>{relativeTime(brief.at, now)}</time> · {nameOf(brief)} v{brief.version}
          </span>
          {happened && <span title="What happened on the board after this version was written"> · since then {happened}</span>}
          {stale && (
            <span>
              {" · "}
              <History className="inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-hidden /> may be out of date
            </span>
          )}
        </p>
        <button
          type="button"
          aria-expanded={open}
          aria-controls={toggle}
          onClick={() => setOpen(!open)}
          className="min-h-11 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline sm:min-h-8"
        >
          {open ? "Show less" : "Show full brief"}
        </button>
      </div>
      <div id={toggle}>
        {error !== null ? (
          <div className="mt-2">
            <Problem error={error} />
          </div>
        ) : text === null ? (
          <p className="mt-1 h-6 w-2/3 rounded-control bg-selected motion-safe:animate-pulse" role="status" aria-label="Loading the brief" />
        ) : !open ? (
          <p className="brief-summary mt-2 line-clamp-2 text-now">{short || <span className="text-muted">The brief has no text yet.</span>}</p>
        ) : (
          <div className="animate-fade-in">
            <div className="mt-2 border-t border-rule pt-3">
              {format === "html" ? (
                <HtmlFrame title={`The brief, ${nameOf(brief)} v${brief.version}`} html={text} className="h-[min(42dvh,480px)]" />
              ) : (
                <div className="brief-body" role="document" aria-label={`The brief, ${nameOf(brief)} v${brief.version}`}>
                  <Markdown text={text} />
                </div>
              )}
            </div>
            <p className="mt-2 flex flex-wrap items-center gap-x-5 text-meta">
              {canEdit && (
                <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => startEditing(brief, text)}>
                  Edit
                </button>
              )}
              <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openHistory(brief.file_id)}>
                {brief.version === 1 ? "Open as a file" : `All ${brief.version} versions`}
              </button>
              <AskButton asking={asking} setAsking={setAsking} label="Ask an agent to update" />
            </p>
            {asking && <AskAgent prompt={updatePrompt(board, brief)} close={() => setAsking(false)} className="mt-1" />}
          </div>
        )}
      </div>
    </section>
  );
}

function Editor({
  board,
  brief,
  editing,
  setEditing,
  me,
  now,
  onSaved,
  onRefused,
  onCancel,
}: {
  board: string;
  brief: BriefSummary | null;
  editing: Editing;
  setEditing: (e: Editing | null) => void;
  me: string | null;
  now: number;
  onSaved: (written: Read) => void;
  onRefused: () => void;
  onCancel: () => void;
}) {
  const ed = editing;
  const [mode, setMode] = useState<"write" | "preview">("write");
  const [busy, setBusy] = useState(false);
  const [refused, setRefused] = useState<ApiError | null>(null);
  const [problem, setProblem] = useState<unknown>(null);
  const [discard, setDiscard] = useState(false);
  const area = useRef<HTMLTextAreaElement>(null);
  const ids = useId();
  useEffect(() => {
    area.current?.focus({ preventScroll: true });
  }, []);

  // The brief on the board now, when it isn't the one this editor opened: someone saved
  // a newer version, switched its format or took it off while the person was writing.
  const moved = ed.fileId === null ? brief !== null : brief === null || brief.file_id !== ed.fileId || brief.version !== ed.base;
  const newer = moved ? brief : null;
  const switching = ed.from !== null && ed.format !== ed.from;
  const name = fileName[ed.format];
  const nextVersion = ed.fileId === null || switching ? 1 : ed.base + 1;

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (busy) return;
    setBusy(true);
    setProblem(null);
    try {
      const body = new Blob([ed.draft], { type: ed.format === "md" ? "text/markdown" : "text/html" });
      const f = await putFile(board, name, ed.base, body, ed.fileId ?? undefined, undefined, { brief: true, replaceFormat: switching });
      setRefused(null);
      onSaved({ key: keyOf(f.id, f.latest.version), body: ed.draft });
    } catch (err) {
      if (err instanceof ApiError && (err.code === "file_changed" || err.code === "file_exists" || err.code === "brief_exists")) {
        setRefused(err);
        onRefused();
      } else setProblem(err);
    } finally {
      setBusy(false);
    }
  };

  // Continuing from the newer version puts its text in the editor and the person's own
  // text beside it, to copy from; nothing they wrote is dropped.
  const continueFrom = async (b: BriefSummary | null) => {
    try {
      const body = b ? await fileText(board, b.file_id, b.version) : ed.draft;
      setEditing({
        draft: body,
        opened: body,
        format: b ? formatOf(b) : ed.format,
        fileId: b?.file_id ?? null,
        base: b?.version ?? 0,
        from: b ? formatOf(b) : null,
        kept: b ? ed.draft : null,
      });
      setRefused(null);
      setMode("write");
    } catch (err) {
      setProblem(err);
    }
  };

  const keys = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void save();
    }
  };

  const dirty = ed.draft !== ed.opened;
  const format = (f: Format) => setEditing({ ...ed, format: f });

  return (
    <form onSubmit={save} className="brief-editor flex animate-fade-in flex-col gap-3" aria-label={ed.fileId ? `Edit the brief, ${fileName[ed.from ?? "md"]} v${ed.base}` : "Write the brief"}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h2 className="text-meta font-bold text-ink">{ed.fileId ? `Editing the brief, from v${ed.base}` : "Write the brief"}</h2>
        <div className="flex items-center gap-x-3 sm:gap-x-4">
          <fieldset className="flex items-center gap-2 text-meta">
            <legend className="sr-only">Format</legend>
            <span className="hidden text-muted sm:inline" aria-hidden>
              Format
            </span>
            <Segmented
              name={`${ids}-format`}
              value={ed.format}
              options={(["md", "html"] as Format[]).map((f) => ({ value: f, label: formatLabel[f] }))}
              onChange={(v) => format(v as Format)}
            />
          </fieldset>
          <div role="group" aria-label="Show" className="flex items-center text-meta">
            <Segmented
              name={`${ids}-mode`}
              value={mode}
              options={[
                { value: "write", label: "Write" },
                { value: "preview", label: "Preview" },
              ]}
              onChange={(v) => setMode(v as "write" | "preview")}
            />
          </div>
        </div>
      </div>

      {(newer || refused || (ed.fileId !== null && brief === null)) && (
        <Changed
          refused={refused}
          newer={newer}
          removed={ed.fileId !== null && brief === null}
          base={ed.base}
          draft={ed.draft}
          me={me}
          now={now}
          board={board}
          onContinue={() => void continueFrom(newer)}
        />
      )}

      {ed.kept !== null && (
        <section aria-label="Your earlier text" className="brief-kept flex flex-col gap-2 rounded-control border border-rule bg-background px-3 py-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-meta font-bold text-muted">Your text, not saved</h3>
            <span className="flex flex-wrap items-center gap-3">
              <CopyButton text={ed.kept} label="Copy your text" variant="secondary" />
              <button type="button" className="min-h-11 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => setEditing({ ...ed, kept: null })}>
                Put it away
              </button>
            </span>
          </div>
          <textarea readOnly value={ed.kept} aria-label="Your earlier text" rows={5} className="quiet-scroll w-full resize-y rounded-control bg-surface px-3 py-2 font-mono text-meta text-ink pointer-coarse:text-[16px]" />
        </section>
      )}

      <label htmlFor={`${ids}-text`} className="sr-only">
        The brief, in {formatLabel[ed.format]}
      </label>
      <textarea
        id={`${ids}-text`}
        ref={area}
        value={ed.draft}
        onChange={(e) => setEditing({ ...ed, draft: e.target.value })}
        onKeyDown={keys}
        rows={10}
        spellCheck={ed.format === "md"}
        placeholder={ed.format === "md" ? "# What this board is for\n\nWhere it stands, in a sentence or two.\n\n## Who's doing what\n\n## Next" : "<h1>What this board is for</h1>\n<p>Where it stands, in a sentence or two.</p>"}
        className={cn(
          "brief-text quiet-scroll min-h-40 w-full resize-y rounded-control border border-field-border bg-background px-3 py-2 font-mono text-meta leading-relaxed text-ink placeholder:text-muted/80 pointer-coarse:text-[16px]",
          mode === "preview" && "hidden",
        )}
      />
      {mode === "preview" && (
        <div aria-label="Preview" role="region" className="brief-preview">
          {ed.draft.trim() === "" ? (
            <p className="rounded-control border border-dashed border-rule px-3 py-6 text-center text-meta text-muted">Nothing to preview yet.</p>
          ) : ed.format === "html" ? (
            <HtmlFrame title="Preview of the brief" html={ed.draft} className="h-[min(40dvh,420px)]" />
          ) : (
            <div className="rounded-control border border-rule px-3 py-2">
              <Markdown text={ed.draft} />
            </div>
          )}
        </div>
      )}

      <p className="text-meta text-muted" id={`${ids}-note`}>
        {switching ? (
          <>
            Saving switches the brief to <strong className="text-ink">{name}</strong>, v1. {fileName[ed.from!]} leaves the board, and its versions stay in the record.
          </>
        ) : (
          <>
            {formatLabel[ed.format]}
            {ed.format === "html" ? ", shown in a sandbox with no scripts or links" : ""}. Saving makes {name} v{nextVersion}; the record keeps every version and who wrote it.
          </>
        )}
      </p>
      {problem !== null && <Problem error={problem} />}

      {discard ? (
        <div role="group" aria-label="Discard your changes?" className="flex flex-wrap items-center gap-3">
          <span>Discard your changes?</span>
          <Button type="button" variant="secondary" onClick={onCancel}>
            Discard
          </Button>
          <Button type="button" variant="quiet" onClick={() => setDiscard(false)}>
            Keep editing
          </Button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" disabled={busy} aria-describedby={`${ids}-note`}>
            {busy ? "Saving…" : switching ? `Save as ${name}` : `Save version ${nextVersion}`}
          </Button>
          <Button type="button" variant="quiet" onClick={() => (dirty ? setDiscard(true) : onCancel())}>
            Cancel
          </Button>
          <span className="hidden text-meta text-muted sm:inline">
            <kbd>⌘</kbd>/<kbd>Ctrl</kbd> <kbd>Enter</kbd> saves
          </span>
        </div>
      )}
    </form>
  );
}

/**
 * Changed says the brief on the board is no longer the one this editor opened, and
 * what to do: see the newer version, copy their own text, or carry on from the newer
 * version with their text beside it. Before a save it's a heads-up; after a refused
 * save it says nothing was saved.
 */
function Changed({
  refused,
  newer,
  removed,
  base,
  draft,
  me,
  now,
  board,
  onContinue,
}: {
  refused: ApiError | null;
  newer: BriefSummary | null;
  removed: boolean;
  base: number;
  draft: string;
  me: string | null;
  now: number;
  board: string;
  onContinue: () => void;
}) {
  const [showing, setShowing] = useState(false);
  const { text, error } = useBriefText(board, showing ? newer : null, null);
  const d = refused?.details as Partial<FileChanged> | undefined;
  let what: ReactNode;
  if (newer) {
    what = (
      <>
        <strong>{who(newer.by, me)}</strong> wrote {nameOf(newer)} v{newer.version} {relativeTime(newer.at, now)}
        {base > 0 ? `, after v${base}, which you opened` : ""}.
      </>
    );
  } else if (removed) {
    what = <>The brief was taken off the board after you opened it.</>;
  } else if (d?.version && d.by) {
    what = (
      <>
        <strong>{who(d.by, me)}</strong> wrote v{d.version}
        {d.at ? ` ${relativeTime(d.at, now)}` : ""}, after the version you opened.
      </>
    );
  } else {
    what = <>The brief changed after you opened it.</>;
  }
  return (
    <div role={refused ? "alert" : "status"} className="brief-conflict flex flex-col gap-2 rounded-control border border-field-border bg-background px-3 py-2.5">
      <p>
        {what} {refused ? "Nothing was saved. Your text is still here." : "Saving now would be refused, so nothing is overwritten. Your text stays here."}
      </p>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        {newer && (
          <button
            type="button"
            aria-expanded={showing}
            onClick={() => setShowing(!showing)}
            className="min-h-11 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
          >
            {showing ? `Hide v${newer.version}` : `Show v${newer.version}`}
          </button>
        )}
        <CopyButton text={draft} label="Copy your text" variant="secondary" />
        {(newer || removed) && (
          <Button type="button" variant="secondary" onClick={onContinue}>
            {newer ? `Continue from v${newer.version}` : "Write it as a new brief"}
          </Button>
        )}
      </div>
      {showing && newer && (
        <div className="animate-fade-in">
          {error !== null ? (
            <Problem error={error} />
          ) : text === null ? (
            <p className="text-meta text-muted" role="status">
              Loading v{newer.version}…
            </p>
          ) : formatOf(newer) === "html" ? (
            <HtmlFrame title={`The brief, ${nameOf(newer)} v${newer.version}`} html={text} className="h-[min(30dvh,320px)]" />
          ) : (
            <div className="brief-newer border-t border-rule pt-2" role="document" aria-label={`${nameOf(newer)} v${newer.version}`}>
              <Markdown text={text} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** Segmented is a pair of choices as radio buttons drawn as one control, reachable with arrow keys. */
function Segmented({ name, value, options, onChange }: { name: string; value: string; options: { value: string; label: string }[]; onChange: (v: string) => void }) {
  return (
    <span className="inline-flex rounded-control border border-rule bg-background p-0.5">
      {options.map((o) => (
        <label
          key={o.value}
          className={cn(
            "relative inline-flex min-h-9 pointer-coarse:min-h-10 cursor-pointer items-center rounded-[6px] px-3 transition-colors duration-[140ms] ease-out has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-accent-strong",
            value === o.value ? "bg-selected font-bold text-ink" : "text-muted hover:text-ink",
          )}
        >
          <input type="radio" name={name} value={o.value} checked={value === o.value} onChange={() => onChange(o.value)} className="sr-only" />
          {o.label}
        </label>
      ))}
    </span>
  );
}

/** writePrompt asks one of the person's agents to write the board's first brief. */
function writePrompt(board: string): string {
  return `On the Aboard board ${board}, write the board's brief. Read the board first (aboard read, aboard task list), then write brief.md: what the board is for, who is here and what each does, how we work, where things stand, and what's next. Save it with aboard brief put brief.md.`;
}

/** updatePrompt asks one of the person's agents to bring the brief up to date. */
function updatePrompt(board: string, b: BriefSummary): string {
  const name = nameOf(b);
  return `On the Aboard board ${board}, bring the brief up to date. Run aboard brief get ${name}, read what happened since v${b.version} (aboard read, aboard task list), update where things stand and what's next, keep what is still true, then save it with aboard brief put ${name}.`;
}

/** AskButton opens the prompt; inline, it sits in a sentence at the sentence's height. */
function AskButton({ asking, setAsking, label, inline }: { asking: boolean; setAsking: (v: boolean) => void; label: string; inline?: boolean }) {
  return (
    <button
      type="button"
      aria-expanded={asking}
      onClick={() => setAsking(!asking)}
      className={cn("ask-agent text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline", !inline && "min-h-11 sm:min-h-8")}
    >
      {label}
    </button>
  );
}

/**
 * AskAgent shows the prompt a person pastes into one of their agents' sessions to have it
 * write or update the brief, with a copy button, the way Add an agent shows a join prompt.
 */
function AskAgent({ prompt, close, className }: { prompt: string; close: () => void; className?: string }) {
  const id = useId();
  return (
    <section
      aria-labelledby={`${id}-title`}
      className={cn("ask-agent-prompt flex w-full flex-col gap-2 rounded-box bg-background px-3.5 pt-2 pb-3 animate-fade-in", className)}
    >
      <div className="flex items-center justify-between gap-2">
        <h3 id={`${id}-title`} className="text-meta font-bold text-ink">
          Ask an agent
        </h3>
        <button
          type="button"
          onClick={close}
          aria-label="Close Ask an agent"
          title="Close"
          className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
        >
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <p className="-mt-1 text-meta text-muted">Paste this into a session of one of your agents on this board. Its new version shows here when it saves.</p>
      <pre
        aria-label="Prompt for the agent's session"
        className="rounded-control border border-rule bg-surface px-3 py-2.5 font-sans text-body whitespace-pre-wrap break-words select-all"
      >
        {prompt}
      </pre>
      <CopyButton text={prompt} label="Copy prompt" variant="secondary" />
    </section>
  );
}
