"use client";

// useBoard keeps one board's state live: the board, its members and their presence, the
// timeline, the board events, and the record's verification. It reads only the public
// API and follows the event stream for changes.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  type Board,
  type BoardEvent,
  type EventPage,
  type Me,
  type Member,
  type Message,
  type MessagePage,
  type ReplyPage,
  ackBoard,
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
  /**
   * known is every message loaded, oldest first: the timeline, the filter's matches, and
   * the threads read for replies whose first message is older than what is loaded.
   */
  known: Message[];
  /** rootless holds the thread roots this reader can't read, so their replies stand alone. */
  rootless: Set<string>;
  /** toMe holds the ids of loaded messages addressed to the person. */
  toMe: Set<string>;
  error: unknown;
  loadEarlier: () => void;
  refresh: () => void;
  /** replace puts a newer copy of a loaded message in place, as after reacting to it. */
  replace: (m: Message) => void;
  /** ack marks the board read up to seq for the person: call it only for what they saw. */
  ack: (seq: number) => void;
  /** activity changes when the board moves or one of the person's agents reads it, so receipts can be read again. */
  activity: number;
  /** readFrom is the person's read position when the page opened, read before it could acknowledge anything. */
  readFrom: number | null;
  /** firstUnread is the first message after the opening cursor, including unloaded messages. */
  firstUnread: number | null;
};

