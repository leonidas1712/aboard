"use client";

import { useEffect, useState } from "react";
import { type Started, signedOutEvent, start } from "./api";
import BoardList from "./board-list";
import BoardView from "./board-view";
import { Header, Problem } from "./chrome";
import Login from "./login";

// The UI is one static page: /?board=NAME shows a board, / the list of boards. aboard
// open lands on /#code=…[&board=NAME]; the page signs in with the code first. A browser
// without a session sees the login page, and goes back to it when its session ends.
export default function Page() {
  const [started, setStarted] = useState<Started | undefined>(undefined);
  const [signedOut, setSignedOut] = useState(false);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    start().then(setStarted, setError);
    const ended = () => setStarted((s) => (s ? { ...s, session: null } : s));
    window.addEventListener(signedOutEvent, ended);
    return () => window.removeEventListener(signedOutEvent, ended);
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
  if (started === undefined) return null;
  if (started.session === null) {
    return (
      <Login
        signedOut={signedOut}
        onSignedIn={(session) => {
          setSignedOut(false);
          setStarted({ ...started, session });
        }}
      />
    );
  }
  const onSignOut = () => {
    setSignedOut(true);
    setStarted({ ...started, session: null });
  };
  return started.board ? (
    <BoardView name={started.board} onSignOut={onSignOut} />
  ) : (
    <BoardList onSignOut={onSignOut} />
  );
}
