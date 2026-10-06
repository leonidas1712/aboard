"use client";

// BoardView shows one board as a room: the conversation in the centre with the message
// box below it, the boards to move between on the left, and this board (its agents and
// people, charter, rules and details) on the right.

import { lab } from "aboard-lab";
import { type CSSProperties, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { ApiError, type MemberRef, type Message, type ReactionName, isArchived, react } from "./api";
import { ArchivedNotice } from "./board-lifecycle";
import { Header, Problem } from "./chrome";
import { Composer } from "./composer";
import { replyRecipients } from "./mentions";
import { FilterChips, FilterControl } from "./filter";
import { type Limits, type PanelSize, SidePanel, clampSize, headerRow, stripWidth } from "./panels";
import { Account } from "./account";
import { usePref } from "./prefs";
import { BoardNav, BoardPanel, type Reveal } from "./sidebars";
import { type Entry, type Thread, Timeline, showMessage } from "./timeline";
import { threadsOf, useThreadPrefs } from "./threads";
import { type Filter, filterActive, useBoard } from "./use-board";
import { type NowPart, boardLabel, eventLine, eventMatches, identitiesOf, identityOf, nowLine, personIdentity } from "./words";

// The reading column the "Now:" line, the timeline and the message box share, centred
// in whatever room the panels leave.
const column = "mx-auto w-full max-w-[848px] px-4 sm:px-6";

const leftPanel: Limits = { initial: 272, min: 240, max: 400 };
const rightPanel: Limits = { initial: 300, min: 260, max: 440 };

export default function BoardView({ name, onSignOut }: { name: string; onSignOut: () => void }) {
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
  // How far the person had read the board when the page opened: their read position on
  // the server, the same in every tab and on every machine.
  const lastSeen = s.readFrom ?? 0;
  const prefs = useThreadPrefs(name);

  const error = s.error ?? postError;
  const me = s.me?.name ?? null;
  const agents = useMemo(() => (s.members ?? []).filter((m) => m.kind === "agent"), [s.members]);
  const people = useMemo(() => (s.members ?? []).filter((m) => m.kind === "human"), [s.members]);
  const roles = useMemo(() => Object.keys(s.board?.roles ?? {}).sort(), [s.board]);
  // Every name and role a message can mention, so the timeline marks only real mentions.

  // Every loaded message: the timeline, the filter's matches and threads read whole.
  const known = s.known;
  const byId = useMemo(() => new Map(known.map((m) => [m.id, m])), [known]);
  const threads = useMemo(() => threadsOf(known), [known]);
  // Replies by the message they answer, oldest first.
  const repliesTo = useMemo(() => {
    const out = new Map<string, Message[]>();
    for (const m of known) {
      if (m.reply_to) out.set(m.reply_to, [...(out.get(m.reply_to) ?? []), m]);
    }
    return out;
  }, [known]);

  // Questions waiting on the person: addressed to them, asking for a reply, and not yet
  // answered (by them when asked by name; by anyone when asked of everyone). Questions
  // inside threads count the same.
  const waiting = useMemo(() => {
    const ids = new Set<string>();
    for (const m of known) {
      if (!m.expects_reply || !s.toMe.has(m.id)) continue;
      const replies = repliesTo.get(m.id) ?? [];
      const answered = m.to.includes("all") ? replies.length > 0 : replies.some((r) => r.sender === "self");
      if (!answered) ids.add(m.id);
    }
    return ids;
  }, [known, s.toMe, repliesTo]);

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

  // A reply counts as new until the person has seen it: after the newest reply they saw
  // in its thread, else after the last message they saw on the board, else after what
  // was loaded when the page opened.
  const opened = useRef<number | null>(null);
  if (opened.current === null && s.messages !== null) opened.current = known.at(-1)?.seq ?? 0;
  const thread = useCallback(
    (root: Message, only: Message[] | null): Thread | undefined => {
      const all = threads.get(root.id) ?? [];
      const last = all.at(-1);
      if (!last) return undefined;
      const repliers: MemberRef[] = [];
      for (const r of [...all].reverse()) {
        if (!repliers.some((x) => x.kind === r.from.kind && x.name === r.from.name)) repliers.push(r.from);
      }
      const since = prefs.seen(root.id) ?? (lastSeen > 0 ? lastSeen : (opened.current ?? 0));
      const waits = all.find((r) => waiting.has(r.id)) ?? null;
      const fresh = all.filter((r) => r.seq > since && r.sender !== "self");
      // A reply to the person they haven't read opens its thread, as a question does: a
      // collapsed reply is never acknowledged, so it would stay pending and unread.
      const from = s.readFrom;
      const forMe = from !== null && all.some((r) => r.seq > from && r.sender !== "self" && s.toMe.has(r.id) && !r.to.includes("all"));
      return {
        root: root.id,
        replies: only ?? all,
        total: all.length,
        lastAt: last.at,
        repliers,
        open: prefs.open(root.id) ?? (waits !== null || forMe),
        filtered: only !== null,
        fresh: fresh.length,
        firstFresh: fresh[0]?.id ?? null,
        waiting: waits,
      };
    },
    [threads, prefs, lastSeen, waiting, s.readFrom, s.toMe],
  );

  const entries = useMemo<Entry[]>(() => {
    const out: Entry[] = [];
    const roots = new Map<string, Message[] | null>();
    // A reply whose thread can't be read stands alone, with the line it quotes.
    const add = (m: Message) => {
      if (m.thread_root === null) {
        if (!roots.has(m.id)) roots.set(m.id, null);
      } else if (s.rootless.has(m.thread_root)) {
        out.push({ kind: "message", seq: m.seq, m });
      } else if (filterActive(filter)) {
        roots.set(m.thread_root, [...(roots.get(m.thread_root) ?? []), m]);
      } else if (!roots.has(m.thread_root)) {
        roots.set(m.thread_root, null);
      }
    };
    for (const m of s.shown ?? []) add(m);
    for (const [id, only] of roots) {
      const root = byId.get(id);
      // A thread whose first message is still loading shows once it arrives.
      if (root) out.push({ kind: "message", seq: root.seq, m: root, thread: thread(root, only) });
    }
    const msgs = s.shown ?? [];
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
  }, [s.shown, s.rootless, s.events, s.board, s.hasEarlier, byId, thread, showEvents, people.length, filter]);

  // An open thread's replies have been seen.
  useEffect(() => {
    for (const e of entries) {
      const last = e.kind === "message" && e.thread?.open ? e.thread.replies.at(-1) : undefined;
      if (last && e.kind === "message" && !e.thread!.filtered) prefs.see(e.m.id, last.seq);
    }
  }, [entries, prefs]);

  const dividerSeq = useMemo(() => {
    if (lastSeen <= 0) return null;
    const next = entries.find((e) => e.kind === "message" && e.seq > lastSeen && e.m.sender !== "self");
    return next?.seq ?? null;
  }, [entries, lastSeen]);

  // A cumulative cursor may pass only a contiguous stretch of presented messages.
  // An unloaded first unread message or a collapsed reply holds it back.
  const { ack } = s;
  const presented = useRef(new Set<number>());
  const onSeen = useCallback(
    (seq: number) => {
      if (filterActive(filter) || document.visibilityState !== "visible" || s.readFrom === null || s.firstUnread === null) return;
      presented.current.add(seq);
      const from = s.readFrom;
      const unread = (s.messages ?? []).filter((m) => m.seq > from);
      if (s.firstUnread > 0 && unread[0]?.seq !== s.firstUnread) return;
      let through = s.readFrom;
      for (const m of unread) {
        if (!presented.current.has(m.seq)) break;
        through = m.seq;
      }
      if (through > s.readFrom) ack(through);
    },
    [filter, ack, s.messages, s.readFrom, s.firstUnread],
  );
  const receiptsAt = useMemo(() => ({ board: name, activity: s.activity }), [name, s.activity]);

  const quote = useCallback(
    (m: Message) => {
      if (!m.reply_to) return null;
      const q = byId.get(m.reply_to);
      return q ? `${q.sender === "self" ? "You" : q.from.name}: ${q.body}` : null;
    },
    [byId],
  );

  // Showing a message opens its thread first, then scrolls to it once it is on the page.
  const [pending, setPending] = useState<string | null>(null);
  const onShow = useCallback(
    (id: string) => {
      const m = byId.get(id);
      if (m?.thread_root) prefs.setOpen(m.thread_root, true);
      setPending(id);
    },
    [byId, prefs],
  );
  useEffect(() => {
    if (pending && showMessage(pending)) setPending(null);
  }, [pending, entries]);

  const onToggle = useCallback((root: string, open: boolean) => prefs.setOpen(root, open), [prefs]);

  // A reaction shows at once from the answer; everyone else's view follows the stream.
  const { replace } = s;
  const onReact = useCallback(
    (m: Message, name: ReactionName, add: boolean) => {
      react(m.id, name, add).then(
        (updated) => {
          setPostError(null);
          replace(updated);
        },
        (e: unknown) => setPostError(e),
      );
    },
    [replace],
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

  // Who a reply goes to unless the person changes it: the asker and the thread's people.
  const replyDefault = useMemo(() => {
    if (!replyTo) return [];
    const root = replyTo.thread_root ?? replyTo.id;
    const first = byId.get(root);
    return replyRecipients(replyTo, [...(first ? [first] : []), ...(threads.get(root) ?? [])], s.members ?? [], me);
  }, [replyTo, byId, threads, s.members, me]);

  // A mention in a message opens the board panel at its members and marks the one named.
  const onMention = useCallback(
    (target: string) => {
      show("board-agents");
      if (!target.startsWith("@")) return;
      const who = CSS.escape(target.slice(1));
      requestAnimationFrame(() => {
        const el = document.querySelector<HTMLElement>(`[data-agent="${who}"], [data-person="${who}"]`);
        if (!el) return;
        el.scrollIntoView({ behavior: "smooth", block: "nearest" });
        el.classList.remove("flash");
        void el.offsetWidth;
        el.classList.add("flash");
      });
    },
    [show],
  );

  const pick = useCallback(
    (member: string) =>
      setFilter((f) => ({
        ...f,
        from: f.from === member ? undefined : member,
      })),
    [],
  );

  // New replies in closed threads, which the timeline doesn't show until opened.
  const unread = entries.flatMap((e) => (e.kind === "message" && e.thread && !e.thread.open && e.thread.fresh > 0 ? [e.thread] : []));
  const now = nowLine(
    {
      agents,
      questions: known.filter((m) => waiting.has(m.id)),
      newReplies: unread.length > 0 ? { count: unread.reduce((n, t) => n + t.fresh, 0), threads: unread.length, target: unread[0].firstFresh ?? unread[0].root } : null,
      lastActivity: known.at(-1)?.at ?? null,
    },
    Date.now(),
  );

  if (s.gone) {
    return (
      <div className="flex min-h-dvh flex-col lg:min-h-0 lg:flex-1">
        <Header account={<Account onSignOut={onSignOut} />} />
        <main className="mx-auto w-full max-w-[640px] px-4 py-8">
          <section aria-label="Board unavailable" className="board-gone flex flex-col gap-2">
            <h1 className="text-title font-bold">This board is no longer available.</h1>
            <p>
              It was deleted, or you are no longer on it. Pick another from <a href="/">your boards</a>.
            </p>
          </section>
        </main>
      </div>
    );
  }

  const notLoggedIn = error instanceof ApiError && error.status === 401;
  if (notLoggedIn || (error && !s.board)) {
    return (
      <div className="flex min-h-dvh flex-col lg:min-h-0 lg:flex-1">
        <Header />
        <main className="mx-auto w-full max-w-[640px] px-4 py-8">
          <Problem error={error} />
        </main>
      </div>
    );
  }

  const loading = s.board === null || s.shown === null;
  // An archived board takes nothing new: no message box, no replies, no reactions.
  const readOnly = isArchived(s.board);
  const columns = `${left.collapsed ? stripWidth : left.width}px minmax(0,1fr) ${right.collapsed ? stripWidth : right.width}px`;
  // The conversation, which only the UI lab ever wraps (lab-seam.ts).
  const centre = (conversation: ReactNode) =>
    lab?.Centre ? (
      <lab.Centre board={name} members={s.members ?? []} identity={identity} onShow={onShow}>
        {conversation}
      </lab.Centre>
    ) : (
      conversation
    );

  return (
    <TooltipProvider delayDuration={250}>
      <div className="flex min-h-dvh flex-col lg:min-h-0 lg:flex-1">
        <Header
          board={name}
          title={s.board?.title}
          starter={s.board?.policy.preset === "starter"}
          visibility={s.board?.visibility}
          shared={people.length > 1}
          onTitle={s.board ? () => show("board-details") : undefined}
          onStarter={() => show("rules")}
          account={<Account admin={people.length > 1 && myAccess === "admin"} onSignOut={onSignOut} />}
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
                <NowLine parts={loading ? null : now} onShow={onShow} />
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
            {centre(
              <>
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
                    onMention={onMention}
                    waiting={waiting}
                    onReply={readOnly ? undefined : setReplyTo}
                    onReact={readOnly ? undefined : onReact}
                    me={me}
                    onToggle={onToggle}
                    onShow={onShow}
                    onSeen={onSeen}
                    receipts={receiptsAt}
                    stick={stick}
                    resetKey={JSON.stringify(filter)}
                    empty={filterActive(filter) ? <NoMatches clear={() => setFilter({})} /> : <Empty agents={agents.length} />}
                  />
                )}
                <div className={column}>
                  {isArchived(s.board) && s.board ? (
                    <ArchivedNotice board={s.board} onChanged={s.refresh} />
                  ) : (
                    <Composer
                      board={name}
                      members={s.members ?? []}
                      roles={roles}
                      me={me}
                      replyTo={replyTo}
                      replyDefault={replyDefault}
                      identity={identity}
                      onCancelReply={() => setReplyTo(null)}
                      onPosted={(m) => {
                        setPostError(null);
                        // A reply opens its thread and is shown there; anything else is newest.
                        if (m.thread_root) {
                          prefs.setOpen(m.thread_root, true);
                          setPending(m.id);
                        } else setStick((n) => n + 1);
                        s.refresh();
                      }}
                      onError={setPostError}
                    />
                  )}
                </div>
              </>,
            )}
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
              meId={s.me?.kind === "human" ? s.me.id : null}
              canInvite={s.me?.kind === "human"}
              from={filter.from}
              onPick={pick}
              reveal={reveal}
              onLifecycle={s.refresh}
            />
          </SidePanel>
        </div>
      </div>
    </TooltipProvider>
  );
}

function NowLine({ parts, onShow }: { parts: NowPart[] | null; onShow: (id: string) => void }) {
  if (parts === null) return <p className="now h-[25px] w-72 animate-pulse rounded-control bg-selected motion-reduce:animate-none" />;
  return (
    <p className="now min-w-0 flex-1 py-2 text-now">
      <strong>Now:</strong>{" "}
      {parts.map((p, i) => {
        const text = p.target ? (
          <button
            type="button"
            className={cn("underline decoration-1 underline-offset-[3px] hover:no-underline", p.attention ? "text-ink" : "now-link text-link")}
            onClick={() => onShow(p.target!)}
          >
            {p.text}
          </button>
        ) : (
          p.text
        );
        return (
          <span key={p.text}>
            {i > 0 && <span className="text-muted"> · </span>}
            {p.attention ? <span className="rounded-[4px] bg-attention px-1 text-ink">{text}</span> : text}
          </span>
        );
      })}
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
