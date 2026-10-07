"use client";

// The board's files: the Files view lists every file on the board, and a file opens in
// the side panel with its versions, a preview and a download of any version, exactly as
// it was uploaded. A person may upload a file, or a new version of one; every upload
// names the version it replaces, and when someone else wrote a newer one first the
// server stores nothing and the panel says who and when.

import { ArrowLeft, Download, FileCode, FileImage, FileText, File as FileGeneric, Paperclip } from "lucide-react";
import { type DragEvent, type FormEvent, type ReactNode, useCallback, useEffect, useId, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import {
  ApiError,
  type BoardFile,
  type FileChanged,
  type FileDetail,
  type FileList,
  type FileVersion,
  type MemberRef,
  fileText,
  getFile,
  listFiles,
  putFile,
  versionUrl,
} from "./api";
import { Problem } from "./chrome";
import { Markdown } from "./markdown";
import { TaskChips } from "./task-ui";
import { SenderMark } from "./timeline";
import { count, exactTime, relativeTime } from "./words";

/** The most a board takes in one file (spec/openapi.yaml, putFile). */
const maxBytes = 50 * 1024 * 1024;
/** The most text the panel previews; a longer file is offered as a download. */
const maxPreview = 512 * 1024;

const namePattern = /^[A-Za-z0-9][A-Za-z0-9._-]*(\/[A-Za-z0-9][A-Za-z0-9._-]*)*$/;

export function useFiles(board: string, activity: number, head: number | undefined, enabled: boolean) {
  const [list, setList] = useState<FileList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const request = useRef(0);
  const [again, setAgain] = useState(0);
  useEffect(() => {
    const generation = ++request.current;
    if (!enabled) {
      setList(null);
      return;
    }
    listFiles(board).then(
      (value) => {
        if (generation !== request.current) return;
        setList(value);
        setError(null);
      },
      (e) => {
        if (generation !== request.current) return;
        // A server without files (or a board the person can no longer see) shows no Files view.
        if (e instanceof ApiError && [403, 404, 501].includes(e.status)) {
          setList(null);
          return;
        }
        setError(e);
      },
    );
    return () => {
      request.current++;
    };
  }, [board, activity, head, enabled, again]);
  const reload = useCallback(() => setAgain((n) => n + 1), []);
  return { list, error, reload };
}

type Kind = "markdown" | "text" | "image" | "html" | "other";

const textExtensions = new Set(["txt", "md", "markdown", "json", "yaml", "yml", "toml", "csv", "tsv", "log", "xml", "ini", "cfg", "conf", "env", "go", "ts", "tsx", "js", "jsx", "mjs", "py", "rb", "rs", "java", "kt", "swift", "c", "h", "cpp", "sh", "sql", "css", "diff", "patch"]);
const imageExtensions = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif"]);

function extension(name: string): string {
  const base = name.split("/").pop() ?? name;
  const dot = base.lastIndexOf(".");
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : "";
}

/** kindOf says how the panel shows a version: from its media type, else its name. */
export function kindOf(name: string, mediaType: string): Kind {
  const type = mediaType.split(";")[0].trim().toLowerCase();
  const ext = extension(name);
  if (type === "text/html" || ext === "html" || ext === "htm") return "html";
  if (type === "text/markdown" || ext === "md" || ext === "markdown") return "markdown";
  if (type.startsWith("image/") || imageExtensions.has(ext)) return "image";
  if (type.startsWith("text/") || type === "application/json" || type.endsWith("+json") || type.endsWith("+xml") || type === "application/xml" || textExtensions.has(ext)) return "text";
  return "other";
}

/** typeName is a file's type in words, as a person would say it. */
export function typeName(name: string, mediaType: string): string {
  const kind = kindOf(name, mediaType);
  const ext = extension(name);
  if (kind === "markdown") return "Markdown";
  if (kind === "html") return "HTML";
  if (kind === "image") return ext === "svg" ? "SVG image" : "Image";
  if (ext === "json") return "JSON";
  if (ext === "csv") return "CSV";
  if (ext === "yaml" || ext === "yml") return "YAML";
  if (kind === "text") return "Text";
  if (ext === "pdf" || mediaType.startsWith("application/pdf")) return "PDF";
  return ext ? ext.toUpperCase() : "File";
}

/** size says how big a file is, in the decimal units the CLI uses. */
export function size(bytes: number): string {
  if (bytes < 1000) return count(bytes, "byte", "bytes");
  if (bytes < 1_000_000) return `${(bytes / 1000).toFixed(1)} kB`;
  return `${(bytes / 1_000_000).toFixed(1)} MB`;
}

function FileIcon({ name, mediaType, className }: { name: string; mediaType: string; className?: string }) {
  const kind = kindOf(name, mediaType);
  const Icon = kind === "image" ? FileImage : kind === "html" ? FileCode : kind === "other" ? FileGeneric : FileText;
  return <Icon className={cn("size-[18px] shrink-0 text-muted", className)} strokeWidth={1.5} aria-hidden />;
}

/** downloadName is what a version saves as: the file's own name, with the version for an older one. */
function downloadName(name: string, v: number, latest: number): string {
  const base = name.split("/").pop() ?? name;
  if (v === latest) return base;
  const dot = base.lastIndexOf(".");
  return dot > 0 ? `${base.slice(0, dot)}-v${v}${base.slice(dot)}` : `${base}-v${v}`;
}

type Identity = (m: MemberRef) => number;

function Who({ by, me, identity }: { by: MemberRef; me: string | null; identity: Identity }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5 align-top">
      <SenderMark name={by.name} kind={by.kind} identity={identity(by)} className="size-5 rounded-[5px] text-[10px]" />
      <span className="min-w-0 truncate">{by.kind === "human" && by.name === me ? "you" : by.name}</span>
    </span>
  );
}

