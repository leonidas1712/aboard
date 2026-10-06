"use client";

// The timeline: messages and board events in chat order, oldest at the top. It opens at
// the newest entry, holds still while you read further up, and offers a way back down.
// Messages from one sender in a row are grouped under one header, the way chats do.

import { lab } from "aboard-lab";
import { ArrowDown, ArrowRight, ChevronRight, CircleQuestionMark, MessageSquare, Reply, Zap } from "lucide-react";
import { Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { BoardEvent, Message, MemberRef } from "./api";
import { mentionedTargets, segments } from "./mentions";
import { type OnReact, ReactButton, Reactions } from "./reactions";
import { ReceiptMark, wantsReceipts } from "./receipts";
import { clockTime, count, displayName, exactTime, markOf, recipients, relativeTime } from "./words";

/**
 * Thread is what a message that starts a thread shows below it: how many replies, when
 * the last came and who wrote them, and, open, the replies themselves, one level deep.
 */
export type Thread = {
  root: string;
  /** replies is what the open thread shows: every reply, or under a filter the matching ones. */
  replies: Message[];
  total: number;
  lastAt: string;
  /** repliers are the thread's distinct reply senders, most recent first. */
  repliers: MemberRef[];
  open: boolean;
  /** filtered is true when a filter picked the replies shown; the thread then stays open. */
  filtered: boolean;
  /** fresh counts replies from others the person hasn't seen yet. */
  fresh: number;
  /** firstFresh is the oldest of those replies. */
  firstFresh: string | null;
  /** waiting is a reply in the thread that waits for the person's answer. */
  waiting: Message | null;
};

export type Entry =
  | { kind: "message"; seq: number; m: Message; thread?: Thread }
  | { kind: "event"; seq: number; e: BoardEvent; line: string };

type Props = {
  entries: Entry[];
  /** dividerSeq is the first entry that is new since the person last looked. */
  dividerSeq: number | null;
  hasEarlier: boolean;
  loadEarlier: () => void;
  /** quote finds the message a reply answers, if it is loaded. */
  quote: (m: Message) => string | null;
  /** answer finds the first reply to a message that asks for one, if it is loaded. */
  answer: (m: Message) => Message | null;
  /** identity is the sender's identity colour, 1 to 8. */
  identity: (from: MemberRef) => number;
  /** onMention shows a mentioned member or role in the board panel. */
  onMention: (target: string) => void;
  /** waiting holds the ids of questions waiting for the person's reply. */
  waiting: Set<string>;
  /** onReply and onReact are absent on a read-only board, which then offers neither. */
  onReply?: (m: Message) => void;
  /** onReact adds or takes back the person's reaction to a message. */
  onReact?: OnReact;
  /** me is the person's name, so their own reactions read "You". */
  me: string | null;
  /** onToggle opens or closes a thread, by its first message's id. */
  onToggle: (root: string, open: boolean) => void;
  /** onShow scrolls to a message, opening its thread first if it is closed. */
  onShow: (id: string) => void;
  /** onSeen runs for each message row presented on screen. */
  onSeen: (seq: number) => void;
  /** receipts says where to read the receipts of the person's messages; null shows none. */
  receipts: ReceiptsAt;
  /** stick asks the timeline to scroll to the newest entry once, as after posting. */
  stick: number;
  /** resetKey changes when the timeline shows something else, such as a new filter. */
  resetKey: string;
  empty: ReactNode;
  /** column is the class of the centred reading column inside the scrolling area. */
  column: string;
};

const nearBottom = 48;
// Messages from one sender within this long of the one before share its header.
const groupWindow = 5 * 60_000;

/** standsAlone is true for messages that keep their own header and outline. */
function standsAlone(m: Message): boolean {
  return m.urgent || m.expects_reply;
}

/** continues says whether b is shown under a's header. A thread below a ends the group. */
function continues(a: Entry | undefined, b: Entry): boolean {
  if (!a || a.kind !== "message" || b.kind !== "message" || a.thread) return false;
  return follows(a.m, b.m);
}

/** follows says whether message y, right after x, shares x's header. */
function follows(x: Message, y: Message): boolean {
  return (
    x.from.name === y.from.name &&
    x.from.kind === y.from.kind &&
    x.to.join() === y.to.join() &&
    !standsAlone(x) &&
    !standsAlone(y) &&
    new Date(y.at).getTime() - new Date(x.at).getTime() < groupWindow
  );
}

export function Timeline({
  entries,
  dividerSeq,
  hasEarlier,
  loadEarlier,
  quote,
  answer,
  identity,
  onMention,
  waiting,
  onReply,
  onReact,
  me,
  onToggle,
  onShow,
  onSeen,
  receipts,
  stick,
  resetKey,
  empty,
  column,
}: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const [showJump, setShowJump] = useState(false);
  const [unseen, setUnseen] = useState(0);
  const lastCount = useRef({ first: 0, last: 0, height: 0, messages: 0 });
  const now = useNow();
  const mentions = useMemo(() => ({ onMention }), [onMention]);

  const newestSeq = entries.at(-1)?.seq ?? 0;
  // latest is the newest message shown anywhere, replies in threads included.
  const latest = entries.reduce((n, e) => Math.max(n, e.seq, e.kind === "message" ? (e.thread?.replies.at(-1)?.seq ?? 0) : 0), 0);
  // Entries already there when the page opened appear at once; only later ones animate in.
  const openedAt = useRef<number | null>(null);
  if (openedAt.current === null) openedAt.current = latest;
  const messageCount = entries.filter((e) => e.kind === "message").length;

  const toBottom = useCallback((smooth: boolean) => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? "smooth" : "auto" });
  }, []);

  // A new filter shows another set of entries: start again at the newest.
  useLayoutEffect(() => {
    atBottom.current = true;
    lastCount.current = { first: 0, last: 0, height: 0, messages: 0 };
    setUnseen(0);
    setShowJump(false);
  }, [resetKey]);

  // Keep the reading position: at the bottom, follow new entries; further up, hold still
  // and count what arrived; when earlier entries are added above, keep the same ones in view.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const prev = lastCount.current;
    const first = entries[0]?.seq ?? 0;
    if (prev.first !== 0 && first < prev.first && !atBottom.current) {
      el.scrollTop += el.scrollHeight - prev.height;
    } else if (atBottom.current) {
      el.scrollTop = el.scrollHeight;
    } else if (messageCount > prev.messages && newestSeq > prev.last) {
      setUnseen((n) => n + entries.filter((e) => e.kind === "message" && e.seq > prev.last).length);
    }
    lastCount.current = {
      first,
      last: newestSeq,
      height: el.scrollHeight,
      messages: messageCount,
    };
  }, [entries, newestSeq, messageCount]);

  useEffect(() => {
    if (stick > 0) {
      atBottom.current = true;
      toBottom(false);
    }
  }, [stick, toBottom]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const observer = new IntersectionObserver((observed) => {
      if (document.visibilityState !== "visible") return;
      for (const row of observed) {
        if (row.isIntersecting) onSeen(Number((row.target as HTMLElement).dataset.seq));
      }
    }, { root: el });
    const observe = () => {
      observer.disconnect();
      for (const row of el.querySelectorAll<HTMLElement>(".message[data-seq]")) observer.observe(row);
    };
    observe();
    document.addEventListener("visibilitychange", observe);
    return () => {
      observer.disconnect();
      document.removeEventListener("visibilitychange", observe);
    };
  }, [entries, onSeen]);

  // The scrollbar shows while the timeline scrolls, then fades back out.
  const scrolling = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    el.dataset.scrolling = "";
    if (scrolling.current) clearTimeout(scrolling.current);
    scrolling.current = setTimeout(() => delete el.dataset.scrolling, 900);
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight < nearBottom;
    atBottom.current = bottom;
    setShowJump(!bottom);
    if (bottom) {
      setUnseen(0);
    }
    lastCount.current.height = el.scrollHeight;
  };

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={scroller}
        onScroll={onScroll}
        className="timeline quiet-scroll h-full overflow-y-auto [overflow-anchor:none]"
        role="log"
        aria-label="Timeline"
        aria-live="polite"
        aria-relevant="additions"
        tabIndex={0}
      >
        <div className={column}>
          {hasEarlier && (
            <div className="flex justify-center pt-3">
              <Button variant="quiet" onClick={loadEarlier}>
                Show earlier messages
              </Button>
            </div>
          )}
          <ol className="flex flex-col pb-4">
            {entries.map((x, i) => {
              const divider = dividerSeq !== null && x.seq === dividerSeq && i > 0;
              const next = entries[i + 1];
              const nextDivider = next !== undefined && dividerSeq !== null && next.seq === dividerSeq;
              return (
                <Fragment key={x.kind === "message" ? x.m.id : x.e.id}>
                  {divider && <NewDivider />}
                  {x.kind === "message" ? (
                    <MessageEntry
                      m={x.m}
                      now={now}
                      quote={quote(x.m)}
                      answer={x.m.expects_reply ? answer(x.m) : null}
                      identity={identity(x.m.from)}
                      mentions={mentions}
                      waiting={waiting.has(x.m.id)}
                      onReply={onReply && (() => onReply(x.m))}
                      onReact={onReact}
                      me={me}
                      onShow={onShow}
                      receipts={receipts}
                      grouped={!divider && continues(entries[i - 1], x)}
                      groupGoesOn={!x.thread && !nextDivider && next !== undefined && continues(x, next)}
                      ruled={i > 0 && !divider && entries[i - 1].kind === "message"}
                      threaded={!!x.thread}
                      arrived={x.seq > openedAt.current!}
                    />
                  ) : (
                    <EventLine e={x.e} line={x.line} now={now} arrived={x.seq > openedAt.current!} />
                  )}
                  {x.kind === "message" && x.thread && (
                    <ThreadBlock
                      root={x.m}
                      thread={x.thread}
                      now={now}
                      quote={quote}
                      answer={answer}
                      identity={identity}
                      mentions={mentions}
                      waitingIds={waiting}
                      onReply={onReply}
                      onReact={onReact}
                      me={me}
                      onToggle={onToggle}
                      onShow={onShow}
                      receipts={receipts}
                      openedAt={openedAt.current!}
                    />
                  )}
                </Fragment>
              );
            })}
          </ol>
          {messageCount === 0 && empty}
        </div>
      </div>
      <div
        className={cn(
          "jump-newest pointer-events-none absolute inset-x-0 bottom-3 flex justify-center transition-[opacity,transform] duration-200 ease-out",
          showJump ? "translate-y-0 opacity-100" : "translate-y-2 opacity-0",
        )}
      >
        <Button
          variant="secondary"
          className={cn("bg-surface", showJump && "pointer-events-auto")}
          tabIndex={showJump ? 0 : -1}
          aria-hidden={!showJump}
          onClick={() => toBottom(true)}
        >
          <ArrowDown strokeWidth={1.5} aria-hidden />
          Jump to newest{unseen > 0 && ` · ${unseen} new`}
        </Button>
      </div>
    </div>
  );
}

