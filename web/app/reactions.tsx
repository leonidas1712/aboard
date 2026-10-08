"use client";

// Reactions on a message: one small button per emoji anyone reacted with, saying how
// many and, on hover or focus, who; clicking one adds or takes back your own. A React
// button beside Reply opens the fixed set. A reaction never wakes an agent and never
// counts as unread, so it is the quiet way to acknowledge.

import { SmilePlus } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type Message, type Reaction, type ReactionName, reactionSet } from "./api";

/** OnReact adds the person's reaction to a message (add true) or takes it back. */
export type OnReact = (m: Message, name: ReactionName, add: boolean) => void;

/** who names the members who reacted, the person as "You": "You, claude and codex". */
function who(r: Reaction, me: string | null): string {
  const names = r.by.map((n) => (r.mine && n === me ? "You" : n));
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`;
}

function wordOf(name: ReactionName): string {
  return reactionSet.find((r) => r.name === name)?.word ?? name;
}

/**
 * Reactions shows a message's reactions under its body; nothing when it has none.
 * Without onReact, on a read-only board, they show as counts that can't be pressed.
 */
export function Reactions({ m, me, onReact }: { m: Message; me: string | null; onReact?: OnReact }) {
  if (m.reactions.length === 0) return null;
  return (
    <ul className="reactions mt-1.5 flex flex-wrap items-center gap-1.5" aria-label="Reactions">
      {m.reactions.map((r) => (
        <li key={r.name}>
          {onReact ? (
            <ReactionToggle m={m} r={r} me={me} onReact={onReact} />
          ) : (
            <span
              data-reaction={r.name}
              title={`${who(r, me)} reacted with ${r.emoji}`}
              className="reaction tap inline-flex h-8 items-center gap-1.5 rounded-control border border-rule bg-surface px-2 text-meta text-ink tabular-nums"
            >
              <span aria-hidden className="text-[15px] leading-none">
                {r.emoji}
              </span>
              <span aria-hidden className="font-bold">
                {r.count}
              </span>
              <span className="sr-only">
                {wordOf(r.name)} {r.emoji}, {r.count}: {who(r, me)}
              </span>
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

/** ReactionToggle is one reaction as a button that adds or takes back your own. */
function ReactionToggle({ m, r, me, onReact }: { m: Message; r: Reaction; me: string | null; onReact: OnReact }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-reaction={r.name}
          aria-pressed={r.mine}
          aria-label={`${wordOf(r.name)} ${r.emoji}, ${r.count}: ${who(r, me)}. ${r.mine ? "Take yours back" : "Add yours"}`}
          onClick={() => onReact(m, r.name, !r.mine)}
          className={cn(
            "reaction inline-flex h-8 items-center gap-1.5 rounded-control border px-2 text-meta tabular-nums transition-colors duration-[140ms] ease-out",
            r.mine ? "mine border-accent-strong bg-selected text-ink hover:bg-surface" : "border-rule bg-surface text-ink hover:border-field-border hover:bg-hover",
          )}
        >
          <span aria-hidden className="text-[15px] leading-none">
            {r.emoji}
          </span>
          <span aria-hidden className="font-bold">
            {r.count}
          </span>
        </button>
      </TooltipTrigger>
      <TooltipContent>
        {who(r, me)} reacted with {r.emoji}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * ReactButton opens the set of reactions. It shows on hover or focus, like Reply, and
 * always on a touch screen. Picking one you already gave takes it back.
 */
export function ReactButton({ m, onReact, label }: { m: Message; onReact: OnReact; label: string }) {
  const mine = new Set(m.reactions.filter((r) => r.mine).map((r) => r.name));
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        className="react-button tap flex h-7 shrink-0 items-center rounded-[6px] px-1.5 text-link opacity-0 transition-opacity duration-[140ms] ease-out group-focus-within:opacity-100 group-hover:opacity-100 hover:bg-hover focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"
        aria-label={`React to ${label}`}
        title="React"
      >
        <SmilePlus className="size-4" strokeWidth={1.5} aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" collisionPadding={12} className="reaction-picker flex min-w-0 gap-0.5" aria-label="Reactions">
        {reactionSet.map((r) => (
          <DropdownMenuItem
            key={r.name}
            className={cn("size-11 justify-center p-0 text-[20px]", mine.has(r.name) && "bg-selected")}
            aria-label={`${mine.has(r.name) ? "Take back" : "React with"} ${r.word}`}
            title={r.word}
            onSelect={() => onReact(m, r.name, !mine.has(r.name))}
          >
            <span aria-hidden>{r.emoji}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
