// Adapted from shadcn/ui (MIT, see NOTICE), restyled with Aboard's tokens.
import { Slot } from "@radix-ui/react-slot";
import { type VariantProps, cva } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-control text-body transition-colors duration-[140ms] ease-out disabled:cursor-not-allowed disabled:opacity-60 [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        // The brand's primary button: the accent under on-accent text, edged in accent-strong.
        primary: "h-11 border border-accent-strong bg-accent px-5 font-bold text-on-accent hover:bg-[color-mix(in_oklab,var(--accent)_88%,var(--ink))] disabled:border-rule disabled:bg-selected disabled:text-muted disabled:opacity-100",
        // On an accent bar, where an accent button would vanish: the bar's ink, in the accent.
        onAccent: "h-11 bg-on-accent px-5 font-bold text-accent hover:bg-on-accent/85",
        secondary: "h-11 border border-ink bg-transparent px-4 font-medium text-ink hover:bg-hover",
        quiet: "h-11 px-3 text-ink hover:bg-hover",
        link: "h-auto p-0 text-link underline underline-offset-[3px] hover:no-underline",
      },
    },
    defaultVariants: { variant: "primary" },
  },
);

export function Button({
  className,
  variant,
  asChild = false,
  ...props
}: ComponentProps<"button"> & VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(buttonVariants({ variant }), className)} {...props} />;
}
