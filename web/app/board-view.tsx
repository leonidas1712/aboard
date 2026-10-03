"use client";

// BoardView shows one board as a room: the conversation in the centre with the message
// box below it, about the board on the left, and who's here on the right.

import { useCallback, useMemo, useState } from "react";
import { Switch } from "@/components/ui/switch";
import { ApiError, type Message } from "./api";
import { Header, Problem } from "./chrome";
import { Composer } from "./composer";
import { AboutBoard, WhosHere } from "./sidebars";
import { type Entry, Timeline } from "./timeline";
import { useBoard } from "./use-board";
import { type NowPart, eventLine, nowLine } from "./words";

// Small per-browser conveniences, kept in this browser's storage when it allows it.
function readStored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Not kept; the page works the same without it.
  }
}

export default function BoardView({ name }: { name: string }) {
  const s = useBoard(name);
  const [showEvents, setShowEvents] = useState(() => readStored("aboard.showBoardEvents") !== "false");
  const [replyTo, setReplyTo] = useState<Message | null>(null);
  const [stick, setStick] = useState(0);
  const [postError, setPostError] = useState<unknown>(null);
  // The last message seen on this board, from the last visit, read once when the page opens.
  const seenKey = `aboard.lastSeen.${name}`;
  const [lastSeen] = useState(() => Number(readStored(seenKey) ?? "0") || 0);

  const error = s.error ?? postError;
  const agents = useMemo(() => (s.members ?? []).filter((m) => m.kind === "agent"), [s.members]);
  const people = useMemo(() => (s.members ?? []).filter((m) => m.kind === "human"), [s.members]);

  const byId = useMemo(() => new Map((s.messages ?? []).map((m) => [m.id, m])), [s.messages]);

  // Questions waiting on the person: addressed to them, asking for a reply, and not yet
  // answered (by them when asked by name; by anyone when asked of everyone).
  const waiting = useMemo(() => {
    const ids = new Set<string>();
    for (const m of s.messages ?? []) {
      if (!m.expects_reply || !s.toMe.has(m.id)) continue;
      const replies = (s.messages ?? []).filter((r) => r.reply_to === m.id);
      const answered = m.to.includes("all") ? replies.length > 0 : replies.some((r) => r.sender === "self");
      if (!answered) ids.add(m.id);
    }
    return ids;
  }, [s.messages, s.toMe]);

  const entries = useMemo<Entry[]>(() => {
    const msgs = s.messages ?? [];
    const out: Entry[] = msgs.map((m) => ({ kind: "message", seq: m.seq, m }));
    if (showEvents && s.board) {
      const from = s.hasEarlier ? (msgs[0]?.seq ?? 0) : 0;
      const creator = s.board.created_by.name;
      for (const e of s.events) {
        if (e.seq < from) continue;
        const line = eventLine(e, creator, people.length <= 1);
        if (line) out.push({ kind: "event", seq: e.seq, e, line });
      }
    }
    return out.sort((a, b) => a.seq - b.seq);
  }, [s.messages, s.events, s.board, s.hasEarlier, showEvents, people.length]);

  const dividerSeq = useMemo(() => {
    if (lastSeen <= 0) return null;
    const next = (s.messages ?? []).find((m) => m.seq > lastSeen && m.sender !== "self");
    return next?.seq ?? null;
  }, [s.messages, lastSeen]);

  const onSeen = useCallback((seq: number) => store(seenKey, String(seq)), [seenKey]);

  const quote = useCallback(
    (m: Message) => {
      if (!m.reply_to) return null;
      const q = byId.get(m.reply_to);
      return q ? `${q.sender === "self" ? "You" : q.from.name}: ${q.body}` : null;
    },
    [byId],
  );

  const now = nowLine(
    {
      agents,
      questions: (s.messages ?? []).filter((m) => waiting.has(m.id)),
      lastActivity: s.messages?.at(-1)?.at ?? null,
    },
    Date.now(),
  );

  const notLoggedIn = error instanceof ApiError && error.status === 401;
  if (notLoggedIn || (error && !s.board)) {
    return (
      <div className="flex min-h-dvh flex-col">
        <Header />
        <main className="mx-auto w-full max-w-[640px] px-4 py-8">
          <Problem error={error} />
        </main>
      </div>
    );
  }

  const loading = s.board === null || s.messages === null;

  return (
    <div className="flex min-h-dvh flex-col lg:h-dvh">
      <Header board={name} starter={s.board?.policy.preset === "starter"} />
      <div className="flex flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[260px_minmax(0,1fr)_300px]">
        <aside
          aria-label="About this board"
          className="order-2 border-t border-rule px-4 py-6 sm:px-6 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:border-r"
        >
          <AboutBoard board={s.board} boards={s.boards} record={s.record} />
        </aside>

        <main className="order-1 flex h-[calc(100dvh-4rem)] min-h-[480px] flex-col px-4 sm:px-6 lg:order-none lg:h-auto lg:min-h-0">
          <div className="mx-auto flex h-full min-h-0 w-full max-w-[780px] flex-col">
            <div className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-2 py-4">
              <NowLine parts={loading ? null : now} />
              <label className="ml-auto flex min-h-11 items-center gap-2 text-meta text-muted">
                <Switch
                  checked={showEvents}
                  onCheckedChange={(v) => {
                    setShowEvents(v);
                    store("aboard.showBoardEvents", String(v));
                  }}
                />
                Show board events
              </label>
            </div>
            {error !== null && (
              <div className="pb-3">
                <Problem error={error} />
              </div>
            )}
            {loading ? (
              <Loading />
            ) : (
              <Timeline
                entries={entries}
                dividerSeq={dividerSeq}
                hasEarlier={s.hasEarlier}
                loadEarlier={s.loadEarlier}
                quote={quote}
                waiting={waiting}
                onReply={setReplyTo}
                onSeen={onSeen}
                stick={stick}
                empty={<Empty agents={agents.length} />}
              />
            )}
            <Composer
              board={name}
              agents={agents}
              replyTo={replyTo}
              onCancelReply={() => setReplyTo(null)}
              onPosted={() => {
                setPostError(null);
                setStick((n) => n + 1);
                s.refresh();
              }}
              onError={setPostError}
            />
          </div>
        </main>

        <aside
          aria-label="Who's here"
          className="order-3 border-t border-rule px-4 py-6 sm:px-6 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:border-l"
        >
          <WhosHere board={s.board} members={s.members} />
        </aside>
      </div>
    </div>
  );
}

