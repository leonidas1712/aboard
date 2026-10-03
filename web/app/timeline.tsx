"use client";

// The timeline: messages and board events in chat order, oldest at the top. It opens at
// the newest entry, holds still while you read further up, and offers a way back down.
// Messages from one sender in a row are grouped under one header, the way chats do.

import { ArrowDown, ArrowRight, CircleQuestionMark, MessageSquare, Reply, Zap } from "lucide-react";
import { Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { BoardEvent, Message, MemberRef } from "./api";
import { clockTime, displayName, exactTime, markOf, recipients, relativeTime } from "./words";

export type Entry = { kind: "message"; seq: number; m: Message } | { kind: "event"; seq: number; e: BoardEvent; line: string };

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
  /** waiting holds the ids of questions waiting for the person's reply. */
  waiting: Set<string>;
  onReply: (m: Message) => void;
  /** onSeen runs with the newest seq while the newest entry is in view. */
  onSeen: (seq: number) => void;
  /** stick asks the timeline to scroll to the newest entry once, as after posting. */
  stick: number;
  /** resetKey changes when the timeline shows something else, such as a new filter. */
  resetKey: string;
  empty: ReactNode;
};

const nearBottom = 48;
// Messages from one sender within this long of the one before share its header.
const groupWindow = 5 * 60_000;

/** standsAlone is true for messages that keep their own header and outline. */
function standsAlone(m: Message): boolean {
  return m.urgent || m.expects_reply;
}

/** continues says whether b is shown under a's header. */
function continues(a: Entry | undefined, b: Entry): boolean {
  if (!a || a.kind !== "message" || b.kind !== "message") return false;
  const x = a.m;
  const y = b.m;
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
  waiting,
  onReply,
  onSeen,
  stick,
  resetKey,
  empty,
}: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const [showJump, setShowJump] = useState(false);
  const [unseen, setUnseen] = useState(0);
  const lastCount = useRef({ first: 0, last: 0, height: 0, messages: 0 });
  const now = useNow();

  const newestSeq = entries.at(-1)?.seq ?? 0;
  // Entries already there when the page opened appear at once; only later ones animate in.
  const openedAt = useRef<number | null>(null);
  if (openedAt.current === null) openedAt.current = newestSeq;
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
    lastCount.current = { first, last: newestSeq, height: el.scrollHeight, messages: messageCount };
  }, [entries, newestSeq, messageCount]);

  useEffect(() => {
    if (stick > 0) {
      atBottom.current = true;
      toBottom(false);
    }
  }, [stick, toBottom]);

  useEffect(() => {
    if (atBottom.current && newestSeq > 0) onSeen(newestSeq);
  }, [newestSeq, onSeen]);

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
      if (newestSeq > 0) onSeen(newestSeq);
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
                    waiting={waiting.has(x.m.id)}
                    onReply={() => onReply(x.m)}
                    grouped={!divider && continues(entries[i - 1], x)}
                    groupGoesOn={!nextDivider && next !== undefined && continues(x, next)}
                    ruled={i > 0 && !divider && entries[i - 1].kind === "message"}
                    arrived={x.seq > openedAt.current!}
                  />
                ) : (
                  <EventLine e={x.e} line={x.line} now={now} arrived={x.seq > openedAt.current!} />
                )}
              </Fragment>
            );
          })}
        </ol>
        {messageCount === 0 && empty}
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

