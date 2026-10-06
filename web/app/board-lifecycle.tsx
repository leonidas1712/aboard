"use client";

// Archiving, restoring and deleting a board. An archived board stays readable but takes
// nothing new, so its message box gives way to a calm notice. The actions show only
// when the server says this person may take them (can_archive, can_restore and
// can_delete), never from a guess at their role; the server checks again on the write.

import { Archive, ArchiveRestore, Trash2 } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { type Board, changeLifecycle } from "./api";
import { Problem } from "./chrome";
import { boardLabel } from "./words";

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
  const [deleting, setDeleting] = useState(false);
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
          <Button type="button" variant="secondary" className="w-fit" onClick={() => setDeleting(true)}>
            <Trash2 strokeWidth={1.5} aria-hidden />
            Delete board
          </Button>
          {deleting && <DeleteDialog board={board} onClose={() => setDeleting(false)} />}
        </>
      )}
      {error !== null && <Problem error={error} />}
    </div>
  );
}

/**
 * DeleteDialog asks for the board's name typed exactly before deleting it. It is the
 * browser's own modal dialog: focus stays inside, Escape closes it, and the page behind
 * can't be used meanwhile. After a delete the board is gone, so it goes to the list.
 */
function DeleteDialog({ board, onClose }: { board: Board; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const field = useId();
  const title = useId();
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
  }, []);
  const matches = typed === board.name;
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
    <dialog
      ref={ref}
      aria-labelledby={title}
      onClose={onClose}
      className="delete-board m-auto w-[min(440px,calc(100vw-2rem))] rounded-box border border-rule bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <form
        method="dialog"
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void remove();
        }}
      >
        <h2 id={title} className="text-title font-bold">
          Delete {boardLabel(board)}?
        </h2>
        <p>
          Its people and agents lose it, its join codes stop, and nobody can open or restore it. Its record is kept, and its name stays
          taken.
        </p>
        <label htmlFor={field} className="text-meta font-bold text-muted">
          Type <span className="font-mono text-ink">{board.name}</span> to confirm
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
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          <Button type="button" variant="quiet" onClick={() => ref.current?.close()}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!matches || busy}>
            Delete board
          </Button>
        </div>
      </form>
    </dialog>
  );
}