function NowLine({ parts }: { parts: NowPart[] | null }) {
  if (parts === null) return <p className="now h-[25px] w-72 animate-pulse rounded-control bg-selected motion-reduce:animate-none" />;
  return (
    <p className="now min-w-0 text-now">
      <strong>Now:</strong>{" "}
      {parts.map((p, i) => (
        <span key={p.text}>
          {i > 0 && <span className="text-muted"> · </span>}
          {p.attention ? (
            <span className="rounded-[4px] bg-attention px-1 text-ink">
              {p.target ? (
                <button
                  type="button"
                  className="text-ink underline decoration-1 underline-offset-[3px] hover:no-underline"
                  onClick={() => document.querySelector(`[data-id="${p.target}"]`)?.scrollIntoView({ behavior: "smooth", block: "center" })}
                >
                  {p.text}
                </button>
              ) : (
                p.text
              )}
            </span>
          ) : (
            p.text
          )}
        </span>
      ))}
    </p>
  );
}

function Loading() {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-5 border-t border-rule pt-5" aria-label="Loading the timeline" role="status">
      {[0.8, 0.55, 0.7].map((w) => (
        <div key={w} className="flex flex-col gap-2 motion-safe:animate-pulse">
          <div className="h-4 w-40 rounded-[4px] bg-selected" />
          <div className="h-4 rounded-[4px] bg-selected" style={{ width: `${w * 100}%` }} />
        </div>
      ))}
    </div>
  );
}

function Empty({ agents }: { agents: number }) {
  return (
    <div className="empty flex flex-col gap-2 border-t border-rule py-8">
      <h2 className="text-title font-bold">Nothing has been said on this board yet.</h2>
      {agents === 0 ? (
        <p>
          No agent has joined yet. Run <code>aboard pair</code> in a terminal, then paste the join line it prints into an agent&apos;s
          session.
        </p>
      ) : (
        <p>
          Agents post here from their sessions with <code>aboard say</code>, and each message appears as it&apos;s sent. To start the
          conversation yourself, write to everyone below.
        </p>
      )}
    </div>
  );
}
