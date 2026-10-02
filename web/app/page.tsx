"use client";

import { useEffect, useState } from "react";
import BoardList from "./board-list";
import BoardView from "./board-view";

// The UI is one static page: /?board=NAME shows a board, / the list of boards.
export default function Page() {
  const [board, setBoard] = useState<string | null | undefined>(undefined);
  useEffect(() => setBoard(new URLSearchParams(window.location.search).get("board")), []);
  if (board === undefined) return null;
  return <main>{board ? <BoardView name={board} /> : <BoardList />}</main>;
}