/** isReaction is true for the events that add or take back a reaction, shown on their message instead. */
function isReaction(e: BoardEvent): boolean {
  return e.type === "reaction.added" || e.type === "reaction.removed";
}

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
  // Threads read whole because a loaded reply's first message wasn't loaded.
  const [extra, setExtra] = useState<Message[]>([]);
  const [rootless, setRootless] = useState<Set<string>>(new Set());
  const [activity, setActivity] = useState(0);
  const [readFrom, setReadFrom] = useState<number | null>(null);
  const [firstUnread, setFirstUnread] = useState<number | null>(null);
  // acked is the highest position this page asked for, so it asks only to move forward.
  const acked = useRef(0);
  const requested = useRef<Set<string>>(new Set());
  // loadedHead is the board's head before the first page of messages was read: those
  // messages already carry every reaction up to it. knownRef is every loaded message.
  const loadedHead = useRef(Number.MAX_SAFE_INTEGER);
  const knownRef = useRef<Message[]>([]);

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

  // replaceMessage puts a newer copy of a message wherever it is loaded.
  const replaceMessage = useCallback((m: Message) => {
    const swap = (ms: Message[]) => (ms.some((x) => x.id === m.id) ? ms.map((x) => (x.id === m.id ? m : x)) : ms);
    setBase((p) => (p ? { ...p, messages: swap(p.messages) } : p));
    setFiltered((p) => (p ? { ...p, messages: swap(p.messages) } : p));
    setExtra(swap);
  }, []);

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
    const reacted = new Set<string>();
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
      const shown = page.events.filter((e) => e.type !== "message.posted" && !e.type.startsWith("joincode.") && !isReaction(e));
      if (shown.length > 0) setEvents((es) => [...es, ...shown]);
      for (const e of page.events) {
        const id = (e.data as { message_id?: string } | undefined)?.message_id;
        if (isReaction(e) && e.seq > loadedHead.current && id) reacted.add(id);
      }
      if (page.next_after === null || page.events.length === 0) break;
    }
    if (!remembered || chain.current.lastSeq >= remembered.seq) rememberHead(b.id, chain.current.lastSeq, chain.current.lastHash);
    setRecord({ state: "verified", count: chain.current.checked });

    // A loaded message someone reacted to since is read again, so its reactions are current.
    for (const id of reacted) {
      const m = knownRef.current.find((x) => x.id === id);
      if (!m) continue;
      const page = await get<MessagePage>(`${path}/messages`, { after: m.seq - 1, limit: 1 });
      if (!live.current) return;
      if (page.messages[0]?.id === id) replaceMessage(page.messages[0]);
    }
  }, [path, readAfter, replaceMessage]);

  useEffect(() => {
    live.current = true;
    run(async () => {
      // The head first: every reaction up to it is already on the messages read after it.
      const first = await get<Board>(path);
      const head = first.head_seq;
      const [page, who] = await Promise.all([
        get<MessagePage>(`${path}/messages`, { newest: true, limit: PAGE }),
        get<Me>("/v1/me"),
      ]);
      // Check coverage after the newest page: a concurrent post must not leave an
      // empty first-unread result beside a page containing new unread messages.
      const unread = await get<MessagePage>(`${path}/messages`, { after: first.read_up_to ?? 0, limit: 1 });
      loadedHead.current = head;
      if (!live.current) return;
      setReadFrom(first.read_up_to ?? 0);
      setFirstUnread(unread.messages[0]?.seq ?? 0);
      setMe(who);
      setBase({ messages: page.messages, prevBefore: page.prev_before });
      newest.current = page.messages.at(-1)?.seq ?? 0;
    });
    reloadBoards();
    run(catchUp);
    const stop = follow({
      head: (b) => {
        if (b === name) {
          run(catchUp);
          setActivity((n) => n + 1);
        }
        reloadBoards();
      },
      unread: (u) => {
        const set = (b: Board) => (b.name === u.board ? { ...b, read_up_to: u.read_up_to, unread: u.unread } : b);
        setBoards((bs) => bs?.map(set) ?? bs);
        setBoard((b) => (b ? set(b) : b));
        // The person's own position is a receipt of messages to them.
        if (u.board === name) setActivity((n) => n + 1);
      },
      read: (r) => {
        if (r.board === name) setActivity((n) => n + 1);
      },
      presence: (p) => {
        if (p.board !== name) return;
        setMembers((ms) =>
          ms?.map((m) =>
            m.name === p.agent
              ? { ...m, presence: p.presence, presence_since: p.presence_since, ...(p.delivery !== undefined && { delivery: p.delivery }) }
              : m,
          ) ?? ms,
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

  const ack = useCallback(
    (seq: number) => {
      if (seq <= acked.current || (board?.read_up_to ?? 0) >= seq) return;
      acked.current = seq;
      ackBoard(name, seq).catch((e) => {
        // A lost login is reported as for any other request; anything else is tried
        // again the next time something is seen.
        if (e instanceof ApiError && e.status === 401) setError(e);
        acked.current = 0;
      });
    },
    [name, board?.read_up_to],
  );

  const known = useMemo(() => {
    const out = new Map<string, Message>();
    for (const m of [...extra, ...(base?.messages ?? []), ...(filtered?.messages ?? [])]) out.set(m.id, m);
    return [...out.values()].sort((a, b) => a.seq - b.seq);
  }, [extra, base, filtered]);
  knownRef.current = known;

  // A reply whose thread starts before what is loaded brings its whole thread in, so the
  // reply shows under its first message.
  useEffect(() => {
    const ids = new Set(known.map((m) => m.id));
    const missing = [...new Set(known.map((m) => m.thread_root).filter((r): r is string => !!r && !ids.has(r)))].filter(
      (r) => !requested.current.has(r),
    );
    for (const root of missing) {
      requested.current.add(root);
      run(async () => {
        const out: Message[] = [];
        let after = 0;
        let page: ReplyPage | null = null;
        try {
          do {
            page = await get<ReplyPage>(`/v1/messages/${encodeURIComponent(root)}/replies`, { after, limit: 200 });
            out.push(...page.replies);
            after = page.next_after ?? 0;
          } while (page.next_after !== null);
        } catch (e) {
          // A thread that can't be read leaves its replies standing alone; a lost login
          // is reported as for any other read.
          if (e instanceof ApiError && e.status === 401) throw e;
          page = null;
        }
        if (!live.current) return;
        if (page?.root) out.unshift(page.root);
        else setRootless((s) => new Set([...s, root]));
        setExtra((x) => [...x, ...out]);
      });
    }
  }, [known, run]);

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
    known,
    rootless,
    toMe,
    error,
    loadEarlier,
    refresh,
    replace: replaceMessage,
    ack,
    activity,
    readFrom,
    firstUnread,
  };
}
