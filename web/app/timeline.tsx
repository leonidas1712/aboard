"use client";

// The timeline: messages and board events in chat order, oldest at the top. It opens at
// the newest entry, holds still while you read further up, and offers a way back down.

import { ArrowDown, ArrowRight, MessageSquare, Reply } from "lucide-react";
import { Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { BoardEvent, Message } from "./api";
import { displayName, exactTime, recipients, relativeTime } from "./words";

export type Entry = { kind: "message"; seq: number; m: Message } | { kind: "event"; seq: number; e: BoardEvent; line: string };

type Props = {
  entries: Entry[];
  /** dividerSeq is the first entry that is new since the person last looked. */
  dividerSeq: number | null;
  hasEarlier: boolean;
  loadEarlier: () => void;
  /** quote finds the message a reply answers, if it is loaded. */
  quote: (m: Message) => string | null;
  /** waiting holds the ids of questions waiting for the person's reply. */
  waiting: Set<string>;
  onReply: (m: Message) => void;
  /** onSeen runs with the newest seq while the newest entry is in view. */
  onSeen: (seq: number) => void;
  /** stick asks the timeline to scroll to the newest entry once, as after posting. */
  stick: number;
  empty: ReactNode;
};

const nearBottom = 48;

export function Timeline({ entries, dividerSeq, hasEarlier, loadEarlier, quote, waiting, onReply, onSeen, stick, empty }: Props) {
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

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
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
        className="timeline h-full overflow-y-auto [overflow-anchor:none]"
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
          {entries.map((x, i) => (
            <Fragment key={x.kind === "message" ? x.m.id : x.e.id}>
              {dividerSeq !== null && x.seq === dividerSeq && i > 0 && <NewDivider />}
              {x.kind === "message" ? (
                <MessageEntry
                  m={x.m}
                  now={now}
                  quote={quote(x.m)}
                  waiting={waiting.has(x.m.id)}
                  onReply={() => onReply(x.m)}
                  afterEvent={i > 0 && entries[i - 1].kind === "event"}
                  arrived={x.seq > openedAt.current!}
                />
              ) : (
                <EventLine e={x.e} line={x.line} now={now} arrived={x.seq > openedAt.current!} />
              )}
            </Fragment>
          ))}
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

function MessageEntry({
  m,
  now,
  quote,
  waiting,
  onReply,
  afterEvent,
  arrived,
}: {
  m: Message;
  now: number;
  quote: string | null;
  waiting: boolean;
  onReply: () => void;
  afterEvent: boolean;
  arrived: boolean;
}) {
  const isReply = m.reply_to !== null;
  const self = m.sender === "self";
  const sender = self ? "You" : displayName(m.from, m.show_owner);
  const about = m.from.kind === "agent" ? [m.from.role && `Role ${m.from.role}`, m.from.harness && `Harness ${m.from.harness}`].filter(Boolean).join(", ") : undefined;
  return (
    <li
      className={cn("message group grid grid-cols-[24px_minmax(0,1fr)] gap-x-2.5 py-3.5", !afterEvent && "border-t border-rule", arrived && "animate-arrive")}
      data-seq={m.seq}
      data-id={m.id}
    >
      <span className="pt-[3px]" title={isReply ? "Reply" : "Message"}>
        {isReply ? (
          <Reply className="size-4 text-accent" strokeWidth={1.5} aria-label="Reply" />
        ) : (
          <MessageSquare className="size-4 text-muted" strokeWidth={1.5} aria-label="Message" />
        )}
      </span>
      <div className="min-w-0">
        <div className="flex items-baseline gap-3">
          <p className="min-w-0 flex-1 break-words">
            <strong title={about}>{sender}</strong>
            <ArrowRight className="mx-1 inline size-3.5 -translate-y-px text-muted" strokeWidth={1.5} aria-label="to" />
            <span>{recipients(m.to)}</span>
            {m.urgent && <span className="ml-2 text-meta text-muted">Urgent</span>}
            {m.expects_reply && <span className="ml-2 text-meta text-muted">Asks for a reply</span>}
          </p>
          {!waiting && (
            <button
              type="button"
              onClick={onReply}
              className="reply-button h-7 shrink-0 rounded-[6px] px-2 text-meta text-link opacity-0 transition-opacity duration-[140ms] ease-out group-focus-within:opacity-100 group-hover:opacity-100 hover:underline focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
              aria-label={`Reply to ${self ? "your message" : m.from.name}`}
            >
              Reply
            </button>
          )}
          <Time at={m.at} now={now} />
        </div>
        {isReply && (
          <p className="truncate text-meta text-muted" title={quote ?? undefined}>
            {quote ?? "Replying to an earlier message"}
          </p>
        )}
        <p className="body mt-0.5 whitespace-pre-wrap break-words">{m.body}</p>
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