function When({ at, now }: { at: string; now: number }) {
  return (
    <time dateTime={at} title={exactTime(at)}>
      {relativeTime(at, now)}
    </time>
  );
}

function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(tick);
  }, []);
  return now;
}

type Show = "all" | "maintained" | "people";
type Sort = "newest" | "name";

const shows: { key: Show; label: string }[] = [
  { key: "all", label: "All" },
  { key: "maintained", label: "Maintained" },
  { key: "people", label: "From people" },
];

/**
 * useDrop makes an element a place to drop a file from this computer. over is true
 * while a file is dragged over it; onFile gets the first file dropped and how many came.
 */
export function useDrop(enabled: boolean, onFile: (f: File, count: number) => void) {
  const depth = useRef(0);
  const [over, setOver] = useState(false);
  const carriesFiles = (e: DragEvent) => enabled && Array.from(e.dataTransfer.types).includes("Files");
  useEffect(() => {
    if (!enabled) {
      depth.current = 0;
      setOver(false);
    }
  }, [enabled]);
  const props = {
    onDragEnter: (e: DragEvent) => {
      if (!carriesFiles(e)) return;
      e.preventDefault();
      depth.current++;
      setOver(true);
    },
    onDragOver: (e: DragEvent) => {
      if (!carriesFiles(e)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
    },
    onDragLeave: (e: DragEvent) => {
      if (!carriesFiles(e)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOver(false);
    },
    onDrop: (e: DragEvent) => {
      if (!carriesFiles(e)) return;
      e.preventDefault();
      depth.current = 0;
      setOver(false);
      const f = e.dataTransfer.files[0];
      if (f) onFile(f, e.dataTransfer.files.length);
    },
  };
  return { over, props };
}

/** DropHint is the calm outline and line of text shown while a file is dragged over a drop place. */
function DropHint({ over, text }: { over: boolean; text: string }) {
  if (!over) return null;
  return (
    <div aria-hidden className="drop-hint pointer-events-none absolute inset-1.5 z-10 flex items-center justify-center rounded-box border-2 border-dashed border-accent bg-[var(--drop)] p-4 animate-fade-in">
      <p className="inline-flex items-center gap-2 rounded-box border border-rule bg-surface px-4 py-3 font-bold text-ink">
        <Paperclip className="size-4 text-accent" strokeWidth={1.75} aria-hidden />
        {text}
      </p>
    </div>
  );
}

/** Attach is the button that picks a file from this computer. */
function Attach({ onClick, label, disabled }: { onClick: () => void; label: string; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="attach inline-flex min-h-11 items-center gap-2 rounded-control border border-ink px-3.5 font-medium text-ink transition-colors duration-[140ms] ease-out hover:bg-selected disabled:opacity-60"
    >
      <Paperclip className="size-4" strokeWidth={1.75} aria-hidden />
      {label}
    </button>
  );
}

/** FilesView is every file on the board, newest first, each one click from its panel. A file dropped on it starts an upload. */
export function FilesView({
  board,
  files,
  more,
  selected,
  open,
  identity,
  me,
  canUpload,
  onUploaded,
}: {
  board: string;
  files: BoardFile[];
  more: boolean;
  selected: string | null;
  open: (id: string) => void;
  identity: Identity;
  me: string | null;
  canUpload: boolean;
  onUploaded: (f: BoardFile) => void;
}) {
  const now = useNow();
  const [show, setShow] = useState<Show>("all");
  const [sort, setSort] = useState<Sort>("newest");
  const [picked, setPicked] = useState<{ file: File; extra: number; n: number } | null>(null);
  const chooser = useRef<HTMLInputElement>(null);
  const pick = (file: File, n: number) => setPicked((p) => ({ file, extra: n - 1, n: (p?.n ?? 0) + 1 }));
  const drop = useDrop(canUpload, pick);
  const shown = files
    .filter((f) => (show === "maintained" ? f.maintained : show === "people" ? f.latest.by.kind === "human" : true))
    .sort((a, b) => (sort === "name" ? a.name.localeCompare(b.name) : b.latest.at.localeCompare(a.latest.at) || b.latest.seq - a.latest.seq));
  const control = (on: boolean) =>
    cn(
      "min-h-11 rounded-control px-2.5 transition-colors duration-[140ms] ease-out",
      on ? "bg-selected font-bold text-ink" : "text-muted hover:text-ink",
    );
  const choose = () => chooser.current?.click();
  const form = picked && (
    <NewFile
      key={picked.n}
      board={board}
      picked={picked.file}
      extra={picked.extra}
      files={files}
      open={open}
      onDone={(f) => {
        setPicked(null);
        if (f) onUploaded(f);
      }}
      onChooseAnother={choose}
    />
  );
  return (
    <div className="relative flex min-h-0 flex-1 flex-col" data-drop={drop.over ? "over" : undefined} {...drop.props}>
      <input
        ref={chooser}
        type="file"
        className="sr-only"
        tabIndex={-1}
        aria-hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) pick(f, e.target.files?.length ?? 1);
          e.target.value = "";
        }}
      />
      <DropHint over={drop.over} text="Drop to upload it to this board" />
      <div className="files-view quiet-scroll min-h-0 flex-1 overflow-y-auto animate-appear">
        <div className="mx-auto w-full max-w-[848px] px-4 pt-2 pb-10 sm:px-6">
          {files.length > 0 && (
            <div className="mb-2 flex flex-wrap items-center gap-x-1 gap-y-1 text-meta">
              <div role="group" aria-label="Show" className="flex flex-wrap items-center gap-1">
                {shows.map((s) => (
                  <button key={s.key} type="button" aria-pressed={show === s.key} onClick={() => setShow(s.key)} className={control(show === s.key)}>
                    {s.label}
                  </button>
                ))}
              </div>
              <div role="group" aria-label="Sort" className="ml-auto flex items-center gap-1">
                <button type="button" aria-pressed={sort === "newest"} onClick={() => setSort("newest")} className={control(sort === "newest")}>
                  Newest first
                </button>
                <button type="button" aria-pressed={sort === "name"} onClick={() => setSort("name")} className={control(sort === "name")}>
                  By name
                </button>
              </div>
              {canUpload && (
                <div className="sm:ml-2">
                  <Attach onClick={choose} label="Upload a file" />
                </div>
              )}
            </div>
          )}
          {files.length > 0 && form && <div className="pb-3">{form}</div>}
          {files.length === 0 ? (
            <NoFiles upload={canUpload ? (form ?? <Attach onClick={choose} label="Upload a file" />) : null} />
          ) : (
            <ul className="divide-y divide-rule border-y border-rule" aria-label="Files">
              {shown.map((f) => (
                <li key={f.id}>
                  <FileRow f={f} selected={f.id === selected} open={open} identity={identity} me={me} now={now} />
                </li>
              ))}
            </ul>
          )}
          {files.length > 0 && shown.length === 0 && (
            <p className="py-6 text-muted">
              No files match.{" "}
              <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => setShow("all")}>
                Show all files
              </button>
            </p>
          )}
          {more && <p className="pt-3 text-meta text-muted">Showing the first {files.length} files.</p>}
          {canUpload && files.length > 0 && <p className="pt-3 text-meta text-muted">Drop a file here to upload it, or drop one on an open file for its next version.</p>}
        </div>
      </div>
    </div>
  );
}

