import type { DiskStat } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { NumberTicker } from "../../ui/motion/NumberTicker";
import { diskPercent, human } from "./format";

/** 磁盘容量卡：复用原生 .disk-card / .disk-stats / .bar 视觉，进入时懒加载，失败静默保留 --。 */
export function DiskCard({ disk }: { disk: DiskStat | null }) {
  const t = useT();
  const pct = disk ? diskPercent(disk) : 0;
  return (
    <div className="disk-card card">
      <div className="disk-stats">
        <div className="disk-stat">
          <span className="label">{t("fs.used")}</span>
          <span className="value">{disk ? <NumberTicker value={disk.used} format={human} /> : "--"}</span>
        </div>
        <div className="disk-stat">
          <span className="label">{t("fs.free")}</span>
          <span className="value">{disk ? <NumberTicker value={disk.free} format={human} /> : "--"}</span>
        </div>
        <div className="disk-stat">
          <span className="label">{t("fs.total")}</span>
          <span className="value">{disk ? <NumberTicker value={disk.total} format={human} /> : "--"}</span>
        </div>
      </div>
      <div className="bar"><div className="bar-fill" style={{ width: `${pct}%` }} /></div>
      <p className="muted small fs-hint">{t("fs.hint")}</p>
    </div>
  );
}
