"use client";

// Signing in. The login page takes a pasted access key: it goes to the server once, in
// the body of one request, and is exchanged for a session kept in a cookie; the page never
// stores it and clears the field as soon as it's sent. A login link from aboard open is
// confirmed first, since anyone can send a link. Neither sends anything secret over plain
// HTTP, except to this computer's own address.

import { type FormEvent, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ApiError, type Pending, type Session, confirmCode, secureEnough, signInWithKey } from "./api";
import { Header } from "./chrome";

type Problem = { what: string; next: string };

function problemOf(err: unknown): Problem {
  if (err instanceof ApiError) return { what: err.message, next: err.hint };
  return { what: "Couldn't reach the Aboard server.", next: "Check your connection, then try again." };
}

function ProblemBox({ problem }: { problem: Problem }) {
  return (
    <div role="alert" className="problem mt-4 rounded-box bg-attention px-4 py-3 text-ink">
      <p className="font-bold">{problem.what}</p>
      {problem.next && <p>{problem.next}</p>}
    </div>
  );
}

/** InsecureNotice says why this page won't sign in: it isn't served over HTTPS. */
export function InsecureNotice() {
  return (
    <div role="alert" className="insecure mt-4 rounded-box bg-attention px-4 py-3 text-ink">
      <p className="font-bold">Signing in with a key needs https.</p>
      <p>
        Use <code>aboard open</code> from a signed-in machine, or reach this server over https.
      </p>
    </div>
  );
}

type Props = {
  /** onSignedIn runs once the browser has a session. */
  onSignedIn: (s: Session) => void;
  /** signedOut is true right after this browser signed out, to say so. */
  signedOut?: boolean;
  /** note says why the page came here, such as a login link that no longer works. */
  note?: string;
};

export default function Login({ onSignedIn, signedOut, note }: Props) {
  const field = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const ids = useId();
  const secure = secureEnough();

  async function submit(e: FormEvent) {
    e.preventDefault();
    // Over plain HTTP the key would cross the network in the clear: refuse before
    // reading it.
    if (!secureEnough()) return;
    const input = field.current;
    const key = input?.value.trim() ?? "";
    if (!input || key === "") return;
    input.value = "";
    setBusy(true);
    setProblem(null);
    try {
      onSignedIn(await signInWithKey(key));
    } catch (err) {
      setProblem(problemOf(err));
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
        {note && (
          <p role="status" className="login-note mt-2">
            {note} Sign in with a key, or run <code>aboard open</code> again.
          </p>
        )}
        {signedOut && (
          <p role="status" className="signed-out mt-2">
            You signed out of this browser. Your keys and your other browsers still work.
          </p>
        )}
        <p className="mt-3 text-muted">
          Paste one of your access keys. This browser exchanges it for a session and doesn't keep the key.
        </p>
        {!secure && <InsecureNotice />}
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
            disabled={!secure}
            placeholder="abh_…"
            className="h-11 w-full rounded-control border border-field-border bg-surface px-3.5 text-body text-ink outline-none placeholder:text-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
          />
          <Button type="submit" disabled={busy || !secure} className="self-start">
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
        {problem && <ProblemBox problem={problem} />}
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

type ConfirmProps = {
  pending: Pending;
  /** session is the session this browser already has, if any. */
  session: Session | null;
  onSignedIn: (s: Session) => void;
  onCancel: () => void;
};

/**
 * ConfirmSignIn asks before a login link signs this browser in. A link can come from
 * anyone, so the person sees whose account it opens and, when the browser is signed in as
 * someone else, that it would switch, before anything changes.
 */
export function ConfirmSignIn({ pending, session, onSignedIn, onCancel }: ConfirmProps) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const who = pending.preview.person.handle;
  const host = typeof window === "undefined" ? "" : window.location.host;

  async function proceed() {
    setBusy(true);
    setProblem(null);
    try {
      onSignedIn(await confirmCode(pending));
    } catch (err) {
      setProblem(problemOf(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-dvh flex-col">
      <Header />
      <main className="mx-auto w-full max-w-[480px] px-4 py-10">
        <h1 className="confirm-sign-in text-title font-bold break-words">
          Sign in to {host} as @{who}?
        </h1>
        <p className="mt-3">
          This link signs this browser in as <strong className="link-person">@{who}</strong> with the key{" "}
          <strong>{pending.preview.key.name}</strong>. Continue only if you opened it yourself, with <code>aboard open</code>.
        </p>
        {session && (
          <div role="alert" className="switch-warning mt-4 rounded-box bg-attention px-4 py-3 text-ink">
            <p className="font-bold">
              You're signed in as <span className="current-person">@{session.person.handle}</span>. This link would sign you in
              as @{who} instead.
            </p>
            <p>Anything you post afterwards would be posted as @{who}.</p>
          </div>
        )}
        <div className="mt-6 flex gap-3">
          <Button type="button" disabled={busy} onClick={proceed}>
            {busy ? "Signing in…" : "Continue"}
          </Button>
          <Button type="button" variant="secondary" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        </div>
        {problem && <ProblemBox problem={problem} />}
      </main>
    </div>
  );
}

/**
 * SignedInNotice says, right after signing in, who this browser is signed in as; with a
 * note, it says that instead, such as a login link that no longer works.
 */
export function SignedInNotice({ session, note, onClose }: { session: Session; note?: string; onClose: () => void }) {
  return (
    <div role="status" className="signed-in-as flex shrink-0 items-center gap-3 border-b border-rule bg-selected px-4 py-1 sm:px-5">
      <p className="min-w-0 flex-1">
        {note && <>{note} </>}
        {note ? "You're still signed in as " : "Signed in as "}
        <strong className="text-title">@{session.person.handle}</strong> with the key {session.key.name}.
      </p>
      <Button type="button" variant="quiet" onClick={onClose}>
        Dismiss
      </Button>
    </div>
  );
}
