"use client";

import { useEffect, useState } from "react";
import { type Board, get } from "./api";
import Problem, { StarterBadge } from "./problem";

// BoardList shows every board the person is on.
export default function BoardList() {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards").then((r) => setBoards(r.boards), setError);
  }, []);

  return (
    <>
      <h1>Boards</h1>
      {error !== null && <Problem error={error} />}
      {boards?.length === 0 && (
        <p className="muted">
          You aren&apos;t on any board yet. Run <code>aboard pair</code> in a terminal to make one.
        </p>
      )}
      <ul className="boards">
        {boards?.map((b) => (
          <li key={b.name}>
            <a href={`/?board=${encodeURIComponent(b.name)}`}>{b.name}</a>
            {b.policy.preset === "starter" && <StarterBadge />}
            {b.charter && <p className="muted">{b.charter}</p>}
          </li>
        ))}
      </ul>
    </>
  );
}
