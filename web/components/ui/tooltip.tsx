// Adapted from shadcn/ui (MIT, see NOTICE), restyled with Aboard's tokens.
"use client";

import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const TooltipProvider = TooltipPrimitive.Provider;
export const Tooltip = TooltipPrimitive.Root;
export const TooltipTrigger = TooltipPrimitive.Trigger;

export function TooltipContent({ className, sideOffset = 6, ...props }: ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        sideOffset={sideOffset}
        collisionPadding={12}
        className={cn(
          "z-50 max-w-[300px] rounded-control border border-rule bg-surface px-3 py-2 text-meta text-ink animate-fade-in",
          className,
        )}
        {...props}
      />
    </TooltipPrimitive.Portal>
  );
}