/** show scrolls to a message and marks it briefly, as when following "answered by". */
function show(id: string) {
  const el = document.querySelector<HTMLElement>(`[data-id="${id}"]`);
  if (!el) return;
  el.scrollIntoView({ behavior: "smooth", block: "center" });
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
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
export function SenderMark({ name, kind, identity, className }: { name: string; kind: "agent" | "human"; identity: number; className?: string }) {
  const mark = markOf(name, kind);
  return (
    <span
      aria-hidden
      className={cn(
        "sender-mark flex size-8 shrink-0 items-center justify-center rounded-control font-bold tracking-[0.02em] select-none",
        mark.length > 1 ? "text-[13px]" : "text-body",
        className,
      )}
      style={{ background: `var(--id-${identity}-bg)`, color: `var(--id-${identity}-fg)` }}
    >
      {mark}
    </span>
  );
}

/** Kind is the small glyph beside the sender's name that says what sort of message it is. */
function Kind({ m }: { m: Message }) {
  const glyph = "inline size-3.5 shrink-0 -translate-y-px";
  if (m.urgent) {
    return (
      <span title="Urgent: delivered into sessions at once">
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

function MessageEntry({
  m,
  now,
  quote,
  answer,
  identity,
  waiting,
  onReply,
  grouped,
  groupGoesOn,
  ruled,
  arrived,
}: {
  m: Message;
  now: number;
  quote: string | null;
  answer: Message | null;
  identity: number;
  waiting: boolean;
  onReply: () => void;
  /** grouped is true when the message shows under the header of the one before. */
  grouped: boolean;
  /** groupGoesOn is true when the next message shows under this one's header. */
  groupGoesOn: boolean;
  /** ruled is true when a rule separates this entry from the message before it. */
  ruled: boolean;
  arrived: boolean;
}) {
  const self = m.sender === "self";
  const sender = self ? "You" : displayName(m.from, m.show_owner);
  const about = m.from.kind === "agent" ? [m.from.role && `Role ${m.from.role}`, m.from.harness && `Harness ${m.from.harness}`].filter(Boolean).join(", ") : undefined;
  const outlined = standsAlone(m) && !answer;
  const replyButton = !waiting && (
    <button
      type="button"
      onClick={onReply}
      className="reply-button h-7 shrink-0 rounded-[6px] px-2 text-meta text-link opacity-0 transition-opacity duration-[140ms] ease-out group-focus-within:opacity-100 group-hover:opacity-100 hover:underline focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      aria-label={`Reply to ${self ? "your message" : m.from.name}`}
    >
      Reply
    </button>
  );
  return (
    <li
      className={cn(
        "message group relative grid grid-cols-[32px_minmax(0,1fr)] gap-x-3 px-2.5 transition-colors duration-200 ease-out",
        grouped ? "pt-0.5" : "pt-3",
        groupGoesOn ? "pb-0.5" : "pb-3",
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
        {grouped ? (
          <time
            dateTime={m.at}
            title={exactTime(m.at)}
            className="-ml-2.5 block text-right text-[12px] leading-[22px] whitespace-nowrap text-muted tabular-nums opacity-0 transition-opacity duration-[140ms] ease-out group-hover:opacity-100 group-focus-within:opacity-100"
          >
            {clockTime(m.at)}
          </time>
        ) : (
          <SenderMark name={m.from.name} kind={m.from.kind} identity={identity} />
        )}
      </span>
      <div className="min-w-0">
        {!grouped && (
          <div className="flex items-baseline gap-3">
            <p className="min-w-0 flex-1 break-words">
              <strong title={about} className="sender">
                {sender}
              </strong>{" "}
              <Kind m={m} />
              <ArrowRight className="mx-1 inline size-3.5 -translate-y-px text-muted" strokeWidth={1.5} aria-label="to" />
              <span>{recipients(m.to)}</span>
              {m.urgent && <span className="ml-2 text-meta text-muted">Urgent</span>}
              {m.expects_reply &&
                (answer ? (
                  <button
                    type="button"
                    className="answered ml-2 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
                    onClick={() => show(answer.id)}
                  >
                    Answered by {answer.sender === "self" ? "you" : answer.from.name}
                  </button>
                ) : (
                  <span className="ml-2 text-meta text-muted">Asks for a reply</span>
                ))}
            </p>
            {replyButton}
            <Time at={m.at} now={now} />
          </div>
        )}
        {m.reply_to !== null && (
          <p className="flex items-center gap-1.5 text-meta text-muted" title={quote ?? undefined}>
            {grouped && <Reply className="size-3.5 shrink-0 text-accent" strokeWidth={1.75} aria-label="Reply" />}
            <span className="truncate">{quote ?? "Replying to an earlier message"}</span>
          </p>
        )}
        <p className={cn("body whitespace-pre-wrap break-words", !grouped && "mt-0.5", grouped && "pr-16")}>{m.body}</p>
        {grouped && <div className="absolute top-0 right-2.5">{replyButton}</div>}
        {waiting && (
          <div className="mt-2.5 flex flex-wrap items-center justify-between gap-3 rounded-box bg-attention px-3.5 py-2.5 text-ink">
            <p>{m.from.name} is waiting for your reply.</p>
            <Button onClick={onReply}>Reply</Button>
          </div>
        )}
      </div>
    </li>
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
