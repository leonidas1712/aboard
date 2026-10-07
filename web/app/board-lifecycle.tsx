"use client";

// Archiving, restoring and deleting a board. An archived board stays readable but takes
// nothing new, so its message box gives way to a calm notice. The actions show only
// when the server says this person may take them (can_archive, can_restore and
// can_delete), never from a guess at their role; the server checks again on the write.

import { Archive, ArchiveRestore, Trash2 } from "lucide-react";
import { useId, useState } from "react";
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
import { type Board, changeLifecycle } from "./api";
import { Problem } from "./chrome";

/** ArchivedNotice takes the message box's place on an archived board. */
export function ArchivedNotice({ board, onChanged }: { board: Board; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const restore = async () => {
    setBusy(true);
    setError(null);
    try {
      await changeLifecycle(board.name, "restore");
      onChanged();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section aria-label="Archived board" className="archived-notice mb-4 flex flex-col gap-3 rounded-box border border-rule bg-surface px-3.5 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="flex items-center gap-2">
          <Archive className="size-4 shrink-0 text-muted" strokeWidth={1.5} aria-hidden />
          This board is archived. It&apos;s read-only.
        </p>
        {board.can_restore && (
          <Button type="button" variant="secondary" disabled={busy} onClick={restore}>
            <ArchiveRestore strokeWidth={1.5} aria-hidden />
            Restore
          </Button>
        )}
      </div>
      {error !== null && <Problem error={error} />}
    </section>
  );
}

/**
 * LifecycleActions sits in the board panel's Details: Archive on an active board, and
 * Delete on an archived one, each only when the server allows it.
 */
export function LifecycleActions({ board, onChanged }: { board: Board; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const archived = board.lifecycle === "archived";
  const canArchive = !archived && board.can_archive === true;
  const canDelete = archived && board.can_delete === true;
  if (!canArchive && !canDelete) return null;

  const archive = async () => {
    setBusy(true);
    setError(null);
    try {
      await changeLifecycle(board.name, "archive");
      onChanged();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="lifecycle flex flex-col gap-2 border-t border-rule pt-3">
      {canArchive && (
        <>
          <p className="text-meta text-muted">Archiving keeps everything readable and stops new messages and joins until it&apos;s restored.</p>
          <Button type="button" variant="secondary" className="w-fit" disabled={busy} onClick={archive}>
            <Archive strokeWidth={1.5} aria-hidden />
            Archive board
          </Button>
        </>
      )}
      {canDelete && (
        <>
          <p className="text-meta text-muted">Deleting ends every way into this board for good. Its record is kept.</p>
          <DeleteDialog board={board} />
        </>
      )}
      {error !== null && <Problem error={error} />}
    </div>
  );
}

/**
 * DeleteDialog asks for the board's name typed exactly before deleting it. Radix's
 * alert dialog keeps focus inside, closes on Escape or Cancel, and returns focus to
 * the button. After a delete the board is gone, so the page goes to the board list.
 */
function DeleteDialog({ board }: { board: Board }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const field = useId();
  const matches = typed === board.name;
  const change = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setTyped("");
      setError(null);
    }
  };
  const remove = async () => {
    if (!matches) return;
    setBusy(true);
    setError(null);
    try {
      await changeLifecycle(board.name, "delete");
      window.location.assign("/");
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  return (
    <AlertDialog open={open} onOpenChange={change}>
      <AlertDialogTrigger asChild>
        <Button type="button" variant="secondary" className="w-fit">
          <Trash2 strokeWidth={1.5} aria-hidden />
          Delete board
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent className="delete-board">
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void remove();
          }}
        >
          <AlertDialogTitle>Delete {board.name}?</AlertDialogTitle>
          <AlertDialogDescription className="rounded-box border border-field-border bg-selected px-3.5 py-3 text-ink">
            Nobody can open it again. Its record is kept.
          </AlertDialogDescription>
          <label htmlFor={field} className="text-meta font-bold text-ink">
            Type {board.name} to confirm
          </label>
          <input
            id={field}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            className="h-11 w-full rounded-control border border-field-border bg-surface px-3 text-ink"
          />
          {error !== null && <Problem error={error} />}
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button type="button" variant="quiet">
                Cancel
              </Button>
            </AlertDialogCancel>
            <Button type="submit" variant="primary" disabled={!matches || busy}>
              Delete
            </Button>
          </AlertDialogFooter>
        </form>
      </AlertDialogContent>
    </AlertDialog>
  );
}
