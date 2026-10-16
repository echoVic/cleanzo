import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../cn";

const badgeVariants = cva(
  "inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium",
  {
    variants: {
      variant: {
        safe: "bg-primary/10 text-primary",
        optional: "bg-muted-background text-muted",
        warn: "bg-destructive/10 text-destructive",
        protected: "bg-muted-background text-muted",
      },
    },
    defaultVariants: { variant: "safe" },
  },
);

export function Badge({ className, variant, ...props }: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}
