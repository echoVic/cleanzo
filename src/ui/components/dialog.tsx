import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "../cn";
import { useAnimatedHeight } from "../motion/height";
import { mergeRefs } from "../motion/refs";

/** 对齐原生 .modal：16px 圆角、padding 22/24/20、大投影；遮罩 var(--overlay) + 模糊。 */
export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;

type DialogContentProps = React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & { animateHeight?: boolean };

// 放在 Portal 的已挂载子树里，确保高度监听注册时两个 ref 都已到位。
const DialogPanel = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>(({ className, children, animateHeight = false, onOpenAutoFocus, onCloseAutoFocus, ...props }, ref) => {
  const outer = React.useRef<HTMLDivElement>(null);
  const inner = React.useRef<HTMLDivElement>(null);
  // 所有弹窗都用受控 state + 普通按钮开启（没有 DialogTrigger），
  // Radix 关闭时只还原 triggerRef，否则焦点会丢给 body，这里自己记住打开者。
  const opener = React.useRef<HTMLElement | null>(null);
  useAnimatedHeight(outer, inner, animateHeight);

  // 多阶段弹窗（如卸载详情：分析中只有 disabled 按钮）挂载时没有可聚焦项，
  // Radix 会把焦点放在面板本身；等异步内容渲染出按钮后把焦点移入首个控件。
  React.useEffect(() => {
    const panel = outer.current;
    if (!panel || typeof MutationObserver === "undefined") return;
    const moveIntoPanel = () => {
      if (document.activeElement !== panel) return;
      const first = panel.querySelector<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      );
      if (first && first.getClientRects().length > 0) first.focus({ preventScroll: true });
    };
    const observer = new MutationObserver(moveIntoPanel);
    observer.observe(panel, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, []);
  return (
    <DialogPrimitive.Content
      ref={mergeRefs(outer, ref)}
      onOpenAutoFocus={(e) => {
        onOpenAutoFocus?.(e);
        const el = document.activeElement as HTMLElement | null;
        opener.current = el && el !== document.body && typeof el.focus === "function" ? el : null;
      }}
      onCloseAutoFocus={(e) => {
        onCloseAutoFocus?.(e);
        const node = opener.current;
        if (!e.defaultPrevented && node && node.isConnected) node.focus({ preventScroll: true });
      }}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 w-[min(92vw,560px)] -translate-x-1/2 -translate-y-1/2 rounded-[16px] border border-border bg-card p-[22px] px-6 pb-5 shadow-[0_24px_64px_rgba(0,0,0,0.4)]",
        "cz-dialog focus:outline-none",
        className,
      )}
      {...props}
    >
      {animateHeight ? <div ref={inner}>{children}</div> : children}
    </DialogPrimitive.Content>
  );
});

/** 通用弹窗；animateHeight 让内容换阶段时高度平滑变化。 */
export const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  DialogContentProps
>((props, ref) => (
  <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay
      className="cz-dialog-overlay fixed inset-0 z-50"
      style={{ background: "var(--overlay)", backdropFilter: "blur(10px) saturate(140%)", WebkitBackdropFilter: "blur(10px) saturate(140%)" }}
    />
    <DialogPanel ref={ref} {...props} />
  </DialogPrimitive.Portal>
));
DialogContent.displayName = DialogPrimitive.Content.displayName;

export function DialogHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("mb-[10px]", className)} {...props} />;
}
export function DialogTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <DialogPrimitive.Title className={cn("m-0 text-[16px] font-bold tracking-[-0.2px]", className)} {...props} />;
}
export function DialogDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <DialogPrimitive.Description className={cn("text-[13px] leading-[1.6] text-muted", className)} {...props} />;
}
