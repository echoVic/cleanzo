import * as React from "react";
import { FolderOpen, RefreshCw } from "lucide-react";
import type { GrowthApp, GrowthConfigPatch, GrowthState, UninstallDetail } from "../../mygo";
import { Badge } from "../../ui/components/badge";
import { Button } from "../../ui/components/button";
import { EmptyGuide } from "../../ui/components/empty-guide";
import { Collapse } from "../../ui/motion/Collapse";
import { useT } from "../../ui/i18n-react";
import { errText, human, toast } from "../../shared";
import { shortHome } from "../../platform";
import { t as translate } from "../../i18n";
import { iconColor } from "../status/format";
import { growthApi } from "./api";
import { fmtDelta, growthBanner, growthView, sortApps, sparkPath, uninstallGap, type GrowthSort } from "./format";
import { AppIcon } from "../icons/AppIcon";

/** main.ts 注入的跳转：残留页签、卸载对话框（都走现有流程，本页不删任何东西）。 */
export interface GrowthHandlers {
  goLeftovers: () => void;
  goUninstall: (appPath: string) => void;
}

/** 占用增长面板的显示状态、访问次数、定位请求和导航回调。 */
export interface GrowthPanelProps extends GrowthHandlers {
  /** 是否正显示本视图：隐藏时不跟着 growthUpdated 刷新。 */
  active: boolean;
  /** 每次进入视图递增：重新拉状态。 */
  visit: number;
  /** 要展开并高亮的行；focusNonce 变了才执行。 */
  focusKey: string | null;
  focusNonce: number;
}