/** useNow re-renders every 30 seconds, so relative times stay true. */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  return now;
}

/**
 * showMessage scrolls to a message and marks it briefly, as when following "answered
 * by". It returns false when the message isn't on the page, as inside a closed thread.
 */
export function showMessage(id: string): boolean {
  const el = document.querySelector<HTMLElement>(`[data-id="${id}"]`);
  if (!el) return false;
  el.scrollIntoView({ behavior: "smooth", block: "center" });
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
  return true;
}

function NewDivider() {
  return (
    <li className="new-divider flex items-center gap-3 py-1" aria-label="New since you last looked">
      <span className="h-px flex-1 bg-accent" />
      <span className="text-meta font-bold text-link">New since you last looked</span>
      <span className="h-px flex-1 bg-accent" />
    </li>
  );
}

function Time({ at, now }: { at: string; now: number }) {
  return (
    <time dateTime={at} title={exactTime(at)} className="shrink-0 text-meta whitespace-nowrap text-muted tabular-nums">
      {relativeTime(at, now)}
    </time>
  );
}

/** SenderMark is a sender's one or two letters on its identity colour. */
export function SenderMark({
  name,
  kind,
  identity,
  className,
}: {
  name: string;
  kind: "agent" | "human";
  identity: number;
  className?: string;
}) {
  const mark = markOf(name, kind);
  return (
    <span
      aria-hidden
      className={cn(
        "sender-mark flex size-8 shrink-0 items-center justify-center rounded-control font-bold tracking-[0.02em] select-none",
        mark.length > 1 ? "text-[13px]" : "text-body",
        className,
      )}
      style={{
        background: `var(--id-${identity}-bg)`,
        color: `var(--id-${identity}-fg)`,
      }}
    >
      {mark}
    </span>
  );
}

