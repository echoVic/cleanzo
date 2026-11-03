/** 分段切换里每一项的样式（系统状态的「按内存 / 按 CPU」、进程页的三个标签）；滑块是 cz-seg-thumb + useIndicator。 */
export const segTrigger =
  "relative z-[1] cursor-pointer whitespace-nowrap rounded-[6px] px-3 py-1 text-xs leading-[18px] text-[var(--muted)] transition-colors " +
  "data-[state=active]:font-semibold data-[state=active]:text-[var(--fg)] " +
  // hover 仅作用于非激活项：激活项始终 fg + 半粗 + 滑块，hover 不应改变它
  "data-[state=inactive]:hover:text-foreground " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]";
