import * as React from "react";
import * as CheckboxPrimitive from "@radix-ui/react-checkbox";
import { cn } from "../cn";

/** 三态：checked / unchecked / indeterminate（半选）。尺寸对齐原生 fs-check 的 15px。 */
export const Checkbox = React.forwardRef<
  React.ElementRef<typeof CheckboxPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>
>(({ className, checked, ...props }, ref) => (
  <CheckboxPrimitive.Root
    ref={ref}
    checked={checked}
    className={cn(
      "cz-cbx-root peer inline-flex h-[15px] w-[15px] shrink-0 rounded-[4px] border border-border-strong bg-card",
      "data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground",
      "data-[state=indeterminate]:border-primary data-[state=indeterminate]:bg-primary data-[state=indeterminate]:text-primary-foreground",
      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
      "disabled:cursor-not-allowed disabled:opacity-45",
      className,
    )}
    {...props}
  >
    <CheckboxPrimitive.Indicator forceMount className="cz-cbx flex items-center justify-center text-current">
      <svg viewBox="0 0 12 12" className="h-[12px] w-[12px]" aria-hidden="true">
        <path className="cz-cbx-check" d="M2.6 6.3 5 8.6 9.5 3.7" />
        <path className="cz-cbx-minus" d="M3.2 6h5.6" />
      </svg>
    </CheckboxPrimitive.Indicator>
  </CheckboxPrimitive.Root>
));
Checkbox.displayName = CheckboxPrimitive.Root.displayName;