/** Kind is the small glyph beside the sender's name that says what sort of message it is. */
function Kind({ m, nested }: { m: Message; nested: boolean }) {
  const glyph = "inline size-3.5 shrink-0 -translate-y-px";
  if (m.urgent) {
    return (
      <span title="Urgent: first in each recipient's next delivery">
        <Zap className={cn(glyph, "text-ink")} strokeWidth={1.75} aria-label="Urgent" />
      </span>
    );
  }
  if (m.expects_reply) {
    return (
      <span title="Asks for a reply">
        <CircleQuestionMark className={cn(glyph, "text-ink")} strokeWidth={1.75} aria-label="Asks for a reply" />
      </span>
    );
  }
  // Inside a thread every message is a reply, so a glyph would say nothing new.
  if (nested) return null;
  if (m.reply_to) {
    return (
      <span title="Reply">
        <Reply className={cn(glyph, "text-accent")} strokeWidth={1.75} aria-label="Reply" />
      </span>
    );
  }
  return (
    <span title="Message">
      <MessageSquare className={cn(glyph, "text-muted")} strokeWidth={1.5} aria-label="Message" />
    </span>
  );
}

/** ReceiptsAt is the board to read receipts on and what moves them; null shows none. */
export type ReceiptsAt = { board: string; activity: number } | null;

