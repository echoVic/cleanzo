import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { cn } from "../cn";
import { useAnimatedHeight } from "../motion/height";
import { mergeRefs } from "../motion/refs";

const FOCUSABLE_SEL =
  'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/** 元素是否真的可聚焦：在 DOM 中、无 inert 祖先、未禁用、非 aria-hidden、有可见尺寸。 */
function isFocusable(el: HTMLElement | null): el is HTMLElement {
  if (!el || !el.isConnected) return false;
  let node: HTMLElement | null = el;
  while (node) {
    if (node.hasAttribute("inert")) return false;
    node = node.parentElement;
  }
  if (el.hasAttribute("disabled")) return false;
  if (el.getAttribute("aria-hidden") === "true") return false;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return false;
  return true;
}

/** opener 不可聚焦时，在 <main> 里找 opener 之后最近的可聚焦元素作为回退。 */
function findFallbackFocus(opener: HTMLElement | null): HTMLElement | null {
  const main = document.querySelector("main");
  if (!main) return null;
  const list = Array.from(main.querySelectorAll<HTMLElement>(FOCUSABLE_SEL)).filter(
    (el) => isFocusable(el) && el.getClientRects().length > 0,
  );
  if (list.length === 0) return null;
  if (!opener) return list[0] ?? null;
  const top = opener.getBoundingClientRect().top;
  const after = list.find((el) => el.getBoundingClientRect().top >= top);
  return after ?? list[0] ?? null;
}

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

  // 所有弹窗都用受控 state + 普通按钮开启（没有 DialogTrigger）。
  // Radix 的 onOpenAutoFocus 在焦点已移入弹窗后才触发，那时 document.activeElement
  // 已是弹窗内控件，无法拿到真正的开启者。所以在弹窗挂载的 layout 阶段（Radix 移焦前）
  // 记住当前焦点，关闭时还给它，避免键盘用户丢位置。
  React.useLayoutEffect(() => {
    const el = document.activeElement as HTMLElement | null;
    opener.current = el && el !== document.body && typeof el.focus === "function" ? el : null;
  }, []);

  // 多阶段弹窗（如卸载详情：分析中只有 disabled 按钮）挂载时没有可聚焦项，
  // Radix 会把焦点放在面板本身；等异步内容渲染出按钮后把焦点移入首个控件。
  React.useEffect(() => {
    const panel = outer.current;
    if (!panel || typeof MutationObserver === "undefined") return;
    const moveIntoPanel = () => {
      if (document.activeElement !== panel) return;
      const first = panel.querySelector<HTMLElement>(FOCUSABLE_SEL);
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
        // 不要在这里重新抓 opener：Radix 移焦后 activeElement 已是弹窗内控件，
        // 真正的开启者已由上方 useLayoutEffect 在移焦前记住。
      }}
      onCloseAutoFocus={(e) => {
        onCloseAutoFocus?.(e);
        if (e.defaultPrevented) return;
        const node = opener.current;
        if (isFocusable(node)) {
          node.focus({ preventScroll: true });
        } else {
          // opener 可能已被移除或在离场动画中被设为 inert（如卸载后行消失），
          // 回退到 <main> 中 opener 之后最近的可聚焦元素，避免焦点丢给 body。
          findFallbackFocus(node)?.focus({ preventScroll: true });
        }
      }}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 w-[min(92vw,560px)] -translate-x-1/2 -translate-y-1/2 rounded-[16px] border border-border bg-card p-[22px] px-6 pb-5 shadow-[0_24px_64px_rgba(0,0,0,0.4)]",
        "cz-dialog focus:outline-none",
        className,
      )}
      aria-modal="true"
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
export function DialogDescription({ className, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("text-[13px] leading-[1.6] text-muted", className)} {...props} />;
}
