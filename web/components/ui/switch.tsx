// Adapted from shadcn/ui (MIT, see NOTICE), restyled with Aboard's tokens.
"use client";

import * as SwitchPrimitive from "@radix-ui/react-switch";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function Switch({ className, ...props }: ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      className={cn(
        "peer inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-field-border bg-surface transition-colors duration-[140ms] ease-out data-[state=checked]:border-accent-strong data-[state=checked]:bg-accent",
        className,
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb className="pointer-events-none block size-3.5 translate-x-0.5 rounded-full bg-field-border transition-transform duration-[140ms] ease-out data-[state=checked]:translate-x-[18px] data-[state=checked]:bg-on-accent" />
    </SwitchPrimitive.Root>
  );
}
