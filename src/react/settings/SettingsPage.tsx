import { isWindows } from "../../platform";
import * as React from "react";
import type { CustomFolder, GrowthConfig, GrowthConfigPatch, MenuBarState, Settings } from "../../mygo";
import { Button, buttonVariants } from "../../ui/components/button";
import { Checkbox } from "../../ui/components/checkbox";
import { cn } from "../../ui/cn";
import { scrollBehavior } from "../../ui/motion/motion";
import { useT, type DictKey, type LangPref } from "../../ui/i18n-react";
import { errText, displayPath, toast } from "../../shared";
import { ruleName, getPref } from "../../i18n";
import { markCleanStale } from "../clean/entry";
import { growthApi } from "../growth/api";
import { settingsApi } from "./api";
import {
  GROUPS, baseNameOf, ignoredLabel, nextDisabledGroups, normalized, resolveFolderName, SaveQueue, toggleMenuBarItem,
  type GroupId,
} from "./settings-helpers";

export interface SettingsCallbacks {
  /** main.ts 桥接：走已有的 language.change 并弹成功/失败 toast；失败时 reject。 */
  changeLanguage: (pref: LangPref) => Promise<void>;
}

interface Props {
  callbacks: SettingsCallbacks;
  /** 每次进入设置页 +1，驱动重新读取配置。 */
  session: number;
  /** 要滚到的一节和请求序号（「菜单栏设置…」进来时 id 为 "menubar"）；序号变了才滚。 */
  focus?: { id: string; nonce: number };
}

/** 不可变地更新错误 Map，触发重渲染。 */
function withError(m: Map<string, unknown>, key: string, e: unknown): Map<string, unknown> {
  const n = new Map(m);
  n.set(key, e);
  return n;
}
function withoutError(m: Map<string, unknown>, key: string): Map<string, unknown> {
  if (!m.has(key)) return m;
  const n = new Map(m);
  n.delete(key);
  return n;
}

