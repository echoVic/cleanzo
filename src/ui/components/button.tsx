import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../cn";

/** 对齐原生 .btn：13px/590、padding 6px 14px、圆角 8px、hover var(--hover)、active scale(0.97)。 */
const buttonVariants = cva(
  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[8px] text-[13px] font-[590] transition-colors disabled:pointer-events-none disabled:opacity-45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&_svg]:shrink-0 active:scale-[0.97]",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-accent-strong",
        destructive: "bg-destructive text-destructive-foreground hover:bg-danger-strong",
        ghost: "border border-border bg-transparent text-foreground hover:bg-hover",
        link: "text-accent-strong underline-offset-4 hover:underline",
      },
      size: {
        default: "px-[14px] py-[6px]",
        sm: "rounded-[7px] px-[10px] py-[4px] text-[12px]",
        lg: "rounded-[10px] px-[30px] py-[11px] text-[14px]",
        icon: "h-[30px] w-[30px] rounded-[8px] p-0",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  ),
);
Button.displayName = "Button";

export { buttonVariants };
