"use client";

import { useEffect, useState } from "react";
import { login } from "./api";
import BoardList from "./board-list";
import BoardView from "./board-view";
import { Header, Problem } from "./chrome";

// The UI is one static page: /?board=NAME shows a board, / the list of boards. aboard
// open lands on /#code=…[&board=NAME]; the page logs in with the code first.
export default function Page() {
  const [board, setBoard] = useState<string | null | undefined>(undefined);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    login().then(
      () => setBoard(new URLSearchParams(window.location.search).get("board")),
      setError,
    );
  }, []);
  if (error !== null) {
    return (
      <div className="flex min-h-dvh flex-col">
        <Header />
        <main className="mx-auto w-full max-w-[640px] px-4 py-8">
          <Problem error={error} />
        </main>
      </div>
    );
  }
  if (board === undefined) return null;
  return board ? <BoardView name={board} /> : <BoardList />;
}
