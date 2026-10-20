import { initReactAITools, showReactAITool } from "./react/ai/entry";
import { initReactSkills, showReactSkills } from "./react/skills/entry";
import { setSettingsLangHandler, showReactSettings } from "./react/settings/entry";
import { showReactLarge } from "./react/large/entry";
import { showReactPhotos } from "./react/photos/entry";
import { showReactApps } from "./react/apps/entry";
import { showReactFullScan } from "./react/fullscan/entry";
import { showReactClean } from "./react/clean/entry";
import { setGoCleanHandler, showReactStatus } from "./react/status/entry";
import { setCrashGoClean } from "./react/list/crash-dumps";
import { bumpLang } from "./ui/i18n-react";
import { $, toast, VIEWS, type View } from "./shared";
// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner, LanguageSettings } from "./mygo";
import { t, currentLang, type LangPref } from "./i18n";
import { connectLanguage } from "./language";

const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));

// ---- i18n ----

function applyStaticI18n() {
  document.documentElement.lang = currentLang() === "zh" ? "zh-CN" : "en";
  document.querySelectorAll<HTMLElement>("[data-i18n]").forEach((el) => {
    el.textContent = t(el.dataset.i18n as never);
  });
  document.querySelectorAll<HTMLElement>("[data-i18n-ph]").forEach((el) => {
    el.setAttribute("placeholder", t(el.dataset.i18nPh as never));
  });
  document.querySelectorAll<HTMLElement>("[data-i18n-title]").forEach((el) => {
    const text = t(el.dataset.i18nTitle as never);
    el.title = text;
    el.setAttribute("aria-label", text);
  });
}

/** Re-render the current view so all dynamic text follows the new language. */
function refreshI18n() {
  applyStaticI18n();
  // 各 React 页面经 bumpLang 重渲染。
  bumpLang();
}

const language = connectLanguage(window.mygo ? {
  locale: window.mygo.locale,
  setLanguage: preference => LanguageSettings.setLanguage(preference),
  onLocaleChange: listener => window.mygo!.on<{ locale: string }>("mygo:locale", data => listener(data.locale)),
} : undefined, refreshI18n);

// 语言切换由 React 设置页的 <select> 触发，统一走这里：成功/失败 toast 与旧页一致。
async function changeLanguage(pref: LangPref): Promise<void> {
  try {
    await language.change(pref);
    toast(t("settings.languageChanged"));
  } catch {
    toast(t("settings.languageFailed"));
    throw new Error("language change failed");
  }
}
setSettingsLangHandler(changeLanguage);

// ---- 视图切换 ----

function switchView(v: View, tool?: string) {
  // AI 工具的几个侧栏项共用一个视图，按 data-tool 区分高亮。
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v && (v !== "aitools" || b.dataset.tool === tool)));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "status") showReactStatus(true);
  else showReactStatus(false);
  if (v === "clean") showReactClean();
  if (v === "aitools" && tool) {
    const names: Record<string, string> = { claude: "Claude Code", codex: "Codex" };
    showReactAITool(tool, names[tool] ?? tool);
  }
  if (v === "skills") showReactSkills();
  if (v === "apps") showReactApps();
  if (v === "fullscan") showReactFullScan();
  if (v === "settings") showReactSettings();
  if (v === "large") showReactLarge();
  if (v === "photos") showReactPhotos();
}
navItems.forEach((b) => b.addEventListener("click", () => switchView(b.dataset.view as View, b.dataset.tool)));

applyStaticI18n();
// AI 工具页是 React 挂载的，无需原生初始化；侧栏显隐由后端 listAITools 决定，启动时已安装即可见。
// 默认落在「系统状态」（HTML 初始高亮已同步），switchView("status") 即开采样。
void initReactAITools().then(initReactSkills);
switchView("status");
setGoCleanHandler(() => switchView("clean"));
setCrashGoClean(() => switchView("clean"));
// 版本号以应用自己为准，不写死在页面里
Cleaner.info()
  .then((i) => {
    if (i.version) $("#app-version").textContent = `Cleanzo v${i.version}`;
  })
  .catch(() => {});

void language.initialize()
  .catch(() => toast(t("settings.languageFailed")));

// 只响应 Go 在 smoke 模式下注入的钩子。
if ("__cleanzoSmoke" in window) window.dispatchEvent(new Event("cleanzo:ready"));
