"use client";

import { X } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";
import { cn } from "@/lib/utils";
import { inboxKeys, keyLabel } from "./keys";

/** Kbd prints a key the way the keys sheet lists it. */
export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return <kbd className={cn("inline-flex h-6 min-w-6 shrink-0 items-center justify-center rounded-[5px] border border-rule bg-surface px-1.5 font-sans text-meta text-muted tabular-nums", className)}>{children}</kbd>;
}

/** KeysSheet lists the Inbox's keys as sentences, from the same table the handlers read. */
export function KeysSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = dialog.current;
    if (!d) return;
    if (open && !d.open) { d.showModal(); d.focus(); }
    else if (!open && d.open) d.close();
  }, [open]);
  return <dialog
    ref={dialog}
    aria-labelledby="keys-title"
    tabIndex={-1}
    onClose={() => { if (document.activeElement === dialog.current) dialog.current?.blur(); onClose(); }}
    onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    className="keys-sheet m-auto focus-visible:outline-none w-[min(460px,calc(100vw-2rem))] rounded-box border border-rule bg-surface p-0 text-ink shadow-float open:animate-fade-in"
  >
    <div className="flex flex-col gap-3 p-5">
      <div className="flex items-center justify-between gap-3">
        <h2 id="keys-title" className="text-title font-bold">Keys in the Inbox</h2>
        <button type="button" onClick={onClose} aria-label="Close" className="-mr-2 inline-flex size-10 items-center justify-center rounded-control text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2.5">
        {inboxKeys.map((k) => <div key={k.id} className="contents">
          <dt className="flex justify-end"><Kbd className="text-ink">{keyLabel(k.id)}</Kbd></dt>
          <dd>{k.what}</dd>
        </div>)}
      </dl>
      <p className="text-meta text-muted">Keys work while no field has focus. Snoozing is kept by this browser only.</p>
    </div>
  </dialog>;
}
