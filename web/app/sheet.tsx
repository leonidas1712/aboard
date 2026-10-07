"use client";

// On a phone the side panels are sheets: each fills the screen over the page it belongs
// to, with a Back button at the top and the phone's own back gesture, and the page
// underneath keeps its place. On a wide screen they are the docked panels (panels.tsx).

import { ArrowLeft } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";

const wideQuery = "(min-width: 1024px)";

/** useWide is true on a screen wide enough to dock the side panels (Tailwind's lg). */
export function useWide(): boolean {
  return useSyncExternalStore(
    (changed) => {
      const q = window.matchMedia(wideQuery);
      q.addEventListener("change", changed);
      return () => q.removeEventListener("change", changed);
    },
    () => window.matchMedia(wideQuery).matches,
    () => true,
  );
}

/**
 * useBackEntry gives a phone view that covers the page (a sheet, an ask read on its own)
 * an entry in the browser's history while it is shown, so the back gesture closes it.
 * When the page closes it instead, the entry is taken back.
 */
export function useBackEntry(active: boolean, onBack: () => void) {
  const back = useRef(onBack);
  useEffect(() => {
    back.current = onBack;
  });
  const id = useId();
  useEffect(() => {
    if (!active) return;
    const ours = () => (history.state as { aboardBack?: string } | null)?.aboardBack === id;
    history.pushState({ ...(history.state ?? {}), aboardBack: id }, "");
    const pop = () => {
      if (!ours()) back.current();
    };
    window.addEventListener("popstate", pop);
    return () => {
      window.removeEventListener("popstate", pop);
      if (ours()) history.back();
    };
  }, [active, id]);
}

type Props = {
  open: boolean;
  onClose: () => void;
  /** side is where the panel docks on a wide screen, so the sheet slides in from there. */
  side: "left" | "right";
  title: ReactNode;
  /** back names where Back returns to ("Conversation", "Inbox"). */
  back: string;
  children: ReactNode;
};

// Menus, popovers and dialogs opened from inside a sheet take Escape first.
const layer = '[role="menu"], [role="alertdialog"], [data-radix-popper-content-wrapper]';

/**
 * Sheet is a side panel on a phone. While open, the page under it is inert, Escape and
 * the browser's back close it (an entry in the history stands for it), and focus goes
 * back to what opened it.
 */
export function Sheet({ open, onClose, side, title, back, children }: Props) {
  const box = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  const id = useId();
  useBackEntry(open, onClose);

  useEffect(() => {
    if (!open) return;
    const el = box.current!;
    const opener = document.activeElement as HTMLElement | null;
    // The page under the sheet can't be reached; menus opened later are new portals and stay live.
    const under = [...document.body.children].filter((c) => c !== el) as HTMLElement[];
    for (const c of under) c.inert = true;
    el.querySelector<HTMLElement>("[data-sheet-back]")?.focus({ preventScroll: true });
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      if (document.querySelector(layer) || el.querySelector('[role="dialog"]')) return;
      e.preventDefault();
      close.current();
    };
    document.addEventListener("keydown", esc);
    return () => {
      for (const c of under) c.inert = false;
      document.removeEventListener("keydown", esc);
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, [open]);

  if (!open || typeof document === "undefined") return null;
  return createPortal(
    <div
      ref={box}
      role="dialog"
      aria-modal="true"
      aria-labelledby={`${id}-title`}
      data-side={side}
      className="sheet quiet-scroll fixed inset-0 z-40 flex flex-col overflow-y-auto overscroll-contain bg-sidebar text-ink [--mark-ring:var(--sidebar)]"
    >
      <div className="glass glass-sidebar sticky top-0 z-10 flex min-h-14 shrink-0 items-center gap-2 border-b border-rule px-2 pt-[env(safe-area-inset-top)]">
        <button
          type="button"
          data-sheet-back
          onClick={onClose}
          className="sheet-back inline-flex min-h-11 shrink-0 items-center gap-1.5 rounded-control px-2.5 text-link transition-colors duration-[140ms] ease-out hover:bg-hover"
        >
          <ArrowLeft className="size-[18px]" strokeWidth={1.75} aria-hidden />
          {back}
        </button>
        <h2 id={`${id}-title`} className="min-w-0 flex-1 truncate pr-3 text-right text-meta font-bold text-muted">
          {title}
        </h2>
      </div>
      <div className={cn("flex flex-col px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))]", "pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))]")}>
        {children}
      </div>
    </div>,
    document.body,
  );
}
