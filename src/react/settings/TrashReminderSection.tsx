import * as React from "react";
import type { TrashReminderConfig, TrashReminderState } from "../../mygo";
import { Checkbox } from "../../ui/components/checkbox";
import { useT } from "../../ui/i18n-react";

export function TrashReminderControls({ state, busy, onChange }: {
  state: TrashReminderState;
  busy: boolean;
  onChange: (config: TrashReminderConfig) => void;
}) {
  const t = useT();
  if (!state.supported) return null;
  const { config } = state;
  return <section className="settings-section">
    <h2>{t("settings.trash.title")}</h2>
    <div className="setting-block card">
      <div className="setting-row setting-stack">
        <label className="check">
          <Checkbox checked={config.enabled} disabled={busy}
            onCheckedChange={(v) => onChange({ ...config, enabled: v === true })} />
          <span>{t("settings.trash.enabled")}</span>
        </label>
        <p className="muted small">{t("settings.trash.note")}</p>
        {!state.fda && <p className="muted small">{t("settings.trash.permission")}</p>}
      </div>
      <div className="setting-row">
        <label className="setting-label" htmlFor="trash-threshold">{t("settings.trash.threshold")}</label>
        <select id="trash-threshold" className="select" value={config.thresholdGB} disabled={busy || !config.enabled}
          onChange={(e) => onChange({ ...config, thresholdGB: Number(e.target.value) })}>
          {[1, 2, 5, 10].map((n) => <option key={n} value={n}>{n} GB</option>)}
        </select>
      </div>
    </div>
  </section>;
}
