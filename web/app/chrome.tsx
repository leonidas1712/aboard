// The parts every screen shares: the header with Aboard's mark, and the problem box.

import type { ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ApiError } from "./api";

/**
 * Mark is Aboard's mark, the same drawing as the tab icon (icon.svg): a room, and inside
 * it two lines of conversation, the later one in the accent. It is drawn with the
 * theme's colours, so it follows the theme this browser chose as well as the system's.
 */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden focusable="false">
      <rect x="2" y="2" width="28" height="28" rx="7" strokeWidth="4" className="fill-surface stroke-ink" />
      <rect x="8" y="10" width="10" height="4" rx="2" className="fill-ink" />
      <rect x="14" y="18" width="10" height="4" rx="2" className="fill-accent" />
    </svg>
  );
}

type HeaderProps = {
  board?: string;
  title?: string | null;
  starter?: boolean;
  account?: ReactNode;
  /** onTitle, when given, makes the board's title a button that shows the board's details. */
  onTitle?: () => void;
  /** onStarter, when given, makes "Starter policy" a button that shows the board's rules. */
  onStarter?: () => void;
};

/**
 * Header shows Aboard's mark and name and, on a board, its title with its name beside
 * it, whether it is on the starter policy, and who you are at the right once the
 * browser is logged in.
 */
export function Header({ board, title, starter, account, onTitle, onStarter }: HeaderProps) {
  const label = title?.trim();
  const words = board && (
    <>
      <span className="text-title font-bold break-words">{label || board}</span>
      {label && <span className="text-meta break-all text-muted">{board}</span>}
    </>
  );
  return (
    <header className="border-b border-rule bg-surface">
      <div className="flex min-h-16 flex-wrap items-center gap-x-5 gap-y-1 px-4 py-2.5 sm:px-5">
        <a href="/" className="flex items-center gap-2 text-[17px] font-bold text-ink no-underline">
          <Mark className="size-[22px]" />
          Aboard
        </a>
        {board && (
          <h1 className="min-w-0">
            {onTitle ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={onTitle}
                    className="board-title -mx-2 flex min-h-11 min-w-0 flex-wrap items-baseline gap-x-2.5 rounded-control px-2 py-2 text-left transition-colors duration-[140ms] ease-out hover:bg-selected"
                  >
                    {words}
                    <span className="sr-only">, board details</span>
                  </button>
                </TooltipTrigger>
                <TooltipContent side="bottom" align="start">
                  Board details
                </TooltipContent>
              </Tooltip>
            ) : (
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2.5">{words}</span>
            )}
          </h1>
        )}
        {starter &&
          (onStarter ? (
            <button
              type="button"
              onClick={onStarter}
              className="starter min-h-11 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
              title="Every member reads everything. See the rules for how to tighten them."
            >
              Starter policy
            </button>
          ) : (
            <span className="starter text-meta text-muted">Starter policy</span>
          ))}
        {account}
      </div>
    </header>
  );
}

/** Problem shows a failed request, and what to do about it. */
export function Problem({ error }: { error: unknown }) {
  let what: ReactNode;
  let next: ReactNode;
  if (error instanceof ApiError && error.status === 401) {
    what = "This browser isn't signed in to Aboard, or its session has ended.";
    next = (
      <>
        <a href="/">Sign in</a> with an access key, or run <code>aboard open</code> in a terminal.
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
