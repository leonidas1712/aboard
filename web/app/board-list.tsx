"use client";

// BoardList shows every board the person is on, live: a board made in a terminal
// appears here without a reload.

import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { type Board, follow, get } from "./api";
import { Header, Problem } from "./chrome";

export default function BoardList() {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    let live = true;
    const load = () =>
      get<{ boards: Board[] }>("/v1/boards").then(
        (r) => live && setBoards(r.boards),
        (e) => live && setError(e),
      );
    load();
    const known = new Map<string, number>();
    const stop = follow({
      head: (b, seq) => {
        if (known.get(b) !== seq) {
          known.set(b, seq);
          load();
        }
      },
      error: (e) => live && setError(e),
    });
    return () => {
      live = false;
      stop();
    };
  }, []);

  return (
    <div className="flex min-h-dvh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-[640px] px-4 py-8 sm:px-6">
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
          <ul className="boards flex flex-col border-t border-rule">
            {boards.map((b) => (
              <li key={b.id} className="border-b border-rule">
                <a
                  href={`/?board=${encodeURIComponent(b.name)}`}
                  className="group flex min-h-11 items-center gap-3 rounded-control px-2 py-3 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-selected"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block font-bold break-all">{b.name}</span>
                    {b.charter && <span className="line-clamp-2 block text-muted">{b.charter}</span>}
                    {b.policy.preset === "starter" && <span className="block text-meta text-muted">Starter policy</span>}
                  </span>
                  <ChevronRight className="size-4 shrink-0 text-muted" strokeWidth={1.5} aria-hidden />
                </a>
              </li>
            ))}
          </ul>
        )}
      </main>
    </div>
  );
}
