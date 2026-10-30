// 菜单栏读数的面板页（设计 docs/superpowers/specs/2026-10-29-menubar-design.md §四）：Go 用系统的弹出框打开 /panel.html?item=cpu&dark=1。
import * as React from "react";
import { createRoot } from "react-dom/client";
import { currentLang, setNativeLocale } from "../i18n";
import { PanelApp } from "./PanelApp";
import { panelItem } from "./format";
import "./panel.css";

// 跟着应用的语言：和主窗口一样先认 mygo 给的 locale；设置里选的语言在同一个 localStorage 里。
if (window.mygo) setNativeLocale(window.mygo.locale);
document.documentElement.lang = currentLang() === "zh" ? "zh-CN" : "en";
// 弹出框跟着菜单栏的浅色、深色走，不一定和应用一样：Go 在地址里用 dark=1 或 0 说明，panel.css 按 data-theme 换颜色。
const dark = new URLSearchParams(location.search).get("dark");
if (dark === "1" || dark === "0") document.documentElement.dataset.theme = dark === "1" ? "dark" : "light";

const item = panelItem(location.search);
const root = document.getElementById("panel-root");
if (root && item) {
  createRoot(root).render(
    <React.StrictMode>
      <PanelApp item={item} />
    </React.StrictMode>,
  );
}
