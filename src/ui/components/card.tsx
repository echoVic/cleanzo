import * as React from "react";
import { cn } from "../cn";

/** 与原生 .card 一致：14px 圆角 + 1px var(--border) + var(--shadow)。 */
export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-[14px] border border-border bg-card shadow-card", className)} {...props} />;
}
