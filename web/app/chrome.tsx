// The parts every screen shares: the header with Aboard's mark, and the problem box.

import { Lock, Users } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ApiError, type Visibility } from "./api";

/**
 * VisibilityLabel says who can see a board. A private board always says so; an open one
 * only when others can be on it (shared), so a person alone on their server never meets
 * the word.
 */
export function VisibilityLabel({ visibility, shared, className }: { visibility?: Visibility; shared: boolean; className?: string }) {
  if (visibility === "private") {
    return (
      <span className={cn("visibility inline-flex items-center gap-1 text-meta text-muted", className)} data-visibility="private" title="Only the people on this board can see it.">
        <Lock className="size-3.5" strokeWidth={1.75} aria-hidden />
        Private
      </span>
    );
  }
  if (visibility === "open" && shared) {
    return (
      <span className={cn("visibility inline-flex items-center gap-1 text-meta text-muted", className)} data-visibility="open" title="Everyone on this server can see this board and join it.">
        <Users className="size-3.5" strokeWidth={1.75} aria-hidden />
        Open
      </span>
    );
  }
  return null;
}

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
  /** visibility and shared decide the open or private label beside the title. */
  visibility?: Visibility;
  shared?: boolean;
  account?: ReactNode;
  /** onTitle, when given, makes the board's title a button that shows the board's details. */
  onTitle?: () => void;
  /** onStarter, when given, makes "Starter policy" a button that shows the board's rules. */
  onStarter?: () => void;
  /** lead comes before the mark (on a phone, the button that opens the boards). */
  lead?: ReactNode;
  /** tools come before the account (on a phone, the button that opens the board panel). */
  tools?: ReactNode;
};

/**
 * Header shows Aboard's mark and name and, on a board, its title with its name beside
 * it, whether it is on the starter policy, and who you are at the right once the
 * browser is logged in.
 */
export function Header({ board, title, starter, visibility, shared = false, account, onTitle, onStarter, lead, tools }: HeaderProps) {
  const label = title?.trim();
  // On a phone the title keeps one line and the board's name gives way to it.
  const words = board && (
    <>
      <span className="text-title font-bold max-lg:min-w-0 max-lg:truncate lg:break-words">{label || board}</span>
      {label && <span className="text-meta break-all text-muted max-sm:hidden">{board}</span>}
    </>
  );
  return (
    // The header stays at the top while a page scrolls under it (D217's glass).
    <header className="glass sticky top-0 z-30 shrink-0 border-b border-rule pt-[env(safe-area-inset-top)]">
      <div className="flex min-h-14 flex-wrap items-center gap-x-3 gap-y-0.5 py-1.5 pr-[max(0.75rem,env(safe-area-inset-right))] pl-[max(0.75rem,env(safe-area-inset-left))] sm:min-h-16 sm:gap-x-5 sm:px-5 sm:py-2.5">
        {lead}
        <a href="/" className="flex min-h-11 items-center gap-2 text-[17px] font-bold text-ink no-underline">
          <Mark className="size-[22px]" />
          <span className={cn(board && "max-sm:sr-only")}>aboard</span>
        </a>
        {board && (
          <h1 className="min-w-0 max-lg:flex-1">
            {onTitle ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={onTitle}
                    className="board-title -mx-2 flex min-h-11 min-w-0 items-baseline max-lg:max-w-full gap-x-2.5 rounded-control px-2 py-2 text-left transition-colors duration-[140ms] ease-out hover:bg-hover lg:flex-wrap"
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
              <span className="flex min-w-0 items-baseline gap-x-2.5 lg:flex-wrap">{words}</span>
            )}
          </h1>
        )}
        {/* On a phone the board's labels take a second line under its title. */}
        <div className="flex items-center gap-x-5 empty:hidden max-lg:order-last max-lg:basis-full max-lg:gap-x-4">
          {board && <VisibilityLabel visibility={visibility} shared={shared} />}
          {starter &&
            (onStarter ? (
              <button
                type="button"
                onClick={onStarter}
                className="starter min-h-11 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline tap max-lg:min-h-9"
                title="Every member reads everything. See the rules for how to tighten them."
              >
                Starter policy
              </button>
            ) : (
              <span className="starter text-meta text-muted">Starter policy</span>
            ))}
        </div>
        {tools}
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
