"use client";

// Files in the conversation. A message carries versions of board files (its `files`),
// shown under its text as cards: what the file is, which version, how big, and who wrote
// that version. A card opens the file panel at that exact version and downloads it. A
// board file's path written in a message's text links to the file too, worked out here
// against the board's file list without changing the text. The message box attaches
// files from the board, or uploads new ones first, before the message is posted.
//
// Files are never people: a card is a box with a file icon, an inline file link is the
// path in mono with a file icon, and neither ever shows an @ or a sender mark in place of
// the file. A mention keeps its tint and bold name.

import { Check, ChevronDown, Download, Paperclip, Search, Upload, X } from "lucide-react";
import { type KeyboardEvent, type ReactNode, createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { ApiError, type BoardFile, type FileDetail, type FileRef, type FileVersion, type MemberRef, getFile, putFile, versionUrl } from "./api";
import { SenderMark } from "./agent-mark";
import { FileIcon, boardName, kindOf, maxBytes, size, typeName, uploadProblem } from "./files";
import { count, identityOf, relativeTime } from "./words";

/** The most files one message attaches (PostMessageRequest.files in spec/openapi.yaml). */
export const maxAttached = 8;

/**
 * FilesRoom is what file cards and links need from the board on screen: its name, its
 * files, and how to open one in the side panel. Outside a board (the Inbox) there is no
 * room, and a card links to the file on its board instead.
 */
type FilesRoom = {
  board: string;
  files: BoardFile[];
  open?: (id: string, version?: number) => void;
  identity: (m: MemberRef) => number;
  me: string | null;
};

const Room = createContext<FilesRoom>({ board: "", files: [], identity: (m) => identityOf(`${m.kind}:${m.name}`), me: null });

export function FilesContext({ children, board, files, open, identity, me }: FilesRoom & { children: ReactNode }) {
  const value = useMemo(() => ({ board, files, open, identity, me }), [board, files, open, identity, me]);
  return <Room.Provider value={value}>{children}</Room.Provider>;
}

// A version never changes once written, so what a page learns about one it keeps. A
// file's detail is read again only when it lacks a version a message names.
const details = new Map<string, Promise<FileDetail>>();

function detailOf(board: string, id: string, again = false): Promise<FileDetail> {
  const key = `${board}\u0000${id}`;
  let p = details.get(key);
  if (!p || again) {
    p = getFile(board, id);
    details.set(key, p);
    p.catch(() => details.delete(key));
  }
  return p;
}

async function versionOf(board: string, id: string, version: number): Promise<{ v: FileVersion; latest: number; all: number[] } | null> {
  let d = await detailOf(board, id);
  if (!d.versions.some((x) => x.version === version)) d = await detailOf(board, id, true);
  const v = d.versions.find((x) => x.version === version);
  return v ? { v, latest: d.latest.version, all: d.versions.map((x) => x.version) } : null;
}

type Info = { state: "loading" } | { state: "ready"; v: FileVersion; latest: number } | { state: "gone" } | { state: "unknown" };

/** useVersion says what a version is: from the board's file list when it is the latest, else from the file's detail. */
function useVersion(board: string, ref: FileRef): Info {
  const room = useContext(Room);
  const listed = room.board === board ? room.files.find((f) => f.id === ref.id) : undefined;
  const quick = listed && listed.latest.version === ref.version ? listed.latest : null;
  const [info, setInfo] = useState<Info>({ state: "loading" });
  useEffect(() => {
    if (quick) return;
    let live = true;
    versionOf(board, ref.id, ref.version).then(
      (r) => live && setInfo(r ? { state: "ready", v: r.v, latest: r.latest } : { state: "gone" }),
      (e) => live && setInfo({ state: e instanceof ApiError && (e.status === 404 || e.status === 403) ? "gone" : "unknown" }),
    );
    return () => {
      live = false;
    };
  }, [board, ref.id, ref.version, quick]);
  if (quick) return { state: "ready", v: quick, latest: quick.version };
  if (info.state === "ready" && listed && listed.latest.version > info.latest) return { ...info, latest: listed.latest.version };
  return info;
}

/** fileHref is where a file opens from outside its board: the board view, with the file in the side panel at that version. */
export function fileHref(board: string, id: string, version: number): string {
  return `/?board=${encodeURIComponent(board)}&file=${encodeURIComponent(id)}&version=${version}`;
}

/** Path is a file's path with its folders quieter than its own name. */
function Path({ name }: { name: string }) {
  const cut = name.lastIndexOf("/");
  return (
    <>
      {cut >= 0 && <span className="text-muted">{name.slice(0, cut + 1)}</span>}
      <span className="font-medium text-ink">{name.slice(cut + 1)}</span>
    </>
  );
}

/** Attachments are a message's files, one card for each version it carries. */
export function Attachments({ files, board }: { files?: FileRef[]; board?: string }) {
  const room = useContext(Room);
  const on = board ?? room.board;
  if (!files?.length || !on) return null;
  return (
    <ul className="attachments mt-2 flex flex-wrap gap-2" aria-label={files.length === 1 ? "Attached file" : `${files.length} attached files`}>
      {files.map((f) => (
        <AttachmentCard key={`${f.id}@${f.version}`} board={on} f={f} />
      ))}
    </ul>
  );
}

function AttachmentCard({ board, f }: { board: string; f: FileRef }) {
  const room = useContext(Room);
  const info = useVersion(board, f);
  const kind = kindOf(f.name, info.state === "ready" ? info.v.media_type : "");
  const gone = info.state === "gone";
  const label = `${f.name}, v${f.version}`;
  const here = room.open && room.board === board;
  const cover =
    "after:absolute after:inset-0 after:rounded-control after:content-[''] focus-visible:outline-none focus-visible:after:outline-2 focus-visible:after:outline-offset-[-1px] focus-visible:after:outline-accent-strong";
  const name = (
    <span className="block truncate" title={f.name}>
      <Path name={f.name} />
    </span>
  );
  const by = info.state === "ready" ? info.v.by : null;
  return (
    <li
      className={cn(
        "attachment group/file relative flex min-h-14 w-full min-w-0 items-center gap-3 rounded-control border bg-surface py-2 pr-1 pl-2 transition-colors duration-[140ms] ease-out sm:w-[19.5rem]",
        gone ? "border-dashed border-field-border" : "border-rule hover:border-field-border",
      )}
      data-attachment={f.name}
      data-version={f.version}
    >
      <span aria-hidden title={info.state === "ready" ? typeName(f.name, info.v.media_type) : undefined} className="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded-[6px] border border-rule bg-background">
        {kind === "image" && info.state === "ready" ? (
          // The exact version, from this server, through the same signed-in download as the panel.
          <img src={versionUrl(board, f.id, f.version)} alt="" loading="lazy" decoding="async" className="size-full object-cover" />
        ) : (
          <FileIcon name={f.name} mediaType={info.state === "ready" ? info.v.media_type : ""} />
        )}
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        {gone ? (
          <span className="text-muted">{name}</span>
        ) : here ? (
          <button type="button" onClick={() => room.open!(f.id, f.version)} className={cn("min-w-0 text-left group-hover/file:underline decoration-1 underline-offset-[3px]", cover)} aria-label={`Open ${label}`}>
            {name}
          </button>
        ) : (
          <a href={fileHref(board, f.id, f.version)} className={cn("min-w-0 text-ink no-underline group-hover/file:underline", cover)} aria-label={`Open ${label} on its board`}>
            {name}
          </a>
        )}
        <span className="flex min-w-0 items-center gap-1.5 text-meta text-muted">
          <span className="shrink-0 font-medium text-ink tabular-nums" title={info.state === "ready" && info.latest > f.version ? `Version ${f.version}; the latest is version ${info.latest}` : `Version ${f.version}`}>
            v{f.version}
            {info.state === "ready" && info.latest > f.version && <span className="font-normal text-muted"> of {info.latest}</span>}
          </span>
          {gone ? (
            <span className="truncate">· no longer on this board</span>
          ) : info.state === "ready" ? (
            <>
              <span className="sr-only">{typeName(f.name, info.v.media_type)},</span>
              <span className="shrink-0">· {size(info.v.size)} ·</span>
              {by && (
                <span className="inline-flex min-w-0 items-center gap-1" title={`v${f.version} written by ${by.name}`}>
                  <SenderMark name={by.name} kind={by.kind} harness={by.harness} identity={room.identity(by)} className="size-4" />
                  <span className="truncate">{by.kind === "human" && by.name === room.me ? "you" : by.name}</span>
                </span>
              )}
            </>
          ) : info.state === "loading" ? (
            <span aria-hidden className="h-3 w-24 rounded-[3px] bg-selected motion-safe:animate-pulse" />
          ) : null}
        </span>
      </span>
      {!gone && (
        <a
          href={versionUrl(board, f.id, f.version)}
          download={f.name.split("/").pop()}
          aria-label={`Download ${label}`}
          title={`Download v${f.version}, exactly as uploaded`}
          className="tap relative z-[1] inline-flex size-9 shrink-0 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink focus-visible:outline-2 focus-visible:outline-accent-strong"
        >
          <Download className="size-4" strokeWidth={1.5} aria-hidden />
        </a>
      )}
    </li>
  );
}

// A path can be linked only where it can't be part of a longer word, path or address.
const before = "(?<![\\p{L}\\p{N}_./@\\\\-])";
const after = "(?![\\p{L}\\p{N}_/-]|\\.[\\p{L}\\p{N}])";
// Names hold only letters, digits, ".", "_", "-" and "/"; of those only "." means anything in a pattern.
const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/**
 * pathPattern finds the board's file paths in text, longest first, each optionally
 * followed by a version ("notes/api.md v2", "notes/api.md@v2"). Only paths with a dot or
 * a slash count, so a file named like a word never turns words into links.
 */
function pathPattern(files: BoardFile[]): RegExp | null {
  const names = files.map((f) => f.name).filter((n) => /[./]/.test(n)).sort((a, b) => b.length - a.length);
  if (names.length === 0) return null;
  try {
    return new RegExp(`${before}(${names.map(escape).join("|")})(?:(?:@| )v([1-9][0-9]{0,5})(?![\\p{L}\\p{N}_]))?${after}`, "gu");
  } catch {
    // A browser without lookbehind shows paths as plain text.
    return null;
  }
}

/**
 * FileLinks is text with each path of a board file in it as a link that opens the file in
 * the side panel, at the version written beside it when there is one. A file the message
 * also attaches stays plain text: its card already opens it. rest draws the text between.
 */
export function FileLinks({ text, skip, rest }: { text: string; skip?: FileRef[]; rest: (text: string, key: number) => ReactNode }) {
  const { files, open } = useContext(Room);
  const pattern = useMemo(() => (open ? pathPattern(files) : null), [files, open]);
  if (!pattern || !open) return rest(text, 0);
  const out: ReactNode[] = [];
  let from = 0;
  let n = 0;
  for (const m of text.matchAll(pattern)) {
    const f = files.find((x) => x.name === m[1]);
    if (!f || skip?.some((s) => s.id === f.id)) continue;
    const asked = m[2] ? Number(m[2]) : null;
    const version = asked !== null && asked <= f.latest.version ? asked : null;
    const end = m.index + (version !== null ? m[0].length : m[1].length);
    if (m.index > from) out.push(rest(text.slice(from, m.index), n++));
    const words = `${f.name}${version !== null ? `, v${version}` : ""}`;
    out.push(
      <button
        key={`f${n++}`}
        type="button"
        data-file-link={f.name}
        onClick={() => open(f.id, version ?? undefined)}
        title={`Open ${words} in the file panel`}
        aria-label={`Open file ${words}`}
        className="file-link rounded-[3px] font-mono text-[0.9em] text-ink underline decoration-[color-mix(in_srgb,var(--ink)_40%,transparent)] decoration-1 underline-offset-[3px] transition-colors duration-[140ms] ease-out hover:decoration-ink"
      >
        <FileIcon name={f.name} mediaType={f.latest.media_type} className="mr-[3px] inline size-[1.05em] -translate-y-px align-middle" />
        {text.slice(m.index, end)}
      </button>,
    );
    from = end;
  }
  if (from === 0) return rest(text, 0);
  if (from < text.length) out.push(rest(text.slice(from), n++));
  return out;
}

// ---------- the message box ----------

/** Draft is a file on its way into a message: a board file at a version, or a local file being put on the board first. */
export type Draft = {
  key: string;
  name: string;
  state: "ready" | "uploading" | "taken" | "failed";
  /** id, version and latest are set once the file is on the board. */
  id?: string;
  version?: number;
  latest?: number;
  mediaType?: string;
  local?: File;
  /** taken is the board file already at the name a local file would take. */
  taken?: BoardFile;
  problem?: ReactNode;
};

/** freeName is name with a number added before its extension, the first not already on the board. */
function freeName(name: string, files: BoardFile[]): string {
  const dot = name.lastIndexOf(".");
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  for (let i = 2; ; i++) {
    const next = `${stem}-${i}${ext}`;
    if (!files.some((f) => f.name === next)) return next;
  }
}

/**
 * useDrafts holds the files a message will carry. A local file goes on the board as soon
 * as it is picked, as a new file (base 0), so the message attaches a version that exists.
 * When its name is taken, nothing is written until the person says how: as the next
 * version of that file (written against the version they saw), or beside it under a free
 * name. Removing a draft takes it off the message, not off the board.
 */
export function useDrafts(board: string, files: BoardFile[], me: string | null, onUploaded: () => void) {
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [note, setNote] = useState("");
  const ids = useRef(0);
  const change = (key: string, d: Partial<Draft>) => setDrafts((all) => all.map((x) => (x.key === key ? { ...x, ...d } : x)));

  const put = (key: string, local: File, name: string, base: number, fileId?: string) => {
    change(key, { state: "uploading", name, problem: undefined, taken: undefined });
    putFile(board, name, base, local, fileId).then(
      (f) => {
        change(key, { state: "ready", id: f.id, name: f.name, version: f.latest.version, latest: f.latest.version, mediaType: f.latest.media_type });
        setNote(`${f.name} v${f.latest.version} is on the board and attached.`);
        onUploaded();
      },
      (e) => change(key, { state: "failed", problem: uploadProblem(e, name, me, Date.now()).text }),
    );
  };

  const room = (n: number) => {
    if (drafts.length + n <= maxAttached) return true;
    setNote(`A message carries at most ${maxAttached} files.`);
    return false;
  };

  const toggle = (f: BoardFile) => {
    const had = drafts.find((d) => d.id === f.id);
    if (had) {
      setDrafts((all) => all.filter((d) => d.key !== had.key));
      setNote(`${f.name} removed from the message.`);
      return;
    }
    if (!room(1)) return;
    setDrafts((all) => [...all, { key: `d${++ids.current}`, name: f.name, state: "ready", id: f.id, version: f.latest.version, latest: f.latest.version, mediaType: f.latest.media_type }]);
    setNote(`${f.name} v${f.latest.version} attached.`);
  };

  const upload = (locals: File[]) => {
    if (locals.length === 0 || !room(locals.length)) return;
    for (const local of locals) {
      const key = `d${++ids.current}`;
      const name = boardName(local.name);
      const taken = files.find((f) => f.name === name);
      if (local.size > maxBytes) {
        setDrafts((all) => [...all, { key, name, local, state: "failed", problem: "Files can be at most 50 MB. Nothing was uploaded." }]);
      } else if (taken) {
        setDrafts((all) => [...all, { key, name, local, state: "taken", taken }]);
      } else {
        setDrafts((all) => [...all, { key, name, local, state: "uploading" }]);
        put(key, local, name, 0);
      }
    }
  };

  /** settle answers a taken name: "next" writes the next version of that file, "beside" a new file under a free name. */
  const settle = (d: Draft, how: "next" | "beside") => {
    if (!d.local || !d.taken) return;
    if (how === "next") put(d.key, d.local, d.taken.name, d.taken.latest.version, d.taken.id);
    else put(d.key, d.local, freeName(d.name, files), 0);
  };

  const remove = (d: Draft) => {
    setDrafts((all) => all.filter((x) => x.key !== d.key));
    setNote(`${d.name} removed from the message.`);
  };
  const setVersion = (d: Draft, version: number) => change(d.key, { version });
  const clear = useCallback(() => setDrafts([]), []);
  const ready = drafts.every((d) => d.state === "ready");
  const selectors = drafts.flatMap((d) => (d.state === "ready" && d.id && d.version ? [{ file: d.id, version: d.version }] : []));
  return { drafts, note, toggle, upload, settle, remove, setVersion, clear, ready, selectors };
}

export type Drafts = ReturnType<typeof useDrafts>;

/** DraftChips are the files a message will carry, above the text, each removable. */
export function DraftChips({ board, d, bodyEmpty }: { board: string; d: Drafts; bodyEmpty: boolean }) {
  if (d.drafts.length === 0) return null;
  return (
    <div className="mb-2 flex flex-col gap-1.5">
      <ul className="draft-files flex flex-wrap items-start gap-1.5" aria-label="Files to attach">
        {d.drafts.map((x) => (
          <DraftChip key={x.key} board={board} x={x} d={d} />
        ))}
      </ul>
      {bodyEmpty && d.ready && <p className="text-meta text-muted">Write a line about {d.drafts.length === 1 ? "the file" : "the files"} to post.</p>}
    </div>
  );
}

function DraftChip({ board, x, d }: { board: string; x: Draft; d: Drafts }) {
  const [versions, setVersions] = useState<number[] | null>(null);
  useEffect(() => {
    if (x.state !== "ready" || !x.id || (x.latest ?? 1) < 2) return;
    let live = true;
    versionOf(board, x.id, x.latest!).then((r) => live && r && setVersions(r.all), () => {});
    return () => {
      live = false;
    };
  }, [board, x.id, x.latest, x.state]);
  const remove = (
    <button
      type="button"
      onClick={() => d.remove(x)}
      className="tap inline-flex size-7 shrink-0 items-center justify-center rounded-[5px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
      aria-label={`Remove ${x.name} from the message`}
      title="Remove from the message"
    >
      <X className="size-3.5" strokeWidth={1.75} aria-hidden />
    </button>
  );
  const head = (
    <span className="flex min-w-0 items-center gap-1.5">
      <FileIcon name={x.name} mediaType={x.mediaType ?? ""} className="size-4" />
      <span className="max-w-[14rem] truncate font-medium max-sm:max-w-[11rem]" title={x.name}>
        {x.name}
      </span>
    </span>
  );
  if (x.state === "taken" && x.taken) {
    return (
      <li className="draft-file flex w-full max-w-[34rem] flex-col gap-2 rounded-control border border-field-border bg-surface px-2.5 py-2 text-meta" data-draft={x.name}>
        <span className="flex items-center gap-2">
          {head}
          <span className="ml-auto">{remove}</span>
        </span>
        <span className="text-ink">
          {x.name} is already on the board, at v{x.taken.latest.version} by {x.taken.latest.by.name}. Nothing is uploaded until you choose.
        </span>
        <span className="flex flex-wrap gap-2">
          <button type="button" onClick={() => d.settle(x, "next")} className="min-h-9 rounded-control border border-ink px-3 font-medium text-ink transition-colors duration-[140ms] ease-out hover:bg-selected pointer-coarse:min-h-11">
            Upload as v{x.taken.latest.version + 1}
          </button>
          <button type="button" onClick={() => d.settle(x, "beside")} className="min-h-9 rounded-control px-3 text-link underline decoration-1 underline-offset-[3px] hover:no-underline pointer-coarse:min-h-11">
            Keep both
          </button>
        </span>
      </li>
    );
  }
  return (
    <li
      className={cn(
        "draft-file inline-flex h-9 max-w-full items-center gap-1.5 rounded-control border bg-surface pr-0.5 pl-2.5 text-meta text-ink pointer-coarse:h-11",
        x.state === "failed" ? "h-auto flex-wrap border-field-border py-1 pointer-coarse:h-auto" : "border-rule",
      )}
      data-draft={x.name}
      aria-busy={x.state === "uploading" || undefined}
    >
      {head}
      {x.state === "uploading" && <span className="text-muted motion-safe:animate-pulse">Uploading…</span>}
      {x.state === "ready" &&
        (versions && versions.length > 1 ? (
          <span className="relative inline-flex items-center">
            <select
              value={x.version}
              onChange={(e) => d.setVersion(x, Number(e.target.value))}
              aria-label={`Version of ${x.name} to attach`}
              className="h-7 cursor-pointer appearance-none rounded-[5px] bg-transparent pr-5 pl-1.5 font-medium tabular-nums text-ink outline-none hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent-strong"
            >
              {versions.map((v) => (
                <option key={v} value={v}>
                  v{v}
                  {v === x.latest ? " · latest" : ""}
                </option>
              ))}
            </select>
            <ChevronDown className="pointer-events-none absolute right-1 size-3 text-muted" strokeWidth={1.75} aria-hidden />
          </span>
        ) : (
          <span className="px-1 font-medium tabular-nums">v{x.version}</span>
        ))}
      {remove}
      {x.state === "failed" && (
        <span role="alert" className="basis-full pb-1 text-ink">
          {x.problem}
        </span>
      )}
    </li>
  );
}

/**
 * AttachButton is the paperclip in the message box. It opens a list of the board's files
 * to search and pick (each picked file attaches at its latest version, which its chip
 * can change), and a way to upload from this computer.
 */
export function AttachButton({ files, d }: { files: BoardFile[]; d: Drafts }) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const button = useRef<HTMLButtonElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const chooser = useRef<HTMLInputElement>(null);
  const ids = useId();
  const now = Date.now();
  const shown = useMemo(() => {
    const words = q.trim().toLowerCase();
    return [...files].filter((f) => !words || f.name.toLowerCase().includes(words)).sort((a, b) => b.latest.at.localeCompare(a.latest.at));
  }, [files, q]);
  const current = Math.min(active, Math.max(0, shown.length - 1));

  const close = useCallback((focus = true) => {
    setOpen(false);
    setQ("");
    setActive(0);
    if (focus) button.current?.focus();
  }, []);
  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!box.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) close(false);
    };
    // Focus moving elsewhere, as to the text, closes the list too.
    const elsewhere = (e: FocusEvent) => {
      if (!box.current?.contains(e.target as Node) && !button.current?.contains(e.target as Node)) close(false);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("focusin", elsewhere);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("focusin", elsewhere);
    };
  }, [open, close]);
  useEffect(() => {
    if (open) document.getElementById(`${ids}-option-${current}`)?.scrollIntoView({ block: "nearest" });
  }, [open, current, ids]);

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (shown.length) setActive((current + (e.key === "ArrowDown" ? 1 : -1) + shown.length) % shown.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (shown[current]) d.toggle(shown[current]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
    }
  };
  const attachedIds = new Set(d.drafts.map((x) => x.id));

  return (
    <>
      <input
        ref={chooser}
        type="file"
        multiple
        className="sr-only"
        tabIndex={-1}
        aria-hidden
        onChange={(e) => {
          d.upload(Array.from(e.target.files ?? []));
          e.target.value = "";
          close();
        }}
      />
      <button
        ref={button}
        type="button"
        onClick={() => (open ? close() : setOpen(true))}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={d.drafts.length ? `Attach files, ${count(d.drafts.length, "file", "files")} attached` : "Attach files"}
        title="Attach files"
        className={cn(
          "attach-files tap inline-flex size-11 shrink-0 items-center justify-center rounded-control text-ink outline-none transition-colors duration-[140ms] ease-out hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent-strong",
          open && "bg-selected",
        )}
      >
        <Paperclip className="size-[18px]" strokeWidth={1.6} aria-hidden />
      </button>
      {open && (
        <div
          ref={box}
          role="dialog"
          aria-label="Attach files"
          className="attach-picker absolute right-0 bottom-full z-40 mb-1 flex max-h-[min(26rem,60dvh)] w-full max-w-[26rem] flex-col overflow-hidden rounded-control border border-rule bg-surface text-ink shadow-float animate-fade-in"
        >
          <div className="relative border-b border-rule p-1.5">
            <Search className="pointer-events-none absolute top-1/2 left-4 size-4 -translate-y-1/2 text-muted" strokeWidth={1.5} aria-hidden />
            <input
              autoFocus
              value={q}
              onChange={(e) => {
                setQ(e.target.value);
                setActive(0);
              }}
              onKeyDown={onKey}
              role="combobox"
              aria-expanded
              aria-controls={`${ids}-list`}
              aria-activedescendant={shown.length ? `${ids}-option-${current}` : undefined}
              aria-label="Find a file on this board"
              placeholder="Find a file on this board"
              spellCheck={false}
              className="h-10 w-full rounded-[6px] bg-transparent pr-2.5 pl-9 text-body outline-none placeholder:text-muted pointer-coarse:h-11 pointer-coarse:text-[16px]"
            />
          </div>
          {files.length === 0 ? (
            <p className="px-3.5 py-3 text-meta text-muted">No files on this board yet. Upload one from this computer.</p>
          ) : shown.length === 0 ? (
            <p className="px-3.5 py-3 text-meta text-muted" role="status">
              No file matches &ldquo;{q.trim()}&rdquo;.
            </p>
          ) : (
            <ul id={`${ids}-list`} role="listbox" aria-multiselectable aria-label="Files on this board" className="quiet-scroll min-h-0 flex-1 overflow-y-auto p-1">
              {shown.map((f, i) => {
                const on = attachedIds.has(f.id);
                return (
                  <li
                    key={f.id}
                    id={`${ids}-option-${i}`}
                    role="option"
                    aria-selected={on}
                    data-file-option={f.name}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      d.toggle(f);
                    }}
                    onMouseMove={() => i !== current && setActive(i)}
                    className={cn("flex min-h-11 cursor-default items-center gap-2.5 rounded-[6px] px-2 py-1.5 select-none", i === current && "bg-selected")}
                  >
                    <span className="flex size-5 shrink-0 items-center justify-center">
                      {on ? <Check className="size-4 text-accent-strong" strokeWidth={2} aria-hidden /> : <FileIcon name={f.name} mediaType={f.latest.media_type} className="size-4" />}
                    </span>
                    <span className="min-w-0 flex-1 truncate" title={f.name}>
                      <Path name={f.name} />
                    </span>
                    <span className="shrink-0 text-meta text-muted tabular-nums">
                      v{f.latest.version} · {relativeTime(f.latest.at, now)}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-x-2 border-t border-rule p-1.5">
            <button
              type="button"
              onClick={() => chooser.current?.click()}
              className="inline-flex min-h-10 items-center gap-2 rounded-[6px] px-2.5 font-medium text-ink transition-colors duration-[140ms] ease-out hover:bg-hover pointer-coarse:min-h-11"
            >
              <Upload className="size-4" strokeWidth={1.6} aria-hidden />
              Upload from this computer
            </button>
            <span className="px-2.5 text-meta text-muted pointer-coarse:hidden">or drop them on the message box</span>
          </div>
        </div>
      )}
    </>
  );
}