export function SettingsPage({ callbacks, session, focus }: Props) {
  const t = useT();
  const [settings, setSettings] = React.useState<Settings | null>(null);
  const settingsRef = React.useRef<Settings | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [readError, setReadError] = React.useState<unknown>(null);
  const [version, setVersion] = React.useState("");
  const [appNames, setAppNames] = React.useState<Map<string, string>>(new Map());
  const [access, setAccess] = React.useState<boolean | null>(null);
  const [permissionError, setPermissionError] = React.useState<unknown>(null);
  const [errors, setErrors] = React.useState<Map<string, unknown>>(new Map());
  /** 非名称保存进行中：锁住编辑控件，防整份配置互相覆盖。 */
  const [locked, setLocked] = React.useState(false);
  const [picking, setPicking] = React.useState(false);
  const [langChanging, setLangChanging] = React.useState(false);
  const [growth, setGrowth] = React.useState<GrowthConfig | null>(null);
  const [growthBusy, setGrowthBusy] = React.useState(false);
  const growthSeqRef = React.useRef(0);
  const growthLoadingRef = React.useRef(false);
  const growthSavingRef = React.useRef(false);
  /** 保存期间来的重读（重进页面、后端广播）：保存结束后补读一次。 */
  const growthReloadRef = React.useRef(false);
  const [menuBar, setMenuBar] = React.useState<MenuBarState | null>(null);
  const [menuBarBusy, setMenuBarBusy] = React.useState(false);
  const menuBarSeqRef = React.useRef(0);
  const menuBarSavingRef = React.useRef(false);
  const [sectionHl, setSectionHl] = React.useState(false);
  const focusedRef = React.useRef(0);

  const loadSeqRef = React.useRef(0);
  const permSeqRef = React.useRef(0);
  const saveDepthRef = React.useRef(0);
  const reloadQueuedRef = React.useRef(false);
  const pickingRef = React.useRef(false);
  const queueRef = React.useRef<SaveQueue | null>(null);
  if (!queueRef.current) queueRef.current = new SaveQueue();

  const busy = loading || locked || picking || !settings;

  const checkPermission = React.useCallback(async () => {
    const seq = ++permSeqRef.current;
    try {
      const granted = await settingsApi.fullDiskAccess();
      if (seq !== permSeqRef.current) return;
      setAccess(granted);
      setPermissionError(null);
    } catch (e) {
      if (seq !== permSeqRef.current) return;
      setAccess(null);
      setPermissionError(e);
    }
  }, []);

  /** 每次进入都重新读取，包括垃圾页新增的忽略项。 */
  const load = React.useCallback(async () => {
    // 保存或选目录期间被切回来：等队列排空后再读，避免整份配置互相覆盖。
    if (saveDepthRef.current > 0 || pickingRef.current) {
      reloadQueuedRef.current = true;
      return;
    }
    const seq = ++loadSeqRef.current;
    setLoading(true);
    setReadError(null);
    setErrors(new Map());
    void checkPermission();
    settingsApi.info().then((i) => {
      if (seq === loadSeqRef.current) setVersion(i.version);
    }).catch(() => {});
    try {
      const s = normalized(await settingsApi.getSettings());
      if (seq !== loadSeqRef.current) return;
      settingsRef.current = s;
      setSettings(s);
      setLoading(false);
      // 名称查不到时保留 ID，不阻塞设置编辑。
      if (s.ignored.some((id) => id.startsWith("app:"))) {
        try {
          const apps = await settingsApi.listApps();
          if (seq !== loadSeqRef.current) return;
          const m = new Map<string, string>();
          apps.forEach((a) => m.set(a.bundleId.toLowerCase(), a.name));
          setAppNames(m);
        } catch { /* 保留稳定 ID */ }
      }
    } catch (e) {
      if (seq !== loadSeqRef.current) return;
      settingsRef.current = null;
      setSettings(null);
      setLoading(false);
      setReadError(e);
    }
  }, [checkPermission]);

  React.useEffect(() => { void load(); }, [session, load]);

  /** 每次进入、后端广播变化时重新读增长配置；保存期间的重读排队，保存结束后补上。 */
  const loadGrowth = React.useCallback(async () => {
    if (growthSavingRef.current) { growthReloadRef.current = true; return; }
    const seq = ++growthSeqRef.current;
    if (isWindows()) {
      setGrowth(null);
      growthLoadingRef.current = false;
      setGrowthBusy(false);
      return;
    }
    growthLoadingRef.current = true;
    setGrowthBusy(true);
    try {
      const s = await settingsApi.growthSummary();
      if (seq === growthSeqRef.current) setGrowth(s.supported ? s.config : null);
    } catch {
      if (seq === growthSeqRef.current) setGrowth(null);
    } finally {
      if (seq === growthSeqRef.current) {
        growthLoadingRef.current = false;
        setGrowthBusy(false);
      }
    }
  }, []);

  React.useEffect(() => { void loadGrowth(); }, [session, loadGrowth]);
  // 菜单栏关掉监控、增长页改了设置都会广播：重读，免得本页拿着旧值显示。
  React.useEffect(() => growthApi.onUpdated(() => { void loadGrowth(); }), [loadGrowth]);

  /**
   * 只发这个控件改的字段；保存后回读后端值，失败也回读（开机启动出错时其余设置已保存并生效）。
   * 读不到时保留原值；序号防止旧读取覆盖新配置。
   */
  const saveGrowth = async (patch: GrowthConfigPatch) => {
    if (!growth || growthLoadingRef.current || growthSavingRef.current) return;
    growthSavingRef.current = true;
    const seq = ++growthSeqRef.current;
    setGrowthBusy(true);
    let failed = false;
    let error: unknown;
    try { await settingsApi.saveGrowthConfig(patch); } catch (e) { failed = true; error = e; }
    try {
      const s = await settingsApi.growthSummary();
      if (seq === growthSeqRef.current) setGrowth(s.supported ? s.config : null);
    } catch (e) {
      if (!failed) { failed = true; error = e; }
    }
    if (failed) toast(errText(error), "err");
    growthSavingRef.current = false;
    setGrowthBusy(false);
    if (growthReloadRef.current) { growthReloadRef.current = false; void loadGrowth(); }
  };

  /** 每次进入、后端广播变化时重读菜单栏读数；不支持（Windows）时整节不显示。 */
  const loadMenuBar = React.useCallback(async () => {
    const seq = ++menuBarSeqRef.current;
    if (isWindows()) {
      setMenuBar(null);
      return;
    }
    try {
      const s = await settingsApi.menuBar();
      if (seq === menuBarSeqRef.current) setMenuBar(s.supported ? s : null);
    } catch {
      if (seq === menuBarSeqRef.current) setMenuBar(null);
    }
  }, []);

  React.useEffect(() => { void loadMenuBar(); }, [session, loadMenuBar]);
  React.useEffect(() => settingsApi.onMenuBarUpdated(() => { void loadMenuBar(); }), [loadMenuBar]);

  /** 勾选框改一项：整份列表发给 Go，之后回读；读数全关时 Go 可能顺带取消了开机启动，所以也重读增长配置。 */
  const saveMenuBar = async (items: string[]) => {
    if (!menuBar || menuBarSavingRef.current) return;
    menuBarSavingRef.current = true;
    setMenuBarBusy(true);
    const seq = ++menuBarSeqRef.current;
    try { await settingsApi.saveMenuBar(items); } catch (e) { toast(errText(e), "err"); }
    try {
      const s = await settingsApi.menuBar();
      if (seq === menuBarSeqRef.current) setMenuBar(s.supported ? s : null);
    } catch { /* 读不到时保留原值 */ }
    menuBarSavingRef.current = false;
    setMenuBarBusy(false);
    void loadGrowth();
  };

  // 「菜单栏设置…」进来：等「菜单栏」一节出现，滚过去并高亮一下；同一个请求只滚一次。
  const menuBarShown = !!menuBar && !isWindows();
  React.useEffect(() => {
    if (!focus || focus.id !== "menubar" || !menuBarShown || loading || focusedRef.current === focus.nonce) return;
    const nonce = focus.nonce;
    const raf = requestAnimationFrame(() => {
      focusedRef.current = nonce;
      document.getElementById("settings-menubar")?.scrollIntoView({ block: "start", behavior: scrollBehavior() });
      setSectionHl(true);
    });
    return () => cancelAnimationFrame(raf);
  }, [focus, menuBarShown, loading]);
  React.useEffect(() => {
    if (!sectionHl) return;
    const id = window.setTimeout(() => setSectionHl(false), 900);
    return () => window.clearTimeout(id);
  }, [sectionHl]);

  // 窗口重新聚焦时重新检查权限（仅本页可见时）。
  React.useEffect(() => {
    const onFocus = () => {
      if (!document.querySelector("#view-settings")?.classList.contains("hidden")) void checkPermission();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [checkPermission]);

  /** 串行保存最新配置；非名称保存期间锁住编辑。 */
  const save = React.useCallback((update: (s: Settings) => Settings, key: string, lock: boolean) => {
    if (!settingsRef.current || loading) return;
    saveDepthRef.current++;
    if (lock) setLocked(true);
    setErrors((prev) => withoutError(prev, key));
    void queueRef.current!.run(async () => {
      const next = update(settingsRef.current!);
      try {
        await settingsApi.saveSettings(next);
        settingsRef.current = next;
        setSettings(next);
        markCleanStale();
      } catch (e) {
        setErrors((prev) => withError(prev, key, e));
      } finally {
        saveDepthRef.current--;
        if (lock && saveDepthRef.current === 0) setLocked(false);
        if (reloadQueuedRef.current && saveDepthRef.current === 0 && !pickingRef.current) {
          reloadQueuedRef.current = false;
          void load();
        }
      }
    });
  }, [loading, load]);

  const onGroupToggle = (g: GroupId, checked: boolean) => {
    save((s) => ({ ...s, disabledGroups: nextDisabledGroups(s.disabledGroups, g, checked) }), `group:${g}`, true);
  };
  const onRestore = (id: string) => {
    save((s) => ({ ...s, ignored: s.ignored.filter((v) => v !== id) }), `ignored:${id}`, true);
  };
  const onCustomEnabled = (folder: CustomFolder, enabled: boolean) => {
    save((s) => ({ ...s, custom: s.custom.map((c) => c.path === folder.path ? { ...c, enabled } : c) }), `custom:${folder.path}`, true);
  };
  const onCustomNameBlur = (folder: CustomFolder, e: React.FocusEvent<HTMLInputElement>) => {
    const input = e.target;
    const name = resolveFolderName(input.value, folder.path);
    // 名称没变且没有在保存：把用户输入的空白规整回去即可。
    if (name === folder.name && saveDepthRef.current === 0) { input.value = name; return; }
    save((s) => ({ ...s, custom: s.custom.map((c) => c.path === folder.path ? { ...c, name } : c) }), `custom:${folder.path}`, false);
  };
  const onRemoveFolder = (folder: CustomFolder) => {
    save((s) => ({ ...s, custom: s.custom.filter((c) => c.path !== folder.path) }), `custom:${folder.path}`, true);
  };
  const onAddFolder = async () => {
    if (!settingsRef.current || loading || locked || picking) return;
    pickingRef.current = true;
    setPicking(true);
    setErrors((prev) => withoutError(prev, "add"));
    try {
      const path = await settingsApi.pickFolder();
      // 用户取消不加。
      if (path) {
        await save(
          (s) => ({ ...s, custom: [...s.custom, { name: baseNameOf(path), path, enabled: true }] }),
          "add",
          true,
        );
      }
    } catch (e) {
      setErrors((prev) => withError(prev, "add", e));
    } finally {
      pickingRef.current = false;
      setPicking(false);
      if (reloadQueuedRef.current) { reloadQueuedRef.current = false; void load(); }
    }
  };

  const onLangChange = async (e: React.ChangeEvent<HTMLSelectElement>) => {
    const pref = e.target.value as LangPref;
    if (pref === getPref()) return;
    setLangChanging(true);
    try {
      await callbacks.changeLanguage(pref);
      // 成功后 language.change 已写偏好并触发 bumpLang，受控 select 跟随 getPref()。
    } catch {
      // 失败时偏好未写，受控 select 自动回退到 getPref()；toast 由 main.ts 弹。
    } finally {
      setLangChanging(false);
    }
  };

  const permissionKey = access === null
    ? (permissionError ? "settings.permissionUnknown" : "settings.checking")
    : access ? "settings.granted" : "settings.notGranted";

  const statusText = readError
    ? t("settings.readFail", { msg: errText(readError) })
    : loading ? t("settings.loading") : "";

  return (
    <div className="cz-react">
      <header className="view-head">
        <h1>{t("nav.settings")}</h1>
      </header>

      {statusText && (
        <p className={cn("settings-status", !!readError && "err")} role="status">
          {statusText}
        </p>
      )}

      {/* 通用 */}
      <section className="settings-section">
        <h2>{t("settings.general")}</h2>
        <div className="setting-block card">
          <div className="setting-row">
            <label className="setting-label" htmlFor="lang-select">{t("settings.lang")}</label>
            <select
              id="lang-select"
              className="select"
              value={getPref()}
              disabled={langChanging}
              onChange={(e) => void onLangChange(e)}
            >
              <option value="system">{t("settings.langSystem")}</option>
              <option value="zh">{t("settings.langZh")}</option>
              <option value="en">{t("settings.langEn")}</option>
            </select>
          </div>
        </div>
      </section>

      {/* 垃圾清理 */}
      <section className="settings-section">
        <h2>{t("nav.clean")}</h2>
        <div className="setting-block card">
          <div className="setting-row setting-stack">
            <h3 className="setting-label">{t("settings.scope")}</h3>
            <div className="settings-groups">
              {GROUPS.map((g) => (
                <div key={g}>
                  <label className="check">
                    <Checkbox
                      checked={!settings?.disabledGroups.includes(g)}
                      disabled={busy}
                      onCheckedChange={(v) => onGroupToggle(g, v === true)}
                    />
                    <span>{g === "review" ? t("settings.reviewItems") : t(`junk.group.${g}` as DictKey)}</span>
                  </label>
                  {errors.has(`group:${g}`) && (
                    <p className="err-inline" role="alert">{errText(errors.get(`group:${g}`))}</p>
                  )}
                </div>
              ))}
            </div>
          </div>

          <div className="setting-row setting-stack">
            <h3 className="setting-label">{t("settings.ignored")}</h3>
            <div className="settings-list">
              {settings && settings.ignored.length > 0
                ? settings.ignored.map((id) => (
                  <div key={id} className="settings-item">
                    <div className="settings-item-line">
                      <span>{ignoredLabel(id, appNames, ruleName)}</span>
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => onRestore(id)}>
                        {t("settings.restore")}
                      </Button>
                    </div>
                    {errors.has(`ignored:${id}`) && (
                      <p className="err-inline" role="alert">{errText(errors.get(`ignored:${id}`))}</p>
                    )}
                  </div>
                ))
                : <p className="muted small">{t("settings.ignoredEmpty")}</p>}
            </div>
          </div>

          <div className="setting-row setting-stack">
            <h3 className="setting-label">{t("settings.custom")}</h3>
            <div className="settings-list">
              {settings?.custom.map((c) => (
                <div key={c.path} className="settings-item">
                  <div className="settings-custom-line">
                    <div className="settings-folder-info">
                      <input
                        key={`${c.path}:${c.name}`}
                        defaultValue={c.name}
                        name="custom-folder-name"
                        autoComplete="off"
                        spellCheck={false}
                        aria-label={t("settings.name")}
                        disabled={busy}
                        onBlur={(e) => onCustomNameBlur(c, e)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") { e.preventDefault(); (e.target as HTMLInputElement).blur(); }
                        }}
                      />
                      <span className="settings-folder-path muted small">{displayPath(c.path)}</span>
                    </div>
                    <label className="check">
                      <Checkbox
                        checked={c.enabled}
                        disabled={busy}
                        onCheckedChange={(v) => onCustomEnabled(c, v === true)}
                      />
                      <span>{t("settings.enabled")}</span>
                    </label>
                    <Button variant="ghost" size="sm" disabled={busy} onClick={() => onRemoveFolder(c)}>
                      {t("settings.remove")}
                    </Button>
                  </div>
                  {errors.has(`custom:${c.path}`) && (
                    <p className="err-inline" role="alert">{errText(errors.get(`custom:${c.path}`))}</p>
                  )}
                </div>
              ))}
            </div>
            <div className="settings-add">
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => void onAddFolder()}>
                {t("settings.addFolder")}
              </Button>
              <span className="muted small">{t("settings.trashNote")}</span>
            </div>
            {errors.has("add") && (
              <p className="err-inline" role="alert">{errText(errors.get("add"))}</p>
            )}
          </div>
        </div>
      </section>

      {menuBar && !isWindows() && (
        <section className="settings-section" id="settings-menubar">
          <h2>{t("settings.menubar")}</h2>
          <div className={cn("setting-block card", sectionHl && "settings-hl")}>
            <div className="setting-row setting-stack">
              <div className="settings-groups">
                {menuBar.available.map((it) => (
                  <label key={it} className="check">
                    <Checkbox
                      checked={menuBar.items.includes(it)}
                      disabled={menuBarBusy}
                      onCheckedChange={(v) => void saveMenuBar(toggleMenuBarItem(menuBar.items, it, v === true, menuBar.available))}
                    />
                    <span>{t(`settings.menubar.item.${it}` as DictKey)}</span>
                  </label>
                ))}
              </div>
              <p className="muted small">{t("settings.menubar.note")}</p>
            </div>
            {growth && (
              <div className="setting-row setting-stack">
                <label className="check">
                  <Checkbox
                    checked={growth.openAtLogin}
                    disabled={growthBusy || menuBarBusy || !(growth.enabled || menuBar.items.length > 0)}
                    onCheckedChange={(v) => void saveGrowth({ openAtLogin: v === true })}
                  />
                  <span>{t("settings.menubar.login")}</span>
                </label>
                {!(growth.enabled || menuBar.items.length > 0) && <p className="muted small">{t("settings.menubar.loginOff")}</p>}
              </div>
            )}
          </div>
        </section>
      )}

      {growth && !isWindows() && (
        <section className="settings-section">
          <h2>{t("settings.growth")}</h2>
          <div className="setting-block card">
            <div className="setting-row setting-stack">
              <label className="check">
                <Checkbox checked={growth.enabled} disabled={growthBusy} onCheckedChange={(v) => void saveGrowth({ enabled: v === true, asked: true })} />
                <span>{t("settings.growth.enable")}</span>
              </label>
              <p className="muted small">{t("settings.growth.enableNote")}</p>
            </div>
            <div className="setting-row">
              <label className="setting-label" htmlFor="growth-daily">{t("settings.growth.daily")}</label>
              <select id="growth-daily" className="select" value={growth.dailyGB} disabled={growthBusy}
                onChange={(e) => void saveGrowth({ dailyGB: Number(e.target.value) })}>
                {[1, 2, 5, 10].map((n) => <option key={n} value={n}>{n} GB</option>)}
              </select>
            </div>
            <div className="setting-row">
              <label className="setting-label" htmlFor="growth-weekly">{t("settings.growth.weekly")}</label>
              <select id="growth-weekly" className="select" value={growth.weeklyGB} disabled={growthBusy}
                onChange={(e) => void saveGrowth({ weeklyGB: Number(e.target.value) })}>
                {[5, 10, 20, 50].map((n) => <option key={n} value={n}>{n} GB</option>)}
              </select>
            </div>
          </div>
        </section>
      )}

      <PermissionsSection access={access} permissionKey={permissionKey as DictKey} permissionError={permissionError}
        onOpen={() => { void settingsApi.openFullDiskAccessSettings().catch((e) => setPermissionError(e)); }} />

      {/* 关于 */}
      <section className="settings-section">
        <h2>{t("settings.about")}</h2>
        <div className="setting-block card">
          <div className="setting-row">
            <span className="setting-label">{t("settings.version")}</span>
            <span className="setting-version">
              <span className="muted">{version}</span>
              <Button variant="ghost" size="sm" onClick={() => { void settingsApi.checkForUpdates().catch(() => {}); }}>
                {t("settings.checkUpdate")}
              </Button>
            </span>
          </div>
          <div className="setting-row">
            <span className="setting-label">{t("settings.feedback")}</span>
            <a
              className={cn(buttonVariants({ variant: "ghost", size: "sm" }))}
              target="_blank"
              rel="noopener noreferrer"
              href="https://github.com/echoVic/cleanzo/issues/new"
            >
              {t("settings.feedbackBtn")}
            </a>
          </div>
        </div>
      </section>
    </div>
  );
}

/** macOS 的磁盘授权设置。 */
export function PermissionsSection({ access, permissionKey, permissionError, onOpen }: {
  access: boolean | null; permissionKey: DictKey; permissionError: unknown; onOpen: () => void;
}) {
  const t = useT();
  if (isWindows()) return null;
  return (
    <section className="settings-section">
      <h2>{t("settings.permissions")}</h2>
      <div className="setting-block card">
        <div className="setting-row setting-permission">
          <div className="settings-permission-info">
            <span className="setting-label">{t("settings.fullDiskAccess")}</span>
            <span className={cn("permission-status", access === true && "granted")} role="status">
              <span className="permission-dot" aria-hidden="true" />
              {t(permissionKey as DictKey)}
            </span>
            <p className="muted small">{t("settings.permissionNote")}</p>
            {permissionError ? (
              <p className="err-inline" role="alert">{errText(permissionError)}</p>
            ) : null}
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={onOpen}
          >
            {t("settings.openSystem")}
          </Button>
        </div>
      </div>
    </section>
  );
}