function MessageEntry({
  m,
  now,
  quote,
  answer,
  identity,
  mentions,
  waiting,
  onReply,
  onReact,
  me,
  onShow,
  receipts,
  grouped,
  groupGoesOn,
  ruled,
  threaded = false,
  nested = false,
  quoted = true,
  arrived,
}: {
  m: Message;
  now: number;
  quote: string | null;
  answer: Message | null;
  identity: number;
  mentions: Mentions;
  waiting: boolean;
  onReply?: () => void;
  onReact?: OnReact;
  me: string | null;
  onShow: (id: string) => void;
  receipts: ReceiptsAt;
  /** grouped is true when the message shows under the header of the one before. */
  grouped: boolean;
  /** groupGoesOn is true when the next message shows under this one's header. */
  groupGoesOn: boolean;
  /** ruled is true when a rule separates this entry from the message before it. */
  ruled: boolean;
  /** threaded is true when the message starts a thread shown right below it. */
  threaded?: boolean;
  /** nested is true for a reply shown inside its thread. */
  nested?: boolean;
  /** quoted is false when the message it replies to is right above it, so no quote is needed. */
  quoted?: boolean;
  arrived: boolean;
}) {
  const self = m.sender === "self";
  const sender = self ? "You" : displayName(m.from, m.show_owner);
  const about =
    m.from.kind === "agent"
      ? [m.from.role && `Role ${m.from.role}`, m.from.harness && `Harness ${m.from.harness}`].filter(Boolean).join(", ")
      : undefined;
  const outlined = standsAlone(m) && !answer;
  const replyButton = !waiting && onReply && (
    <button
      type="button"
      onClick={onReply}
      className="reply-button h-7 shrink-0 rounded-[6px] px-2 text-meta text-link opacity-0 transition-opacity duration-[140ms] ease-out group-focus-within:opacity-100 group-hover:opacity-100 hover:underline focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      aria-label={`Reply to ${self ? "your message" : m.from.name}`}
    >
      Reply
    </button>
  );
  const actions = (onReact || replyButton) && (
    <span className="flex shrink-0 items-center gap-0.5 self-center">
      {onReact && <ReactButton m={m} onReact={onReact} label={self ? "your message" : m.from.name} />}
      {replyButton}
    </span>
  );
  return (
    <li
      className={cn(
        "message group relative grid px-2.5 transition-colors duration-200 ease-out",
        nested ? "reply grid-cols-[24px_minmax(0,1fr)] gap-x-2.5" : "grid-cols-[32px_minmax(0,1fr)] gap-x-3",
        grouped ? "pt-0.5" : nested ? "pt-2" : "pt-3",
        groupGoesOn ? "pb-0.5" : threaded ? "pb-1.5" : nested ? "pb-2" : "pb-3",
        ruled && !grouped && "border-t border-rule",
        self && "own bg-own",
        self && !grouped && "rounded-t-box",
        self && !groupGoesOn && "rounded-b-box",
        outlined && "my-1.5 rounded-box border",
        outlined && (m.urgent ? "urgent border-[var(--outline-strong)]" : "asks border-[var(--outline-faint)]"),
        arrived && "animate-arrive",
      )}
      data-seq={m.seq}
      data-id={m.id}
      data-sender={m.from.name}
      data-grouped={grouped || undefined}
    >
      <span>
        {grouped && nested ? null : grouped ? (
          <time
            dateTime={m.at}
            title={exactTime(m.at)}
            className="-ml-2.5 block text-right text-[12px] leading-[22px] whitespace-nowrap text-muted tabular-nums opacity-0 transition-opacity duration-[140ms] ease-out group-hover:opacity-100 group-focus-within:opacity-100"
          >
            {clockTime(m.at)}
          </time>
        ) : (
          <SenderMark
            name={m.from.name}
            kind={m.from.kind}
            identity={identity}
            className={nested ? "size-6 rounded-[6px] text-[11px]" : undefined}
          />
        )}
      </span>
      <div className="min-w-0">
        {!grouped && (
          <div className="flex items-baseline gap-3">
            <p className="min-w-0 flex-1 break-words">
              <strong title={about} className="sender">
                {sender}
              </strong>{" "}
              <Kind m={m} nested={nested} />
              <ArrowRight className="mx-1 inline size-3.5 -translate-y-px text-muted" strokeWidth={1.5} aria-label="to" />
              <span>{recipients(m.to)}</span>
              {m.urgent && <span className="ml-2 text-meta text-muted">Urgent</span>}
              {m.expects_reply &&
                (answer ? (
                  <button
                    type="button"
                    className="answered ml-2 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
                    onClick={() => onShow(answer.id)}
                  >
                    Answered by {answer.sender === "self" ? "you" : answer.from.name}
                  </button>
                ) : (
                  <span className="ml-2 text-meta text-muted">Asks for a reply</span>
                ))}
              {lab?.MessageMeta && receipts && <lab.MessageMeta board={receipts.board} message={m} grouped={false} />}
            </p>
            {actions}
            <Time at={m.at} now={now} />
          </div>
        )}
        {m.reply_to !== null && quoted && (
          <p className="flex items-center gap-1.5 text-meta text-muted" title={quote ?? undefined}>
            {grouped && <Reply className="size-3.5 shrink-0 text-accent" strokeWidth={1.75} aria-label="Reply" />}
            <span className="truncate">{quote ?? "Replying to an earlier message"}</span>
          </p>
        )}
        {grouped && lab?.MessageMeta && receipts && <lab.MessageMeta board={receipts.board} message={m} grouped />}
        <p className={cn("body whitespace-pre-wrap break-words", !grouped && "mt-0.5", grouped && "pr-24")}>
          <Body m={m} mentions={mentions} />
        </p>
        {lab?.MessageFooter && receipts && <lab.MessageFooter board={receipts.board} message={m} />}
        <Reactions m={m} me={me} onReact={onReact} />
        {receipts && wantsReceipts(m) && <ReceiptMark board={receipts.board} seq={m.seq} activity={receipts.activity} />}
        {grouped && actions && <div className="absolute top-0 right-2.5">{actions}</div>}
        {waiting && (
          <div className="mt-2.5 flex flex-wrap items-center justify-between gap-3 rounded-box bg-attention px-3.5 py-2.5 text-ink">
            <p>{m.from.name} is waiting for your reply.</p>
            {onReply && <Button onClick={onReply}>Reply</Button>}
          </div>
        )}
      </div>
    </li>
  );
}

