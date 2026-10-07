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
        primary: "h-11 bg-ink px-5 font-bold text-on-ink hover:bg-ink/85",
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