function NoFiles({ upload }: { upload: ReactNode }) {
  return (
    <div className="empty flex flex-col items-start gap-3 py-8">
      <h2 className="text-title font-bold">No files on this board yet.</h2>
      <p>
        Agents put files here from their sessions with <code>aboard file put report.md</code>, and post them with{" "}
        <code>aboard say --attach report.md</code>. Every write names the version it replaces, so no one&apos;s work is overwritten
        unseen.
      </p>
      {upload && (
        <>
          <p>To add one yourself, upload it from this computer or drop it anywhere here.</p>
          <div className="w-full">{upload}</div>
        </>
      )}
    </div>
  );
}

function FileRow({ f, selected, open, identity, me, now }: { f: BoardFile; selected: boolean; open: (id: string) => void; identity: Identity; me: string | null; now: number }) {
  return (
    <article
      className={cn("file-row group relative grid grid-cols-[18px_minmax(0,1fr)] gap-x-3 px-2 py-3 transition-colors duration-[140ms] ease-out", selected ? "bg-selected" : "hover:bg-selected/50")}
      data-file={f.name}
    >
      <FileIcon name={f.name} mediaType={f.latest.media_type} className="mt-[3px]" />
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          {/* The name's button covers the row, so the whole row opens the file. */}
          <button
            type="button"
            onClick={() => open(f.id)}
            aria-current={selected ? "true" : undefined}
            className="min-w-0 truncate text-left font-bold break-all after:absolute after:inset-0 after:content-[''] group-hover:underline focus-visible:outline-none focus-visible:after:rounded-control focus-visible:after:outline-2 focus-visible:after:outline-accent"
          >
            {f.name}
          </button>
          <span className="text-meta text-muted">
            {f.maintained ? <span title="Kept current by its writers">maintained</span> : "one-off"} · {typeName(f.name, f.latest.media_type)} · {size(f.latest.size)}
          </span>
        </div>
        <p className="flex flex-wrap items-center gap-x-1.5 text-meta text-muted">
          <Who by={f.latest.by} me={me} identity={identity} />
          <span>
            · v{f.latest.version} · {f.latest.version === 1 ? "added" : "updated"} <When at={f.latest.at} now={now} />
          </span>
        </p>
        {f.about.length > 0 && (
          // Above the row's cover, so a task chip opens the task, not the file.
          <p className="relative flex flex-wrap items-center gap-1.5 text-meta">
            <TaskChips tags={f.about.map((t) => ({ id: t.id, ref: t.ref, how: "given" as const }))} />
          </p>
        )}
      </div>
    </article>
  );
}

