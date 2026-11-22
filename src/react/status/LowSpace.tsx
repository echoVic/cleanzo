import * as React from "react";
import type { SafeSpace } from "../../mygo";
import { errText, toast } from "../../shared";
import { useT } from "../../ui/i18n-react";
import { CRASH_RULE_ID, forgetCrashDumps } from "../list/crash-dumps";
import { statusApi } from "./api";
import { fmtMem } from "./format";
import { SAFE_ARM_MS, SAFE_FRESH_MS, SAFE_MIN, SAFE_RESULT_MS, safeNames } from "./safe-space";

/** 现在几点、过一会儿再做一件事；测试换掉它们来拨钟。 */
export const lowSpaceClock = {
  now: () => Date.now(),
  later: (fn: () => void, ms: number): number => window.setTimeout(fn, ms),
  cancel: (id: number) => window.clearTimeout(id),
};

// 最近一次量到的：提示条随磁盘读数出现又消失，量到的结果留在这里，短时间内回来不用再量。
let measured: { space: SafeSpace; when: number } | null = null;

/** 忘掉量到的结果（清理过以后它不再作数）。 */
export function forgetSafeSpace() {
  measured = null;
}

type State =
  | { kind: "idle" }
  /** since：按钮出现的时刻。 */
  | { kind: "ready"; space: SafeSpace; since: number }
  | { kind: "cleaning"; total: number; bytes: number }
  | { kind: "done"; freed: number; inUse: number };

/**
 * 磁盘不足提示条的内容。停在页面上时量一次「扫描会自己勾上、直接删除」的那些行：
 * 够多就写明有多少、是哪几类，并给一个直接清掉它们的按钮；清理可以停，结果写回这一条。
 * 量不出来或太少时就是原来那一句和「去清理」。
 * onHold：清理中和刚清完的一小会儿要求这一条留着，哪怕磁盘已经不算不足了。
 */
export function LowSpaceNotice({ free, active, onGoClean, onHold }: {
  free: number; active: boolean; onGoClean: () => void; onHold?: (hold: boolean) => void;
}) {
  const t = useT();
  const [state, setState] = React.useState<State>({ kind: "idle" });
  const stateRef = React.useRef(state);
  stateRef.current = state;
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);

  React.useEffect(() => {
    if (!active || stateRef.current.kind === "cleaning") return;
    const now = lowSpaceClock.now();
    if (measured && now - measured.when < SAFE_FRESH_MS) {
      if (stateRef.current.kind === "idle") setState({ kind: "ready", space: measured.space, since: now });
      return;
    }
    let live = true;
    statusApi.safeSpace().then((space) => {
      measured = { space, when: lowSpaceClock.now() };
      if (live && stateRef.current.kind !== "cleaning") setState({ kind: "ready", space, since: lowSpaceClock.now() });
    }, () => { /* 量不出来：留着原来那一句 */ });
    return () => { live = false; };
  }, [active]);

  const clean = async () => {
    const s = stateRef.current;
    if (s.kind !== "ready" || lowSpaceClock.now() - s.since < SAFE_ARM_MS) return;
    const ids = s.space.rows.map((row) => row.id);
    setState({ kind: "cleaning", total: s.space.bytes, bytes: 0 });
    stoppedRef.current = false;
    forgetSafeSpace();
    const { ch, done } = statusApi.cleanSafe(ids, s.space.at);
    chanRef.current = ch;
    let freed = 0, inUse = 0;
    try {
      const completion = done.then(() => null, (e: unknown) => e);
      for await (const ev of ch) {
        if (ev.kind === "removed") {
          const bytes = ev.bytes;
          if (bytes != null) setState((prev) => (prev.kind === "cleaning" ? { ...prev, bytes } : prev));
        } else if (ev.kind === "done" && ev.result) {
          freed += ev.result.freed;
          inUse += ev.result.inUse;
          const bytes = ev.bytes ?? freed;
          setState((prev) => (prev.kind === "cleaning" ? { ...prev, bytes } : prev));
        }
      }
      const error = await completion;
      if (error !== null && !stoppedRef.current) throw error;
      if (freed > 0) toast(t("clean.done.freed", { size: fmtMem(freed) }));
    } catch (e) {
      toast(t("clean.fail", { msg: errText(e) }), "err");
    } finally {
      chanRef.current = null;
      if (ids.includes(CRASH_RULE_ID)) forgetCrashDumps();
      setState({ kind: "done", freed, inUse });
    }
  };

  const stop = () => {
    stoppedRef.current = true;
    chanRef.current?.close();
  };

  const onHoldRef = React.useRef(onHold);
  onHoldRef.current = onHold;
  // 这一条被拿走时（页面出错、采样重开）不再要求留着。
  React.useEffect(() => () => onHoldRef.current?.(false), []);
  React.useEffect(() => {
    if (state.kind === "cleaning") {
      onHoldRef.current?.(true);
      return;
    }
    if (state.kind !== "done") return;
    const timer = lowSpaceClock.later(() => onHoldRef.current?.(false), SAFE_RESULT_MS);
    return () => lowSpaceClock.cancel(timer);
  }, [state.kind]);

  const offer = state.kind === "ready" && state.space.bytes >= SAFE_MIN ? state.space : null;
  return (
    <>
      <div className="st-callout-body">
        {state.kind === "cleaning" ? (
          // 进度数字一直在变：提示条是 role="status"，读屏只听到「正在清理…」，结束时的那一句照常播报。
          <>
            <span className="sr-only">{t("clean.busy")}</span>
            <span className="num" aria-hidden="true">{t("status.safe.cleaning", { size: fmtMem(state.bytes), total: fmtMem(state.total) })}</span>
          </>
        ) : state.kind === "done" && state.freed > 0 ? (
          // 清完以后磁盘可能已经不算不足：说结果，不再说「只剩」。
          <span className="num">
            {t("status.safe.done", { size: fmtMem(state.freed), free: fmtMem(free) })}
            {state.inUse > 0 ? t("phrase.sep") + t("clean.done.inUse", { n: state.inUse }) : ""}
          </span>
        ) : t("status.alert.diskLow", { size: fmtMem(free) })}
        {offer && (
          <span className="st-extra">{t("status.safe.line", { size: fmtMem(offer.bytes), names: safeNames(offer.rows, t) })}</span>
        )}
      </div>
      <div className="st-callout-actions">
        {offer && <button className="btn primary small" type="button" onClick={() => void clean()}>{t("status.safe.clean", { size: fmtMem(offer.bytes) })}</button>}
        {state.kind === "cleaning"
          ? <button className="btn ghost small" type="button" onClick={stop}>{t("clean.stop")}</button>
          : <button className="btn ghost small" type="button" onClick={onGoClean}>{t("status.disk.clean")}</button>}
      </div>
    </>
  );
}
