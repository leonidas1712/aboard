"use client";

import { useEffect, useState } from "react";
import { type Session, type Started, signedOutEvent, start } from "./api";
import People from "./people";
import BoardList from "./board-list";
import BoardView from "./board-view";
import { Header, Problem } from "./chrome";
import Login, { ConfirmSignIn, SignedInNotice } from "./login";

// The UI is one static page: /?board=NAME shows a board, / the list of boards. aboard
// open lands on /#code=…[&board=NAME]; unless the browser is already signed in as the
// code's person, the page asks before it signs in with the code. A browser without a
// session sees the login page, and goes back to it when its session ends.
export default function Page() {
  const [started, setStarted] = useState<Started | undefined>(undefined);
  const [signedOut, setSignedOut] = useState(false);
  const [notice, setNotice] = useState<Session | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    start().then((s) => {
      setStarted(s);
      if (s.note && s.session) setNotice(s.session);
    }, setError);
    const ended = () => setStarted((s) => (s ? { ...s, session: null, pending: undefined } : s));
    // A login link opened in a tab already on this page only changes the fragment; load
    // the page again so the link goes through start, and its confirmation, like any other.
    const linked = () => {
      if (new URLSearchParams(window.location.hash.slice(1)).has("code")) window.location.reload();
    };
    window.addEventListener(signedOutEvent, ended);
    window.addEventListener("hashchange", linked);
    return () => {
      window.removeEventListener(signedOutEvent, ended);
      window.removeEventListener("hashchange", linked);
    };
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
  const signedIn = (session: Session, board = started.board) => {
    setSignedOut(false);
    setNotice(session);
    setStarted({ board, session });
  };
  if (started.pending) {
    return (
      <ConfirmSignIn
        pending={started.pending}
        session={started.session}
        onSignedIn={(s) => signedIn(s)}
        onCancel={() => {
          // The link chose the board too; cancelling leaves both behind.
          history.replaceState(null, "", "/");
          setStarted({ board: null, session: started.session });
        }}
      />
    );
  }
  if (started.session === null) {
    return <Login signedOut={signedOut} note={started.note} onSignedIn={(s) => signedIn(s)} />;
  }
  const onSignOut = () => {
    setSignedOut(true);
    setNotice(null);
    setStarted({ ...started, session: null });
  };
  const peoplePage = typeof window !== "undefined" && new URLSearchParams(window.location.search).get("view") === "people";
  const view = peoplePage ? <People onSignOut={onSignOut} /> : started.board ? (
    <BoardView name={started.board} onSignOut={onSignOut} />
  ) : (
    <BoardList onSignOut={onSignOut} />
  );
  return (
    <div className={started.board ? "flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip" : undefined}>
      {notice && <SignedInNotice session={notice} note={started.note} onClose={() => setNotice(null)} />}
      {view}
    </div>
  );
}
