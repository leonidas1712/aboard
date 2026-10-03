"use client";

// useBoard keeps one board's state live: the board, its members and their presence, the
// timeline, the board events, and the record's verification. It reads only the public
// API and follows the event stream for changes.

import { useCallback, useEffect, useRef, useState } from "react";
import {
  type Board,
  type BoardEvent,
  type EventPage,
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

export type BoardState = {
  board: Board | null;
  boards: Board[] | null;
  members: Member[] | null;
  messages: Message[] | null;
  /** hasEarlier is true when messages older than the first one shown exist. */
  hasEarlier: boolean;
  events: BoardEvent[];
  record: RecordCheck;
  /** toMe holds the ids of shown messages addressed to the person. */
  toMe: Set<string>;
  error: unknown;
  loadEarlier: () => void;
  refresh: () => void;
};

export function useBoard(name: string): BoardState {
  const path = `/v1/boards/${encodeURIComponent(name)}`;
  const [board, setBoard] = useState<Board | null>(null);
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [messages, setMessages] = useState<Message[] | null>(null);
  const [prevBefore, setPrevBefore] = useState<number | null>(null);
  const [events, setEvents] = useState<BoardEvent[]>([]);
  const [record, setRecord] = useState<RecordCheck>({ state: "checking" });
  const [toMe, setToMe] = useState<Set<string>>(new Set());
  const [error, setError] = useState<unknown>(null);

  // Reads run one at a time, so a live update never races the first page. newest is the
  // seq of the newest message shown; chain is how far the record is verified.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const newest = useRef(0);
  const chain = useRef<Chain>(emptyChain);
  const broken = useRef(false);
  const live = useRef(true);
  const known = useRef<Set<string>>(new Set());

  const run = useCallback((read: () => Promise<void>) => {
    queue.current = queue.current.then(read).catch((e) => {
      if (live.current) setError(e);
    });
  }, []);

  const loadBoards = useCallback(async () => {
    const r = await get<{ boards: Board[] }>("/v1/boards");
    if (!live.current) return;
    known.current = new Set(r.boards.map((b) => b.name));
    setBoards(r.boards);
  }, []);

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

    // Messages after the newest one shown.
    for (;;) {
      const page = await get<MessagePage>(`${path}/messages`, { after: newest.current, limit: PAGE });
      if (!live.current) return;
      const last = page.messages.at(-1);
      if (last) {
        newest.current = last.seq;
        setMessages((shown) => [...(shown ?? []), ...page.messages.filter((x) => !shown?.some((s) => s.id === x.id))]);
      }
      if (page.next_after === null) break;
    }

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
  }, [path]);

  useEffect(() => {
    live.current = true;
    run(async () => {
      const page = await get<MessagePage>(`${path}/messages`, { newest: true, limit: PAGE });
      if (!live.current) return;
      setMessages(page.messages);
      setPrevBefore(page.prev_before);
      newest.current = page.messages.at(-1)?.seq ?? 0;
    });
    run(loadBoards);
    run(catchUp);
    const stop = follow({
      head: (b) => {
        if (b === name) run(catchUp);
        else if (!known.current.has(b)) run(loadBoards);
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
  }, [path, name, run, catchUp, loadBoards]);

  const loadEarlier = useCallback(() => {
    if (prevBefore === null) return;
    run(async () => {
      const page = await get<MessagePage>(`${path}/messages`, { newest: true, before: prevBefore, limit: PAGE });
      if (!live.current) return;
      setMessages((shown) => [...page.messages, ...(shown ?? [])]);
      setPrevBefore(page.prev_before);
    });
  }, [path, prevBefore, run]);

  const refresh = useCallback(() => run(catchUp), [run, catchUp]);

  return {
    board,
    boards,
    members,
    messages,
    hasEarlier: prevBefore !== null,
    events,
    record,
    toMe,
    error,
    loadEarlier,
    refresh,
  };
}
