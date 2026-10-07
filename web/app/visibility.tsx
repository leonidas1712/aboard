"use client";

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { type Board, isArchived, post } from "./api";
import { Problem } from "./chrome";
import { shellWord } from "./people";

type Preview = { reveals: { messages: number; files: number } | null; join_codes_canceled: number };

/** VisibilityControl previews access changes before an owner commits them. */
export function VisibilityControl({ board, owner, onChanged }: { board: Board; owner: boolean; onChanged: () => void }) {
  const [open, setOpen] = useState(false);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const target = board.visibility === "open" ? "private" : "open";
  const allowed = owner && !(isArchived(board) && target === "open");
  const generation = useRef(0);
  const context = useRef("");
  context.current = `${board.id}:${board.visibility}:${allowed}`;
  useEffect(() => { generation.current++; setOpen(false); setPreview(null); setError(null); setBusy(false); }, [board.id, board.visibility, allowed]);
  const path = `/v1/boards/${encodeURIComponent(board.name)}/visibility`;
  const start = async () => {
    const request = ++generation.current;
    const key = context.current;
    const current = () => generation.current === request && context.current === key;
    setOpen(true); setBusy(true); setPreview(null); setError(null);
    try { const result = await post<Preview>(path, { visibility: target, dry_run: true }); if (current()) setPreview(result); }
    catch (e) { if (current()) setError(e); }
    finally { if (current()) setBusy(false); }
  };
  const confirm = async () => {
    setBusy(true); setError(null);
    try { await post(path, { visibility: target }); setOpen(false); onChanged(); }
    catch (e) { setError(e); }
    finally { setBusy(false); }
  };
  return <div className="flex flex-col gap-2">
    <p><span className="text-muted">Visibility: </span>{board.visibility === "open" ? "Open" : "Private"}</p>
    <p className="text-meta text-muted">{board.visibility === "open" ? "People on this server can see this board and join it." : "Only people on this board can read it."}</p>
    {allowed && <Button variant="secondary" onClick={start}>Change visibility</Button>}
    <AlertDialog open={open} onOpenChange={(value) => { if (!busy || !preview) { if (!value) { generation.current++; setBusy(false); } setOpen(value); } }}><AlertDialogContent>
      <AlertDialogTitle>Make {board.title || board.name} {target}?</AlertDialogTitle>
      <AlertDialogDescription>{target === "open" ? "Everyone on this server will be able to see this board and join it. After joining, they can read its whole history and files. People already on the board keep their access." : "Only people already on the board will be able to read it. People already on the board keep their access and agents. Working join codes will be canceled, and agents will no longer be allowed to add people."}</AlertDialogDescription>
      {busy && !preview && <p role="status">Checking the change…</p>}
      {preview && <p className="text-meta text-muted">{target === "open" && preview.reveals ? `This makes ${preview.reveals.messages} messages and ${preview.reveals.files} files readable after joining.` : `${preview.join_codes_canceled} working join codes will be canceled.`}</p>}
      {error !== null && <Problem error={error} />}
      <code className="break-all text-meta">aboard board visibility {target} --board {shellWord(board.name)} --server {typeof window === "undefined" ? "" : shellWord(window.location.origin)}</code>
      <AlertDialogFooter><AlertDialogCancel asChild><Button variant="secondary" disabled={busy && preview !== null}>Cancel</Button></AlertDialogCancel><Button disabled={busy || !preview || error !== null} onClick={confirm}>{busy && preview ? "Changing…" : `Make ${target}`}</Button></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
  </div>;
}
