"use client";

// useBoard keeps one board's state live: the board, its members and their presence, the
// timeline, the board events, and the record's verification. It reads only the public
// API and follows the event stream for changes.

import { useCallback, useEffect, useRef, useState } from "react";
import {
  type Board,
  type BoardEvent,
  type EventPage,
  type Me,
  type Member,
  type Message,
  type MessagePage,
  follow,
  get,
} from "./api";
import { type Chain, type Problem, emptyChain, rememberHead, rememberedHead, verify } from "./record";

const PAGE = 50;
const EVENT_PAGE = 200;

export type RecordCheck = { state: "checking" } | { state: "verified"; count: number } | { state: "failed"; problem: Problem };

/** Filter narrows the timeline with the API's own filters, so paging stays correct. */
export type Filter = { from?: string; role?: string; toMe?: boolean };

export function filterActive(f: Filter): boolean {
  return !!(f.from || f.role || f.toMe);
}

function filterQuery(f: Filter) {
  return { from: f.from, role: f.role, to_me: f.toMe };
}

export type BoardState = {
  board: Board | null;
  boards: Board[] | null;
  members: Member[] | null;
  /** me is the person this browser acts as. */
  me: Me | null;
  /** messages is the newest stretch of the timeline, unfiltered, oldest first. */
  messages: Message[] | null;
  /** shown is what the timeline shows: messages, or the filter's matches. */
  shown: Message[] | null;
  /** hasEarlier is true when messages older than the first one shown exist. */
  hasEarlier: boolean;
  events: BoardEvent[];
  record: RecordCheck;
  /** toMe holds the ids of loaded messages addressed to the person. */
  toMe: Set<string>;
  error: unknown;
  loadEarlier: () => void;
  refresh: () => void;
};

type Page = { messages: Message[]; prevBefore: number | null };