/**
 * ThreadBlock sits under a message that starts a thread: a row saying how many replies,
 * when the last came and who wrote them, which opens and closes the replies, one level
 * deep, oldest first. Closed, a reply the person hasn't seen shows as "N new", and a
 * question waiting for them still shows.
 */
function ThreadBlock({
  root,
  thread,
  now,
  quote,
  answer,
  identity,
  mentions,
  waitingIds,
  onReply,
  onReact,
  me,
  onToggle,
  onShow,
  receipts,
  openedAt,
}: {
  root: Message;
  thread: Thread;
  now: number;
  quote: (m: Message) => string | null;
  answer: (m: Message) => Message | null;
  identity: (from: MemberRef) => number;
  mentions: Mentions;
  waitingIds: Set<string>;
  onReply?: (m: Message) => void;
  onReact?: OnReact;
  me: string | null;
  onToggle: (root: string, open: boolean) => void;
  onShow: (id: string) => void;
  receipts: ReceiptsAt;
  openedAt: number;
}) {
  const id = `thread-${root.id}`;
  const open = thread.open || thread.filtered;
  const replies = count(thread.total, "reply", "replies");
  const summary = (
    <>
      <span className="flex shrink-0 items-center gap-1" aria-hidden>
        {thread.repliers.slice(0, 3).map((r) => (
          <SenderMark key={`${r.kind}:${r.name}`} name={r.name} kind={r.kind} identity={identity(r)} className="size-5 rounded-[5px] text-[10px]" />
        ))}
      </span>
      <span className="thread-count font-bold text-link">{thread.filtered ? `${thread.replies.length} of ${replies} match` : replies}</span>
      <span className="text-muted">
        · last <time dateTime={thread.lastAt} title={exactTime(thread.lastAt)}>{relativeTime(thread.lastAt, now)}</time>
      </span>
      {thread.fresh > 0 && !open && (
        <span className="thread-new inline-flex animate-fade-in items-center gap-1.5 font-bold text-link">
          <span className="size-2 rounded-full bg-accent" aria-hidden />
          {thread.fresh} new
        </span>
      )}
    </>
  );
  const toggle = thread.filtered ? (
    <p className="thread-toggle flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 text-meta">{summary}</p>
  ) : (
    <button
      type="button"
      className="thread-toggle -ml-1.5 flex min-h-11 max-w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-control px-1.5 text-left text-meta transition-colors duration-[140ms] ease-out hover:bg-selected"
      aria-expanded={open}
      aria-controls={open ? id : undefined}
      aria-label={`${open ? "Hide" : "Show"} ${replies}${thread.fresh > 0 && !open ? `, ${thread.fresh} new` : ""}`}
      onClick={() => onToggle(root.id, !open)}
    >
      {summary}
      <ChevronRight
        className={cn("size-3.5 shrink-0 text-muted transition-transform duration-200 ease-out", open && "rotate-90")}
        strokeWidth={1.75}
        aria-hidden
      />
    </button>
  );
  return (
    <li className="thread pr-2.5 pb-2 pl-[54px] max-sm:pl-[42px]" data-thread={root.id}>
      {lab?.ThreadMeta && receipts ? (
        // Only the UI lab adds to this row (lab-seam.ts).
        <div className="flex flex-wrap items-center gap-x-2">
          {toggle}
          <lab.ThreadMeta board={receipts.board} root={root} />
        </div>
      ) : (
        toggle
      )}
      {!open && thread.waiting && (
        <div className="mt-1 flex flex-wrap items-center justify-between gap-3 rounded-box bg-attention px-3.5 py-2.5 text-ink">
          <p>{thread.waiting.from.name} is waiting for your reply in this thread.</p>
          {onReply && <Button onClick={() => onReply(thread.waiting!)}>Reply</Button>}
        </div>
      )}
      {open && (
        <div id={id} className="replies animate-fade-in">
          <ol className="-ml-[28px] flex flex-col border-l border-rule pl-[17px] max-sm:-ml-[16px] max-sm:pl-[5px]" aria-label={`Replies to ${root.from.name}`}>
            {thread.replies.map((r, i) => {
              const prev = thread.replies[i - 1];
              const next = thread.replies[i + 1];
              return (
                <MessageEntry
                  key={r.id}
                  m={r}
                  now={now}
                  quote={quote(r)}
                  answer={r.expects_reply ? answer(r) : null}
                  identity={identity(r.from)}
                  mentions={mentions}
                  waiting={waitingIds.has(r.id)}
                  onReply={onReply && (() => onReply(r))}
                  onReact={onReact}
                  me={me}
                  onShow={onShow}
                  receipts={receipts}
                  grouped={prev !== undefined && follows(prev, r)}
                  groupGoesOn={next !== undefined && follows(r, next)}
                  ruled={false}
                  nested
                  quoted={r.reply_to !== root.id && r.reply_to !== prev?.id}
                  arrived={r.seq > openedAt}
                />
              );
            })}
          </ol>
          {!thread.filtered && onReply && (
            <button
              type="button"
              className="thread-reply -ml-1.5 min-h-9 rounded-control px-1.5 text-meta text-link hover:underline"
              onClick={() => onReply(root)}
            >
              Reply in thread
            </button>
          )}
        </div>
      )}
    </li>
  );
}