/** 按 App 汇总的占用、增长记录与目录明细。放在进程页的磁盘占用标签里，没有自己的页头。 */
export function GrowthPanel({ active, visit, focusKey, focusNonce, goLeftovers, goUninstall }: GrowthPanelProps) {
  const t = useT();
  const [state, setState] = React.useState<GrowthState | null>(null);
  const [sort, setSort] = React.useState<GrowthSort>("week");
  const [open, setOpen] = React.useState<string | null>(null);
  const [details, setDetails] = React.useState<Record<string, UninstallDetail | null>>({});
  const [hl, setHl] = React.useState<string | null>(null);
  const focused = React.useRef<{ key: string; nonce: number } | null>(null);
  /** 上次核对卸载明细时的记录（时间、条数）。 */
  const measured = React.useRef<{ lastAt: number; records: number } | null>(null);

  const load = React.useCallback(async () => {
    try {
      const s = await growthApi.state();
      setState(s);
      // 有了新记录才重新核对卸载能移走的内容；只是刷新状态时保留已取到的明细，展开的行不闪。
      const was = measured.current;
      if (!was || was.lastAt !== s.status.lastAt || was.records !== s.records) {
        measured.current = { lastAt: s.status.lastAt, records: s.records };
        setDetails({});
      }
    }
    catch (e) { toast(translate("growth.loadFail", { msg: errText(e) }), "err"); }
  }, []); // useT 每次渲染返回新函数；加载回调保持稳定，错误文案仍读取当前语言。
  React.useEffect(() => { if (active) void load(); }, [load, visit, active]);
  // 只在显示时跟着 growthUpdated 刷新：隐藏的页面不拉状态，也不在别的页面弹读取失败。
  React.useEffect(() => active ? growthApi.onUpdated(() => { void load(); }) : undefined, [load, active]);

  const focusReady = !!state?.supported && state.records > 0 && state.apps.some((a) => a.key === focusKey);
  // 通知 / 菜单栏定位：等目标行加载后展开、滚到中间，高亮 900ms。
  React.useEffect(() => {
    if (!focusKey || !focusReady) return;
    if (focused.current?.key === focusKey && focused.current.nonce === focusNonce) return;
    setOpen(focusKey);
    let id: number | undefined;
    const raf = requestAnimationFrame(() => {
      const row = document.querySelector(`[data-growth-key="${CSS.escape(focusKey)}"]`);
      if (!row) return;
      focused.current = { key: focusKey, nonce: focusNonce };
      row.scrollIntoView({ block: "center" });
      setHl(focusKey);
      id = window.setTimeout(() => setHl(null), 900);
    });
    return () => { cancelAnimationFrame(raf); window.clearTimeout(id); setHl(null); };
  }, [focusKey, focusNonce, focusReady]);

  // 展开已安装 App 时取卸载明细，写「卸载能移走多少、另外多少不会」。
  React.useEffect(() => {
    const row = state?.apps.find((a) => a.key === open);
    if (!row?.appPath || row.key in details) return;
    let current = true;
    growthApi.uninstallDetail(row.appPath).then(
      (d) => { if (current) setDetails((m) => ({ ...m, [row.key]: d })); },
      () => { if (current) setDetails((m) => ({ ...m, [row.key]: null })); },
    );
    return () => { current = false; };
  }, [open, state, details]);

  const save = async (patch: GrowthConfigPatch) => {
    try { await growthApi.saveConfig(patch); await load(); }
    catch (e) { toast(errText(e), "err"); }
  };
  const measure = async () => {
    try { await growthApi.measureNow(); await load(); }
    catch (e) { toast(errText(e), "err"); }
  };
  const reveal = (path: string) => growthApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  const toggleIgnore = (a: GrowthApp) => void save(a.ignored ? { unignore: a.key } : { ignore: a.key });

  const view = growthView(state);
  if (!state || view === "unsupported") return null;
  const banner = growthBanner(state);
  const atLeast = (s: string) => t("growth.atLeast", { size: s });
  const fmtTime = (sec: number) => new Date(sec * 1000).toLocaleString();

  return (
    <div className="cz-growth-panel">
      {banner === "optin" && (
        <div className="cz-growth-banner">
          <strong>{t("growth.optin.title")}</strong>
          <p className="muted">{t("growth.optin.body")}</p>
          <div className="cz-growth-actions">
            <Button onClick={() => void save({ enabled: true, asked: true })}>{t("growth.optin.enable")}</Button>
            <Button variant="ghost" onClick={() => void save({ asked: true })}>{t("growth.optin.later")}</Button>
          </div>
        </div>
      )}
      {banner === "off" && (
        <div className="cz-growth-banner cz-growth-banner-quiet">
          <span className="muted">{t("growth.off")}</span>
          <Button variant="ghost" size="sm" onClick={() => void save({ enabled: true })}>{t("growth.off.enable")}</Button>
        </div>
      )}

      <div className="cz-growth-statusline">
        <p className="cz-growth-status muted">
          {state.status.lastAt ? t("growth.status.last", { time: fmtTime(state.status.lastAt) }) : t("growth.status.never")}
          {state.config.enabled && state.status.nextAt > 0 && <> · {t("growth.status.next", { time: fmtTime(state.status.nextAt) })}</>}
        </p>
        {/* 还没有记录时，入口是空状态里的大按钮，这里不再放一个。 */}
        {view !== "empty" && (
          <Button size="sm" onClick={() => void measure()} disabled={state.status.running}>
            <RefreshCw size={14} className={state.status.running ? "animate-spin" : undefined} />
            {state.status.running ? t("growth.measuring") : t("growth.measureNow")}
          </Button>
        )}
      </div>
      {!state.status.fda && <p className="cz-growth-note muted">{t("growth.fda")}</p>}
      {state.config.enabled && state.status.notifications === "denied" && <p className="cz-growth-note muted">{t("growth.notifDenied")}</p>}

      {view === "empty" && (
        <EmptyGuide gradId="growth-empty" title={t("growth.empty.title")} desc={t("growth.empty.desc")}
          ctaText={state.status.running ? t("growth.measuring") : t("growth.measureNow")}
          onCta={() => void measure()} busy={state.status.running}>
          <path d="M6 6v12h12M9 14l3-4 3 2 3-5" />
        </EmptyGuide>
      )}

      {(view === "baseline" || view === "list") && (
        <>
          {view === "baseline" && <p className="cz-growth-note muted">{t("growth.baseline")}</p>}
          <div className="cz-growth-toolbar">
            <select className="select" value={view === "baseline" ? "size" : sort} disabled={view === "baseline"}
              onChange={(e) => setSort(e.target.value as GrowthSort)} aria-label={t("growth.sort.week")}>
              <option value="week">{t("growth.sort.week")}</option>
              <option value="size">{t("growth.sort.size")}</option>
            </select>
          </div>
          <ul className="cz-growth-list">
            <li className="cz-growth-row cz-growth-head muted" aria-hidden>
              <span />
              <span>{t("growth.col.size")}</span>
              <span>{t("growth.col.day")}</span>
              <span>{t("growth.col.week")}</span>
              <span className="cz-growth-month">{t("growth.col.month")}</span>
              <span className="cz-growth-spark" />
            </li>
            {sortApps(state.apps, view === "baseline" ? "size" : sort).map((a) => {
              const isOpen = open === a.key;
              const gap = a.appPath ? uninstallGap(a.size, details[a.key] ?? null) : null;
              const spark = sparkPath(a.spark, 72, 18);
              return (
                <li key={a.key} data-growth-key={a.key} className={`cz-growth-item${hl === a.key ? " cz-growth-hl" : ""}`}>
                  <button type="button" className="cz-growth-row" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : a.key)}>
                    <span className="cz-growth-name">
                      <AppIcon path={a.appPath} box="cz-growth-icon">
                        <span className="cz-growth-icon" style={{ background: iconColor(a.name) }} aria-hidden>
                          {(a.name.replace(/^\.+/, "") || a.name).charAt(0).toUpperCase()}
                        </span>
                      </AppIcon>
                      <span className="cz-growth-label">{a.name}</span>
                      {a.kind === "macos" && <Badge variant="optional">{t("growth.badge.macos")}</Badge>}
                      {a.kind === "unknown" && <Badge variant="optional">{t("growth.badge.unknown")}</Badge>}
                      {a.ignored && <Badge variant="optional">{t("growth.badge.ignored")}</Badge>}
                    </span>
                    <span className="num">{human(a.size)}{a.partial && " *"}</span>
                    <span className="num">{fmtDelta(a.day, atLeast)}</span>
                    <span className="num">{fmtDelta(a.week, atLeast)}</span>
                    <span className="num cz-growth-month">{fmtDelta(a.month, atLeast)}</span>
                    <svg className="cz-growth-spark" viewBox="0 0 72 18" width="72" height="18" aria-hidden>
                      {spark && <path d={spark} />}
                    </svg>
                  </button>
                  <Collapse open={isOpen}>
                    <div className="cz-growth-detail">
                      {a.partial && <p className="muted">{t("growth.partial")}</p>}
                      <ul>
                        {a.folders.map((f) => (
                          <li key={f.path} className="cz-growth-folder">
                            <span className="cz-growth-path" title={f.path}>{shortHome(f.path)}</span>
                            {f.denied && <span className="muted">{t("growth.denied")}</span>}
                            {f.outside && <span className="muted">{t("growth.outside")}</span>}
                            <span className="num cz-growth-fsize">{human(f.size)}{!f.complete && " *"}</span>
                            <span className="num cz-growth-fweek">{fmtDelta(f.week, atLeast)}</span>
                            <Button variant="ghost" size="sm" className="cz-growth-reveal" onClick={() => void reveal(f.path)} aria-label={t("growth.reveal")}>
                              <FolderOpen size={14} />
                            </Button>
                          </li>
                        ))}
                      </ul>
                      {gap && <p className="muted">{t("growth.uninstallGap", { freed: human(gap.freed), rest: human(gap.rest) })}</p>}
                      <div className="cz-growth-actions">
                        {a.appPath && <Button variant="ghost" size="sm" onClick={() => goUninstall(a.appPath)}>{t("growth.uninstall")}</Button>}
                        {a.orphan && <Button variant="ghost" size="sm" onClick={goLeftovers}>{t("growth.goLeftovers")}</Button>}
                        <Button variant="ghost" size="sm" onClick={() => toggleIgnore(a)}>{a.ignored ? t("growth.unignore") : t("growth.ignore")}</Button>
                      </div>
                    </div>
                  </Collapse>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </div>
  );
}
