import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../cn";

/**
 * 逐项对齐原生 .btn（style.css）：
 * - 基础：13px / 590、padding 6px 14px、圆角 8px、1px 透明描边（所有按钮同高）、
 *   底色 var(--btn-bg) 半透明白、hover var(--hover)、active scale(0.97)、disabled 0.45。
 * - ghost = .btn.ghost：描边 var(--border)，底色仍是 var(--btn-bg)（不是透明）。
 * - sm = .btn.small：4px 10px / 12px / 圆角 7px；sm + ghost 时文字为 muted，hover 回到前景色。
 * - lg = .btn.big：11px 30px / 14px / 圆角 10px。
 */
const buttonVariants = cva(
  "inline-flex cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-[8px] border border-transparent bg-btn-bg text-[13px] font-[590] text-foreground " +
    "transition-[background-color,color,box-shadow,transform,opacity,border-color] duration-[130ms] " +
    "hover:bg-hover active:scale-[0.97] disabled:cursor-default disabled:opacity-45 disabled:active:scale-100 " +
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-accent-strong",
        destructive: "bg-destructive text-destructive-foreground hover:bg-danger-strong",
        ghost: "border-border",
        link: "border-0 bg-transparent text-accent-strong underline-offset-4 hover:bg-transparent hover:underline",
      },
      size: {
        default: "px-[14px] py-[6px]",
        sm: "rounded-[7px] px-[10px] py-[4px] text-[12px]",
        lg: "rounded-[10px] px-[30px] py-[11px] text-[14px]",
        icon: "h-[30px] w-[30px] rounded-[8px] p-0",
      },
    },
    compoundVariants: [
      { variant: "ghost", size: "sm", className: "text-muted hover:text-foreground" },
    ],
    defaultVariants: { variant: "default", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, type = "button", ...props }, ref) => (
    <button ref={ref} type={type} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  ),
);
Button.displayName = "Button";

export { buttonVariants };
