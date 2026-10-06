"use client";

// Removing an agent from the board panel, and showing the agents removed before. The
// Remove action shows only where the server says this person may remove the agent
// (can_remove), never from a guess at their role; the server checks again on the write.

import { UserMinus } from "lucide-react";
import { useState } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { type Member, type RemovedBy, removeAgent, removedAgents } from "./api";
import { Problem } from "./chrome";

/**
 * RemoveAgent asks before removing an agent for good. Radix's alert dialog keeps focus
 * inside, closes on Escape or Cancel, and returns focus to the button.
 */
export function RemoveAgent({ board, agent, onRemoved }: { board: string; agent: Member; onRemoved: () => void }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const change = (next: boolean) => {
    setOpen(next);
    if (!next) setError(null);
  };
  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      await removeAgent(board, agent.name);
      setOpen(false);
      onRemoved();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog open={open} onOpenChange={change}>
      <AlertDialogTrigger asChild>
        <Button type="button" variant="quiet" className="remove-agent w-fit" aria-label={`Remove ${agent.name}`}>
          <UserMinus strokeWidth={1.5} aria-hidden />
          Remove
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent className="remove-agent-dialog">
        <div className="flex flex-col gap-3">
          <AlertDialogTitle>Remove {agent.name}?</AlertDialogTitle>
          <AlertDialogDescription className="rounded-box bg-attention px-3.5 py-3 text-ink">
            {agent.name} leaves {board} for good: its sessions can&apos;t read or post here any more. Its messages stay on the board.
          </AlertDialogDescription>
          {error !== null && <Problem error={error} />}
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button type="button" variant="quiet">
                Cancel
              </Button>
            </AlertDialogCancel>
            <Button type="button" variant="primary" disabled={busy} onClick={() => void remove()}>
              Remove
            </Button>
          </AlertDialogFooter>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}

const removedByWords: Record<RemovedBy, string> = {
  person: "removed by its person",
  board_owner: "removed by an owner",
  admin: "removed by a server admin",
  self: "left",
};

/** removedText says how and when an agent's seat ended: "left 2026-10-04". */
export function removedText(m: Member): string {
  const how = m.removed_by ? removedByWords[m.removed_by] : m.status === "left" ? "left" : "removed";
  return m.removed_at ? `${how} ${m.removed_at.slice(0, 10)}` : how;
}

/** RemovedAgents is a separate "Show removed" list of the agents whose seats ended. */
export function RemovedAgents({ board }: { board: string }) {
  const [list, setList] = useState<Member[] | null>(null);
  const [shown, setShown] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const toggle = async () => {
    if (shown) {
      setShown(false);
      return;
    }
    setError(null);
    try {
      setList(await removedAgents(board));
      setShown(true);
    } catch (e) {
      setError(e);
    }
  };
  return (
    <section className="removed-agents flex flex-col gap-2">
      <button
        type="button"
        aria-expanded={shown}
        onClick={() => void toggle()}
        className="w-fit text-meta text-muted underline-offset-[3px] hover:text-ink hover:underline"
      >
        {shown ? "Hide removed" : "Show removed"}
      </button>
      {shown && list !== null && (
        <ul className="flex flex-col gap-1" aria-label="Removed agents">
          {list.length === 0 && <li className="text-meta text-muted">No agents were removed from this board.</li>}
          {list.map((m) => (
            <li key={m.id} className="flex items-baseline justify-between gap-3" data-removed-agent={m.name}>
              <span className="break-all text-muted">{m.name}</span>
              <span className="text-meta text-muted">{removedText(m)}</span>
            </li>
          ))}
        </ul>
      )}
      {error !== null && <Problem error={error} />}
    </section>
  );
}