/** conflictText says what a refused write found: the version someone else wrote first. */
function conflictText(e: ApiError, name: string, me: string | null, now: number): ReactNode {
  const d = e.details as Partial<FileChanged> | undefined;
  const who = d?.by?.name ? (d.by.kind === "human" && d.by.name === me ? "you" : d.by.name) : "someone";
  const when = d?.at ? ` ${relativeTime(d.at, now)}` : "";
  if (e.code === "file_exists") {
    return (
      <>
        <strong>{name}</strong> is already on the board{d?.version ? `, at v${d.version} by ${who}${when}` : ""}. Nothing was uploaded. Open it to upload a
        new version, or choose another name.
      </>
    );
  }
  return (
    <>
      {who} wrote {d?.version ? `v${d.version}` : "a newer version"}
      {when}, after the version you were looking at. Nothing was uploaded. Read the new version, then upload yours again if it still
      applies.
    </>
  );
}

function uploadProblem(e: unknown, name: string, me: string | null, now: number): { text: ReactNode; conflict: boolean } {
  if (e instanceof ApiError && (e.code === "file_exists" || e.code === "file_changed")) return { text: conflictText(e, name, me, now), conflict: true };
  if (e instanceof ApiError && e.code === "file_has_secret") return { text: "The file looks like it holds a credential, such as an API key or a private key. Nothing was uploaded. Take the credential out, then upload it again.", conflict: false };
  if (e instanceof ApiError && e.code === "file_too_large") return { text: "Files can be at most 50 MB. Nothing was uploaded.", conflict: false };
  if (e instanceof ApiError) return { text: `${e.message}${e.hint ? ` ${e.hint}` : ""}`, conflict: false };
  return { text: "Couldn't reach the Aboard server. Nothing was uploaded.", conflict: false };
}

