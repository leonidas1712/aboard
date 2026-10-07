"use client";

// The board view's side panels. Each has a header row (its title and a button that
// hides it) on the same line as the conversation's "Now:" line. On a wide screen a
// hidden panel leaves a thin strip whose button, in the same row, shows it again, and a
// panel's inner edge can be dragged, or moved from the keyboard, within limits. This
// browser remembers both. On a narrow screen the panels stack below the conversation.

import { PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen } from "lucide-react";
import { type KeyboardEvent, type PointerEvent, type ReactNode, useRef } from "react";
import { cn } from "@/lib/utils";

export type PanelSize = { width: number; collapsed: boolean };

/** The strip a hidden panel leaves, in pixels. */
export const stripWidth = 56;

/** Limits are a panel's narrowest, widest and first width. */
export type Limits = { min: number; max: number; initial: number };

/** clampSize keeps a remembered size within limits, whatever an older page stored. */
export function clampSize(s: PanelSize, l: Limits): PanelSize {
  const w = Number.isFinite(s?.width) ? s.width : l.initial;
  return { collapsed: !!s?.collapsed, width: Math.round(Math.min(l.max, Math.max(l.min, w))) };
}

/** The header row every column shares, so their first lines align. */
export const headerRow = "flex min-h-14 items-center";

type Props = {
  side: "left" | "right";
  /** title heads the panel; label names it for assistive technology and its buttons. */
  title: ReactNode;
  label: string;
  size: PanelSize;
  setSize: (s: PanelSize) => void;
  limits: Limits;
  children: ReactNode;
  className?: string;
};

export function SidePanel({ side, title, label, size, setSize, limits, children, className }: Props) {
  const drag = useRef<{ x: number; width: number } | null>(null);
  const clamp = (w: number) => Math.round(Math.min(limits.max, Math.max(limits.min, w)));
  const left = side === "left";
  const Close = left ? PanelLeftClose : PanelRightClose;
  const Open = left ? PanelLeftOpen : PanelRightOpen;
  const id = `${side}-panel-title`;

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, width: size.width };
  };
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    const moved = e.clientX - drag.current.x;
    setSize({ collapsed: false, width: clamp(drag.current.width + (left ? moved : -moved)) });
  };
  const onPointerUp = () => {
    drag.current = null;
  };
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.shiftKey ? 48 : 16;
    const grow = left ? "ArrowRight" : "ArrowLeft";
    const shrink = left ? "ArrowLeft" : "ArrowRight";
    if (e.key === grow) setSize({ collapsed: false, width: clamp(size.width + step) });
    else if (e.key === shrink) setSize({ collapsed: false, width: clamp(size.width - step) });
    else if (e.key === "Home") setSize({ collapsed: false, width: limits.min });
    else if (e.key === "End") setSize({ collapsed: false, width: limits.max });
    else return;
    e.preventDefault();
  };

  const iconButton =
    "inline-flex size-10 shrink-0 items-center justify-center rounded-control text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink";

  return (
    <aside
      aria-labelledby={id}
      data-collapsed={size.collapsed || undefined}
      className={cn("side-panel relative min-w-0 border-t border-rule bg-sidebar lg:min-h-0 lg:border-t-0", left ? "lg:border-r" : "lg:border-l", className)}
    >
      {/* The strip: only the button that shows the panel again, in the header row. */}
      <div className={cn(headerRow, "hidden justify-center", size.collapsed && "lg:flex")}>
        <button
          type="button"
          className={cn("panel-open", iconButton)}
          aria-label={`Show ${label}`}
          aria-expanded={false}
          title={`Show ${label}`}
          onClick={() => setSize({ ...size, collapsed: false })}
        >
          <Open className="size-[18px]" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <div className={cn("quiet-scroll flex h-full flex-col px-4 pb-6 sm:px-6 lg:overflow-y-auto lg:px-5", size.collapsed && "lg:hidden")}>
        {/* On a wide screen the header row stays in place while the panel scrolls, so its
            title and hide button are always in reach. */}
        <div className={cn(headerRow, "glass glass-sidebar justify-between gap-3 pt-2 lg:sticky lg:top-0 lg:z-10 lg:pt-0")}>
          <h2 id={id} className="text-meta font-bold text-muted">
            {title}
          </h2>
          <button
            type="button"
            className={cn(iconButton, "panel-close -mr-2.5 hidden lg:inline-flex")}
            aria-label={`Hide ${label}`}
            aria-expanded
            title={`Hide ${label}`}
            onClick={() => setSize({ ...size, collapsed: true })}
          >
            <Close className="size-[18px]" strokeWidth={1.5} aria-hidden />
          </button>
        </div>
        {children}
      </div>
      {!size.collapsed && (
        // A focusable separator is the ARIA pattern for a splitter that resizes a panel.
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label={`Resize ${label}`}
          aria-valuenow={size.width}
          aria-valuemin={limits.min}
          aria-valuemax={limits.max}
          tabIndex={0}
          title="Drag to resize; double-click to reset"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          onDoubleClick={() => setSize({ collapsed: false, width: limits.initial })}
          onKeyDown={onKeyDown}
          className={cn(
            "resize-handle group absolute inset-y-0 z-10 hidden w-3 cursor-col-resize touch-none outline-none lg:block",
            left ? "-right-1.5" : "-left-1.5",
          )}
        >
          <span className="mx-auto block h-full w-0.5 bg-transparent transition-colors duration-[140ms] ease-out group-hover:bg-accent group-focus-visible:bg-accent" />
        </div>
      )}
    </aside>
  );
}
