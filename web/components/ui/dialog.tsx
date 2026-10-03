// Adapted from shadcn/ui (MIT, see NOTICE), restyled with Aboard's tokens: the dialog
// uses the surface colour and a rule border, never a shadow, and fades in and out.
"use client";

import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;

export function DialogContent({ className, children, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="dialog-overlay fixed inset-0 z-50 bg-[var(--scrim)] data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out" />
      <DialogPrimitive.Content
        className={cn(
          "fixed top-[max(1rem,8vh)] left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-[540px] -translate-x-1/2 flex-col overflow-y-auto rounded-box border border-rule bg-surface text-ink quiet-scroll",
          "data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out",
          className,
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          className="absolute top-3 right-3 inline-flex size-10 items-center justify-center rounded-control text-muted transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink"
          aria-label="Close"
          title="Close"
        >
          <X className="size-[18px]" strokeWidth={1.5} aria-hidden />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn("text-title font-bold", className)} {...props} />;
}

export function DialogDescription({ className, ...props }: ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("text-meta text-muted", className)} {...props} />;
}