export function useBoard(name: string, filter: Filter): BoardState {
  const path = `/v1/boards/${encodeURIComponent(name)}`;
  const [board, setBoard] = useState<Board | null>(null);
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [base, setBase] = useState<Page | null>(null);
  const [filtered, setFiltered] = useState<Page | null>(null);
  const [events, setEvents] = useState<BoardEvent[]>([]);
  const [record, setRecord] = useState<RecordCheck>({ state: "checking" });
  const [toMe, setToMe] = useState<Set<string>>(new Set());
  const [error, setError] = useState<unknown>(null);

  // Reads run one at a time, so a live update never races a first page. newest is the
  // seq of the newest message loaded, for the timeline and for the filter's matches;
  // chain is how far the record is verified.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const newest = useRef(0);
  const newestMatch = useRef(0);
  const matchesLoaded = useRef(false);
  const chain = useRef<Chain>(emptyChain);
  const broken = useRef(false);
  const live = useRef(true);
  // boardsQueued is set while a reload of the list of boards waits in the queue, so a
  // burst of head changes (one per board when the stream opens) reloads it once.
  const boardsQueued = useRef(false);
  const active = filterActive(filter);
  const filterKey = active ? JSON.stringify(filterQuery(filter)) : "";
  const current = useRef<Filter | null>(null);
  current.current = active ? filter : null;

  const run = useCallback((read: () => Promise<void>) => {
    queue.current = queue.current.then(read).catch((e) => {
      if (live.current) setError(e);
    });
  }, []);

  const loadBoards = useCallback(async () => {
    const r = await get<{ boards: Board[] }>("/v1/boards");
    if (!live.current) return;
    setBoards(r.boards);
  }, []);

  // reloadBoards rereads the list of boards, whose message counts change with every head.
  const reloadBoards = useCallback(() => {
    if (boardsQueued.current) return;
    boardsQueued.current = true;
    run(async () => {
      boardsQueued.current = false;
      await loadBoards();
    });
  }, [run, loadBoards]);

  // after reads every page of messages after seq that match query, and adds them.
  const readAfter = useCallback(
    async (from: { current: number }, query: Record<string, string | boolean | undefined>, add: (ms: Message[]) => void) => {
      for (;;) {
        const page = await get<MessagePage>(`${path}/messages`, { ...query, after: from.current, limit: PAGE });
        if (!live.current) return;
        const last = page.messages.at(-1);
        if (last) {
          from.current = last.seq;
          add(page.messages);
        }
        if (page.next_after === null) return;
      }
    },
    [path],
  );

  const catchUp = useCallback(async () => {
    const [b, m, mine] = await Promise.all([
      get<Board>(path),
      get<{ members: Member[] }>(`${path}/members`),
      get<MessagePage>(`${path}/messages`, { to_me: true, newest: true, limit: PAGE }),
    ]);
    if (!live.current) return;
    setBoard(b);
    setMembers(m.members);
    setToMe(new Set(mine.messages.map((x) => x.id)));

    const append = (set: typeof setBase) => (ms: Message[]) =>
      set((p) => ({ prevBefore: p?.prevBefore ?? null, messages: [...(p?.messages ?? []), ...ms.filter((x) => !p?.messages.some((s) => s.id === x.id))] }));
    await readAfter(newest, {}, append(setBase));
    const f = current.current;
    if (f && matchesLoaded.current) await readAfter(newestMatch, filterQuery(f), append(setFiltered));

    // Events after the last verified one: check them, then keep the ones the timeline shows.
    if (broken.current) return;
    const remembered = rememberedHead(b.id);
    for (;;) {
      const page = await get<EventPage>(`${path}/events`, { after: chain.current.lastSeq, limit: EVENT_PAGE });
      if (!live.current) return;
      const moved = page.events.find((e) => remembered && e.seq === remembered.seq && e.hash !== remembered.hash);
      const result = await verify(chain.current, page.events);
      if (!live.current) return;
      chain.current = result.chain;
      const problem = result.problem ?? (moved ? { seq: moved.seq, reason: "head_changed" as const } : null);
      if (problem) {
        broken.current = true;
        setRecord({ state: "failed", problem });
        return;
      }
      const shown = page.events.filter((e) => e.type !== "message.posted" && !e.type.startsWith("joincode."));
      if (shown.length > 0) setEvents((es) => [...es, ...shown]);
      if (page.next_after === null || page.events.length === 0) break;
    }
    if (!remembered || chain.current.lastSeq >= remembered.seq) rememberHead(b.id, chain.current.lastSeq, chain.current.lastHash);
    setRecord({ state: "verified", count: chain.current.checked });
  }, [path, readAfter]);

  useEffect(() => {
    live.current = true;
    run(async () => {
      const [page, who] = await Promise.all([get<MessagePage>(`${path}/messages`, { newest: true, limit: PAGE }), get<Me>("/v1/me")]);
      if (!live.current) return;
      setMe(who);
      setBase({ messages: page.messages, prevBefore: page.prev_before });
      newest.current = page.messages.at(-1)?.seq ?? 0;
    });
    reloadBoards();
    run(catchUp);
    const stop = follow({
      head: (b) => {
        if (b === name) run(catchUp);
        reloadBoards();
      },
      presence: (p) => {
        if (p.board !== name) return;
        setMembers((ms) =>
          ms?.map((m) => (m.name === p.agent ? { ...m, presence: p.presence, presence_since: p.presence_since } : m)) ?? ms,
        );
      },
      error: (e) => {
        if (live.current) setError(e);
      },
    });
    return () => {
      live.current = false;
      stop();
    };
  }, [path, name, run, catchUp, reloadBoards]);

  // A new filter starts from its newest matches; no filter shows the timeline itself.
  useEffect(() => {
    setFiltered(null);
    newestMatch.current = 0;
    matchesLoaded.current = false;
    if (!filterKey) return;
    const query = JSON.parse(filterKey) as Record<string, string | boolean | undefined>;
    run(async () => {
      if (JSON.stringify(current.current && filterQuery(current.current)) !== filterKey) return;
      const page = await get<MessagePage>(`${path}/messages`, { ...query, newest: true, limit: PAGE });
      if (!live.current) return;
      setFiltered({ messages: page.messages, prevBefore: page.prev_before });
      newestMatch.current = page.messages.at(-1)?.seq ?? 0;
      matchesLoaded.current = true;
    });
  }, [filterKey, path, run]);

  const view = active ? filtered : base;
  const prevBefore = view?.prevBefore ?? null;

  const loadEarlier = useCallback(() => {
    if (prevBefore === null) return;
    const query = filterKey ? (JSON.parse(filterKey) as Record<string, string | boolean | undefined>) : {};
    const set = filterKey ? setFiltered : setBase;
    run(async () => {
      const page = await get<MessagePage>(`${path}/messages`, { ...query, newest: true, before: prevBefore, limit: PAGE });
      if (!live.current) return;
      set((p) => ({ messages: [...page.messages, ...(p?.messages ?? [])], prevBefore: page.prev_before }));
    });
  }, [path, prevBefore, filterKey, run]);

  const refresh = useCallback(() => run(catchUp), [run, catchUp]);

  return {
    board,
    boards,
    members,
    me,
    messages: base?.messages ?? null,
    shown: view?.messages ?? null,
    hasEarlier: prevBefore !== null,
    events,
    record,
    toMe,
    error,
    loadEarlier,
    refresh,
  };
}
