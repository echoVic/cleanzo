import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../cn";

/** 对齐原生 .tag：11px/600、padding 1px 7px、圆角 999px。 */
const badgeVariants = cva(
  "inline-flex items-center rounded-full px-[7px] py-[1px] text-[11px] font-semibold leading-[1.5]",
  {
    variants: {
      variant: {
        safe: "bg-accent-soft text-accent-strong",
        optional: "bg-bg-subtle text-muted",
        warn: "bg-danger-soft text-danger",
        protected: "bg-bg-subtle text-muted",
      },
    },
    defaultVariants: { variant: "safe" },
  },
);

export function Badge({ className, variant, ...props }: React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badgeVariants>) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />;
}
