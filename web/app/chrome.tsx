// The parts every screen shares: the header, section headings, and the problem box.

import { MessagesSquare } from "lucide-react";
import type { ReactNode } from "react";
import { ApiError } from "./api";

/**
 * Header shows the product name and, on a board, its title with its name beside it, and
 * who you are at the right once the browser is logged in. heading, when given, takes the
 * title's place: the board view passes the title as a button that opens Board details.
 */
export function Header({
  board,
  title,
  heading,
  starter,
  account,
}: { board?: string; title?: string | null; heading?: ReactNode; starter?: boolean; account?: ReactNode }) {
  return (
    <header className="border-b border-rule bg-surface">
      <div className="flex min-h-16 flex-wrap items-center gap-x-5 gap-y-1 px-4 py-2.5 sm:px-5">
      <a href="/" className="flex items-center gap-2 text-[17px] font-bold text-ink no-underline">
        <MessagesSquare className="size-[22px]" strokeWidth={1.5} aria-hidden />
        Aboard
      </a>
      {heading ??
        (board && (
          <h1 className="flex min-w-0 flex-wrap items-baseline gap-x-2.5">
            <span className="text-title font-bold break-words">{title?.trim() || board}</span>
            {title?.trim() && <span className="text-meta break-all text-muted">{board}</span>}
          </h1>
        ))}
      {starter && (
        <a href="#rules" className="text-meta text-link" title="Every member reads everything. See the rules for how to tighten them.">
          Starter policy
        </a>
      )}
      {account}
      </div>
    </header>
  );
}

export function SectionHeading({ id, children }: { id?: string; children: ReactNode }) {
  return (
    <h2 id={id} className="mb-2 text-meta font-bold text-muted">
      {children}
    </h2>
  );
}

/** Problem shows a failed request, and what to do about it. */
export function Problem({ error }: { error: unknown }) {
  let what: ReactNode;
  let next: ReactNode;
  if (error instanceof ApiError && error.status === 401) {
    what = "This browser isn't logged in to Aboard, or its login has ended.";
    next = (
      <>
        Run <code>aboard open</code> again in a terminal to log in.
      </>
    );
  } else if (error instanceof ApiError && error.code === "board_not_found") {
    what = error.message;
    next = (
      <>
        Pick a board from <a href="/">your boards</a>.
      </>
    );
  } else if (error instanceof ApiError) {
    what = error.message;
    next = error.hint;
  } else {
    what = "Couldn't reach the Aboard server.";
    next = (
      <>
        Start it with <code>aboard up</code>, then reload this page.
      </>
    );
  }
  return (
    <div role="alert" className="problem rounded-box bg-attention px-4 py-3 text-ink">
      <p className="font-bold">{what}</p>
      {next && <p>{next}</p>}
    </div>
  );
}
