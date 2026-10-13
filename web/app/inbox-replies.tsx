"use client";

// The Inbox's replies waiting: messages an agent or a person sent the signed-in person
// expecting a reply. Each is a row in the list and, picked, a detail with a reply box;
// the reply is an ordinary reply to that message, sent as the person.

import { CornerDownLeft } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { type Board, type Message, type MessagePage, type ReplyPage, ackBoard, get, post, repliesWaiting } from "./api";
import { askSummary } from "./asks";
import { Attachments } from "./attachments";
import { Problem } from "./chrome";
import { keyLabel, pressed, typing } from "./keys";
import { Kbd } from "./keys-sheet";
import { SenderMark } from "./agent-mark";
import { plainInline } from "./brief";
import { Markdown } from "./markdown";
import { andList, identityOf, relativeTime } from "./words";

/** repliesChanged is the event a sent reply raises, so every count of replies waiting reads again. */
export const repliesChanged = "aboard:replies-changed";

/** whoWaits names the sender and, for an agent, the person it works for. */
export function whoWaits(m: Message): string {
  return m.from.kind === "agent" && m.from.owner ? `${m.from.name} (${m.from.owner}'s agent)` : m.from.name;
}

/**
 * threadRecipients is who a reply to m that names no one goes to, worked out as the
 * server does: the author of every message in its thread, m's author first, and every
 * member they addressed by name, leaving out the person and anyone not on the board.
 */
export async function threadRecipients(m: Message, me: string, onBoard: (name: string) => boolean): Promise<string[]> {
  const root = m.thread_root ?? m.id;
  const page = await get<ReplyPage>(`/v1/messages/${encodeURIComponent(root)}/replies`, { limit: 200 });
  const names: string[] = [];
  const add = (name: string) => { if (name !== me && !names.includes(name) && onBoard(name)) names.push(name); };
  for (const x of [m, ...(page.root ? [page.root] : []), ...page.replies]) {
    if (x.sender !== "self") add(x.from.name);
    for (const t of x.to) if (t.startsWith("@") && t.length > 1) add(t.slice(1));
  }
  return names;
}

/**
 * markReadThrough moves the person's read position on m's board past m and their own
 * reply, once they answered m in place. The position is cumulative, so it moves only
 * over a stretch that holds nothing else unread: an older message they haven't seen
 * holds it back. It never passes a message it didn't list and check, so it stops at
 * the last listed message, never at a sequence number past it.
 */
export async function markReadThrough(m: Message, reply: Message): Promise<void> {
  const board = encodeURIComponent(m.board!);
  const at = (await get<Board>(`/v1/boards/${board}`)).read_up_to;
  if (at === undefined || at >= reply.seq) return;
  const page = await get<MessagePage>(`/v1/boards/${board}/messages`, { after: at, before: reply.seq + 1, limit: 200 });
  let through = at;
  for (const x of page.messages) {
    if (x.id !== m.id && x.sender !== "self") break;
    through = x.seq;
  }
  if (through > at) await ackBoard(m.board!, through);
}

/** ReplyRow is one message waiting for a reply in the Inbox list. */
export function ReplyRow({ m, board, age, selected, onRead }: { m: Message; board: ReactNode; age: string; selected: boolean; onRead: () => void }) {
  return <li><button type="button" data-reply={m.id} onClick={onRead} aria-current={selected ? "true" : undefined} className={cn("grid min-h-14 w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-3 rounded-control px-2.5 py-3 text-left transition-colors duration-[140ms] ease-out hover:bg-hover active:bg-selected", selected && "bg-selected hover:bg-selected")}>
    <SenderMark name={m.from.name} kind={m.from.kind} harness={m.from.harness} identity={identityOf(`${m.from.kind}:${m.from.name}`)} className="mt-0.5 size-7" />
    <span className="flex min-w-0 flex-col">
      <span className="flex min-w-0 items-baseline gap-3"><span className={cn("min-w-0 flex-1 truncate", selected && "font-bold")}>{plainInline(askSummary(m.body))}</span><span className="shrink-0 text-meta text-muted tabular-nums">{age}</span></span>
      <span className="truncate text-meta text-muted">{board} · {whoWaits(m)}</span>
    </span>
  </button></li>;
}

/**
 * ReplyDetail shows a message waiting for a reply, whole, with a box to reply in place.
 * R moves to the box, the send key sends, and Escape leaves the box keeping the text.
 */