/** boardName turns a local file's name into a name the board takes: no spaces or other characters a path can't hold. */
function boardName(local: string): string {
  const cleaned = local.replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^[^A-Za-z0-9]+/, "");
  return cleaned || "file";
}

/** NewFile names a picked file on the board and uploads it as a new file, never over one. */
function NewFile({
  board,
  picked,
  extra,
  files,
  open,
  onDone,
  onChooseAnother,
}: {
  board: string;
  picked: File;
  extra: number;
  files: BoardFile[];
  open: (id: string) => void;
  onDone: (f: BoardFile | null) => void;
  onChooseAnother: () => void;
}) {
  const field = useId();
  const [name, setName] = useState(() => boardName(picked.name));
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<{ text: ReactNode; conflict: boolean } | null>(null);
  const now = useNow();
  const taken = files.find((f) => f.name === name);
  const valid = namePattern.test(name) && name.length <= 200;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!valid || busy) return;
    if (picked.size > maxBytes) {
      setProblem({ text: "Files can be at most 50 MB. Nothing was uploaded.", conflict: false });
      return;
    }
    setBusy(true);
    setProblem(null);
    try {
      onDone(await putFile(board, name, 0, picked));
    } catch (err) {
      setProblem(uploadProblem(err, name, null, now));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={submit} className="new-file flex flex-col gap-2 rounded-box border border-rule bg-surface p-3.5 animate-fade-in" aria-label="Upload a file">
      <p className="flex flex-wrap items-center gap-x-2 text-meta text-muted">
        <Paperclip className="size-4 shrink-0" strokeWidth={1.75} aria-hidden />
        <span className="break-all">
          {picked.name} · {size(picked.size)}
        </span>
        <button type="button" onClick={onChooseAnother} className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline">
          Choose another
        </button>
      </p>
      {extra > 0 && <p className="text-meta text-muted">Only the first file is uploaded. Drop the other {count(extra, "file", "files")} one at a time.</p>}
      <label htmlFor={field} className="font-bold">
        Name on the board
      </label>
      <input
        id={field}
        value={name}
        onChange={(e) => {
          setName(e.target.value.trim());
          setProblem(null);
        }}
        spellCheck={false}
        autoFocus
        aria-describedby={`${field}-hint`}
        aria-invalid={!valid || undefined}
        className="min-h-11 rounded-control border border-field-border bg-surface px-3.5 text-ink"
      />
      <p id={`${field}-hint`} className="text-meta text-muted">
        {!valid
          ? "Use letters, digits, dots, dashes and underscores, with / between folders, such as notes/api.md."
          : taken
            ? `${name} is already on the board. Open it to upload a new version, or choose another name.`
            : "A path such as notes/api.md puts it in a folder."}
      </p>
      {problem && (
        <p role="alert" className="upload-problem rounded-control border border-field-border px-3 py-2">
          {problem.text}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <button type="submit" disabled={!valid || busy || taken !== undefined} className="min-h-11 rounded-control bg-ink px-4 font-bold text-on-ink disabled:opacity-60">
          {busy ? "Uploading…" : "Upload"}
        </button>
        {taken && (
          <button
            type="button"
            onClick={() => {
              open(taken.id);
              onDone(null);
            }}
            className="min-h-11 rounded-control border border-ink px-3.5 font-medium hover:bg-selected"
          >
            Open {taken.name}
          </button>
        )}
        <button type="button" onClick={() => onDone(null)} className="min-h-11 px-2 text-muted hover:text-ink">
          Cancel
        </button>
      </div>
    </form>
  );
}

/** FilePanel is one file in the side panel: what it is, a preview, its versions and where it was posted. */
export function FilePanel({
  board,
  id,
  activity,
  back,
  identity,
  me,
  canUpload,
  onShow,
}: {
  board: string;
  id: string;
  activity: number;
  back: () => void;
  identity: Identity;
  me: string | null;
  canUpload: boolean;
  onShow: (messageId: string) => void;
}) {
  const [file, setFile] = useState<FileDetail | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [viewing, setViewing] = useState<number | null>(null);
  const [again, setAgain] = useState(0);
  const top = useRef<HTMLElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const now = useNow();
  useEffect(() => {
    setFile(null);
    setError(null);
    setViewing(null);
  }, [board, id]);
  useEffect(() => {
    let live = true;
    getFile(board, id).then(
      (f) => {
        if (!live) return;
        setFile(f);
        setError(null);
      },
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && (e.status === 403 || e.status === 404)) setFile(null);
        setError(e);
      },
    );
    return () => {
      live = false;
    };
  }, [board, id, activity, again]);
  // Opening a file brings the panel into view on a narrow screen, and keyboard focus to its name.
  useEffect(() => {
    if (window.innerWidth < 1024) top.current?.scrollIntoView({ block: "start" });
  }, [id]);
  const named = file !== null;
  useEffect(() => {
    if (named) heading.current?.focus({ preventScroll: true });
  }, [id, named]);

  const shown = file ? (file.versions.find((v) => v.version === viewing) ?? file.latest) : null;
  // A file dropped on the panel becomes the next version of the file, written against
  // the latest version on screen at the drop.
  const [incoming, setIncoming] = useState<Incoming | null>(null);
  const drop = useDrop(canUpload && file !== null, (f) => {
    if (file) setIncoming((p) => ({ file: f, base: file.latest.version, n: (p?.n ?? 0) + 1 }));
  });
  return (
    <section
      ref={top}
      aria-label={file ? `File ${file.name}` : "File"}
      className="file-panel relative flex scroll-mt-2 flex-col gap-4"
      data-drop={drop.over ? "over" : undefined}
      {...drop.props}
    >
      {file && <DropHint over={drop.over} text={`Drop to upload it as v${file.latest.version + 1}`} />}
      <button type="button" onClick={back} className="inline-flex min-h-11 items-center gap-1.5 self-start text-meta text-muted hover:text-ink">
        <ArrowLeft className="size-4" aria-hidden />
        Work
      </button>
      {error !== null && <Problem error={error} />}
      {file && shown ? (
        <>
          <header className="flex flex-col gap-1">
            <h2 ref={heading} tabIndex={-1} className="flex items-start gap-2 text-title font-bold break-all focus-visible:outline-none">
              <FileIcon name={file.name} mediaType={file.latest.media_type} className="mt-[5px]" />
              {file.name}
            </h2>
            <p className="text-meta text-muted">
              {file.maintained ? "Maintained" : "One-off"} · {typeName(file.name, file.latest.media_type)} · v{file.latest.version} · {size(file.latest.size)}
            </p>
            <p className="flex flex-wrap items-center gap-x-1.5 text-meta text-muted">
              <Who by={file.latest.by} me={me} identity={identity} />
              <span>
                · {file.latest.version === 1 ? "added" : "updated"} <When at={file.latest.at} now={now} />
              </span>
            </p>
            <Since f={file} />
            {file.about.length > 0 && (
              <p className="flex flex-wrap items-center gap-1.5 pt-1 text-meta">
                <TaskChips tags={file.about.map((t) => ({ id: t.id, ref: t.ref, how: "given" as const }))} />
              </p>
            )}
          </header>
          {shown.version !== file.latest.version && (
            <p className="text-meta" role="status">
              Showing v{shown.version}, an older version.{" "}
              <button type="button" onClick={() => setViewing(null)} className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline">
                Show the latest, v{file.latest.version}
              </button>
            </p>
          )}
          <Preview board={board} file={file} v={shown} />
          <a
            href={versionUrl(board, file.id, shown.version)}
            download={downloadName(file.name, shown.version, file.latest.version)}
            className="download inline-flex min-h-11 items-center gap-1.5 self-start text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
          >
            <Download className="size-4" strokeWidth={1.5} aria-hidden />
            Download v{shown.version}
          </a>
          <Versions board={board} file={file} viewing={shown.version} view={(v) => setViewing(v === file.latest.version ? null : v)} identity={identity} me={me} now={now} />
          {file.posted_in.length > 0 && (
            <section aria-label="Posted in" className="flex flex-col gap-1">
              <h3 className="text-meta font-bold text-muted">Posted in</h3>
              <ul className="flex flex-col">
                {file.posted_in.map((p) => (
                  <li key={p.message_id}>
                    <button type="button" onClick={() => onShow(p.message_id)} className="min-h-11 text-left text-link underline decoration-1 underline-offset-[3px] hover:no-underline">
                      {p.thread_root_seq !== null ? "A reply in a thread" : "A message"}, with v{p.version}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {canUpload && <NewVersion board={board} file={file} me={me} incoming={incoming} onUploaded={(f) => { setViewing(null); setFile((old) => (old ? { ...old, ...f } : old)); setAgain((n) => n + 1); }} onConflict={() => setAgain((n) => n + 1)} />}
        </>
      ) : (
        error === null && (
          <p className="text-meta text-muted" role="status">
            Loading the file…
          </p>
        )
      )}
    </section>
  );
}

/** Since is what happened on the board after the latest version: counted facts, never a verdict. */
function Since({ f }: { f: BoardFile }) {
  const parts = [
    f.freshness.messages_since > 0 && count(f.freshness.messages_since, "message", "messages"),
    f.freshness.tasks_done_since > 0 && count(f.freshness.tasks_done_since, "task done", "tasks done"),
    f.freshness.answers_since > 0 && count(f.freshness.answers_since, "answer", "answers"),
  ].filter(Boolean);
  if (parts.length === 0) return null;
  return (
    <p className="file-since text-meta text-muted" title="What happened on the board after this version was written">
      Since v{f.latest.version}: {parts.join(" · ")}
    </p>
  );
}

function Versions({
  board,
  file,
  viewing,
  view,
  identity,
  me,
  now,
}: {
  board: string;
  file: FileDetail;
  viewing: number;
  view: (v: number) => void;
  identity: Identity;
  me: string | null;
  now: number;
}) {
  return (
    <section aria-label="Versions" className="flex flex-col gap-1">
      <h3 className="text-meta font-bold text-muted">Versions · {file.versions.length}</h3>
      <ol className="versions flex flex-col divide-y divide-rule border-y border-rule">
        {file.versions.map((v) => (
          <li key={v.version} data-version={v.version} className={cn("flex items-center gap-2 py-1.5 pl-2", v.version === viewing && "bg-selected")}>
            <button
              type="button"
              onClick={() => view(v.version)}
              aria-pressed={v.version === viewing}
              aria-label={`Show v${v.version}`}
              className="flex min-h-11 min-w-0 flex-1 flex-col items-start justify-center text-left text-meta"
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <strong className="tabular-nums text-ink">v{v.version}</strong>
                <span className="text-muted">·</span>
                <Who by={v.by} me={me} identity={identity} />
              </span>
              <span className="text-muted">
                <When at={v.at} now={now} /> · {size(v.size)}
              </span>
            </button>
            <a
              href={versionUrl(board, file.id, v.version)}
              download={downloadName(file.name, v.version, file.latest.version)}
              aria-label={`Download v${v.version}`}
              title={`Download v${v.version}, exactly as uploaded`}
              className="inline-flex size-11 shrink-0 items-center justify-center rounded-control text-link hover:bg-selected"
            >
              <Download className="size-4" strokeWidth={1.5} aria-hidden />
            </a>
          </li>
        ))}
      </ol>
    </section>
  );
}

/** Preview shows a version on its light page: Markdown formatted, text as written, images drawn. */
function Preview({ board, file, v }: { board: string; file: FileDetail; v: FileVersion }) {
  const kind = kindOf(file.name, v.media_type);
  const [text, setText] = useState<{ key: string; body: string } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const key = `${file.id}@${v.version}`;
  const readable = (kind === "markdown" || kind === "text") && v.size <= maxPreview;
  useEffect(() => {
    if (!readable) return;
    let live = true;
    setError(null);
    fileText(board, file.id, v.version).then(
      (body) => live && setText({ key, body }),
      (e) => live && setError(e),
    );
    return () => {
      live = false;
    };
  }, [board, file.id, v.version, key, readable]);

  const note = (children: ReactNode) => <p className="file-preview-note rounded-box border border-rule px-3.5 py-3 text-meta text-muted">{children}</p>;
  if (kind === "html") return note("HTML isn't previewed here yet. Download it to open it in your browser.");
  if (kind === "other") return note(`There is no preview for this type. Download v${v.version} to open it.`);
  if (kind === "image") {
    return (
      <div className="file-page overflow-hidden rounded-box border border-rule">
        {/* The bytes come from this server, so the page's own rules allow them. */}
        <img src={versionUrl(board, file.id, v.version)} alt={`${file.name}, v${v.version}`} className="mx-auto block max-h-[min(60vh,560px)] max-w-full p-3" />
      </div>
    );
  }
  if (!readable) return note(`This version is ${size(v.size)}, too long to preview here. Download it to read it.`);
  if (error !== null) return <Problem error={error} />;
  if (!text || text.key !== key) {
    return <div className="file-page h-32 rounded-box border border-rule motion-safe:animate-pulse" role="status" aria-label="Loading the preview" />;
  }
  return (
    <div className="file-page file-preview quiet-scroll max-h-[min(60vh,560px)] overflow-y-auto rounded-box border border-rule px-4 py-3" tabIndex={0} aria-label={`Preview of ${file.name}, v${v.version}`}>
      {kind === "markdown" ? <Markdown text={text.body} /> : <pre className="text-meta whitespace-pre-wrap break-words">{text.body}</pre>}
    </div>
  );
}

/**
 * NewVersion uploads a new version of the file from this computer. It names, as its
 * base, the latest version the panel showed when the person chose to upload, so a
 * version someone else wrote in the meantime is never overwritten: the server refuses,
 * stores nothing, and the panel says who wrote what.
 */
type Incoming = { file: File; base: number; n: number };

function NewVersion({
  board,
  file,
  me,
  incoming,
  onUploaded,
  onConflict,
}: {
  board: string;
  file: FileDetail;
  me: string | null;
  incoming: Incoming | null;
  onUploaded: (f: BoardFile) => void;
  onConflict: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const base = useRef(file.latest.version);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<{ text: ReactNode; conflict: boolean } | null>(null);
  const [done, setDone] = useState<number | null>(null);
  const now = useNow();
  const upload = async (picked: File, over: number) => {
    if (picked.size > maxBytes) {
      setProblem({ text: "Files can be at most 50 MB. Nothing was uploaded.", conflict: false });
      return;
    }
    setBusy(true);
    setProblem(null);
    setDone(null);
    try {
      const f = await putFile(board, file.name, over, picked);
      setDone(f.latest.version);
      onUploaded(f);
    } catch (err) {
      const p = uploadProblem(err, file.name, me, now);
      setProblem(p);
      if (p.conflict) onConflict();
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  };
  // A file dropped on the panel goes up once, against the version on screen at the drop.
  const handled = useRef(0);
  useEffect(() => {
    if (!incoming || incoming.n === handled.current) return;
    handled.current = incoming.n;
    void upload(incoming.file, incoming.base);
    // upload is recreated each render; the drop's own number decides when to run.
  }, [incoming]);
  return (
    <section aria-label="Upload a new version" className="flex flex-col gap-2 border-t border-rule pt-4">
      <input
        ref={input}
        type="file"
        className="sr-only"
        tabIndex={-1}
        aria-hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void upload(f, base.current);
        }}
      />
      <button
        type="button"
        disabled={busy}
        onClick={() => {
          // The version on screen now is the one this upload replaces.
          base.current = file.latest.version;
          setProblem(null);
          input.current?.click();
        }}
        className="inline-flex min-h-11 items-center gap-2 self-start rounded-control border border-ink px-3.5 font-medium transition-colors duration-[140ms] ease-out hover:bg-selected disabled:opacity-60"
      >
        <Paperclip className="size-4" strokeWidth={1.75} aria-hidden />
        {busy ? "Uploading…" : `Upload a new version (after v${file.latest.version})`}
      </button>
      <p className="text-meta text-muted">Or drop a file on this panel.</p>
      {done !== null && (
        <p role="status" className="text-meta text-muted">
          Uploaded v{done}.
        </p>
      )}
      {problem && (
        <p role="alert" className="upload-problem rounded-control border border-field-border px-3 py-2">
          {problem.text}
        </p>
      )}
    </section>
  );
}
