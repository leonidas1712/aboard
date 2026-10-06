"use client";

// BoardList shows every board the person is on, live, with a few facts to tell them
// apart: who is on each, how much has been said and when, and its policy. A board made
// in a terminal appears here without a reload.

import { ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { type Board, type Member, follow, get, isArchived } from "./api";
import { ArchivedGroup } from "./sidebars";
import { Account } from "./account";
import { Header, Problem, VisibilityLabel } from "./chrome";
import { boardLabel, count, exactTime, policyName, relativeTime } from "./words";

/** Facts are what the list says about one board's members, read from the public API. */
type Facts = { agents: number; working: number; people: number };

async function factsOf(b: Board): Promise<Facts> {
  const { members } = await get<{ members: Member[] }>(`/v1/boards/${encodeURIComponent(b.name)}/members`);
  const agents = members.filter((m) => m.kind === "agent");
  return {
    agents: agents.length,
    working: agents.filter((a) => a.presence === "working").length,
    people: members.filter((m) => m.kind === "human").length,
  };
}

export default function BoardList({ onSignOut }: { onSignOut: () => void }) {
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
      get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then(
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
      unread: (u) => {
        if (!live) return;
        setBoards((bs) => bs?.map((b) => (b.name === u.board ? { ...b, read_up_to: u.read_up_to, unread: u.unread } : b)) ?? bs);
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
  const active = boards?.filter((b) => !isArchived(b)) ?? null;
  const archived = boards?.filter((b) => isArchived(b)) ?? [];

  return (
    <div className="flex min-h-dvh flex-col">
      <Header account={<Account onSignOut={onSignOut} />} />
      <main className="mx-auto w-full max-w-[960px] px-4 py-8 sm:px-6">
        <h1 className="mb-4 text-headline font-bold">Your boards</h1>
        {error !== null && <Problem error={error} />}
        {boards === null && error === null && (
          <div className="h-20 rounded-box bg-selected motion-safe:animate-pulse" role="status" aria-label="Loading your boards" />
        )}
        {active?.length === 0 && archived.length === 0 && (
          <p>
            You aren&apos;t on any board yet. Run <code>aboard pair</code> in a terminal to make one.
          </p>
        )}
        {active && active.length > 0 && (
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
              {active.map((b) => (
                <BoardRow key={b.id} board={b} facts={facts[b.name]} showPeople={showPeople} now={now} />
              ))}
            </tbody>
          </table>
        )}
        {archived.length > 0 && (
          <div className="mt-6">
            <ArchivedGroup count={archived.length} startOpen={false}>
              <table className="boards w-full border-collapse text-left max-md:block">
                <tbody className="max-md:block">
                  {archived.map((b) => (
                    <BoardRow key={b.id} board={b} facts={facts[b.name]} showPeople={showPeople} now={now} />
                  ))}
                </tbody>
              </table>
            </ArchivedGroup>
          </div>
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
        <VisibilityLabel visibility={b.visibility} shared={(f?.people ?? 1) > 1} className="ml-2" />
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
        </>
      ) : (
        <td colSpan={showPeople ? 2 : 1} className="py-3 pr-4 align-top max-md:inline max-md:p-0">
          <span className="inline-block h-4 w-24 rounded-[4px] bg-selected motion-safe:animate-pulse" aria-label="Loading" />
        </td>
      )}
      <td className={cn(cell, "messages tabular-nums md:text-right")}>
        {sep}
        <span className="md:hidden">{count(b.message_count ?? 0, "message", "messages")}</span>
        <span className="max-md:hidden">{b.message_count ?? 0}</span>
        {(b.unread ?? 0) > 0 && <span className="unread ml-2 font-bold text-ink">{b.unread} unread</span>}
      </td>
      <td className={cell}>
        {sep}
        {b.last_message_at ? (
          <time dateTime={b.last_message_at} title={exactTime(b.last_message_at)}>
            <span className="md:hidden">last </span>
            {relativeTime(b.last_message_at, now)}
          </time>
        ) : (
          <span className="text-muted">none yet</span>
        )}
      </td>
      <td className="py-3 pr-2 align-top max-md:block max-md:p-0 max-md:text-meta max-md:text-muted">{policyName(b.policy)}</td>
    </tr>
  );
}