/** Mentions is what a message body needs to mark its mentions: what a click on one does. */
type Mentions = { onMention: (target: string) => void };

/**
 * Body is a message's text as written, with each mention the server recorded when it
 * was posted marked as a button that shows the member or role in the board panel. The
 * text stays text, never HTML.
 */
function Body({ m, mentions }: { m: Message; mentions: Mentions }) {
  return segments(m.body, mentionedTargets(m)).map((s, i) =>
    s.target ? (
      <button
        key={i}
        type="button"
        data-target={s.target}
        onClick={() => mentions.onMention(s.target!)}
        className="mention rounded-[4px] bg-[var(--mention)] px-0.5 font-bold text-ink decoration-1 underline-offset-[3px] hover:underline"
        title={`Show ${s.target.startsWith("@") ? s.target.slice(1) : `the ${s.target.slice(5)} role`} in the board panel`}
      >
        {s.text}
      </button>
    ) : lab?.Text ? (
      <lab.Text key={i} text={s.text} />
    ) : (
      <Fragment key={i}>{s.text}</Fragment>
    ),
  );
}

function EventLine({ e, line, now, arrived }: { e: BoardEvent; line: string; now: number; arrived: boolean }) {
  return (
    <li className={cn("board-event flex justify-center px-6 py-2.5", arrived && "animate-arrive")}>
      <p
        className="max-w-full rounded-full border border-rule bg-surface px-3 py-0.5 text-center text-meta text-muted"
        title={exactTime(e.at)}
      >
        {line}
        <span className="sr-only">, {relativeTime(e.at, now)}</span>
      </p>
    </li>
  );
}
