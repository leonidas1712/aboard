"use client";

// The board view's side panels on a wide screen: each collapses to a thin strip with a
// button that opens it again, and its width can be dragged, or set from the keyboard,
// within limits. This browser remembers both. On a narrow screen the panels stack below
// the conversation and neither applies.

import { PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen } from "lucide-react";
import { type KeyboardEvent, type PointerEvent, type ReactNode, useRef } from "react";
import { cn } from "@/lib/utils";

export type PanelSize = { width: number; collapsed: boolean };

/** The strip a collapsed panel leaves, in pixels. */
export const stripWidth = 48;

type Props = {
  side: "left" | "right";
  label: string;
  size: PanelSize;
  setSize: (s: PanelSize) => void;
  min: number;
  max: number;
  /** initial is the width a double-click on the edge goes back to. */
  initial: number;
  children: ReactNode;
  className?: string;
};

export function SidePanel({ side, label, size, setSize, min, max, initial, children, className }: Props) {
  const drag = useRef<{ x: number; width: number } | null>(null);
  const clamp = (w: number) => Math.round(Math.min(max, Math.max(min, w)));
  const left = side === "left";
  const Close = left ? PanelLeftClose : PanelRightClose;
  const Open = left ? PanelLeftOpen : PanelRightOpen;

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
    else if (e.key === "Home") setSize({ collapsed: false, width: min });
    else if (e.key === "End") setSize({ collapsed: false, width: max });
    else return;
    e.preventDefault();
  };

  return (
    <aside
      aria-label={label}
      data-collapsed={size.collapsed || undefined}
      className={cn(
        "side-panel relative border-t border-rule lg:min-h-0 lg:border-t-0",
        left ? "lg:border-r" : "lg:border-l",
        className,
      )}
    >
      {/* The strip: only the button that opens the panel again. */}
      <div className={cn("hidden h-full flex-col items-center pt-3", size.collapsed && "lg:flex")}>
        <button
          type="button"
          className="panel-open inline-flex size-11 items-center justify-center rounded-control text-ink transition-colors duration-[140ms] ease-out hover:bg-selected"
          aria-label={`Show ${label}`}
          aria-expanded={false}
          title={`Show ${label}`}
          onClick={() => setSize({ ...size, collapsed: false })}
        >
          <Open className="size-[18px]" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <div className={cn("quiet-scroll h-full px-4 py-5 sm:px-6 lg:overflow-y-auto lg:pt-3", size.collapsed && "lg:hidden")}>
        <div className={cn("mb-1 hidden lg:flex", left ? "justify-end" : "justify-start")}>
          <button
            type="button"
            className="panel-close -mx-2 inline-flex size-11 items-center justify-center rounded-control text-muted transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink"
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
          aria-valuemin={min}
          aria-valuemax={max}
          tabIndex={0}
          title="Drag to resize; double-click to reset"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          onDoubleClick={() => setSize({ collapsed: false, width: initial })}
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
