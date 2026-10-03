"use client";

// BoardList shows every board the person is on, live, with a few facts to tell them
// apart: who is on each, how much has been said and when, and its policy. A board made
// in a terminal appears here without a reload.

import { ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { type Board, type Member, type MessagePage, follow, get } from "./api";
import { Header, Problem } from "./chrome";
import { boardLabel, count, exactTime, policyName, relativeTime } from "./words";

/** Facts are what the list says about one board, read from the public API. */
type Facts = { agents: number; working: number; people: number; messages: number; last: string | null };

const PAGE = 200;

async function factsOf(b: Board): Promise<Facts> {
  const path = `/v1/boards/${encodeURIComponent(b.name)}`;
  const { members } = await get<{ members: Member[] }>(`${path}/members`);
  // Messages are counted by reading them, a page at a time; a board's list is short.
  let messages = 0;
  let last: string | null = null;
  for (let after = 0; ; ) {
    const page = await get<MessagePage>(`${path}/messages`, { after, limit: PAGE });
    messages += page.messages.length;
    last = page.messages.at(-1)?.at ?? last;
    if (page.next_after === null) break;
    after = page.next_after;
  }
  const agents = members.filter((m) => m.kind === "agent");
  return {
    agents: agents.length,
    working: agents.filter((a) => a.presence === "working").length,
    people: members.filter((m) => m.kind === "human").length,
    messages,
    last,
  };
}

export default function BoardList() {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [facts, setFacts] = useState<Record<string, Facts>>({});
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(() => Date.now());
  const listed = useRef<Board[]>([]);

  useEffect(() => {
    let live = true;
    const loadFacts = (b: Board) =>
      factsOf(b).then(
        (f) => live && setFacts((all) => ({ ...all, [b.name]: f })),
        () => {}, // the row shows without facts
      );
    const load = () =>
      get<{ boards: Board[] }>("/v1/boards").then(
        (r) => {
          if (!live) return;
          listed.current = r.boards;
          setBoards(r.boards);
          setNow(Date.now());
          for (const b of r.boards) void loadFacts(b);
        },
        (e) => live && setError(e),
      );
    void load();
    const known = new Map<string, number>();
    const stop = follow({
      head: (b, seq) => {
        if (known.get(b) !== seq) {
          known.set(b, seq);
          void load();
        }
      },
      presence: (p) => {
        const b = listed.current.find((x) => x.name === p.board);
        if (b) void loadFacts(b);
      },
      error: (e) => live && setError(e),
    });
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => {
      live = false;
      stop();
      clearInterval(tick);
    };
  }, []);

  const showPeople = Object.values(facts).some((f) => f.people > 1);

  return (
    <div className="flex min-h-dvh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-[960px] px-4 py-8 sm:px-6">
        <h1 className="mb-4 text-headline font-bold">Your boards</h1>
        {error !== null && <Problem error={error} />}
        {boards === null && error === null && (
          <div className="h-20 rounded-box bg-selected motion-safe:animate-pulse" role="status" aria-label="Loading your boards" />
        )}
        {boards?.length === 0 && (
          <p>
            You aren&apos;t on any board yet. Run <code>aboard pair</code> in a terminal to make one.
          </p>
        )}
        {boards && boards.length > 0 && (
          <table className="boards w-full border-collapse text-left max-md:block">
            <thead className="max-md:sr-only">
              <tr className="border-b border-rule text-meta text-muted">
                <th scope="col" className="py-2 pr-4 pl-2 font-bold">
                  Board
                </th>
                <th scope="col" className="py-2 pr-4 font-bold">
                  Agents
                </th>
                {showPeople && (
                  <th scope="col" className="py-2 pr-4 font-bold">
                    People
                  </th>
                )}
                <th scope="col" className="py-2 pr-4 text-right font-bold">
                  Messages
                </th>
                <th scope="col" className="py-2 pr-4 font-bold">
                  Last message
                </th>
                <th scope="col" className="py-2 pr-2 font-bold">
                  Policy
                </th>
              </tr>
            </thead>
            <tbody className="max-md:block">
              {boards.map((b) => (
                <BoardRow key={b.id} board={b} facts={facts[b.name]} showPeople={showPeople} now={now} />
              ))}
            </tbody>
          </table>
        )}
      </main>
    </div>
  );
}

function BoardRow({ board: b, facts: f, showPeople, now }: { board: Board; facts?: Facts; showPeople: boolean; now: number }) {
  const href = `/?board=${encodeURIComponent(b.name)}`;
  const cell = "py-3 pr-4 align-top max-md:inline max-md:p-0";
  const sep = <span className="text-muted md:hidden"> · </span>;
  return (
    <tr className="board-row group relative border-b border-rule transition-colors duration-[140ms] ease-out hover:bg-selected max-md:block max-md:py-3">
      <td className="py-3 pr-4 pl-2 align-top max-md:block max-md:p-0 max-md:pr-8">
        {/* The whole row is the link's target; the link itself is the board's title. */}
        <a href={href} className="font-bold text-ink no-underline after:absolute after:inset-0 after:content-['']">
          {boardLabel(b)}
        </a>
        {b.title && <span className="ml-2 text-meta break-all text-muted">{b.name}</span>}
        {b.charter && <p className="line-clamp-1 max-w-[42ch] text-meta text-muted">{b.charter}</p>}
        <ChevronRight className="absolute top-4 right-2 size-4 text-muted md:hidden" strokeWidth={1.5} aria-hidden />
      </td>
      {f ? (
        <>
          <td className={cn(cell, "tabular-nums")}>
            {count(f.agents, "agent", "agents")}
            {f.working > 0 && <span className="text-muted">, {f.working} working</span>}
          </td>
          {showPeople && (
            <td className={cn(cell, "tabular-nums")}>
              {sep}
              {count(f.people, "person", "people")}
            </td>
          )}
          <td className={cn(cell, "tabular-nums md:text-right")}>
            {sep}
            <span className="md:hidden">{count(f.messages, "message", "messages")}</span>
            <span className="max-md:hidden">{f.messages}</span>
          </td>
          <td className={cell}>
            {sep}
            {f.last ? (
              <time dateTime={f.last} title={exactTime(f.last)}>
                <span className="md:hidden">last </span>
                {relativeTime(f.last, now)}
              </time>
            ) : (
              <span className="text-muted">none yet</span>
            )}
          </td>
        </>
      ) : (
        <td colSpan={showPeople ? 4 : 3} className="py-3 pr-4 align-top max-md:block max-md:p-0">
          <span className="block h-4 w-48 rounded-[4px] bg-selected motion-safe:animate-pulse" aria-label="Loading" />
        </td>
      )}
      <td className="py-3 pr-2 align-top max-md:block max-md:p-0 max-md:text-meta max-md:text-muted">{policyName(b.policy)}</td>
    </tr>
  );
}
