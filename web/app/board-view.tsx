"use client";

// BoardView shows one board as a room: the conversation in the centre with the message
// box below it, the boards to move between on the left, and this board (its agents and
// people, charter, rules and details) on the right.

import { type CSSProperties, useCallback, useMemo, useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { ApiError, type MemberRef, type Message } from "./api";
import { Header, Problem } from "./chrome";
import { Composer } from "./composer";
import { FilterChips, FilterControl } from "./filter";
import { type Limits, type PanelSize, SidePanel, clampSize, headerRow, stripWidth } from "./panels";
import { Account } from "./account";
import { readStored, store, usePref } from "./prefs";
import { BoardNav, BoardPanel, type Reveal } from "./sidebars";
import { type Entry, Timeline } from "./timeline";
import { type Filter, filterActive, useBoard } from "./use-board";
import { type NowPart, boardLabel, eventLine, eventMatches, identitiesOf, identityOf, nowLine, personIdentity } from "./words";

// The reading column the "Now:" line, the timeline and the message box share, centred
// in whatever room the panels leave.
const column = "mx-auto w-full max-w-[848px] px-4 sm:px-6";

const leftPanel: Limits = { initial: 272, min: 240, max: 400 };
const rightPanel: Limits = { initial: 300, min: 260, max: 440 };

export default function BoardView({ name }: { name: string }) {
  const [filter, setFilter] = useState<Filter>({});
  const s = useBoard(name, filter);
  const [showEvents, setShowEvents] = usePref("aboard.showBoardEvents", true);
  const [leftPref, setLeft] = usePref<PanelSize>("aboard.panel.left", {
    width: leftPanel.initial,
    collapsed: false,
  });
  const [rightPref, setRight] = usePref<PanelSize>("aboard.panel.right", {
    width: rightPanel.initial,
    collapsed: false,
  });
  const left = clampSize(leftPref, leftPanel);
  const right = clampSize(rightPref, rightPanel);
  const [replyTo, setReplyTo] = useState<Message | null>(null);
  const [stick, setStick] = useState(0);
  const [postError, setPostError] = useState<unknown>(null);
  const [reveal, setReveal] = useState<Reveal>(null);
  // The last message seen on this board, from the last visit, read once when the page opens.
  const seenKey = `aboard.lastSeen.${name}`;
  const [lastSeen] = useState(() => Number(readStored(seenKey) ?? "0") || 0);

  const error = s.error ?? postError;
  const me = s.me?.name ?? null;
  const agents = useMemo(() => (s.members ?? []).filter((m) => m.kind === "agent"), [s.members]);
  const people = useMemo(() => (s.members ?? []).filter((m) => m.kind === "human"), [s.members]);

  const byId = useMemo(() => new Map([...(s.messages ?? []), ...(s.shown ?? [])].map((m) => [m.id, m])), [s.messages, s.shown]);
  // Replies by the message they answer, oldest first, from the unfiltered timeline.
  const repliesTo = useMemo(() => {
    const out = new Map<string, Message[]>();
    for (const m of s.messages ?? []) {
      if (m.reply_to) out.set(m.reply_to, [...(out.get(m.reply_to) ?? []), m]);
    }
    return out;
  }, [s.messages]);

  // Questions waiting on the person: addressed to them, asking for a reply, and not yet
  // answered (by them when asked by name; by anyone when asked of everyone).
  const waiting = useMemo(() => {
    const ids = new Set<string>();
    for (const m of s.messages ?? []) {
      if (!m.expects_reply || !s.toMe.has(m.id)) continue;
      const replies = repliesTo.get(m.id) ?? [];
      const answered = m.to.includes("all") ? replies.length > 0 : replies.some((r) => r.sender === "self");
      if (!answered) ids.add(m.id);
    }
    return ids;
  }, [s.messages, s.toMe, repliesTo]);

  const answer = useCallback(
    (m: Message) => (repliesTo.get(m.id) ?? []).find((r) => r.from.name !== m.from.name || r.from.kind !== m.from.kind) ?? null,
    [repliesTo],
  );

  const colours = useMemo(() => {
    const members = s.members ?? [];
    const out = new Map<string, number>();
    for (const p of members.filter((m) => m.kind === "human")) out.set(`human:${p.name}`, personIdentity(p.name, s.me));
    const agentIds = members.filter((m) => m.kind === "agent");
    const byId = identitiesOf(
      agentIds.map((a) => a.id),
      new Set(out.values()),
    );
    for (const a of agentIds) out.set(`agent:${a.name}`, byId.get(a.id) ?? 1);
    return out;
  }, [s.members, s.me]);
  const identity = useCallback(
    (from: MemberRef) => colours.get(`${from.kind}:${from.name}`) ?? identityOf(`${from.kind}:${from.name}`),
    [colours],
  );

  const entries = useMemo<Entry[]>(() => {
    const msgs = s.shown ?? [];
    const out: Entry[] = msgs.map((m) => ({ kind: "message", seq: m.seq, m }));
    if (showEvents && s.board && !filter.toMe) {
      const from = s.hasEarlier ? (msgs[0]?.seq ?? 0) : 0;
      const creator = s.board.created_by.name;
      for (const e of s.events) {
        if (e.seq < from || !eventMatches(e, filter.from, filter.role)) continue;
        const line = eventLine(e, creator, people.length <= 1);
        if (line) out.push({ kind: "event", seq: e.seq, e, line });
      }
    }
    return out.sort((a, b) => a.seq - b.seq);
  }, [s.shown, s.events, s.board, s.hasEarlier, showEvents, people.length, filter]);

  const dividerSeq = useMemo(() => {
    if (lastSeen <= 0) return null;
    const next = (s.shown ?? []).find((m) => m.seq > lastSeen && m.sender !== "self");
    return next?.seq ?? null;
  }, [s.shown, lastSeen]);

  const onSeen = useCallback(
    (seq: number) => {
      if (!filterActive(filter)) store(seenKey, String(seq));
    },
    [seenKey, filter],
  );

  const quote = useCallback(
    (m: Message) => {
      if (!m.reply_to) return null;
      const q = byId.get(m.reply_to);
      return q ? `${q.sender === "self" ? "You" : q.from.name}: ${q.body}` : null;
    },
    [byId],
  );

  const mine = (s.members ?? []).find((m) => m.kind === "human" && m.name === me);
  const myAccess = mine?.access ?? null;

  // show opens the board panel, if hidden, at one of its sections.
  const show = useCallback(
    (section: string) => {
      if (right.collapsed) setRight({ ...right, collapsed: false });
      setReveal((r) => ({ section, n: (r?.n ?? 0) + 1 }));
    },
    [right, setRight],
  );

  const pick = useCallback(
    (member: string) =>
      setFilter((f) => ({
        ...f,
        from: f.from === member ? undefined : member,
      })),
    [],
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

  const loading = s.board === null || s.shown === null;
  const columns = `${left.collapsed ? stripWidth : left.width}px minmax(0,1fr) ${right.collapsed ? stripWidth : right.width}px`;

  return (
    <TooltipProvider delayDuration={250}>
      <div className="flex min-h-dvh flex-col lg:h-dvh">
        <Header
          board={name}
          title={s.board?.title}
          starter={s.board?.policy.preset === "starter"}
          onTitle={s.board ? () => show("board-details") : undefined}
          onStarter={() => show("rules")}
          account={<Account admin={people.length > 1 && myAccess === "admin"} />}
        />
        <div
          className="board-columns flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[var(--columns)]"
          style={{ "--columns": columns } as CSSProperties}
        >
          <SidePanel
            side="left"
            title="Boards"
            label="board list"
            size={left}
            setSize={setLeft}
            limits={leftPanel}
            className="order-3 lg:order-none"
          >
            <nav aria-label="Boards">
              <BoardNav current={name} boards={s.boards} />
            </nav>
          </SidePanel>

          <main className="order-1 flex h-[calc(100dvh-4rem)] min-h-[480px] min-w-0 flex-col lg:order-none lg:h-auto lg:min-h-0">
            <div className={column}>
              <div className={cn(headerRow, "items-start justify-between gap-x-4 py-1.5")}>
                <NowLine parts={loading ? null : now} />
                <FilterControl
                  filter={filter}
                  setFilter={setFilter}
                  showEvents={showEvents}
                  setShowEvents={setShowEvents}
                  members={s.members ?? []}
                  me={me}
                />
              </div>
              <FilterChips filter={filter} setFilter={setFilter} showEvents={showEvents} setShowEvents={setShowEvents} me={me} />
              {error !== null && (
                <div className="pb-3">
                  <Problem error={error} />
                </div>
              )}
            </div>
            {loading ? (
              <div className={cn(column, "min-h-0 flex-1")}>
                <Loading />
              </div>
            ) : (
              <Timeline
                column={column}
                entries={entries}
                dividerSeq={filterActive(filter) ? null : dividerSeq}
                hasEarlier={s.hasEarlier}
                loadEarlier={s.loadEarlier}
                quote={quote}
                answer={answer}
                identity={identity}
                waiting={waiting}
                onReply={setReplyTo}
                onSeen={onSeen}
                stick={stick}
                resetKey={JSON.stringify(filter)}
                empty={filterActive(filter) ? <NoMatches clear={() => setFilter({})} /> : <Empty agents={agents.length} />}
              />
            )}
            <div className={column}>
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

          <SidePanel
            side="right"
            title={s.board ? boardLabel(s.board) : name}
            label="board panel"
            size={right}
            setSize={setRight}
            limits={rightPanel}
            className="order-2 lg:order-none"
          >
            <BoardPanel
              board={s.board}
              members={s.members}
              record={s.record}
              me={me}
              canInvite={s.me?.kind === "human"}
              from={filter.from}
              onPick={pick}
              reveal={reveal}
            />
          </SidePanel>
        </div>
      </div>
    </TooltipProvider>
  );
}

function NowLine({ parts }: { parts: NowPart[] | null }) {
  if (parts === null) return <p className="now h-[25px] w-72 animate-pulse rounded-control bg-selected motion-reduce:animate-none" />;
  return (
    <p className="now min-w-0 flex-1 py-2 text-now">
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

function NoMatches({ clear }: { clear: () => void }) {
  return (
    <div className="empty flex flex-col items-start gap-2 border-t border-rule py-8">
      <h2 className="text-title font-bold">No messages match these filters.</h2>
      <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={clear}>
        Show every message
      </button>
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
