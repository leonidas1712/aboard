"use client";

// The login page: a browser without a session signs in by pasting an access key. The key
// goes to the server once, in the body of one request, and is exchanged for a session
// kept in a cookie; the page never stores it, and clears the field as soon as it's sent.

import { type FormEvent, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ApiError, type Session, signInWithKey } from "./api";
import { Header } from "./chrome";

type Props = {
  /** onSignedIn runs once the browser has a session. */
  onSignedIn: (s: Session) => void;
  /** signedOut is true right after this browser signed out, to say so. */
  signedOut?: boolean;
};

export default function Login({ onSignedIn, signedOut }: Props) {
  const field = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<{ what: string; next: string } | null>(null);
  const ids = useId();
  // A browser keeps a Secure cookie only over HTTPS, or on this computer's own address.
  const secure = typeof window === "undefined" || window.isSecureContext;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const input = field.current;
    const key = input?.value.trim() ?? "";
    if (!input || key === "") return;
    input.value = "";
    setBusy(true);
    setProblem(null);
    try {
      onSignedIn(await signInWithKey(key));
    } catch (err) {
      if (err instanceof ApiError) setProblem({ what: err.message, next: err.hint });
      else setProblem({ what: "Couldn't reach the Aboard server.", next: "Check your connection, then try again." });
      input.focus();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-dvh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-[480px] px-4 py-10">
        <h1 className="text-title font-bold">Sign in to Aboard</h1>
        {signedOut && (
          <p role="status" className="signed-out mt-2">
            You signed out of this browser. Your keys and your other browsers still work.
          </p>
        )}
        <p className="mt-3 text-muted">
          Paste one of your access keys. This browser exchanges it for a session and doesn't keep the key.
        </p>
        {!secure && (
          <div role="alert" className="mt-4 rounded-box bg-attention px-4 py-3 text-ink">
            <p className="font-bold">This page isn't served over HTTPS, so the browser won't keep a sign-in.</p>
            <p>Open this server's https:// address instead.</p>
          </div>
        )}
        <form method="post" onSubmit={submit} className="mt-6 flex flex-col gap-3" aria-describedby={`${ids}-help`}>
          <label htmlFor={`${ids}-key`} className="font-bold">
            Access key
          </label>
          <input
            ref={field}
            id={`${ids}-key`}
            name="key"
            type="password"
            autoComplete="current-password"
            spellCheck={false}
            autoCapitalize="off"
            required
            placeholder="abh_…"
            className="h-11 w-full rounded-control border border-field-border bg-surface px-3.5 text-body text-ink outline-none placeholder:text-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
          <Button type="submit" disabled={busy} className="self-start">
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
        {problem && (
          <div role="alert" className="problem mt-4 rounded-box bg-attention px-4 py-3 text-ink">
            <p className="font-bold">{problem.what}</p>
            {problem.next && <p>{problem.next}</p>}
          </div>
        )}
        <div id={`${ids}-help`} className="mt-8 flex flex-col gap-2 text-meta text-muted">
          <p>
            Make a key for this browser in a terminal with <code>aboard keys create &lt;name&gt;</code>, and keep it in your
            password manager.
          </p>
          <p>
            On the computer that runs <code>aboard</code>, <code>aboard open</code> signs this browser in without a key.
          </p>
        </div>
      </main>
    </div>
  );
}