export function ReplyDetail({ m, me, onBoard, boardTitle, boardHref, status, now, onLeave, onSent, keys }: { m: Message; me: string; onBoard: (name: string) => boolean; boardTitle: string; boardHref: string; status: ReactNode; now: number; onLeave: () => void; onSent: (reply: Message) => void; keys: ReactNode }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const inFlight = useRef(false);
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const board = m.board!;
  // The reply goes to everyone in the thread; until the thread is read, its sender.
  const [to, setTo] = useState<string[]>([m.from.name]);
  const onBoardNow = useRef(onBoard);
  onBoardNow.current = onBoard;
  useEffect(() => {
    let live = true;
    threadRecipients(m, me, (name) => onBoardNow.current(name)).then((names) => { if (live && names.length > 0) setTo(names); }, () => { /* The sender stays named. */ });
    return () => { live = false; };
  }, [m, me]);
  const toWords = andList(to);
  const send = async () => {
    const body = text.trim();
    if (inFlight.current || !body) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    // A retry of the same words reuses its key, so a reply the server took is never posted twice.
    if (attempt.current?.body !== body) attempt.current = { body, key: crypto.randomUUID() };
    try {
      const reply = await post<Message>(`/v1/boards/${encodeURIComponent(board)}/messages`, { body, reply_to: m.id }, attempt.current.key);
      window.dispatchEvent(new CustomEvent(repliesChanged));
      // Answered here, the message has been read; a failure leaves it unread on the board, and nothing else.
      await markReadThrough(m, reply).catch(() => {});
      onSent(reply);
    } catch (e) { setError(e); }
    finally { inFlight.current = false; setBusy(false); }
  };
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (typing(e) || !pressed("reply", e)) return;
      e.preventDefault();
      box.current?.focus();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  return <article className="flex max-w-[640px] flex-col gap-5">
    <p className="text-meta text-muted"><a href={boardHref} onClick={onLeave} className="tap">{boardTitle}</a></p>
    <p className="flex flex-wrap items-center gap-x-2 gap-y-1"><SenderMark name={m.from.name} kind={m.from.kind} harness={m.from.harness} identity={identityOf(`${m.from.kind}:${m.from.name}`)} /><strong>{m.from.name}</strong><span className="text-meta text-muted">{m.from.kind === "agent" && m.from.owner && `(${m.from.owner}'s agent) `}waits for your reply · {relativeTime(m.at, now)}</span>{status}</p>
    <div className="flex flex-col gap-2"><Markdown text={m.body} className="reply-waiting-body text-now" /><Attachments board={board} files={m.files} /></div>
    <form className="flex max-w-[640px] flex-col gap-2 rounded-control border border-field-border bg-surface p-2.5" onSubmit={(e) => { e.preventDefault(); void send(); }}>
      <label htmlFor={`reply-${m.id}`} className="flex items-center gap-2 text-meta text-muted">Your reply, sent to {toWords}<Kbd>{keyLabel("reply")}</Kbd></label>
      <textarea id={`reply-${m.id}`} ref={box} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => {
        if (pressed("send", e)) { e.preventDefault(); void send(); }
        else if (pressed("leave", e)) { e.preventDefault(); box.current?.blur(); }
      }} rows={3} className="w-full resize-y rounded-control border border-field-border bg-surface px-2.5 py-1.5 text-ink" disabled={busy} />
      <div className="flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy || !text.trim()} className="inline-flex min-h-11 items-center gap-2 rounded-control border border-accent-strong bg-accent px-3.5 font-bold text-on-accent disabled:border-rule disabled:bg-selected disabled:text-muted">{busy ? "Sending…" : `Send to ${toWords}`}{!busy && <span aria-hidden className="font-normal opacity-80 pointer-coarse:hidden">{keyLabel("send")}</span>}</button>
        <span className="text-meta text-muted pointer-coarse:hidden">{keyLabel("leave")} leaves the box</span>
      </div>
    </form>
    {error !== null && <Problem error={error} />}
    <p className="flex flex-wrap items-center gap-x-5 gap-y-1 text-meta text-muted">
      <a href={boardHref} onClick={onLeave} className="inline-flex min-h-11 items-center gap-1.5">Open on the board<CornerDownLeft className="size-3 pointer-coarse:hidden" aria-label="Enter" /></a>
      {keys}
    </p>
  </article>;
}

/**
 * useRepliesWaitingCount is how many messages wait for the person's reply, for the
 * Inbox's count beside the boards. It reads again when the boards change, every 30
 * seconds, and after a reply is sent.
 */
export function useRepliesWaitingCount(boards: Board[] | null): number {
  const [n, setN] = useState(0);
  // The boards change with every message on a busy board: reads never overlap, and
  // whatever changed during one is read once after it.
  const read = useRef({ busy: false, again: false, mounted: true });
  const load = useCallback(() => {
    const r = read.current;
    if (r.busy) { r.again = true; return; }
    r.busy = true;
    void repliesWaiting(1).then((w) => { if (r.mounted) setN(w.count); }, () => { /* The count stays as it was until the next read. */ }).finally(() => {
      r.busy = false;
      if (r.mounted && r.again) { r.again = false; load(); }
    });
  }, []);
  useEffect(() => {
    const r = read.current;
    r.mounted = true;
    const tick = setInterval(() => document.visibilityState === "visible" && load(), 30_000);
    window.addEventListener(repliesChanged, load);
    return () => { r.mounted = false; clearInterval(tick); window.removeEventListener(repliesChanged, load); };
  }, [load]);
  useEffect(load, [boards, load]);
  return n;
}
